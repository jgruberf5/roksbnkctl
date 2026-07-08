package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/forge"
)

// Env vars for BNK Forge connection details (parallel to the flags). The
// password is ONLY ever read from the env or an interactive prompt — never
// persisted; the resulting session token is cached in the OS keychain instead.
const (
	envForgeURL      = "BNK_FORGE_URL"
	envForgeUser     = "BNK_FORGE_USER"
	envForgePassword = "BNK_FORGE_PASSWORD"
)

// tryRegisterBNKForge is a best-effort post-`cluster up` hook. When the
// workspace opts in (config.yaml `bnkforge.register: true`), it registers the
// just-provisioned ROKS cluster with a co-located BNK Forge (v3) install via
// its REST API — credential-backed, so BNK Forge derives the kubeconfig on
// demand from an IBM Cloud credential template (no perishable kubeconfig stored).
//
// It NEVER fails the deploy: a missing URL/credentials or a registration error
// is a one-line note, not an error — the cluster is already up either way, and
// the operator can re-run `roksbnkctl bnkforge register` later. Runs
// non-interactively (credentials from env / the cached token only).
func tryRegisterBNKForge(ctx context.Context, cctx *config.Context) {
	if cctx == nil || cctx.Workspace == nil || cctx.Workspace.BNKForge == nil || !cctx.Workspace.BNKForge.Register {
		return
	}
	if err := registerWithBNKForge(ctx, cctx, cctx.Workspace.BNKForge, false); err != nil {
		fmt.Fprintf(os.Stderr, "→ BNK Forge registration didn't complete (%v) — the cluster is up; register later with `roksbnkctl bnkforge register`.\n", err)
	}
}

// registerWithBNKForge registers the workspace's cluster with BNK Forge v3 via
// its REST API. Shared by the best-effort post-`cluster up` hook (which discards
// the error) and the explicit `roksbnkctl bnkforge register` command (which
// surfaces it). bf may be nil.
//
// interactive=true permits prompting for the username / password; the auto-hook
// passes false, so it relies on env vars (BNK_FORGE_USER/PASSWORD) or a cached
// session token and otherwise errors rather than blocking a deploy on a prompt.
func registerWithBNKForge(ctx context.Context, cctx *config.Context, bf *config.BNKForgeCfg, interactive bool) error {
	if cctx == nil || cctx.Workspace == nil {
		return fmt.Errorf("no workspace context")
	}
	if bf == nil {
		bf = &config.BNKForgeCfg{}
	}

	url := bf.URL
	if url == "" {
		url = os.Getenv(envForgeURL)
	}
	if url == "" {
		return fmt.Errorf("no BNK Forge URL (set bnkforge.url, %s, or --url)", envForgeURL)
	}

	out, err := config.ReadClusterOutputs(cctx.WorkspaceName)
	if err != nil || out.ClusterID == "" {
		return fmt.Errorf("no cluster id recorded in cluster-outputs.json yet — run `roksbnkctl cluster up` first")
	}
	name := out.ClusterName
	if name == "" {
		name = cctx.WorkspaceName
	}

	// IBM Cloud API key — stored in Forge as a credential template so Forge can
	// derive the cluster's kubeconfig on demand.
	apiKey, err := (&cred.Resolver{
		Workspace:      cctx.WorkspaceName,
		Source:         cctx.Workspace.IBMCloud.APIKeySource,
		NonInteractive: !interactive,
	}).IBMCloudAPIKey(ctx)
	if err != nil {
		return fmt.Errorf("resolving IBM Cloud API key: %w", err)
	}

	client := forge.New(url, bf.Insecure)

	// Auth: reuse a cached session token if still valid; else log in and cache
	// the new token. The password itself is never persisted.
	client.Token = config.ForgeTokenFromKeychain(cctx.WorkspaceName)
	if !client.TokenValid(ctx) {
		user := os.Getenv(envForgeUser)
		if user == "" {
			user = bf.Username
		}
		if user == "" && interactive {
			user = promptString("BNK Forge username", "")
		}
		if user == "" {
			return fmt.Errorf("no BNK Forge username (set bnkforge.username, %s, or --username)", envForgeUser)
		}
		pass, perr := resolveForgePassword(interactive)
		if perr != nil {
			return perr
		}
		if lerr := client.Login(ctx, user, pass); lerr != nil {
			return fmt.Errorf("BNK Forge login failed: %w", lerr)
		}
		if serr := config.SaveForgeTokenToKeychain(cctx.WorkspaceName, client.Token); serr != nil {
			fmt.Fprintf(os.Stderr, "  note: could not cache the Forge session token (%v) — will re-authenticate next time\n", serr)
		}
	}

	fmt.Fprintf(os.Stderr, "→ Registering cluster %q with BNK Forge (%s)…\n", name, url)

	rg := cctx.Workspace.IBMCloud.ResourceGroup
	if rg == "" {
		rg = "default"
	}
	tid, err := client.EnsureIBMCredentialTemplate(ctx, "roksbnkctl-"+cctx.WorkspaceName, apiKey, rg)
	if err != nil {
		return fmt.Errorf("ensuring IBM credential template: %w", err)
	}

	projName := bf.Project
	if projName == "" {
		projName = cctx.WorkspaceName
	}
	pid, err := client.EnsureProject(ctx, projName)
	if err != nil {
		return fmt.Errorf("ensuring project %q: %w", projName, err)
	}

	fid, err := client.RegisterCluster(ctx, pid, forge.RegisterRequest{
		Name:       name,
		Provider:   "IBM",
		ClusterID:  out.ClusterID,
		Region:     out.Region,
		TemplateID: tid,
	})
	if err != nil {
		return fmt.Errorf("registering cluster with BNK Forge: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ Registered cluster %q with BNK Forge (project %q, forge cluster id %d).\n", name, projName, fid)
	return nil
}

// resolveForgePassword returns the BNK Forge password: BNK_FORGE_PASSWORD env,
// else a hidden interactive prompt. Never persisted.
func resolveForgePassword(interactive bool) (string, error) {
	if p := os.Getenv(envForgePassword); p != "" {
		return p, nil
	}
	if !interactive || !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("no BNK Forge password (set %s, or run `roksbnkctl bnkforge register` at a terminal)", envForgePassword)
	}
	fmt.Fprint(os.Stderr, "Enter BNK Forge password: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading BNK Forge password: %w", err)
	}
	p := strings.TrimSpace(string(b))
	if p == "" {
		return "", errors.New("empty BNK Forge password")
	}
	return p, nil
}
