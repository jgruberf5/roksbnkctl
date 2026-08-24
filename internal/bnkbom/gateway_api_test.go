package bnkbom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The manifest every test here builds on. Two artifacts is enough to prove the
// bundle is unioned rather than replacing anything.
const gwTestManifest = `
f5_helm_repo: oci://repo.f5.com
f5_docker_repo: repo.f5.com
releases:
  - version: 2.4.0-x
    helm_charts:
      - name: charts/f5-tmm
        version: 1.2.3
    docker_images:
      - name: images/tmm-img
        version: 1.2.3
`

func buildForTest(t *testing.T, opts Options) *BOM {
	t.Helper()
	opts.ManifestVersion = "2.4.0-x"
	bom, err := Build([]byte(gwTestManifest), opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return bom
}

// The bundle is unioned on its own condition, and only then. A BOM that carries
// it unconditionally drags a megabyte of CRDs into every 2.3 mirror; one that
// never carries it leaves an mTLS install with nothing to apply.
func TestBundleEntersTheBOMOnlyWhenAVersionIsAsked(t *testing.T) {
	plain := buildForTest(t, Options{})
	if _, _, files := plain.Counts(); files != 0 {
		t.Errorf("a BOM built with no bundle version carries %d files, want 0", files)
	}

	withBundle := buildForTest(t, Options{GatewayAPIBundleVersion: "1.5.0"})
	charts, images, files := withBundle.Counts()
	if files != 1 {
		t.Fatalf("Counts() reports %d files, want 1", files)
	}
	// The union must ADD, not replace: the F5 artifacts are still there.
	if pc, pi, _ := plain.Counts(); charts != pc || images != pi {
		t.Errorf("the bundle changed the chart/image counts: %d/%d, want %d/%d", charts, images, pc, pi)
	}

	var got Artifact
	for _, a := range withBundle.Artifacts {
		if a.Kind == KindFile {
			got = a
		}
	}
	if got.SHA256 == "" {
		t.Fatal("the bundle artifact carries no sha256 pin, so the mirror would refuse it")
	}
	if got.Origin != OriginGatewayAPI {
		t.Errorf("origin = %q, want %q — `registry bom` would attribute it to something that never mentioned it", got.Origin, OriginGatewayAPI)
	}
	if got.Tag != "v1.5.0" {
		t.Errorf("tag = %q, want v1.5.0 (the upstream release tag)", got.Tag)
	}
	if !strings.HasSuffix(got.SourceURL, "/v1.5.0/"+GatewayAPIBundleFile) {
		t.Errorf("source URL %q does not point at the v1.5.0 release asset", got.SourceURL)
	}
}

// An unpinned version fails the BUILD. Dropping it instead would produce a BOM
// that replicates clean, verifies clean and reports complete while missing the
// one artifact an mTLS install cannot start without.
func TestBuildRefusesAnUnpinnedBundleVersion(t *testing.T) {
	bom, err := Build([]byte(gwTestManifest), Options{
		ManifestVersion:         "2.4.0-x",
		GatewayAPIBundleVersion: "9.9.9",
	})
	if err == nil {
		t.Fatal("an unpinned version built a BOM; it would be replicated unverified or silently omitted")
	}
	if bom != nil {
		t.Error("a BOM was returned alongside the refusal")
	}
	if !strings.Contains(err.Error(), "1.5.0") {
		t.Errorf("the refusal does not name a version that IS pinned, so it is unactionable: %v", err)
	}
}

// A "v" prefix is the upstream release tag's spelling; F5's reference spells the
// same release without one. Both have to resolve to the same pin, or an operator
// who copies the version out of the release page gets a refusal for a release
// this build supports.
func TestBundleVersionAcceptsBothSpellings(t *testing.T) {
	bare, err := GatewayAPIBundle("1.5.0", "")
	if err != nil {
		t.Fatalf("bare version: %v", err)
	}
	prefixed, err := GatewayAPIBundle("v1.5.0", "")
	if err != nil {
		t.Fatalf("v-prefixed version: %v", err)
	}
	if bare != prefixed {
		t.Errorf("1.5.0 and v1.5.0 produced different artifacts:\n %+v\n %+v", bare, prefixed)
	}
}

// A source override moves WHERE the bytes come from and must not move WHAT they
// have to be. If it relaxed the pin, pointing at a proxy would be a way to
// install anything at all.
func TestSourceOverrideKeepsThePin(t *testing.T) {
	upstream, err := GatewayAPIBundle("1.5.0", "")
	if err != nil {
		t.Fatal(err)
	}
	proxied, err := GatewayAPIBundle("1.5.0", "https://proxy.example.com/gw/standard-install.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if proxied.SourceURL != "https://proxy.example.com/gw/standard-install.yaml" {
		t.Errorf("the override did not take: %q", proxied.SourceURL)
	}
	if proxied.SHA256 != upstream.SHA256 {
		t.Errorf("the override changed the pin: %q vs %q", proxied.SHA256, upstream.SHA256)
	}
	if proxied.Tag != upstream.Tag {
		t.Errorf("the override changed the tag: %q vs %q", proxied.Tag, upstream.Tag)
	}
}

// The pin is only worth having if it is the pin for the bytes upstream actually
// serves. Nothing in this repo can check that by reading itself, so this fetches
// the release — and skips, rather than failing, where the network cannot reach
// it (an air-gapped build host is the normal case for this product).
func TestBundlePinMatchesUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("fetches ~1 MB from github.com; part of the full suite")
	}
	art, err := GatewayAPIBundle(defaultTestBundleVersion, "")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, art.SourceURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("github.com unreachable from this host: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("upstream answered HTTP %d; not a pin failure", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Skipf("reading the release asset: %v", err)
	}

	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != art.SHA256 {
		t.Fatalf("the pinned sha256 no longer matches what upstream serves\n  pinned %s\n  actual %s\n"+
			"  a release asset changed under a tag, or the pin is wrong — do not update it without "+
			"establishing which", art.SHA256, got)
	}
	if len(body) != gatewayAPIBundleV150Bytes {
		t.Errorf("the release asset is %d bytes, the reviewed one was %d", len(body), gatewayAPIBundleV150Bytes)
	}

	// The shape the installer assumes: CRDs plus one admission policy and its
	// binding, and no container images — nothing to pull at runtime, which is why
	// carrying the FILE through the mirror is sufficient.
	docs := strings.Count(string(body), "\nkind: ")
	crds := strings.Count(string(body), "\nkind: CustomResourceDefinition")
	if crds != 8 || docs != 10 {
		t.Errorf("the bundle is %d documents of which %d are CRDs; the reviewed one was 10 and 8", docs, crds)
	}
	if strings.Contains(string(body), "\n        image: ") {
		t.Error("the bundle now carries a container image; the mirror copies the FILE only, so that image " +
			"would be pulled from the internet by a disconnected cluster")
	}
	// The policy the bundle installs must not be the one the install-time sweep
	// deletes. Asserted from the bytes rather than from a constant, so upstream
	// renaming it onto the swept name is caught here.
	if !strings.Contains(string(body), gatewayAPIBundlePolicyName) {
		t.Errorf("the bundle no longer ships a policy named %s; the orchestration guard that "+
			"checks it against the sweep is now checking for something absent", gatewayAPIBundlePolicyName)
	}
}

// Facts established by reviewing the v1.5.0 release asset, kept next to the test
// that re-establishes them rather than in the production path, which has no use
// for them beyond the sha256.
const (
	defaultTestBundleVersion  = "1.5.0"
	gatewayAPIBundleV150Bytes = 1023753
	// The bundle's OWN admission policy. Spelled here so the assertion above is
	// independent of the orchestration package's copy — two independently stated
	// halves is what makes their agreement mean something.
	gatewayAPIBundlePolicyName = "safe-upgrades.gateway.networking.k8s.io"
)
