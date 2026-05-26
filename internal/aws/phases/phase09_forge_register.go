package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// Phase09ForgeRegister registers the EKS cluster with a running bnk-forge
// instance between Phase08 (cluster active) and Phase10 (node group).
//
// Registration is MCP-first with REST fallback on catalog-gap errors.
// The entire attempt is wrapped in a 3-retry loop with exponential backoff.
// On final failure the phase writes forge-link.json with Status="pending"
// and returns nil — AWS infra must not be blocked by forge availability.
//
// D-005: CheckAuthOrDie is called at entry even though forge calls do not
// touch AWS — the sentinel may have tripped during phase 08 EKS waits.
func Phase09ForgeRegister(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)

	if cl.Forge == nil || !cl.Forge.Enabled {
		fmt.Fprintln(os.Stderr, "[phase 09] forge: not enabled, skipping")
		return nil
	}

	forgeURL := cl.Forge.ResolveURL()
	mcpURL := cl.Forge.MCPURL
	if mcpURL == "" {
		mcpURL = forge.DefaultMCPURL
	}

	// Resolve REST credentials and warn when the built-in default is in use.
	forgeUsername := cl.Forge.ResolveUsername()
	forgePassword, usingDefaultPwd := cl.Forge.ResolvePassword()
	if usingDefaultPwd {
		fmt.Fprintln(os.Stderr, "[forge] using default password; set AWSBNKCTL_FORGE_PASSWORD or forge.password for your deployment")
	}
	restCreds := forge.RestCreds{Username: forgeUsername, Password: forgePassword}

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 09] dry-run: would register with forge at %s\n", mcpURL)
		st.Set("FORGE_PROJECT_ID", "dry-run-project")
		st.Set("FORGE_CLUSTER_ID", "dry-run-cluster")
		st.Set("FORGE_LINK_PATH", filepath.Join(cl.StateDir(), forge.LinkFileName))
		st.Set("FORGE_STATUS", "dry-run")
		return nil
	}

	workspaceDir := cl.StateDir()

	// Idempotency: check for existing link.
	if existing, err := forge.ReadLink(workspaceDir); err == nil {
		if existing.IsRegistered() {
			fmt.Fprintln(os.Stderr, "[phase 09] forge link exists, skipping")
			setForgeState(st, existing, workspaceDir)
			return nil
		}
		// Status == "pending" → operator wants retry; fall through.
		fmt.Fprintln(os.Stderr, "[phase 09] forge link status=pending, retrying registration")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("phase09: read existing forge link: %w", err)
	}

	// Build the bootstrap kubeconfig in-memory.
	clusterARN := st.Get("EKS_CLUSTER_ARN")
	if clusterARN == "" {
		return fmt.Errorf("phase09: EKS_CLUSTER_ARN not in state (run phase08 first)")
	}
	endpoint := st.Get("EKS_ENDPOINT")
	if endpoint == "" {
		return fmt.Errorf("phase09: EKS_ENDPOINT not in state (run phase08 first)")
	}
	ca := st.Get("EKS_CA")
	if ca == "" {
		return fmt.Errorf("phase09: EKS_CA not in state (run phase08 first)")
	}
	clusterName := cl.Metadata.Name
	region := cl.Metadata.Region

	kubeconfig, err := renderKubeconfig(clusterARN, endpoint, ca, clusterName, region)
	if err != nil {
		return fmt.Errorf("phase09: render bootstrap kubeconfig: %w", err)
	}

	credTplID := 0
	if cl.Forge != nil {
		credTplID = cl.Forge.CredentialTemplateID
	}
	req := forge.RegisterRequest{
		WorkspaceName:        clusterName,
		WorkspaceDir:         workspaceDir,
		ClusterName:          clusterName,
		Region:               region,
		Kubeconfig:           kubeconfig,
		PostRegisterScan:     false, // scan is out of scope for slice 4
		CredentialTemplateID: credTplID,
	}

	// Ensure forge client is available.
	if clients.ForgeClient == nil {
		clients.AttachForgeClient(true, mcpURL)
	}

	// 4 attempts with 1s/3s/9s backoff between them (attempt 1, sleep 1s,
	// attempt 2, sleep 3s, attempt 3, sleep 9s, attempt 4).
	backoff := []time.Duration{1 * time.Second, 3 * time.Second, 9 * time.Second}
	var lastErr error
	for i := 0; i < 4; i++ {
		var res forge.RegisterResult
		res, lastErr = tryForgeRegister(ctx, clients.ForgeClient, forgeURL, req, restCreds)
		if lastErr == nil {
			fmt.Fprintf(os.Stderr, "[phase 09] registered with forge — project=%d cluster=%d\n",
				res.Link.ProjectID, res.Link.ClusterID)
			setForgeState(st, res.Link, workspaceDir)
			return st.Save()
		}
		fmt.Fprintf(os.Stderr, "[phase 09] forge registration attempt %d failed: %v\n", i+1, lastErr)
		if i < len(backoff) {
			select {
			case <-time.After(backoff[i]):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	// All retries failed — write pending link so operator can retry manually.
	fmt.Fprintf(os.Stderr, "[phase 09] forge handoff failed after 3 retries — wrote forge-link.json with status=pending; run 'awsbnkctl forge register' to retry\n")
	pendingLink := &forge.Link{
		Workspace: clusterName,
		Status:    "pending",
	}
	if werr := forge.WriteLink(workspaceDir, pendingLink); werr != nil {
		fmt.Fprintf(os.Stderr, "[phase 09] warning: could not write pending forge-link.json: %v\n", werr)
	} else {
		st.Set("FORGE_STATUS", "pending")
		st.Set("FORGE_LINK_PATH", filepath.Join(workspaceDir, forge.LinkFileName))
	}
	// Soft-fail: return nil so up continues.
	return nil
}

// Phase09ForgeRegisterDown unregisters the EKS cluster from forge as part of
// the phased destroy sequence. Runs between Phase10NodeGroupDown and
// Phase08EKSClusterDown.
//
// D-005: CheckAuthOrDie is called at entry even though forge calls do not
// touch AWS.
func Phase09ForgeRegisterDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, keepLink bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)

	if keepLink {
		fmt.Fprintln(os.Stderr, "[phase 09 down] forge: --keep-forge-link, preserving link")
		return nil
	}

	workspaceDir := cl.StateDir()
	link, err := forge.ReadLink(workspaceDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "[phase 09 down] forge: no link, nothing to unregister")
			return nil
		}
		return fmt.Errorf("phase09 down: read forge link: %w", err)
	}

	// Resolve forge URLs and credentials: prefer cluster.yaml values, fall
	// back to cached link fields / defaults. This handles the case where the
	// operator removed the forge: block from cluster.yaml between up and down
	// — cl.Forge is nil but a forge-link.json still exists with the original URLs.
	forgeURL := intent.DefaultForgeRESTURL
	mcpURL := forge.DefaultMCPURL
	var restCreds forge.RestCreds
	if cl.Forge != nil {
		forgeURL = cl.Forge.ResolveURL()
		if cl.Forge.MCPURL != "" {
			mcpURL = cl.Forge.MCPURL
		}
		forgeUsername := cl.Forge.ResolveUsername()
		forgePassword, usingDefaultPwd := cl.Forge.ResolvePassword()
		if usingDefaultPwd {
			fmt.Fprintln(os.Stderr, "[forge] using default password; set AWSBNKCTL_FORGE_PASSWORD or forge.password for your deployment")
		}
		restCreds = forge.RestCreds{Username: forgeUsername, Password: forgePassword}
	} else {
		// cl.Forge is nil — use URLs cached in the link file if available.
		if link.ForgeURL != "" {
			forgeURL = link.ForgeURL
		}
		if link.ForgeMCPURL != "" {
			mcpURL = link.ForgeMCPURL
		}
		// Credentials: resolve from env only (no ForgeSpec to read yaml from).
		forgePassword, usingDefaultPwd := (*intent.ForgeSpec)(nil).ResolvePassword()
		if usingDefaultPwd {
			fmt.Fprintln(os.Stderr, "[forge] using default password; set AWSBNKCTL_FORGE_PASSWORD or forge.password for your deployment")
		}
		restCreds = forge.RestCreds{
			Username: (*intent.ForgeSpec)(nil).ResolveUsername(),
			Password: forgePassword,
		}
	}

	if clients.ForgeClient == nil {
		clients.AttachForgeClient(true, mcpURL)
	}

	// Try MCP unregister first.
	mcpErr := forge.Unregister(ctx, clients.ForgeClient, workspaceDir, false)
	if mcpErr == nil {
		fmt.Fprintln(os.Stderr, "[phase 09 down] forge: unregistered via MCP")
		st.Set("FORGE_STATUS", "")
		st.Set("FORGE_PROJECT_ID", "")
		st.Set("FORGE_CLUSTER_ID", "")
		st.Set("FORGE_LINK_PATH", "")
		return st.Save()
	}

	// MCP failed — fall back to REST only for catalog-gap errors (mirrors up path).
	// For non-catalog-gap errors (auth, connectivity), skip REST and soft-fail.
	if forge.IsMCPCatalogGapErr(mcpErr) {
		restErr := forge.UnregisterREST(ctx, forgeURL, link, restCreds)
		if restErr == nil || is404ByString(restErr) {
			// REST succeeded (or 404 — already gone forge-side).
			if rerr := forge.RemoveLink(workspaceDir); rerr != nil {
				fmt.Fprintf(os.Stderr, "[phase 09 down] warning: could not remove forge-link.json: %v\n", rerr)
			}
			fmt.Fprintln(os.Stderr, "[phase 09 down] forge: unregistered via REST fallback")
			st.Set("FORGE_STATUS", "")
			st.Set("FORGE_PROJECT_ID", "")
			st.Set("FORGE_CLUSTER_ID", "")
			st.Set("FORGE_LINK_PATH", "")
			return st.Save()
		}
		// Both paths failed — log and keep the link file. Don't block teardown.
		fmt.Fprintf(os.Stderr, "[phase 09 down] warning: forge unregister failed (MCP: %v; REST: %v) — link preserved for manual cleanup\n",
			mcpErr, restErr)
		return nil
	}

	// Non-catalog-gap MCP failure — soft-fail immediately, keep link.
	fmt.Fprintf(os.Stderr, "[phase 09 down] warning: forge unregister failed (%v) — link preserved\n", mcpErr)
	return nil
}

// tryForgeRegister attempts MCP-first registration with REST fallback on
// catalog-gap errors. Returns the RegisterResult on success.
func tryForgeRegister(ctx context.Context, c *forge.Client, restURL string, req forge.RegisterRequest, creds forge.RestCreds) (forge.RegisterResult, error) {
	res, err := forge.Register(ctx, c, req)
	if err == nil {
		return res, nil
	}
	if !forge.IsMCPCatalogGapErr(err) {
		return forge.RegisterResult{}, err
	}
	// MCP catalog gap — fall back to REST.
	fmt.Fprintf(os.Stderr, "[phase 09] MCP catalog gap detected (%v) — falling back to REST\n", err)
	res, err = forge.RegisterREST(ctx, restURL, req, creds)
	if err != nil {
		return forge.RegisterResult{}, err
	}
	fmt.Fprintln(os.Stderr, "[phase 09] registered via REST (MCP fallback)")
	return res, nil
}

// setForgeState writes forge registration state keys.
func setForgeState(st *state.State, link *forge.Link, workspaceDir string) {
	st.Set("FORGE_PROJECT_ID", fmt.Sprintf("%d", link.ProjectID))
	st.Set("FORGE_CLUSTER_ID", fmt.Sprintf("%d", link.ClusterID))
	st.Set("FORGE_LINK_PATH", filepath.Join(workspaceDir, forge.LinkFileName))
	status := link.Status
	if status == "" {
		status = "registered"
	}
	st.Set("FORGE_STATUS", status)
}

// is404ByString checks whether an error message indicates a 404 response,
// used to tolerate "already gone" during forge unregister via REST.
func is404ByString(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "404") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "not_found")
}
