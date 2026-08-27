package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// force=false always here: this is the AUTOMATIC registration that runs as part of
	// cluster/bnk up. An unattended step must never silently take a cluster from another
	// project — that is exactly the harm in issue #54.
	if err := registerWithBNKForge(ctx, cctx, cctx.Workspace.BNKForge, false, false); err != nil {
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
func registerWithBNKForge(ctx context.Context, cctx *config.Context, bf *config.BNKForgeCfg, interactive, force bool) error {
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

	client, err := newForgeClientForWorkspace(ctx, cctx, bf, interactive)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "→ Registering cluster %q with BNK Forge (%s)…\n", name, url)

	rg := cctx.Workspace.IBMCloud.ResourceGroup
	if rg == "" {
		rg = "default"
	}
	tid, err := client.EnsureIBMCredentialTemplate(ctx, forge.IBMCredentialTemplate{
		Name:          "roksbnkctl-" + cctx.WorkspaceName,
		APIKey:        apiKey,
		ResourceGroup: rg,
		// Region and the COS instance come from the workspace so blueprint
		// inputs declaring `source: credential_template` have something to
		// inherit; without them Forge stores null and those inputs resolve to
		// nothing (#223).
		Region:      cctx.Workspace.IBMCloud.Region,
		COSInstance: cctx.Workspace.COS.Instance,
	})
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

	kubeconfig, err := readClusterKubeconfig()
	if err != nil {
		return err
	}

	fid, err := client.RegisterCluster(ctx, pid, forge.RegisterRequest{
		Name:          name,
		Provider:      "IBM",
		CloudProvider: "ibm", // the platform Forge displays (else shows "Unknown")
		ClusterID:     out.ClusterID,
		Region:        out.Region,
		TemplateID:    tid,
		Kubeconfig:    base64.StdEncoding.EncodeToString([]byte(kubeconfig)),
	}, force)
	if err != nil {
		return fmt.Errorf("registering cluster with BNK Forge: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ Registered cluster %q with BNK Forge (project %q, forge cluster id %d).\n", name, projName, fid)
	return nil
}

// resolveForgePassword returns the BNK Forge password: BNK_FORGE_PASSWORD env,
// else a hidden interactive prompt. Never persisted.
func resolveForgePassword(interactive bool, bf *config.BNKForgeCfg) (string, error) {
	// Precedence: env, then the workspace field, then the prompt. The env wins so
	// a CI runner can override a stored value without editing the file, matching
	// how every other credential in this tool resolves.
	if p := os.Getenv(envForgePassword); p != "" {
		return p, nil
	}
	if bf != nil && bf.PasswordB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(bf.PasswordB64)
		if err != nil {
			return "", fmt.Errorf("bnkforge.password_b64 is not valid base64: %w\n"+
				"  It holds the base64 of the password, not the password itself — "+
				"`printf %%s 'secret' | base64`", err)
		}
		if p := strings.TrimSpace(string(raw)); p != "" {
			return p, nil
		}
	}
	if !interactive || !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("no BNK Forge password (set %s, set bnkforge.password_b64, "+
			"or run `roksbnkctl bnkforge register` at a terminal)", envForgePassword)
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

// readClusterKubeconfig returns the cluster kubeconfig BNK Forge requires in the
// register body — the cert-based forge kubeconfig `cluster up` wrote (ROKS
// rejects token kubeconfigs), falling back to KUBECONFIG / ~/.kube/config.
func readClusterKubeconfig() (string, error) {
	if p, err := config.ForgeKubeconfigPath(); err == nil {
		if b, rerr := os.ReadFile(p); rerr == nil && len(b) > 0 {
			return string(b), nil
		}
	}
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		if b, err := os.ReadFile(kc); err == nil && len(b) > 0 {
			return string(b), nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".kube", "config")); err == nil && len(b) > 0 {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("no cluster kubeconfig found (forge kubeconfig, KUBECONFIG, or ~/.kube/config) — run `roksbnkctl cluster up` first")
}

// unregisterFromBNKForge removes this workspace's cluster from its Forge
// project. The mirror image of registerWithBNKForge, and deliberately quieter:
// a teardown runs when things are already half gone, so every "it was not
// there" is a success, not an error.
//
// It never creates anything. registerWithBNKForge calls EnsureProject, which
// will happily bring a project into existence; doing that on the way down would
// leave behind the very thing being removed.
func unregisterFromBNKForge(ctx context.Context, cctx *config.Context, bf *config.BNKForgeCfg, interactive bool) error {
	if cctx == nil || cctx.Workspace == nil {
		return fmt.Errorf("no workspace context")
	}
	if bf == nil {
		bf = &config.BNKForgeCfg{}
	}
	url := bf.URL
	if v := os.Getenv(envForgeURL); v != "" {
		url = v
	}
	if url == "" {
		return fmt.Errorf("no BNK Forge URL (set bnkforge.url, %s, or --url)", envForgeURL)
	}

	out, err := config.ReadClusterOutputs(cctx.WorkspaceName)
	name := ""
	if err == nil {
		name = out.ClusterName
	}
	if name == "" {
		name = cctx.WorkspaceName
	}

	client, err := newForgeClientForWorkspace(ctx, cctx, bf, interactive)
	if err != nil {
		return err
	}

	projName := bf.Project
	if projName == "" {
		projName = cctx.WorkspaceName
	}
	pid, err := client.ProjectIDByName(ctx, projName)
	if err != nil {
		return fmt.Errorf("looking up project %q: %w", projName, err)
	}
	if pid == 0 {
		fmt.Fprintf(os.Stderr, "✓ no BNK Forge project %q — nothing to unregister\n", projName)
		return nil
	}

	id, err := client.UnregisterCluster(ctx, pid, name)
	if err != nil {
		return fmt.Errorf("unregistering cluster %q from BNK Forge: %w", name, err)
	}
	if id == 0 {
		fmt.Fprintf(os.Stderr, "✓ cluster %q is not registered in project %q — nothing to do\n", name, projName)
		return nil
	}
	fmt.Fprintf(os.Stderr, "✓ unregistered cluster %q (id %d) from BNK Forge project %q\n", name, id, projName)
	return nil
}

// newForgeClient builds the Forge client for a workspace's settings, resolving
// the transport's trust once so both call sites cannot drift apart on it.
// bf must be non-nil — registerWithBNKForge and unregisterFromBNKForge both
// normalize a nil config at their entry.
//
// When both a pinned CA (bnkforge.ca_b64) and `insecure` are set, forge.New
// itself ignores Insecure (see forge.Options) — pinning authenticates the
// connection, disabling verification abandons it. That precedence is enforced
// in forge.New ALONE; here we only say it out loud, so a stale `insecure: true`
// in config.yaml doesn't leave the operator believing verification is off.
func newForgeClient(url string, bf *config.BNKForgeCfg) (*forge.Client, error) {
	opts := forge.Options{Insecure: bf.Insecure}
	if strings.TrimSpace(bf.CAB64) != "" {
		pem, err := config.DecodeB64Field("bnkforge.ca_b64", bf.CAB64)
		if err != nil {
			return nil, err
		}
		opts.CAPEM = pem
		if opts.Insecure {
			fmt.Fprintln(os.Stderr, "→ bnkforge: a CA is pinned (bnkforge.ca_b64), so `insecure` is ignored and the certificate IS verified.")
		}
	}
	return forge.New(url, opts)
}

// newForgeClientForWorkspace builds an authenticated Forge client for the
// workspace: cached session token if still valid, otherwise log in and cache the
// new one. The password itself is never persisted.
//
// Extracted from runBNKForgeRegister / runBNKForgeUnregister, which carried
// byte-identical copies of this block, so `bnkforge ssh-credential` did not
// become a third (#222).
func newForgeClientForWorkspace(ctx context.Context, cctx *config.Context, bf *config.BNKForgeCfg, interactive bool) (*forge.Client, error) {
	url := bf.URL
	if v := os.Getenv(envForgeURL); v != "" {
		url = v
	}
	if url == "" {
		return nil, fmt.Errorf("no BNK Forge URL (set bnkforge.url, %s, or --url)", envForgeURL)
	}
	client, err := newForgeClient(url, bf)
	if err != nil {
		return nil, err
	}
	client.Token = config.ForgeTokenFromKeychain(cctx.WorkspaceName)
	if client.TokenValid(ctx) {
		return client, nil
	}
	user := os.Getenv(envForgeUser)
	if user == "" {
		user = bf.Username
	}
	if user == "" && interactive {
		user = promptString("BNK Forge username", "")
	}
	if user == "" {
		return nil, fmt.Errorf("no BNK Forge username (set bnkforge.username, %s, or --username)", envForgeUser)
	}
	pass, perr := resolveForgePassword(interactive, bf)
	if perr != nil {
		return nil, perr
	}
	if lerr := client.Login(ctx, user, pass); lerr != nil {
		return nil, fmt.Errorf("BNK Forge login failed: %w", lerr)
	}
	if serr := config.SaveForgeTokenToKeychain(cctx.WorkspaceName, client.Token); serr != nil {
		fmt.Fprintf(os.Stderr, "  note: could not cache the Forge session token (%v) — will re-authenticate next time\n", serr)
	}
	return client, nil
}
