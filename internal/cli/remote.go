package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// clientRunner adapts *remote.Client to the remoteRunner seam the
// kubeconfig self-heal (selfheal.go) uses, so the heal-vs-outage logic
// is unit-testable without a real SSH connection. It runs short
// non-interactive probe/heal commands (no stdin, no TTY).
type clientRunner struct{ c *remote.Client }

func (cr clientRunner) Run(ctx context.Context, argv []string, stdout, stderr io.Writer) (int, error) {
	return cr.c.Run(ctx, argv, remote.RunOpts{Stdout: stdout, Stderr: stderr})
}

// rejectOnFlag is the "lifecycle commands don't support --on" gate. Used
// by up/down/plan/apply/init's RunE so the user gets a clear pointer to
// PRD 03 instead of a confusing local-exec attempt.
//
// Phase 3 (PRD 03) is where SSH becomes a backend rather than a one-shot
// dispatch — when that lands, this gate goes away.
func rejectOnFlag(cmdName string) error {
	if flagOn == "" {
		return nil
	}
	return fmt.Errorf("--on not supported on `roksbnkctl %s` in v0.7. Use --backend ssh in a future release once Phase 3 lands (see docs/prd/03-EXECUTION-BACKENDS.md)", cmdName)
}

// dispatchRemote opens an SSH connection to the named target, runs argv
// remotely, streams I/O, and exits roksbnkctl with the remote process's
// exit code (or with 126/127 on auth/connect failures per PRD 01).
//
// envExtra MUST be machine-portable (values, not local filesystem
// paths): IBMCLOUD_API_KEY / IC_API_KEY / IBMCLOUD_REGION /
// IBMCLOUD_VERSION_CHECK. Callers source workspaceEnvCore() (NOT
// workspaceEnv(), which appends the local-only KUBECONFIG path).
//
// Sprint 15 chokepoint: the core-vs-local-only split now has exactly
// ONE classification (orchestration.LocalOnlyEnvKeys), consumed by both
// workspaceEnvCore (the local-side scrub of os.Environ) and this single
// boundary assertion at the SSH wire. The previous scattered
// defense-in-depth scrub list is gone; this one assertion — applied at
// the single point every --on dispatch funnels through, just before
// bytes cross the wire — is the structural guarantee that no local path
// is ever sent, cheaper than proving every DisableFlagParsing
// passthrough caller can't construct one. Correctness comes from never
// sending a local path, not from the target sshd's AcceptEnv.
//
// The remote sshd's AcceptEnv config decides which of the remaining
// (machine-portable) vars actually pass through; users who hit
// "ibmcloud not logged in" on the remote should configure AcceptEnv on
// the jumphost (see chapter 16, "Behaviour details" in
// book/src/16-on-flag-ssh-jumphosts.md).
//
// On success this function does NOT return — it calls os.Exit. The
// remote-side exit code is the only useful thing for scripts and CI.
func dispatchRemote(ctx context.Context, target string, argv []string, envExtra []string, tty bool) error {
	// THE single SSH-boundary assertion (Sprint 15 chokepoint): strip
	// every local-path-valued var per the one
	// orchestration.LocalOnlyEnvKeys classification, here at the one
	// point every --on dispatch funnels through, just before bytes cross
	// the wire. No-op when callers pass workspaceEnvCore() (the common
	// case); the guarantee against the Sprint 13 Issue 1 class is
	// structural — it does not depend on proving every passthrough
	// caller's env construction.
	envExtra = remoteSafeEnv(envExtra)

	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}

	t, err := remote.LoadTarget(cctx.WorkspaceName, target)
	if err != nil {
		if errors.Is(err, remote.ErrTargetNotFound) {
			return fmt.Errorf("%w (try `roksbnkctl targets list`)", err)
		}
		return err
	}

	tfOutputs, err := loadTFOutputsForTarget(ctx, cctx, t)
	if err != nil {
		// tf-output: keys can't resolve without the outputs map; fail
		// closed. Other key sources don't need it but the helper is
		// best-effort and only errors on a true read failure.
		return err
	}

	signer, err := remote.ResolveSigner(t, tfOutputs)
	if err != nil {
		return err
	}
	t.Signer = signer
	t.HostKeyCallback = remote.HostKeyCallback(remote.HostKeyOptions{Insecure: flagInsecureHostKey})

	client, err := remote.Connect(ctx, t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "roksbnkctl: connect %s: %v\n", t.Name, err)
		os.Exit(remote.ExitConnectFailed)
	}
	defer client.Close()

	// Sprint 14 / option C part B — remote kubeconfig self-heal. For an
	// `--on <target> kubectl|oc` dispatch, ensure the target actually
	// has a usable kubeconfig before running the wrapped command;
	// repair it on the fly if missing (the part-A cloud-init hardening
	// only helps NEW deploys — this unblocks already-broken/already-
	// running jumphosts with no `terraform` recreate). No-op for
	// non-kubectl/oc argv. Cluster id comes from the same workspace
	// plumbing tryAutoKubeconfig uses — NOT re-derived at the CLI layer
	// (the Sprint 12/13 path-re-derivation bug class). A heal failure
	// (genuine outage) aborts with a clear error rather than letting
	// kubectl fall back to localhost:8080.
	if kubectlOrOC(argv) {
		clusterID := clusterFromTFOutput(ctx, cctx)
		if clusterID == "" && cctx.Workspace != nil {
			clusterID = cctx.Workspace.Cluster.Name
		}
		// Resolve the workspace credentials so the self-heal can
		// (re)authenticate the target's ibmcloud CLI before `ks cluster
		// config --admin` — an already-broken jumphost whose cloud-init
		// login fork failed silently needs the login healed too, not just
		// the kubeconfig (Sprint 14 / issues/issue_sprint14_staff.md
		// Issue 1, 2026-05-18 live finding). Same resolver workspaceEnvCore
		// uses. Best-effort: a resolve failure passes an empty key (the
		// heal then falls back to assume-pre-logged-in and, if that's
		// false, surfaces the real ibmcloud error — no worse than before,
		// and an optional self-heal must not abort the dispatch).
		var apiKey, region, resourceGroup string
		if cctx.Workspace != nil {
			region = cctx.Workspace.IBMCloud.Region
			resourceGroup = cctx.Workspace.IBMCloud.ResourceGroup
			resolver := &cred.Resolver{
				Workspace: cctx.WorkspaceName,
				Source:    cctx.Workspace.IBMCloud.APIKeySource,
			}
			if k, kerr := resolver.IBMCloudAPIKey(ctx); kerr == nil {
				apiKey = k
			}
		}
		if herr := maybeSelfHealRemoteKubeconfig(ctx, clientRunner{client}, argv, clusterID, apiKey, region, resourceGroup); herr != nil {
			fmt.Fprintf(os.Stderr, "roksbnkctl: remote kubeconfig self-heal: %v\n", herr)
			os.Exit(remote.ExitAuthFailed)
		}
	}

	code, err := client.Run(ctx, argv, remote.RunOpts{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Env:    envExtra,
		TTY:    tty,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "roksbnkctl: remote run: %v\n", err)
		os.Exit(remote.ExitAuthFailed)
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// dispatchRemoteShell opens an interactive PTY shell on the target.
func dispatchRemoteShell(ctx context.Context, target string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}
	t, err := remote.LoadTarget(cctx.WorkspaceName, target)
	if err != nil {
		if errors.Is(err, remote.ErrTargetNotFound) {
			return fmt.Errorf("%w (try `roksbnkctl targets list`)", err)
		}
		return err
	}
	tfOutputs, err := loadTFOutputsForTarget(ctx, cctx, t)
	if err != nil {
		return err
	}
	signer, err := remote.ResolveSigner(t, tfOutputs)
	if err != nil {
		return err
	}
	t.Signer = signer
	t.HostKeyCallback = remote.HostKeyCallback(remote.HostKeyOptions{Insecure: flagInsecureHostKey})

	client, err := remote.Connect(ctx, t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "roksbnkctl: connect %s: %v\n", t.Name, err)
		os.Exit(remote.ExitConnectFailed)
	}
	defer client.Close()

	return client.Shell(ctx, remote.ShellOpts{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
}

// loadTFOutputsForTarget pulls a flat map[string]string of TF outputs
// when the target's KeySource is "tf-output:<name>". Returns nil for
// every other source (we don't need TF state, so don't open it).
//
// On open / read errors when we DO need outputs, returns the error so
// the caller fails fast — silently falling back to "key not found"
// produces a confusing downstream message.
func loadTFOutputsForTarget(ctx context.Context, cctx *config.Context, t *remote.Target) (map[string]string, error) {
	if t == nil || !needsTFOutputs(t) {
		return nil, nil
	}
	stateDir, err := config.WorkspaceStateDir(cctx.WorkspaceName)
	if err != nil {
		return nil, err
	}
	tfws, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, stateDir, "", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("opening tf workspace: %w", err)
	}
	outs, err := tfws.Output(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading tf outputs: %w", err)
	}
	flat := make(map[string]string, len(outs))
	for k, v := range outs {
		var s string
		if json.Unmarshal(v.Value, &s) == nil {
			flat[k] = s
		}
	}
	return flat, nil
}

func needsTFOutputs(t *remote.Target) bool {
	if t == nil {
		return false
	}
	return t.KeyPath == "" && t.KeySource != "" && t.KeySource != "agent"
}

func init() {
	// Wire the SSH backend's target resolver to the same tf-output-aware
	// signer the legacy --on path uses. The exec package can't import
	// internal/cli (cycle), so the cli layer pushes a fully-resolved
	// target back into the backend via SetSSHTargetResolver.
	//
	// PRD 03 §"SSH" — backend resolves its target identically to --on so
	// users don't have to maintain two key-resolution paths.
	execbackend.SetSSHTargetResolver(func(workspace, name string) (*remote.Target, map[string][]byte, error) {
		if workspace == "" {
			return nil, nil, fmt.Errorf("ssh backend: no workspace set")
		}
		t, err := remote.LoadTarget(workspace, name)
		if err != nil {
			return nil, nil, err
		}
		// Reload workspace cctx so loadTFOutputsForTarget can pull
		// outputs (only needed for the tf-output: key source).
		cctx, err := config.New(workspace)
		if err != nil {
			return nil, nil, err
		}
		tfOutputs, err := loadTFOutputsForTarget(context.Background(), cctx, t)
		if err != nil {
			return nil, nil, err
		}
		signer, err := remote.ResolveSigner(t, tfOutputs)
		if err != nil {
			return nil, nil, err
		}
		t.Signer = signer
		t.HostKeyCallback = remote.HostKeyCallback(remote.HostKeyOptions{Insecure: flagInsecureHostKey})
		return t, nil, nil
	})
}
