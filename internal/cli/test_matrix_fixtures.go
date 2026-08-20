package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/test"
	corev1 "k8s.io/api/core/v1"
)

// fixturePlanFor builds the render plan from the matrix spec's fixtures
// + gateway identity. The TCP route defaults to the iperf3 server fixture
// (roksbnkctl-iperf3:5201) so an L4Route has a backend out of the box.
func fixturePlanFor(spec *test.MatrixSpec) test.FixturePlan {
	g := spec.Gateway
	return test.FixturePlan{
		Gateway:        g,
		HTTPBackend:    spec.Fixtures.HTTPBackend,
		Routes:         spec.Fixtures.Routes,
		GatewayName:    g.Name,
		HTTPSection:    g.HTTPSection,
		HTTPSSection:   g.HTTPSSection,
		TCPSection:     g.TCPSection,
		TCPBackendSvc:  k8s.Iperf3SvcName,
		TCPBackendPort: k8s.Iperf3Port,
	}
}

// renderMatrixFixtures returns the fixtures manifest for --dry-run
// preview, or "" if no manifest-shaped fixtures are enabled. Render
// errors surface as a commented line rather than aborting the plan.
func renderMatrixFixtures(spec *test.MatrixSpec) string {
	if !spec.Fixtures.HTTPBackend && !spec.Fixtures.Routes {
		return ""
	}
	manifest, err := test.RenderFixtures(fixturePlanFor(spec))
	if err != nil {
		return "# fixture render error: " + err.Error()
	}
	return manifest
}

// deployMatrixFixtures stands up the runner-owned fixtures: the iperf3
// server (reusing the throughput fixture), then the manifest-shaped
// fixtures (HTTP backend + routes + TLS secret) via SSA apply.
func deployMatrixFixtures(ctx context.Context, spec *test.MatrixSpec) error {
	if spec.Fixtures.Iperf3Server {
		fmt.Fprintln(os.Stderr, "→ Deploying iperf3 server fixture")
		kc, err := k8s.NewFromDefault()
		if err != nil {
			return err
		}
		if err := kc.DeployIperf3(ctx, k8s.Iperf3Options{
			Namespace:   k8s.Iperf3Namespace,
			ServiceType: corev1.ServiceTypeClusterIP,
		}); err != nil {
			return err
		}
		if err := kc.WaitIperf3Ready(ctx, k8s.Iperf3Namespace, 0); err != nil {
			return err
		}
	}

	manifest := renderMatrixFixtures(spec)
	if manifest == "" {
		return nil
	}
	fmt.Fprintln(os.Stderr, "→ Applying matrix fixtures (HTTP backend / routes / TLS)")
	f, err := os.CreateTemp("", "matrix-fixtures-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(manifest); err != nil {
		f.Close()
		return err
	}
	f.Close()

	apply := &k8s.ApplyOptions{
		Filename: f.Name(),
		Force:    true,
		IOStreams: genericiooptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stderr,
			ErrOut: os.Stderr,
		},
	}
	return apply.Run(ctx)
}

// coreFixtureTypes are the built-in kinds the fixtures create. Always present
// on any cluster, so they resolve even when the BNK route CRDs do not.
var coreFixtureTypes = []string{"deployments", "services", "secrets"}

// teardownMatrixFixtures removes everything deployMatrixFixtures created,
// best-effort. The manifest fixtures all carry the managed-by label, so
// they go in one label-selected delete per type group.
func teardownMatrixFixtures(spec *test.MatrixSpec) {
	tctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if spec.Fixtures.Iperf3Server {
		if kc, err := k8s.NewFromDefault(); err == nil {
			if err := kc.TeardownIperf3(tctx, k8s.Iperf3Namespace); err != nil {
				fmt.Fprintf(os.Stderr, "warning: tearing down iperf3 fixture: %v\n", err)
			}
		}
	}

	if !spec.Fixtures.HTTPBackend && !spec.Fixtures.Routes {
		return
	}
	ns := spec.Gateway.AppNamespace
	if ns == "" {
		ns = "default"
	}
	// One delete per type group, NOT one comma-joined delete of all of them.
	//
	// cli-runtime's builder resolves every requested type before deleting
	// anything and returns on the FIRST unknown one, so a single call naming a
	// CRD the cluster lacks deletes nothing at all — the deployments, services
	// and secrets leak alongside it. That is the failure mode on exactly the
	// cluster where cleanup matters most: one where the BNK install failed or
	// was removed, so its route CRDs are absent. Splitting them means an
	// unresolvable route kind costs only that kind.
	//
	// The route CRDs come from test.FixtureRouteCRDs, not a literal. This list
	// named a route CRD BNK never installs (#99) long after the fixture that
	// created it was corrected, because the two were written apart and nothing
	// tied them together. Deriving both from one declaration is what ties them.
	groups := append([]string{strings.Join(coreFixtureTypes, ",")}, test.FixtureRouteCRDs()...)
	for _, types := range groups {
		del := &k8s.DeleteOptions{
			Args:          []string{types},
			Namespace:     ns,
			LabelSelector: test.FixtureLabelKey + "=" + test.FixtureLabelValue,
			IOStreams: genericiooptions.IOStreams{
				Out:    os.Stderr,
				ErrOut: os.Stderr,
			},
		}
		if err := del.Run(tctx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: tearing down matrix fixtures %s (label %s=%s in %s): %v\n",
				types, test.FixtureLabelKey, test.FixtureLabelValue, ns, err)
		}
	}
}
