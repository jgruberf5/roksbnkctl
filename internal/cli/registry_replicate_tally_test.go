package cli

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/registry/mirror"
)

// #150, after review. The invariant is that every artifact that fails to copy
// increments the count that later gates the install. That used to be an inline
// loop, so the only available test was a source scan for the assignment — and
// an adversarial review demonstrated the obvious hole by commenting the line
// out and watching the test still pass. A substring scan cannot tell live code
// from a comment.
//
// summarizeReplication is that loop, extracted, so the count can be driven with
// synthetic results and asserted for real.

func res(name string, err error, skipped bool) mirror.Result {
	return mirror.Result{
		Artifact: bnkbom.Artifact{Kind: bnkbom.KindImage, Name: name, Tag: "v1"},
		Digest:   "sha256:" + name,
		Err:      err,
		Skipped:  skipped,
	}
}

func TestSummarizeReplicationCountsEveryFailure(t *testing.T) {
	boom := errors.New("401 unauthorized")
	results := []mirror.Result{
		res("a", nil, false),
		res("b", boom, false),
		res("c", nil, true), // skipped still counts as present in the mirror
		res("d", boom, false),
		res("e", boom, false),
	}

	mirrored, failed := summarizeReplication(results, io.Discard, true)

	if failed != 3 {
		t.Errorf("failed = %d, want 3 — this number becomes MissingCount and gates the install", failed)
	}
	if len(mirrored) != 2 {
		t.Errorf("mirrored = %d artifacts, want 2 (the two that copied, including the skipped one)", len(mirrored))
	}
	for _, a := range mirrored {
		if a.Name == "b" || a.Name == "d" || a.Name == "e" {
			t.Errorf("%s failed to copy and must not be recorded as mirrored", a.Name)
		}
	}
}

// A clean run must report zero, which is what clears the flag from a previous
// partial attempt without anyone editing the record by hand.
func TestSummarizeReplicationReportsZeroOnACleanRun(t *testing.T) {
	results := []mirror.Result{res("a", nil, false), res("b", nil, true), res("c", nil, false)}
	mirrored, failed := summarizeReplication(results, io.Discard, true)
	if failed != 0 {
		t.Errorf("a clean run must report 0 failures, got %d", failed)
	}
	if len(mirrored) != 3 {
		t.Errorf("all three artifacts should be recorded, got %d", len(mirrored))
	}
}

// Every failure has to be named, or an operator sees a count with no way to
// learn which artifacts to chase.
func TestSummarizeReplicationNamesEachFailure(t *testing.T) {
	var out strings.Builder
	summarizeReplication([]mirror.Result{
		res("a", nil, false),
		res("harbor-thing", errors.New("401 unauthorized"), false),
	}, &out, true)

	got := out.String()
	if !strings.Contains(got, "FAIL") || !strings.Contains(got, "harbor-thing") {
		t.Errorf("the failing artifact should be named:\n%s", got)
	}
	// --quiet suppresses the per-success chatter but never the failures.
	if strings.Contains(got, "copied") {
		t.Errorf("quiet mode should not print successes:\n%s", got)
	}
}
