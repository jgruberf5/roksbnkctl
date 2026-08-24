package orchestration

import (
	"context"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/registry/mirror"
)

// bundlePolicyName is the ValidatingAdmissionPolicy the Gateway API
// standard-install bundle ships. Stated here INDEPENDENTLY of the production
// code, which never names it: the whole question these tests answer is whether
// this name and admissionSweepName can collide, and deriving one from the other
// would make the answer true by construction.
const bundlePolicyName = "safe-upgrades.gateway.networking.k8s.io"

// A trimmed stand-in for standard-install.yaml: two of its eight CRDs, plus the
// admission policy and binding it really ships, under their real names.
//
// Deliberately NOT the megabyte itself. Committing the real asset would put a
// second, unversioned copy of the supply chain in the tree — and the pin test in
// internal/bnkbom already checks the real bytes against upstream. What matters
// here is the SHAPE, and the names, which this reproduces exactly.
const bundleYAML = `# a licence header, which parses to nothing
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gatewayclasses.gateway.networking.k8s.io
spec:
  group: gateway.networking.k8s.io
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: httproutes.gateway.networking.k8s.io
spec:
  group: gateway.networking.k8s.io
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: "` + bundlePolicyName + `"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: ` + bundlePolicyName + `
`

func parseBundle(t *testing.T, y string) []*unstructured.Unstructured {
	t.Helper()
	objs, err := k8s.ParseManifest([]byte(y))
	if err != nil {
		t.Fatalf("parsing the bundle: %v", err)
	}
	if len(objs) != 4 {
		t.Fatalf("parsed %d objects, want 4 — the fixture or the parser changed and this test "+
			"would prove nothing", len(objs))
	}
	return objs
}

func namedAdmissionObj(kind, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "admissionregistration.k8s.io/v1",
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name},
	}}
}

// THE interaction (#185). The bundle installs its own
// ValidatingAdmissionPolicy while the sweep is running and deleting OpenShift's.
// They are different objects; an over-broad sweep would delete what the bundle
// had just installed, and the symptom would be indistinguishable from the bundle
// never having applied — no error, no denied write, just an absent policy.
//
// So this runs the REAL sweep loop against a cluster holding both, and checks
// which one is standing afterwards.
func TestSweepDeletesOpenShiftsPolicyAndLeavesTheBundlesAlone(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		admissionSweepGVRs[0]: "ValidatingAdmissionPolicyBindingList",
		admissionSweepGVRs[1]: "ValidatingAdmissionPolicyList",
		admissionSweepGVRs[2]: "ValidatingWebhookConfigurationList",
	}
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		namedAdmissionObj("ValidatingAdmissionPolicy", admissionSweepName),
		namedAdmissionObj("ValidatingAdmissionPolicyBinding", admissionSweepName),
		namedAdmissionObj("ValidatingAdmissionPolicy", bundlePolicyName),
		namedAdmissionObj("ValidatingAdmissionPolicyBinding", bundlePolicyName),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	runAdmissionSweepLoop(ctx, dc, 5*time.Millisecond)

	bg := context.Background()
	// OpenShift's must be gone, or the sweep is not doing its job and this test
	// would pass vacuously against a loop that deletes nothing at all.
	for _, gvr := range admissionSweepGVRs[:2] {
		if _, err := dc.Resource(gvr).Get(bg, admissionSweepName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("%s/%s survived the sweep (err = %v) — the sweep is inert, so the survival "+
				"check below proves nothing", gvr.Resource, admissionSweepName, err)
		}
	}
	// The bundle's must survive.
	for _, gvr := range admissionSweepGVRs[:2] {
		if _, err := dc.Resource(gvr).Get(bg, bundlePolicyName, metav1.GetOptions{}); err != nil {
			t.Errorf("the sweep deleted the bundle's own %s/%s (%v) — the bundle would be undone "+
				"seconds after it was applied, and the install would look as though it never applied",
				gvr.Resource, bundlePolicyName, err)
		}
	}
}

// The same property, asserted where the install can act on it: nothing the
// bundle ships may be something the sweep deletes. This is the production check,
// so a future collision refuses the install instead of producing a cluster whose
// policy quietly vanished.
func TestBundleObjectsAreNotSweepTargets(t *testing.T) {
	if err := checkBundleSurvivesTheSweep(parseBundle(t, bundleYAML)); err != nil {
		t.Fatalf("the real bundle was judged a sweep target: %v", err)
	}

	// And the check has teeth: a bundle whose policy IS named what the sweep
	// deletes must be refused. Without this the assertion above is satisfied by a
	// function that returns nil unconditionally.
	collided := strings.ReplaceAll(bundleYAML, bundlePolicyName, admissionSweepName)
	err := checkBundleSurvivesTheSweep(parseBundle(t, collided))
	if err == nil {
		t.Fatal("a bundle shipping the swept name was accepted; the sweep would delete it again within seconds")
	}
	if !strings.Contains(err.Error(), admissionSweepName) {
		t.Errorf("the refusal does not name the colliding object: %v", err)
	}
}

// The trimmed fixture above reproduces the shape; this checks the shape is
// still the real one. It fetches the actual release, parses it with the SAME
// parser the install uses, and puts it through the SAME collision check —
// because everything else here is downstream of an assumption about a file that
// lives on someone else's server and can change without us.
//
// Skipped, not failed, where the network cannot reach github.com: an air-gapped
// build host is the normal case for this product.
func TestTheRealBundleParsesAndSurvivesTheSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("fetches ~1 MB from github.com; part of the full suite")
	}
	art, err := bnkbom.GatewayAPIBundle(config.DefaultGatewayAPIVersion, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	raw, err := mirror.FetchAndVerifyFile(ctx, art.SourceURL, art.SHA256)
	if err != nil {
		t.Skipf("cannot fetch the release asset from this host: %v", err)
	}

	objs, err := k8s.ParseManifest(raw)
	if err != nil {
		t.Fatalf("the installer's parser cannot read the real bundle: %v", err)
	}
	kinds := map[string]int{}
	for _, o := range objs {
		if o.GetName() == "" {
			t.Errorf("%s parsed with no name; server-side apply addresses objects by name", o.GetKind())
		}
		kinds[o.GetKind()]++
	}
	if len(objs) != 10 || kinds["CustomResourceDefinition"] != 8 ||
		kinds["ValidatingAdmissionPolicy"] != 1 || kinds["ValidatingAdmissionPolicyBinding"] != 1 {
		t.Errorf("the bundle now parses to %d objects %v; the reviewed one was 8 CRDs, "+
			"one ValidatingAdmissionPolicy and its binding", len(objs), kinds)
	}

	if err := checkBundleSurvivesTheSweep(objs); err != nil {
		t.Errorf("the real bundle would be deleted by the sweep that runs alongside its own apply: %v", err)
	}
	// And the fixture's name is the real one — otherwise every offline test here
	// is checking a policy that does not exist.
	found := false
	for _, o := range objs {
		if o.GetKind() == "ValidatingAdmissionPolicy" {
			found = o.GetName() == bundlePolicyName
		}
	}
	if !found {
		t.Errorf("upstream no longer names its policy %q; the offline fixtures in this file "+
			"are testing a name nothing ships", bundlePolicyName)
	}
}

// admissionSweepWouldDelete has to answer for the sweep as it is actually
// written — the exact name, in the group the sweep addresses — and nothing
// wider. A version that matched on the group alone would flag the bundle's own
// policy and refuse every mTLS install.
func TestAdmissionSweepWouldDeleteIsExact(t *testing.T) {
	for _, tc := range []struct {
		name  string
		obj   *unstructured.Unstructured
		swept bool
	}{
		{"openshift policy", namedAdmissionObj("ValidatingAdmissionPolicy", admissionSweepName), true},
		{"openshift binding", namedAdmissionObj("ValidatingAdmissionPolicyBinding", admissionSweepName), true},
		{"the bundle's policy", namedAdmissionObj("ValidatingAdmissionPolicy", bundlePolicyName), false},
		{"same name, another group", &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": admissionSweepName},
		}}, false},
		{"nil", nil, false},
	} {
		if got := admissionSweepWouldDelete(tc.obj); got != tc.swept {
			t.Errorf("%s: admissionSweepWouldDelete = %v, want %v", tc.name, got, tc.swept)
		}
	}
}

// The ORDER is the design. The bundle has to go on after the sweep is running
// (the ingress operator's policy blocks it otherwise) and before terraform (the
// CRDs must exist before the FLO crd-installer's window), and a bundle failure
// must stop the run rather than leaving an mTLS install misconfigured in a way
// nothing reports.
func TestBundleIsInstalledInsideTheSweepWindowAndBeforeTheApply(t *testing.T) {
	var seq []string
	err := applyBNKInSweepWindow(true,
		func() func() {
			seq = append(seq, "sweep-start")
			return func() { seq = append(seq, "sweep-stop") }
		},
		func() error { seq = append(seq, "bundle"); return nil },
		func() error { seq = append(seq, "apply"); return nil },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"sweep-start", "bundle", "apply", "sweep-stop"}
	if strings.Join(seq, ",") != strings.Join(want, ",") {
		t.Errorf("order was %v, want %v", seq, want)
	}
}

func TestABundleFailureStopsTheApply(t *testing.T) {
	applied := false
	err := applyBNKInSweepWindow(true,
		func() func() { return func() {} },
		func() error { return errBundleTest },
		func() error { applied = true; return nil },
	)
	if err == nil {
		t.Fatal("a failed bundle install let the apply proceed; the CNE controller would be configured " +
			"for a Gateway API the cluster does not carry, and nothing would say so")
	}
	if applied {
		t.Error("terraform ran anyway")
	}
}

// No sweep means no bundle: both are gated on the same question, so installing
// one here would put the CRDs on with nothing holding the admission policy open.
func TestNoSweepMeansNoBundle(t *testing.T) {
	bundled, swept := false, false
	if err := applyBNKInSweepWindow(false,
		func() func() { swept = true; return func() {} },
		func() error { bundled = true; return nil },
		func() error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	if swept {
		t.Error("the sweep started when it was not needed")
	}
	if bundled {
		t.Error("the bundle was installed with no sweep running; the ingress operator's policy would refuse it")
	}
}

var errBundleTest = errTest("bundle could not be fetched")

type errTest string

func (e errTest) Error() string { return string(e) }

// The install pulls the bundle out of the mirror BY DIGEST when replicate
// recorded one. A tag can be moved under a mirror; a digest cannot.
func TestMirroredBundleRefPrefersTheDigest(t *testing.T) {
	art, err := bnkbom.GatewayAPIBundle("1.5.0", "")
	if err != nil {
		t.Fatal(err)
	}
	rec := &config.RegistryMirror{
		ChartHost: "us.icr.io/bnk",
		ImageHost: "us.icr.io/bnk",
		Artifacts: []config.MirrorArtifact{
			{Kind: "image", Name: "images/tmm-img", Tag: "1.2.3", Digest: "sha256:aaa"},
			{Kind: "file", Name: art.Name, Tag: art.Tag, Digest: "sha256:bbb"},
		},
	}
	ref, ok := mirroredBundleRef(rec, art)
	if !ok {
		t.Fatal("the recorded bundle was not found in the mirror record")
	}
	if ref != "us.icr.io/bnk/"+art.Name+"@sha256:bbb" {
		t.Errorf("ref = %q, want the digest form", ref)
	}

	// Without a digest it falls back to the tag rather than giving up: a record
	// written by an older replicate is still usable, and the pin is checked on
	// the pulled bytes either way.
	rec.Artifacts[1].Digest = ""
	if ref, _ := mirroredBundleRef(rec, art); ref != "us.icr.io/bnk/"+art.Name+":"+art.Tag {
		t.Errorf("tag fallback = %q", ref)
	}

	// A mirror that does not carry the bundle must be reported as such, not
	// silently treated as "fetch it from the internet instead" — a disconnected
	// cluster has no internet, and that is the whole reason it is in the mirror.
	rec.Artifacts = rec.Artifacts[:1]
	if ref, ok := mirroredBundleRef(rec, art); ok {
		t.Errorf("a mirror with no bundle produced the ref %q", ref)
	}
}
