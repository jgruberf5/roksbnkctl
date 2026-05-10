package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

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
// envExtra is the workspace-derived KEY=VALUE list (IBMCLOUD_API_KEY,
// KUBECONFIG, etc.) that workspaceEnv() would have applied locally. The
// remote sshd's AcceptEnv config decides which actually pass through;
// users who hit "ibmcloud not logged in" on the remote should configure
// AcceptEnv on the jumphost (see chapter 16, "Behaviour details" in
// book/src/16-on-flag-ssh-jumphosts.md).
//
// On success this function does NOT return — it calls os.Exit. The
// remote-side exit code is the only useful thing for scripts and CI.
func dispatchRemote(ctx context.Context, target string, argv []string, envExtra []string, tty bool) error {
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
