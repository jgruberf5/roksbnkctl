package mirror

import (
	"context"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

// Issue #270. VerifyAll re-resolves the SOURCE digest and compares the mirror to
// whatever upstream holds at that moment. With a floating tag that makes a
// perfectly good mirror report as mismatched the instant upstream re-pushes, and
// in the air-gapped case -- where the source is unreachable by definition -- it
// can only fail. VerifyAllRecorded compares against the digest replication
// actually wrote.

// TestVerifyAllRecorded_SurvivesTheSourceMoving is the regression. The source tag
// is re-pushed with different content AFTER the mirror was made, exactly as
// Docker Hub moving `latest` would. VerifyAll must now fail; VerifyAllRecorded
// must still pass, because the mirror is unchanged and still matches what was
// recorded.
func TestVerifyAllRecorded_SurvivesTheSourceMoving(t *testing.T) {
	host := startRegistry(t)
	opts := []crane.Option{crane.Insecure}
	tgt := fakeTarget{host: host, ns: "mirror"}

	a := bnkbom.Artifact{Kind: bnkbom.KindImage, SourceHost: host, Name: "src/img", Tag: "latest"}
	bom := &bnkbom.BOM{Artifacts: []bnkbom.Artifact{a}}

	// Replicate: push v1 to the source, copy it to the mirror, record the digest.
	first, err := random.Image(512, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(first, host+"/src/img:latest", opts...); err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(first, tgt.PushRef(a), opts...); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{Target: tgt, Insecure: true}

	rec := eng.VerifyAll(context.Background(), bom)
	if len(rec) != 1 || rec[0].Err != nil {
		t.Fatalf("precondition: mirror should verify before the source moves: %+v", rec)
	}
	recorded := map[string]string{RecordedKey(a): rec[0].Digest}

	// Upstream moves the tag. The mirror is untouched.
	second, err := random.Image(512, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(second, host+"/src/img:latest", opts...); err != nil {
		t.Fatal(err)
	}

	if bad := eng.Verify(context.Background(), bom); len(bad) != 1 {
		t.Fatalf("source-comparing Verify should now report a mismatch, got %d bad", len(bad))
	}

	got := eng.VerifyAllRecorded(context.Background(), bom, recorded)
	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	if got[0].Err != nil {
		t.Errorf("recorded verify failed on an unchanged mirror: %v", got[0].Err)
	}
}

// TestVerifyAllRecorded_CatchesAChangedMirror proves the check still bites. If it
// passed anything with a record, it would be worse than the bug it replaces.
func TestVerifyAllRecorded_CatchesAChangedMirror(t *testing.T) {
	host := startRegistry(t)
	opts := []crane.Option{crane.Insecure}
	tgt := fakeTarget{host: host, ns: "mirror"}

	a := bnkbom.Artifact{Kind: bnkbom.KindImage, SourceHost: host, Name: "src/img", Tag: "v1"}
	bom := &bnkbom.BOM{Artifacts: []bnkbom.Artifact{a}}

	img, err := random.Image(512, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(img, host+"/src/img:v1", opts...); err != nil {
		t.Fatal(err)
	}
	// The mirror holds something ELSE than what was recorded.
	other, err := random.Image(512, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(other, tgt.PushRef(a), opts...); err != nil {
		t.Fatal(err)
	}
	srcDigest, err := crane.Digest(host+"/src/img:v1", opts...)
	if err != nil {
		t.Fatal(err)
	}

	eng := &Engine{Target: tgt, Insecure: true}
	got := eng.VerifyAllRecorded(context.Background(), bom, map[string]string{RecordedKey(a): srcDigest})
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("a mirror that differs from the record must fail: %+v", got)
	}
}

// TestVerifyAllRecorded_FallsBackWhenUnrecorded pins the fallback. An artifact
// absent from the record must still be compared against the source rather than
// waved through -- a partial inventory must not turn into partial verification.
func TestVerifyAllRecorded_FallsBackWhenUnrecorded(t *testing.T) {
	host := startRegistry(t)
	opts := []crane.Option{crane.Insecure}
	tgt := fakeTarget{host: host, ns: "mirror"}

	a := bnkbom.Artifact{Kind: bnkbom.KindImage, SourceHost: host, Name: "src/img", Tag: "v1"}
	bom := &bnkbom.BOM{Artifacts: []bnkbom.Artifact{a}}

	img, err := random.Image(512, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(img, host+"/src/img:v1", opts...); err != nil {
		t.Fatal(err)
	}
	// Never mirrored. With no record for it, the source-comparing path must run
	// and report it missing at the target.
	eng := &Engine{Target: tgt, Insecure: true}
	got := eng.VerifyAllRecorded(context.Background(), bom, map[string]string{"images/somethingelse:v9": "sha256:dead"})
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("an unrecorded, unmirrored artifact must fail via the source path: %+v", got)
	}
}
