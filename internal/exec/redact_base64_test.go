package exec

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

// #145. The redactor's whole reason for existing is that a wrapped tool might
// print a credential the backend never meant to emit. In this system those
// credentials move base64-encoded as a matter of routine — every *_b64 config
// field, every Kubernetes Secret value — so a redactor that knows only the raw
// form covers half the shapes the same secret takes.
//
// The demo masker in scripts/demos/lib/demo-format.sh already registered both
// forms, and says why: "configs often carry the base64 form". This closes the
// same gap in the product path.

const apiKey = "an-ibm-cloud-api-key-value-long-enough"

// write drives the full writer contract: Close() flushes the held-back tail,
// so a secret at the very end of the stream is still scanned.
func write(t *testing.T, secrets []string, s string) string {
	t.Helper()
	var buf bytes.Buffer
	r := NewRedactor(&buf, secrets)
	if _, err := io.WriteString(r, s); err != nil {
		t.Fatalf("write: %v", err)
	}
	if c, ok := r.(io.Closer); ok {
		if err := c.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	return buf.String()
}

// The standalone encodings: a value encoded on its own, which is what a
// Kubernetes Secret's data map and every *_b64 config field contain.
func TestStandaloneBase64FormsAreRedacted(t *testing.T) {
	for name, enc := range map[string]string{
		"std":    base64.StdEncoding.EncodeToString([]byte(apiKey)),
		"rawStd": base64.RawStdEncoding.EncodeToString([]byte(apiKey)),
		"url":    base64.URLEncoding.EncodeToString([]byte(apiKey)),
		"rawURL": base64.RawURLEncoding.EncodeToString([]byte(apiKey)),
	} {
		t.Run(name, func(t *testing.T) {
			got := write(t, []string{apiKey}, "data:\n  api-key: "+enc+"\n")
			if strings.Contains(got, enc) {
				t.Errorf("the %s encoding of the secret reached the writer:\n%s", name, got)
			}
			if !strings.Contains(got, redactMarker) {
				t.Errorf("expected %s in the output, got:\n%s", redactMarker, got)
			}
		})
	}
}

// The harder shape: the secret encoded as part of a LARGER blob, e.g. a whole
// kubeconfig run through base64. None of the standalone encodings appear —
// the characters depend on where the secret starts modulo 3 — so this is what
// the alignment-shifted middles exist for.
//
// All three offsets are exercised, because covering only offset 0 would pass
// with the standalone forms alone and prove nothing.
func TestBase64EmbeddedInALargerBlobIsRedacted(t *testing.T) {
	for off := 0; off < 3; off++ {
		prefix := strings.Repeat("x", off)
		blob := base64.StdEncoding.EncodeToString([]byte(
			"kubeconfig-preamble-" + prefix + apiKey + "-trailing-content"))

		got := write(t, []string{apiKey}, "blob: "+blob+"\n")
		if !strings.Contains(got, redactMarker) {
			t.Errorf("offset %d: the secret is inside this base64 blob and was not redacted:\n%s", off, got)
		}
		// And the encoding of the secret at this alignment must be gone: a
		// marker alone could come from an unrelated match.
		mid := alignedMiddle([]byte(apiKey), (len("kubeconfig-preamble-")+off)%3)
		if len(mid) > 0 && strings.Contains(got, string(mid)) {
			t.Errorf("offset %d: the aligned encoding survived in the output:\n%s", off, got)
		}
	}
}

// alignedMiddle's contract is that its result appears VERBATIM in any standard
// base64 stream containing the secret at that alignment. Asserting the contract
// directly pins the arithmetic — an off-by-one in the front or tail trim would
// still redact something and pass the test above.
func TestAlignedMiddleActuallyAppearsInTheStream(t *testing.T) {
	raw := []byte(apiKey)
	for off := 0; off < 3; off++ {
		mid := alignedMiddle(raw, off)
		if len(mid) == 0 {
			t.Fatalf("offset %d produced no middle", off)
		}
		// Build several streams that place the secret at this alignment with
		// different surrounding content; the middle must appear in every one.
		for _, tail := range []string{"", "z", "zz", "zzz", "trailing-bytes"} {
			head := strings.Repeat("h", off)
			stream := base64.StdEncoding.EncodeToString([]byte(head + apiKey + tail))
			if !strings.Contains(stream, string(mid)) {
				t.Errorf("offset %d, tail %q: middle %q does not appear in %q", off, tail, mid, stream)
			}
		}
	}
}

// Split writes: the base64 form must survive chunking exactly as the raw form
// does. maxLen is derived from the registered entries, so the encoded forms —
// which are LONGER than the raw secret — must widen the hold-back window. If
// maxLen were still computed from the raw values only, a split encoding would
// leak.
func TestBase64FormIsRedactedWhenSplitAcrossWrites(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte(apiKey))
	full := "prefix " + enc + " suffix"

	for _, at := range []int{1, 7, len(full) / 2, len(full) - 3} {
		var buf bytes.Buffer
		r := NewRedactor(&buf, []string{apiKey})
		if _, err := io.WriteString(r, full[:at]); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(r, full[at:]); err != nil {
			t.Fatal(err)
		}
		if c, ok := r.(io.Closer); ok {
			_ = c.Close()
		}
		if strings.Contains(buf.String(), enc) {
			t.Errorf("split at %d leaked the encoded secret:\n%s", at, buf.String())
		}
	}
}

// The raw form must keep working — the encoded variants are additional, not a
// replacement.
func TestRawFormStillRedacted(t *testing.T) {
	got := write(t, []string{apiKey}, "before "+apiKey+" after")
	if strings.Contains(got, apiKey) {
		t.Errorf("the raw secret leaked:\n%s", got)
	}
	if !strings.HasPrefix(got, "before ") || !strings.HasSuffix(got, " after") {
		t.Errorf("surrounding text must survive intact, got:\n%s", got)
	}
}

// Redacting legitimate output is its own failure. Very short secrets get no
// encoded variants, because their encodings are short enough to collide with
// ordinary text.
func TestShortSecretsGetNoBase64Variants(t *testing.T) {
	if v := base64Variants("abc"); v != nil {
		t.Errorf("a 3-byte secret should produce no variants, got %q", v)
	}
	if v := base64Variants(strings.Repeat("k", minBase64Secret)); len(v) == 0 {
		t.Error("a secret at the threshold should produce variants")
	}
}

// Ordinary output must pass through untouched — a redactor that fires on
// unrelated text trains people to distrust it.
func TestUnrelatedOutputIsUnchanged(t *testing.T) {
	const clean = "Creating cluster bnk-demo in us-south...\nnamespace/bnk created\n"
	if got := write(t, []string{apiKey}, clean); got != clean {
		t.Errorf("clean output was modified:\ngot:  %q\nwant: %q", got, clean)
	}
}

// Overlapping registrations must redact the LONGER match, not whichever was
// registered first — otherwise part of the longer secret survives in the clear.
func TestLongestMatchWins(t *testing.T) {
	got := write(t, []string{"secret", "secret-with-more"}, "x secret-with-more y")
	if strings.Contains(got, "-with-more") {
		t.Errorf("the shorter secret matched first and left the remainder exposed:\n%s", got)
	}
}
