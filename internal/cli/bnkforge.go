package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
)

// tryRegisterBNKForge is a best-effort post-`cluster up` hook. When the
// workspace opts in (config.yaml `bnkforge.register: true`) and the `bnk-forge`
// CLI is installed, it registers the just-provisioned ROKS cluster with a
// co-located BNK Forge install — credential-backed, so BNK Forge derives the
// kubeconfig on demand from the IBM Cloud credential (no perishable kubeconfig
// gets stored).
//
// It NEVER fails the deploy: a missing CLI, no session, or a registration
// error is a one-line note, not an error — the cluster is already up either
// way, and the operator can re-run `bnk-forge clusters register` later.
//
// Division of labour: all BNK Forge knowledge — auth resolution (its stored
// ~/.bnk-forge/config.json session, or an interactive login for a separate
// session), credential-template select-or-create, and the registration call —
// lives in `bnk-forge clusters register`. roksbnkctl only supplies the cluster
// facts (id, region, name) and the IBM Cloud API key (so the CLI can create a
// credential template if one doesn't exist yet).
func tryRegisterBNKForge(ctx context.Context, cctx *config.Context) {
	if cctx == nil || cctx.Workspace == nil || cctx.Workspace.BNKForge == nil || !cctx.Workspace.BNKForge.Register {
		return
	}
	// The auto-hook swallows every error into a one-line note: the cluster is
	// already up, and the operator can re-run `roksbnkctl bnkforge register`.
	if err := registerWithBNKForge(ctx, cctx, cctx.Workspace.BNKForge); err != nil {
		fmt.Fprintf(os.Stderr, "→ BNK Forge registration didn't complete (%v) — the cluster is up; register later with `roksbnkctl bnkforge register`.\n", err)
	}
}

// registerWithBNKForge performs the actual credential-backed registration by
// shelling out to `bnk-forge clusters register`. It's shared by the best-effort
// post-`cluster up` hook (tryRegisterBNKForge, which discards the error) and the
// explicit `roksbnkctl bnkforge register` command (which surfaces it). bf may be
// nil — the on-demand command passes effective overrides that aren't persisted.
//
// It returns an error on a genuine failure (CLI absent, no recorded cluster id,
// or a non-zero exit) rather than printing a note itself, so the caller decides
// how loud to be.
func registerWithBNKForge(ctx context.Context, cctx *config.Context, bf *config.BNKForgeCfg) error {
	if cctx == nil || cctx.Workspace == nil {
		return fmt.Errorf("no workspace context")
	}
	if bf == nil {
		bf = &config.BNKForgeCfg{}
	}

	bin, err := exec.LookPath("bnk-forge")
	if err != nil {
		return fmt.Errorf("the `bnk-forge` CLI is not on PATH (it ships with BNK Forge, not roksbnkctl)")
	}

	out, err := config.ReadClusterOutputs(cctx.WorkspaceName)
	if err != nil || out.ClusterID == "" {
		return fmt.Errorf("no cluster id recorded in cluster-outputs.json yet — run `roksbnkctl cluster up` first")
	}

	name := out.ClusterName
	if name == "" {
		name = cctx.WorkspaceName
	}

	args := []string{
		"clusters", "register",
		"--name", name,
		"--cluster-id", out.ClusterID,
		"--region", out.Region,
		"--provider", "IBM",
	}
	if bf.Project != "" {
		args = append(args, "--project", bf.Project)
	}
	if bf.URL != "" {
		args = append(args, "--url", bf.URL)
	}

	// Supply the IBM Cloud API key so the CLI can create a credential template
	// if the operator hasn't got one yet. Resolve NON-interactively (env →
	// keychain → config); if it can't be resolved without a prompt, leave it
	// to the CLI (which selects an existing template or asks for the key). The
	// child inherits our env, so a set IBMCLOUD_API_KEY already flows through.
	childEnv := os.Environ()
	resolver := &cred.Resolver{
		Workspace:      cctx.WorkspaceName,
		Source:         cctx.Workspace.IBMCloud.APIKeySource,
		NonInteractive: true,
	}
	if key, kerr := resolver.IBMCloudAPIKey(ctx); kerr == nil && key != "" {
		childEnv = append(childEnv, "IBMCLOUD_API_KEY="+key)
	}

	fmt.Fprintf(os.Stderr, "→ Registering cluster %q with BNK Forge…\n", name)
	c := exec.CommandContext(ctx, bin, args...)
	c.Env = childEnv
	c.Stdin = os.Stdin   // pass the TTY through for the login / select prompts
	c.Stdout = os.Stderr // keep BNK Forge's output on stderr; roksbnkctl's stdout stays clean
	c.Stderr = os.Stderr
	return c.Run()
}
