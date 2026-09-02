package cli

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/registry/mirror"
)

// recordedDigests keys the workspace's mirror record, and VerifyAllRecorded looks
// artifacts up by mirror.RecordedKey. Those two string formats are built in
// different packages from different types, and nothing but this test makes them
// agree.
//
// Divergence fails SILENTLY and in the worst possible direction: every lookup
// misses, every artifact falls back to source comparison, and `registry verify`
// goes back to exactly the #270 behaviour this was written to fix -- while still
// reporting success on a reachable source. There is no error to notice.
func TestRecordedDigestKeyMatchesTheLookupKey(t *testing.T) {
	for _, a := range []bnkbom.Artifact{
		{Kind: bnkbom.KindImage, SourceHost: "registry.k8s.io", Name: "kubectl", Tag: "v1.36.0"},
		{Kind: bnkbom.KindChart, SourceHost: "repo.f5.com", Name: "charts/coremond", Tag: "2.4.0"},
		{Kind: bnkbom.KindImage, SourceHost: "quay.io", Name: "jetstack/cert-manager-controller", Tag: "v1.17.3"},
	} {
		// The key the record is written and read under...
		rec := config.MirrorArtifact{Kind: string(a.Kind), Name: a.Name, Tag: a.Tag, Digest: "sha256:abc"}
		fromRecord := rec.Kind + "/" + rec.Name + ":" + rec.Tag
		// ...must be the key the engine looks up.
		if want := mirror.RecordedKey(a); fromRecord != want {
			t.Errorf("record key %q != mirror.RecordedKey %q — every lookup would miss "+
				"and verify would silently fall back to comparing against the source", fromRecord, want)
		}
	}
}

// TestRecordedDigestsSkipsEmptyDigests: an inventory entry with no digest carries
// no information, and admitting it as "" would make the engine compare a target
// digest against the empty string and report a mismatch on a good mirror.
func TestRecordedDigestsSkipsEmptyDigests(t *testing.T) {
	name := "recdigest"
	_, _ = workspaceFixture(t, name)
	if err := config.WriteRegistryMirror(name, &config.RegistryMirror{
		Target:    "oci",
		Namespace: "mirror",
		Artifacts: []config.MirrorArtifact{
			{Kind: "images", Name: "kubectl", Tag: "v1.36.0", Digest: "sha256:aaa"},
			{Kind: "images", Name: "nodigest", Tag: "v1"},
		},
	}); err != nil {
		t.Fatalf("write mirror record: %v", err)
	}

	got := recordedDigests(name)
	if got["images/kubectl:v1.36.0"] != "sha256:aaa" {
		t.Errorf("recorded digest missing: %#v", got)
	}
	if _, ok := got["images/nodigest:v1"]; ok {
		t.Error("an entry with no digest must be omitted, so the engine falls back to the source path")
	}
}

// TestRecordedDigestsIsEmptyWithoutARecord — a workspace with no mirror record
// must yield no map, so VerifyAllRecorded degrades to VerifyAll rather than
// treating "nothing recorded" as "nothing to check".
func TestRecordedDigestsIsEmptyWithoutARecord(t *testing.T) {
	name := "norecord"
	_, _ = workspaceFixture(t, name)
	if got := recordedDigests(name); len(got) != 0 {
		t.Errorf("recordedDigests on a workspace with no record = %#v, want empty", got)
	}
}
