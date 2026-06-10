package bnkbom

import (
	"os"
	"reflect"
	"testing"
)

func readSample(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/manifest-sample.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func TestParseManifest(t *testing.T) {
	bom, err := ParseManifest(readSample(t), "")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if bom.ManifestVersion != "9.9.9-test.0" {
		t.Errorf("ManifestVersion = %q, want 9.9.9-test.0", bom.ManifestVersion)
	}
	charts, images := bom.Counts()
	if charts != 3 || images != 2 {
		t.Fatalf("Counts() = (%d charts, %d images), want (3, 2)", charts, images)
	}

	// Names keep their repository path; the oci:// scheme is stripped from the
	// helm host; everything is OriginManifest.
	want := map[string]Artifact{
		"charts/sample-operator": {Kind: KindChart, SourceHost: "repo.f5.com", Name: "charts/sample-operator", Tag: "v1.2.3-0.0.1", Origin: OriginManifest},
		"utils/sample-util":      {Kind: KindChart, SourceHost: "repo.f5.com", Name: "utils/sample-util", Tag: "0.9.0", Origin: OriginManifest},
		"images/sample-tmm":      {Kind: KindImage, SourceHost: "repo.f5.com", Name: "images/sample-tmm", Tag: "v4.5.6-0.0.2", Origin: OriginManifest},
	}
	got := map[string]Artifact{}
	for _, a := range bom.Artifacts {
		got[a.Name] = a
	}
	for name, w := range want {
		if g, ok := got[name]; !ok {
			t.Errorf("missing artifact %q", name)
		} else if g != w {
			t.Errorf("artifact %q = %+v, want %+v", name, g, w)
		}
	}
	if ref := want["images/sample-tmm"].Ref(); ref != "repo.f5.com/images/sample-tmm:v4.5.6-0.0.2" {
		t.Errorf("Ref() = %q", ref)
	}
}

func TestParseManifest_VersionSelect(t *testing.T) {
	data := readSample(t)
	if _, err := ParseManifest(data, "does-not-exist"); err == nil {
		t.Error("ParseManifest with a bogus version: want error, got nil")
	}
	if _, err := ParseManifest(data, "9.9.9-test.0"); err != nil {
		t.Errorf("ParseManifest with the real version: %v", err)
	}
}

func TestBuild_IncludeDeps(t *testing.T) {
	data := readSample(t)

	// Without deps: just the manifest's 3 charts + 2 images.
	bare, err := Build(data, Options{IncludeDeps: false})
	if err != nil {
		t.Fatalf("Build(no deps): %v", err)
	}
	if c, i := bare.Counts(); c != 3 || i != 2 {
		t.Errorf("no-deps Counts() = (%d, %d), want (3, 2)", c, i)
	}

	// With deps: + cert-manager chart (1) and its 5 quay.io images + bitnami/kubectl.
	full, err := Build(data, Options{IncludeDeps: true, CertManagerVersion: "v1.17.3", NodeLabelerImageTag: "1.31"})
	if err != nil {
		t.Fatalf("Build(deps): %v", err)
	}
	if c, i := full.Counts(); c != 4 || i != 8 {
		t.Fatalf("deps Counts() = (%d, %d), want (4 charts, 8 images)", c, i)
	}

	byRef := map[string]Artifact{}
	for _, a := range full.Artifacts {
		byRef[a.Ref()] = a
	}
	for _, ref := range []string{
		"charts.jetstack.io/charts/cert-manager:v1.17.3",
		"quay.io/jetstack/cert-manager-controller:v1.17.3",
		"quay.io/jetstack/cert-manager-webhook:v1.17.3",
		"docker.io/bitnami/kubectl:1.31",
	} {
		if _, ok := byRef[ref]; !ok {
			t.Errorf("expected dep artifact %q in BOM", ref)
		}
	}
	if a := byRef["docker.io/bitnami/kubectl:1.31"]; a.Origin != OriginNodeLabeler {
		t.Errorf("bitnami/kubectl Origin = %q, want %q", a.Origin, OriginNodeLabeler)
	}
}

func TestBuild_Deterministic(t *testing.T) {
	data := readSample(t)
	opts := Options{IncludeDeps: true, CertManagerVersion: "v1.17.3", NodeLabelerImageTag: "1.31"}
	a, err := Build(data, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(data, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("Build is not deterministic across runs")
	}

	// Sorted: every chart sorts before every image (Kind "chart" < "image").
	seenImage := false
	for _, art := range a.Artifacts {
		if art.Kind == KindImage {
			seenImage = true
		} else if seenImage {
			t.Error("artifacts not sorted: a chart follows an image")
			break
		}
	}
}
