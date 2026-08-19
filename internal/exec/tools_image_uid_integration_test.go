//go:build integration

package exec

// #104 — the tools images must run under an ARBITRARY uid, not only uid 1000.
//
// WHY THIS TEST IS NOT PART OF THE k8s SUITE. The Job path deliberately leaves
// RunAsUser unset (see runAsJob) so the cluster's own admission may assign one.
// Against kind that resolves to the image's own USER — uid 1000 — and everything
// passes. Against OpenShift, which is what ROKS is, the SCC assigns an arbitrary
// high uid from the namespace range with gid 0, and an image whose HOME is
// writable only by uid 1000 fails on its first config write.
//
// So the k8s test passes on the platform we TEST on and would fail on the
// platform we SHIP to. Forcing the uid here reproduces the shipping platform on
// any docker daemon, with no cluster at all.
//
// The uid below is a real OpenShift-shaped value: SCC ranges start at 1000000000
// and step by 10000 per namespace.

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const arbitraryOpenShiftUID = "1000650000"

func imagePresent(ref string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "image", "inspect", ref).Run() == nil
}

func TestIntegration_ToolsImages_RunUnderAnArbitraryUID(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not reachable; skipping integration test")
	}

	// argv per image is the cheapest invocation that still exercises the thing
	// that broke: for ibmcloud, `--version` writes $HOME/.bluemix on first run
	// regardless of subcommand, which is exactly the failure #104 describes.
	cases := []struct {
		name string
		ref  string
		args []string
	}{
		{"ibmcloud", toolImages["ibmcloud"], []string{"ibmcloud", "--version"}},
		{"iperf3", "ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:" + toolImageTag(), []string{"--version"}},
		{"h2load", "ghcr.io/jgruberf5/roksbnkctl-tools-h2load:" + toolImageTag(), []string{"--version"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !imagePresent(c.ref) {
				t.Skipf("%s not present locally; skipping (pull it to run this)", c.ref)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			// gid 0 as well as the uid: OpenShift keeps gid 0, and it is what
			// makes a group-writable HOME reachable. Testing the uid alone
			// would not reproduce the platform.
			argv := append([]string{"run", "--rm", "-u", arbitraryOpenShiftUID + ":0", c.ref}, c.args...)
			out, err := exec.CommandContext(ctx, "docker", argv...).CombinedOutput()
			got := string(out)

			if err != nil {
				t.Fatalf("%s failed under uid %s: %v\n%s", c.name, arbitraryOpenShiftUID, err, got)
			}
			// A permission error is the specific regression, and the CLI can
			// report it on stdout while still exiting 0 — so the exit code
			// alone is not enough to catch it.
			if strings.Contains(got, "permission denied") {
				t.Errorf("%s hit a permission error under uid %s (HOME not group-writable?):\n%s",
					c.name, arbitraryOpenShiftUID, got)
			}
		})
	}
}
