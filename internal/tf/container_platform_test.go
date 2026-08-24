package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// containerPlatform has been wrong twice, in different directions, and each time
// the cluster still LOOKED healthy — so it gets a guard.
//
//   - "Generic" (the chart default): the CNE controller looks for the
//     kubeadm-config ConfigMap only kubeadm-built clusters have, aborts at
//     Reconciled=False, and never creates TMM's internal NAD (#189). Every other
//     signal reported healthy.
//   - "OCP" (a correction that was still wrong): F5Tmm reconciles, but the
//     lifecycle operator creates 16 component CRs and SILENTLY SKIPS CSRC. No
//     CSRC CR, no f5-spk-csrc pods, and so no macvlan-internal NAD — which CSRC
//     creates at runtime. Nothing is logged by FLO or the controller.
//
// "IBM" is what engineering's approved reference cluster runs, read from its live
// F5Tmm. It is accepted by the chart's values.schema.json enum
// ["Generic","OCP","Robin","AON","AWS","IBM"] even though values.yaml documents
// only the first four — so it cannot be found by reading the chart's own comment.
//
// Verified on a live cluster: switching OCP -> IBM produced csrcs=1, six
// f5-spk-csrc pods, and macvlan-internal.
func TestContainerPlatformIsIBM(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "terraform", "modules", "flo", "modules", "flo", "main.tf"))
	if err != nil {
		t.Fatalf("read flo main.tf: %v", err)
	}
	src := string(b)

	assign := regexp.MustCompile(`(?m)^\s*containerPlatform\s*=\s*"([^"]*)"\s*$`)
	m := assign.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("no containerPlatform assignment found in the FLO helm values; " +
			"this guard can no longer see what it checks")
	}
	if m[1] != "IBM" {
		t.Errorf("containerPlatform is %q, want \"IBM\".\n"+
			"  Generic  -> F5Tmm aborts on kubeadm-config, silently (#189)\n"+
			"  OCP      -> FLO silently skips CSRC, so macvlan-internal is never created\n"+
			"  IBM      -> what the approved reference cluster runs\n"+
			"  Both wrong values leave every other health signal reporting success.", m[1])
	}
}
