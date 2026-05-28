package phases

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/demo/diameter"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

// demoDiameterClientPath is the remote path where diameter_client.py is staged.
const demoDiameterClientPath = "/home/ec2-user/demo/diameter_client.py"

// demoDiameterResponderPath is the remote path where responder.py is staged.
const demoDiameterResponderPath = "/home/ec2-user/demo/responder.py"

// Phase17dDemoStage is a DEMO_MODE-gated phase that pre-stages the demo client
// tooling on the jumphost after Phase17c (so the 10.0.10.x data-path ENI is
// attached and configured) and before Phase18. It:
//
//  1. Installs grpcurl v1.9.3 to /usr/local/bin (skip-if-present, idempotent).
//  2. Verifies curl is present (ships on AL2023; no reinstall needed).
//  3. Copies diameter_client.py + responder.py from the embedded diameter.ClientFS.
//  4. Verifies the EICE data-path end-to-end: opens an SSH tunnel and confirms
//     the BNK_EXT ENI (10.0.10.x) carries the expected IP address, proving the
//     data-path source is usable by curl probes.
//  5. Writes DEMO_CLIENT_STAGED_AT (RFC3339) to state.
//
// Self-gates on cl.DemoEnabled() so a normal (non-demo) up is byte-for-byte
// unchanged. Gate is also enforced at the call site in lifecycle.go.
//
// On dryRun, prints intended actions and returns nil without reading state or
// making remote calls (dry-run of Phase17b leaves "i-dry-run" placeholder
// instance IDs so any real SSH call would fail anyway).
func Phase17dDemoStage(ctx context.Context, cl *intent.Cluster, st *state.State, _ *Clients, dryRun bool) error {
	// Belt-and-suspenders: self-gate so this phase is safe even if a future
	// caller forgets the call-site gate.
	if !cl.DemoEnabled() {
		return nil
	}

	fmt.Fprintf(os.Stderr, "[phase 17d] demo-stage: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 17d] dry-run: would install grpcurl v1.9.3 to /usr/local/bin (skip-if-present)")
		fmt.Fprintln(os.Stderr, "[phase 17d] dry-run: would verify curl present on jumphost")
		fmt.Fprintf(os.Stderr, "[phase 17d] dry-run: would copy diameter_client.py → %s\n", demoDiameterClientPath)
		fmt.Fprintf(os.Stderr, "[phase 17d] dry-run: would copy responder.py → %s\n", demoDiameterResponderPath)
		fmt.Fprintln(os.Stderr, "[phase 17d] dry-run: would verify EICE data-path (ip addr | grep BNK_EXT_ENI_IP)")
		fmt.Fprintln(os.Stderr, "[phase 17d] dry-run: would write DEMO_CLIENT_STAGED_AT to state")
		return nil
	}

	// Read jumphost coordinates from state. Both keys are written by Phase17b and
	// are guaranteed present when DemoEnabled() (ValidateDemo requires jumphost.enabled).
	instanceID := st.Get("JUMPHOST_INSTANCE_ID")
	if instanceID == "" {
		return fmt.Errorf("phase17d: JUMPHOST_INSTANCE_ID not in state (run phase17b first)")
	}
	sourceIP := st.Get("JUMPHOST_BNK_EXT_ENI_IP")
	if sourceIP == "" {
		return fmt.Errorf("phase17d: JUMPHOST_BNK_EXT_ENI_IP not in state (run phase17b first)")
	}

	opts := jumphost.ProbeOptions{
		Region:     cl.Metadata.Region,
		InstanceID: instanceID,
		SourceIP:   sourceIP,
	}

	// Step 1+2: install grpcurl (skip-if-present) + verify curl.
	// Both are staging commands (string-based; no file transfer).
	installCmds := []string{
		jumphost.GrpcurlInstallCmd(),
		`command -v curl || (echo "curl not found" && exit 1)`,
	}
	outs, err := jumphost.RunStagingCommands(ctx, opts, installCmds)
	if err != nil {
		partial := ""
		if len(outs) > 0 {
			partial = outs[len(outs)-1]
		}
		return fmt.Errorf("phase17d: staging commands failed: %w (remote stdout: %q)", err, partial)
	}
	fmt.Fprintln(os.Stderr, "[phase 17d] grpcurl installed + curl verified")

	// Step 3a: copy diameter_client.py from embedded FS.
	clientBytes, err := fs.ReadFile(diameter.ClientFS(), "diameter_client.py")
	if err != nil {
		return fmt.Errorf("phase17d: reading embedded diameter_client.py: %w", err)
	}
	if err := jumphost.CopyFileViaEICE(ctx, opts, clientBytes, demoDiameterClientPath); err != nil {
		return fmt.Errorf("phase17d: copying diameter_client.py: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17d] copied diameter_client.py → %s\n", demoDiameterClientPath)

	// Step 3b: copy responder.py from embedded FS.
	responderBytes, err := fs.ReadFile(diameter.ClientFS(), "responder.py")
	if err != nil {
		return fmt.Errorf("phase17d: reading embedded responder.py: %w", err)
	}
	if err := jumphost.CopyFileViaEICE(ctx, opts, responderBytes, demoDiameterResponderPath); err != nil {
		return fmt.Errorf("phase17d: copying responder.py: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17d] copied responder.py → %s\n", demoDiameterResponderPath)

	// Step 4: verify EICE data-path end-to-end.
	// Runs `ip addr | grep <sourceIP>` on the jumphost, proving:
	//   (a) the SSH tunnel through EICE is functional, AND
	//   (b) the BNK_EXT ENI (10.0.10.x) carries the expected address,
	//       confirming the data-path source for curl probes is usable.
	// A bare `echo ok` does NOT satisfy AC #4.
	verifyCmd := fmt.Sprintf("ip addr | grep '%s'", sourceIP)
	verOuts, err := jumphost.RunStagingCommands(ctx, opts, []string{verifyCmd})
	if err != nil {
		partial := ""
		if len(verOuts) > 0 {
			partial = verOuts[0]
		}
		return fmt.Errorf("phase17d: EICE data-path verify failed (BNK_EXT_ENI_IP=%s not found on jumphost): %w (remote stdout: %q)", sourceIP, err, partial)
	}
	fmt.Fprintf(os.Stderr, "[phase 17d] EICE data-path verified: BNK_EXT_ENI_IP %s confirmed on jumphost\n", sourceIP)

	// Step 5: record staged-at timestamp.
	st.Set("DEMO_CLIENT_STAGED_AT", time.Now().UTC().Format(time.RFC3339))
	if err := st.Save(); err != nil {
		return fmt.Errorf("phase17d: saving state: %w", err)
	}

	fmt.Fprintln(os.Stderr, "[phase 17d] demo client staging complete")
	return nil
}
