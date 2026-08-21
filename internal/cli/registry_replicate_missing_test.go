package cli

import (
	"os"
	"strings"
	"testing"
)

// #150. The render refuses an incomplete mirror record, which only helps if the
// commands that WRITE records actually set the count. Both writers are one
// assignment inside a long function — the shape where a check on the reading
// side quietly guards nothing.
//
// A source guard, because both paths need a live registry to reach: replicate
// copies ~89 artifacts over the network and delete removes them. #119 settled
// on the same approach for the same reason.
func TestReplicateRecordsTheFailureCount(t *testing.T) {
	src, err := os.ReadFile("registry_replicate.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	if !strings.Contains(body, "MissingCount:") {
		t.Error("registry replicate does not record MissingCount, so a partial mirror is " +
			"indistinguishable from a complete one and the tfvars render's check reads a field " +
			"nothing ever sets")
	}
	// It must record the count it already computed, not a constant — writing 0
	// unconditionally would satisfy the check above and defeat the purpose.
	if !strings.Contains(body, "MissingCount:    failed") {
		t.Error("MissingCount should carry the `failed` count computed from the results, " +
			"so a clean run records 0 and clears a previous partial attempt")
	}
}

// A half-torn-down mirror is no safer to install from than a half-filled one.
// `registry delete` keeps a record when some deletions fail, holding only the
// artifacts it could NOT remove.
func TestDeleteRecordsWhatItRemoved(t *testing.T) {
	src, err := os.ReadFile("registry_delete.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "rec.MissingCount += deleted") {
		t.Error("registry delete leaves a record describing a mirror it has partially emptied " +
			"without recording that the deleted artifacts are now missing")
	}
	// Accumulates rather than assigns: the record may already have been
	// incomplete before the delete ran.
	if strings.Contains(body, "rec.MissingCount = deleted") {
		t.Error("assigning discards a pre-existing MissingCount; the mirror is missing what it " +
			"was already missing PLUS what was just removed")
	}
}
