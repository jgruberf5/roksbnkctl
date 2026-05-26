package cli

import (
	"fmt"

	execbackend "github.com/JLCode-tech/awsbnkctl/internal/exec"
	"github.com/JLCode-tech/awsbnkctl/internal/remote"
)

func init() {
	// Wire the SSH backend's target resolver to the signer the legacy
	// --on path uses. The exec package can't import internal/cli (cycle),
	// so the cli layer pushes a fully-resolved target back into the backend
	// via SetSSHTargetResolver.
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
		// Pass nil for tfOutputs — the tf-output: key source is removed.
		// Non-TF key sources (agent, key_path) do not need TF state.
		signer, err := remote.ResolveSigner(t, nil)
		if err != nil {
			return nil, nil, err
		}
		t.Signer = signer
		t.HostKeyCallback = remote.HostKeyCallback(remote.HostKeyOptions{Insecure: flagInsecureHostKey})
		return t, nil, nil
	})
}
