package exec

// Sprint 3 / PRD 04 — output stream redactor tests.
//
// Asserts the contract from staff.md Priority 3:
//
//	func NewRedactor(w io.Writer, secrets []string) io.Writer
//
// PRD 04 cross-backend principle #1 ("never log credentials") requires
// defense-in-depth: backends shouldn't print secrets, but if a wrapped tool
// does (an `ibmcloud` debug-mode dump, a stack trace including env, etc.),
// the redactor MUST mask the value before it reaches the caller's
// stdout/stderr.
//
// The redactor must buffer across writes — a secret split across two
// `io.Writer.Write` calls (e.g., when the underlying pipe flushes mid-token)
// must still be redacted. Without buffering, naive substring replacement
// would miss the split case and leak the value.

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

const redactedMarker = "[REDACTED]"

// TestRedactor_SingleWrite covers the simple case: secret appears in a single
// Write() call.
func TestRedactor_SingleWrite(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, []string{"super-secret-key"})

	if _, err := io.WriteString(r, "before super-secret-key after"); err != nil {
		t.Fatalf("write: %v", err)
	}
	flush(r)

	got := buf.String()
	if strings.Contains(got, "super-secret-key") {
		t.Errorf("secret leaked through single-write redactor: %q", got)
	}
	if !strings.Contains(got, redactedMarker) {
		t.Errorf("expected %q in output, got %q", redactedMarker, got)
	}
}

// TestRedactor_SplitAcrossWrites covers the buffering requirement: secret
// arrives in two pieces across two Write() calls.
func TestRedactor_SplitAcrossWrites(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, []string{"super-secret-key"})

	// "super-" then "secret-key" — the redactor must buffer the prefix
	// "super-" until it can decide whether the full secret arrives.
	if _, err := io.WriteString(r, "before super-"); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if _, err := io.WriteString(r, "secret-key after"); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	flush(r)

	got := buf.String()
	if strings.Contains(got, "super-secret-key") {
		t.Errorf("secret leaked across split writes: %q", got)
	}
	if !strings.Contains(got, redactedMarker) {
		t.Errorf("expected redaction marker in split output, got %q", got)
	}
}

// TestRedactor_NoFalsePositive covers the negative case: a string that's a
// strict prefix of the secret, not the full secret, must pass through
// unchanged.
func TestRedactor_NoFalsePositive(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, []string{"super-secret-key"})

	if _, err := io.WriteString(r, "this string mentions super-but-not-the-full-secret here"); err != nil {
		t.Fatalf("write: %v", err)
	}
	flush(r)

	got := buf.String()
	if !strings.Contains(got, "super-but-not-the-full-secret") {
		t.Errorf("non-secret prefix was incorrectly redacted: %q", got)
	}
	if strings.Contains(got, redactedMarker) {
		t.Errorf("redaction marker appeared on non-match: %q", got)
	}
}

// TestRedactor_MultipleSecrets covers the case where the redactor is
// configured with two secrets and the stream contains both.
func TestRedactor_MultipleSecrets(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, []string{"key-aaa-111", "key-bbb-222"})

	if _, err := io.WriteString(r, "first key-aaa-111 then key-bbb-222 done"); err != nil {
		t.Fatalf("write: %v", err)
	}
	flush(r)

	got := buf.String()
	if strings.Contains(got, "key-aaa-111") {
		t.Errorf("first secret leaked: %q", got)
	}
	if strings.Contains(got, "key-bbb-222") {
		t.Errorf("second secret leaked: %q", got)
	}
}

// TestRedactor_EmptySecrets covers the no-op case: when there are no secrets
// to redact, the wrapper must pass bytes through unchanged. (Defensive: when
// the cred resolver returns an empty key — say, the ssh-only path that
// doesn't touch IBM Cloud — backends still wrap stdout, and that wrap must
// not corrupt the stream.)
func TestRedactor_EmptySecrets(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, nil)

	in := "no secrets here, just regular tool output\n"
	if _, err := io.WriteString(r, in); err != nil {
		t.Fatalf("write: %v", err)
	}
	flush(r)

	if got := buf.String(); got != in {
		t.Errorf("pass-through corrupted: got %q, want %q", got, in)
	}
}

// TestRedactor_SecretAtBoundary covers the edge where the secret falls at the
// very last byte of a Write call — the redactor must hold the tail until it
// can prove no continuation makes a match.
func TestRedactor_SecretAtBoundary(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, []string{"BOUNDARY-SECRET"})

	// First write ends mid-secret; second write completes it.
	if _, err := io.WriteString(r, "leading text BOUNDARY-"); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if _, err := io.WriteString(r, "SECRET trailing"); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	flush(r)

	if got := buf.String(); strings.Contains(got, "BOUNDARY-SECRET") {
		t.Errorf("secret at write boundary leaked: %q", got)
	}
}

// TestRedactor_SecretRepeated covers the case where the same secret appears
// twice in the stream. Both occurrences must be redacted.
func TestRedactor_SecretRepeated(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, []string{"DUPLICATE-KEY"})

	if _, err := io.WriteString(r, "DUPLICATE-KEY in middle DUPLICATE-KEY again"); err != nil {
		t.Fatalf("write: %v", err)
	}
	flush(r)

	got := buf.String()
	if strings.Contains(got, "DUPLICATE-KEY") {
		t.Errorf("repeated secret leaked at least once: %q", got)
	}
	if c := strings.Count(got, redactedMarker); c < 2 {
		t.Errorf("expected at least 2 redaction markers, got %d in %q", c, got)
	}
}

// flush calls Close() if the redactor implements io.Closer. The redactor's
// trailing-byte buffer (used to handle split-secret detection) must be
// flushed at end-of-stream; backends invoke this after the wrapped command
// exits. If the staff implementation doesn't use io.Closer, an alternative
// flushing API (e.g. a Flush() method or a known sentinel byte) is fine —
// the test only asserts that whatever flushing path the redactor exposes
// drains everything to the underlying writer.
func flush(w io.Writer) {
	if c, ok := w.(io.Closer); ok {
		_ = c.Close()
	}
}
