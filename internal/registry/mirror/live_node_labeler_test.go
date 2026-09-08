//go:build livemirror

// Live verification of #270 against a real registry.
//
//	set -a; . ~/.roksbnkctl-creds/artifactory.env; set +a
//	go test -tags livemirror ./internal/registry/mirror/ -run TestLive -v
//
// Build-tagged because it needs credentials and network; CI never runs it. It
// exists because "no live replication run" was listed as a gap in PR #284 when
// the means to close it were already on this machine.
package mirror

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/registry/ocireg"
)

func liveTarget(t *testing.T) *ocireg.Target {
	t.Helper()
	host, user, tok := os.Getenv("ART_DOMAIN"), os.Getenv("ART_USER"), os.Getenv("ART_TOKEN")
	if host == "" || user == "" || tok == "" {
		t.Skip("ART_DOMAIN/ART_USER/ART_TOKEN not set")
	}
	return &ocireg.Target{
		Host:      host,
		Namespace: "bnk-mirror/live270",
		Auth:      authn.FromConfig(authn.AuthConfig{Username: user, Password: tok}),
	}
}

// The artifact this PR changes the BOM to produce, built through Deps() rather
// than hand-written, so the live run exercises what `registry replicate` would
// actually mirror.
func liveNodeLabeler(t *testing.T) bnkbom.Artifact {
	t.Helper()
	for _, a := range bnkbom.Deps("v1.17.3", "v1.36.0") {
		if a.Origin == bnkbom.OriginNodeLabeler {
			return a
		}
	}
	t.Fatal("Deps() produced no node-labeler artifact")
	return bnkbom.Artifact{}
}

// TestLiveReplicateAndVerifyNodeLabeler proves, against a real registry:
//
//  1. the pinned image is actually pullable and mirrorable (the pin is not just
//     a string that parses);
//  2. source-comparing verification passes right after replication;
//  3. recorded verification passes WITHOUT the source being reachable at all —
//     the air-gapped case, simulated by pointing the BOM's SourceHost at a host
//     that does not resolve.
//
// (3) is the whole point of #270: VerifyAll cannot answer in that state, and
// VerifyAllRecorded can.
func TestLiveReplicateAndVerifyNodeLabeler(t *testing.T) {
	tgt := liveTarget(t)
	a := liveNodeLabeler(t)
	t.Logf("source ref: %s", a.Ref())
	t.Logf("target ref: %s", tgt.PushRef(a))

	eng := &Engine{Target: tgt, Concurrency: 1}
	bom := &bnkbom.BOM{Artifacts: []bnkbom.Artifact{a}}
	ctx := context.Background()

	res := eng.Replicate(ctx, bom)
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	if res[0].Err != nil {
		t.Fatalf("replicating %s: %v", a.Ref(), res[0].Err)
	}
	recordedDigest := res[0].Digest
	t.Logf("replicated, digest %s", recordedDigest)
	t.Cleanup(func() {
		for _, d := range eng.Delete(context.Background(), []bnkbom.Artifact{
			{Kind: a.Kind, SourceHost: a.SourceHost, Name: a.Name, Tag: a.Tag, Digest: recordedDigest},
		}) {
			if d.Err != nil {
				t.Logf("cleanup: %v (leftover at %s)", d.Err, tgt.PushRef(a))
			}
		}
	})

	if bad := eng.Verify(ctx, bom); len(bad) != 0 {
		t.Fatalf("source-comparing verify failed right after replication: %+v", bad)
	}
	t.Log("VerifyAll (source comparison) passed")

	// Air-gap: the source no longer resolves. Same artifact, same mirror.
	gone := a
	gone.SourceHost = "unreachable.invalid"
	goneBOM := &bnkbom.BOM{Artifacts: []bnkbom.Artifact{gone}}

	if bad := eng.Verify(ctx, goneBOM); len(bad) != 1 {
		t.Fatalf("with an unreachable source, VerifyAll should fail; got %d bad", len(bad))
	}
	t.Log("VerifyAll fails when the source is unreachable — the #270 condition")

	got := eng.VerifyAllRecorded(ctx, goneBOM,
		map[string]string{RecordedKey(gone): recordedDigest})
	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	if got[0].Err != nil {
		t.Fatalf("recorded verify failed with the source unreachable: %v", got[0].Err)
	}
	fmt.Fprintln(os.Stderr, "  VerifyAllRecorded passed with NO source access — this is the fix")
}
