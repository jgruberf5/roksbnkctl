package config

import "testing"

func TestDeleteRegistryMirror(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	rec := &RegistryMirror{
		Target:    "icr",
		ImageHost: "de.icr.io/ns",
		Artifacts: []MirrorArtifact{{Name: "images/x", Tag: "v1"}},
	}
	if err := WriteRegistryMirror("ws", rec); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegistryMirror("ws"); err != nil {
		t.Fatalf("read after write: %v", err)
	}

	if err := DeleteRegistryMirror("ws"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegistryMirror("ws"); err == nil {
		t.Error("record still present after DeleteRegistryMirror")
	}

	// Idempotent: deleting an absent record is not an error.
	if err := DeleteRegistryMirror("ws"); err != nil {
		t.Errorf("delete absent record: %v", err)
	}
}
