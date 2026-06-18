package config

import (
	"errors"
	"testing"
)

func TestRegistryMirror_RoundTrip(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	if err := SaveWorkspace("ws", &Workspace{}); err != nil {
		t.Fatal(err)
	}

	// Missing → sentinel.
	if _, err := ReadRegistryMirror("ws"); !errors.Is(err, ErrNoRegistryMirror) {
		t.Fatalf("ReadRegistryMirror(missing) = %v, want ErrNoRegistryMirror", err)
	}

	in := &RegistryMirror{
		Target:          "icr",
		Namespace:       "bnk-mirror",
		ChartHost:       "default-route-openshift-image-registry.apps.x/bnk-mirror",
		ImageHost:       "image-registry.openshift-image-registry.svc:5000/bnk-mirror",
		ManifestVersion: "2.3.0-3.2598.3-0.0.170",
		Artifacts: []MirrorArtifact{
			{Kind: "image", Name: "images/tmm-img", Tag: "v10.159.3-0.1.5", Digest: "sha256:abc"},
		},
	}
	if err := WriteRegistryMirror("ws", in); err != nil {
		t.Fatal(err)
	}
	if in.RecordedAt.IsZero() {
		t.Error("WriteRegistryMirror should stamp RecordedAt")
	}

	got, err := ReadRegistryMirror("ws")
	if err != nil {
		t.Fatalf("ReadRegistryMirror: %v", err)
	}
	if got.ImageHost != in.ImageHost || got.ChartHost != in.ChartHost || got.Namespace != in.Namespace {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Digest != "sha256:abc" {
		t.Errorf("artifacts not round-tripped: %+v", got.Artifacts)
	}
}
