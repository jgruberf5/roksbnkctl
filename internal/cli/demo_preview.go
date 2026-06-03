package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/awsbnkctl/internal/ui"
)

// flagPreview* are the demo preview subcommand's own flag variables.
var (
	flagPreviewName  string
	flagPreviewFail  bool
	flagPreviewSpeed float64
)

var demoPreviewCmd = &cobra.Command{
	Use:   "preview",
	Short: "Play the up/down rocket animation locally (no AWS) to preview the demo UX",
	Long: `awsbnkctl demo preview replays the rocket launch (up) and the SpaceX-style
landing (down) animations with simulated timing — no AWS calls, no cluster, no
cost. Use it to see what 'up --demo' and 'down' look like before running for real.

Run it in an interactive terminal (the animation needs a TTY).

  awsbnkctl demo preview                 # launch then landing, success path
  awsbnkctl demo preview --fail          # show the MISSION ABORT path
  awsbnkctl demo preview --speed 3       # 3x faster`,
	RunE: runDemoPreview,
}

func init() {
	demoPreviewCmd.Flags().StringVar(&flagPreviewName, "name", "bnk-demo", "cluster name shown in the header")
	demoPreviewCmd.Flags().BoolVar(&flagPreviewFail, "fail", false, "preview the failure (MISSION ABORT) path instead of a clean success")
	demoPreviewCmd.Flags().Float64Var(&flagPreviewSpeed, "speed", 1.0, "animation speed multiplier (2 = twice as fast)")
	demoCmd.AddCommand(demoPreviewCmd)
}

// previewStep is one simulated (stage, sub-step) beat in the preview.
type previewStep struct {
	num  int
	name string
}

func runDemoPreview(_ *cobra.Command, _ []string) error {
	stages := []ui.Stage{
		{Num: 1, Label: "VPC · subnets · IGW · NAT"},
		{Num: 2, Label: "EKS control plane"},
		{Num: 3, Label: "Nodes · kubeconfig · ENIs · jumphost"},
		{Num: 4, Label: "BNK supply chain · activation"},
	}

	// Launch order (mirrors runPhasedUp's stage grouping).
	launch := []previewStep{
		{1, "preflight"}, {1, "vpc"}, {1, "subnets"}, {1, "igw"}, {1, "nat"}, {1, "route-tables"}, {1, "iam"},
		{2, "eks-cluster"}, {2, "vpc-cni-prefix"},
		{3, "node-group"}, {3, "kubeconfig"}, {3, "tmm-node-label"}, {3, "secondary-enis"}, {3, "jumphost"}, {3, "iface-discovery"}, {3, "irsa-oidc"},
		{4, "ebs-csi-hugepages"}, {4, "k8s-foundation"}, {4, "flo-helm"}, {4, "cne-instance"}, {4, "license"}, {4, "cwc-heal"}, {4, "activation-poll"}, {4, "postflight"},
	}
	// Landing order (reverse stages, mirrors runPhasedDown).
	landing := []previewStep{
		{4, "otel-certs"}, {4, "activation-poll"}, {4, "license"}, {4, "cne-instance"}, {4, "flo-helm"}, {4, "k8s-foundation"},
		{3, "node-group"}, {3, "jumphost"}, {3, "secondary-enis"}, {3, "iface-discovery"}, {3, "irsa-oidc"},
		{2, "eks-cluster"}, {2, "vpc-cni-prefix"},
		{1, "iam"}, {1, "nat"}, {1, "igw"}, {1, "subnets"}, {1, "vpc"},
	}

	if flagPreviewSpeed <= 0 {
		flagPreviewSpeed = 1
	}
	pause := func(ms int) {
		time.Sleep(time.Duration(float64(ms)/flagPreviewSpeed) * time.Millisecond)
	}

	// play drives a renderer through the steps. failAt < 0 means a clean run.
	play := func(rdr ui.Renderer, steps []previewStep, failAt int) {
		rdr.Start(stages)
		pause(700)
		for i, s := range steps {
			rdr.PhaseBegin(s.num, s.name)
			pause(300)
			if failAt >= 0 && i == failAt {
				rdr.PhaseEnd(s.num, s.name, fmt.Errorf("simulated failure at %q", s.name))
				rdr.Finish(fmt.Errorf("simulated failure"))
				return
			}
			rdr.PhaseEnd(s.num, s.name, nil)
		}
		rdr.Finish(nil)
	}

	launchR := ui.NewRenderer(os.Stderr, flagPreviewName, true, flagNoColor)
	if _, plain := launchR.(ui.PlainRenderer); plain {
		fmt.Fprintln(os.Stderr, "demo preview needs an interactive terminal (TTY); "+
			"it has nothing to show with piped output or --no-color.")
		return nil
	}

	fmt.Fprintln(os.Stderr)
	failAt := -1
	if flagPreviewFail {
		failAt = len(launch) / 2
	}
	play(launchR, launch, failAt)
	if flagPreviewFail {
		fmt.Fprintln(os.Stderr)
		return nil
	}

	pause(1400)
	fmt.Fprintln(os.Stderr)
	landingR := ui.NewDescentRenderer(os.Stderr, flagPreviewName, true, flagNoColor)
	play(landingR, landing, -1)
	fmt.Fprintln(os.Stderr)
	return nil
}
