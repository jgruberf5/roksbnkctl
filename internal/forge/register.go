package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// RegisterRequest carries everything `forge register` needs from the
// caller. The caller is responsible for resolving the workspace dir and
// providing the kubeconfig — this package doesn't reach into config/.
type RegisterRequest struct {
	WorkspaceName string // logical workspace label (used in project name)
	WorkspaceDir  string // filesystem dir where forge_link.json lives
	ProjectName   string // forge project name; empty → "awsbnkctl-<workspace>"
	ClusterName   string // EKS cluster name registered into forge
	Region        string // AWS region
	Kubeconfig    []byte // raw YAML for the EKS cluster

	// AWSProfile is the named AWS profile forge should use when minting
	// EKS bearer tokens for this cluster. When empty, Register fills it
	// from os.Getenv("AWS_PROFILE") so the forge backend records the
	// same identity the operator used to provision the cluster — without
	// it forge falls back to its own global env, which silently breaks
	// for clusters outside the backend's default region. Set to "-" to
	// explicitly skip sending an AWS profile.
	AWSProfile string

	// CredentialTemplateID is the forge credential template ID to attach to
	// the newly-created project. When 0, no credential is attached and forge
	// falls back to its default (typically the operator must wire it manually).
	CredentialTemplateID int

	// If true, after CreateCluster awsbnkctl calls scan_cluster +
	// bnk_health to seed forge's view and smoke-test the link.
	PostRegisterScan bool
}

// RegisterResult is what forge register returns to the CLI for display.
type RegisterResult struct {
	Link        *Link
	ScanOutput  string // raw JSON from scan_cluster (empty if skipped)
	HealthCheck string // raw JSON from bnk_health (empty if skipped)
	ForgeURL    string
}

// Register executes the awsbnkctl→forge handoff:
//
//  1. Reuse an existing link if one exists (idempotent — re-running
//     `forge register` should not duplicate projects/clusters).
//  2. Otherwise: create_project → create_cluster.
//  3. Optionally: scan_cluster + bnk_health smoke check.
//  4. Write forge_link.json so `forge status` / `forge unregister` work.
//
// If the workspace already has a link, Register returns it unchanged
// after a get_cluster sanity check.
func Register(ctx context.Context, c *Client, req RegisterRequest) (RegisterResult, error) {
	if req.WorkspaceName == "" {
		return RegisterResult{}, errors.New("forge.Register: workspace name is required")
	}
	if req.WorkspaceDir == "" {
		return RegisterResult{}, errors.New("forge.Register: workspace dir is required")
	}
	if req.ClusterName == "" {
		return RegisterResult{}, errors.New("forge.Register: cluster name is required")
	}
	if len(req.Kubeconfig) == 0 {
		return RegisterResult{}, errors.New("forge.Register: kubeconfig is empty")
	}

	if req.ProjectName == "" {
		req.ProjectName = "awsbnkctl-" + req.WorkspaceName
	}
	if req.AWSProfile == "" {
		req.AWSProfile = os.Getenv("AWS_PROFILE")
	}

	// Idempotency: existing link wins — but only when fully registered.
	// A "pending" link means the forge handoff failed previously; treat it
	// the same as no link so we fall through to the create_project →
	// create_cluster path and complete the registration.
	if existing, err := ReadLink(req.WorkspaceDir); err == nil {
		if existing.IsRegistered() {
			if _, gerr := c.GetCluster(ctx, existing.ClusterID); gerr != nil {
				return RegisterResult{Link: existing, ForgeURL: c.URL()},
					fmt.Errorf("workspace already linked to forge cluster_id=%d but get_cluster failed: %w",
						existing.ClusterID, gerr)
			}
			return RegisterResult{Link: existing, ForgeURL: c.URL()}, nil
		}
		// Link exists but is not registered (e.g. Status=="pending") — fall through.
	} else if !errors.Is(err, os.ErrNotExist) {
		return RegisterResult{}, fmt.Errorf("read existing link: %w", err)
	}

	// 1) create project
	proj, err := c.CreateProject(ctx, CreateProjectRequest{
		Name:                 req.ProjectName,
		ProjectType:          "cloud-aws",
		CloudProvider:        "aws",
		Region:               req.Region,
		AWSProfile:           sendableAWSProfile(req.AWSProfile),
		Environment:          "dev",
		Description:          fmt.Sprintf("Created by awsbnkctl for workspace %q", req.WorkspaceName),
		CredentialTemplateID: req.CredentialTemplateID,
	})
	if err != nil {
		return RegisterResult{}, fmt.Errorf("create_project: %w", err)
	}

	// 2) create cluster
	cluster, err := c.CreateCluster(ctx, CreateClusterRequest{
		ProjectID:     proj.Project.ID,
		Name:          req.ClusterName,
		CloudProvider: "aws",
		Region:        req.Region,
	}, req.Kubeconfig)
	if err != nil {
		// Cluster create failed — try to roll back the project so
		// re-running doesn't leave half-states. Best-effort.
		_ = c.DeleteProject(ctx, proj.Project.ID, true)
		return RegisterResult{}, fmt.Errorf("create_cluster: %w", err)
	}

	link := &Link{
		ForgeMCPURL:  c.URL(),
		ProjectID:    proj.Project.ID,
		ProjectName:  proj.Project.Name,
		ClusterID:    cluster.Cluster.ID,
		ClusterName:  cluster.Cluster.Name,
		RegisteredAt: time.Now().UTC(),
		Workspace:    req.WorkspaceName,
	}
	if err := WriteLink(req.WorkspaceDir, link); err != nil {
		return RegisterResult{Link: link, ForgeURL: c.URL()},
			fmt.Errorf("registration succeeded but writing forge_link.json failed: %w", err)
	}

	out := RegisterResult{Link: link, ForgeURL: c.URL()}

	// 3) optional post-register scan + smoke check
	if req.PostRegisterScan {
		if s, err := c.ScanCluster(ctx, cluster.Cluster.ID); err != nil {
			out.ScanOutput = "<scan_cluster failed: " + err.Error() + ">"
		} else {
			out.ScanOutput = s
		}
		if h, err := c.BNKHealth(ctx, cluster.Cluster.ID); err != nil {
			out.HealthCheck = "<bnk_health failed: " + err.Error() + ">"
		} else {
			out.HealthCheck = h
		}
	}

	return out, nil
}

// sendableAWSProfile returns the profile string actually transmitted to
// forge. Callers use "-" to opt out of sending a profile (forge will then
// fall back to its backend-global env); any other empty / dash variant
// is treated as "no profile set" and returns "".
func sendableAWSProfile(profile string) string {
	if profile == "-" || profile == "" {
		return ""
	}
	return profile
}

// Unregister tears down the forge-side registration for a workspace.
// It always removes the local link file on success.
//
//	purge=false → delete cluster only (default; preserves the project for
//	  any other clusters or modules the operator may have added).
//	purge=true  → delete cluster AND project (force=true).
//
// Returns os.ErrNotExist if the workspace has never been registered.
func Unregister(ctx context.Context, c *Client, workspaceDir string, purge bool) error {
	link, err := ReadLink(workspaceDir)
	if err != nil {
		return err
	}

	if err := c.DeleteCluster(ctx, link.ClusterID); err != nil {
		// 404 is fine — the cluster was already gone forge-side.
		if !is404(err) {
			return fmt.Errorf("delete_cluster: %w", err)
		}
	}
	if purge {
		if err := c.DeleteProject(ctx, link.ProjectID, true); err != nil && !is404(err) {
			return fmt.Errorf("delete_project: %w", err)
		}
	}
	if err := RemoveLink(workspaceDir); err != nil {
		return fmt.Errorf("remove forge_link.json: %w", err)
	}
	return nil
}

// StatusResult is what `forge status` returns.
type StatusResult struct {
	Link          *Link
	Reachable     bool
	ForgeVersion  string
	ClusterDetail string // raw get_cluster JSON
	Err           error
}

// Status returns the current state of the workspace's forge link.
// Returns os.ErrNotExist if there is no link.
func Status(ctx context.Context, c *Client, workspaceDir string) (StatusResult, error) {
	link, err := ReadLink(workspaceDir)
	if err != nil {
		return StatusResult{}, err
	}
	out := StatusResult{Link: link}

	if v, err := c.SystemVersion(ctx); err == nil {
		out.Reachable = true
		out.ForgeVersion = v
	} else {
		out.Err = fmt.Errorf("forge unreachable at %s: %w", c.URL(), err)
		return out, nil
	}
	if d, err := c.GetCluster(ctx, link.ClusterID); err == nil {
		out.ClusterDetail = d
	} else {
		out.Err = fmt.Errorf("get_cluster(%d): %w", link.ClusterID, err)
	}
	return out, nil
}

// is404 is a heuristic for the not-found case — MCP tool errors are
// flattened to strings, so we sniff for the conventional markers.
func is404(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not_found") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "http 404") ||
		strings.Contains(s, "\"status_code\": 404")
}
