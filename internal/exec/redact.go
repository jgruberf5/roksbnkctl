package exec

import (
	"bytes"
	"encoding/base64"
	"io"
	"sort"
)

// redactMarker is the placeholder substituted for any matched secret.
// PRD 04 §"Cross-backend principles" #1 — backends shouldn't leak creds,
// but the redactor catches accidental tool-side prints (e.g., an
// `ibmcloud` debug dump that includes env vars). The marker is plain
// ASCII so it shows up cleanly in any sink (terminal, file, log
// pipeline). The validator's redact_test.go uses the same string under
// the name `redactMarker`; ours is `redactMarker` to avoid a same-
// package redeclare collision.
const redactMarker = "[REDACTED]"

// redactor wraps an io.Writer and substitutes redactMarker for any
// occurrence of one of secrets in the output stream.
//
// Buffering: the redactor must hold back the trailing tail of each
// Write call up to (maxSecretLen-1) bytes so a secret split across two
// Write calls is still caught. Without buffering, naive substring
// replacement on each Write would miss split secrets — the canonical
// failure mode validators test against. Closers must call Close() at
// end-of-stream to flush the held-back tail.
type redactor struct {
	w       io.Writer
	secrets [][]byte
	maxLen  int
	buf     bytes.Buffer

	// byFirst indexes secrets by their first byte. scan consults it at every
	// position in the stream, so without it the cost is
	// O(len(data) * len(secrets)) — and registering the encoded forms
	// multiplies len(secrets) several-fold. With it, the overwhelming majority
	// of positions look up an empty bucket and advance, which makes the cost
	// independent of how many secrets are registered.
	byFirst [256][]int
}

// NewRedactor wraps w; any byte sequence matching one of the non-empty
// secrets is replaced with [REDACTED] before reaching the underlying
// writer. Buffering across writes catches secrets split across chunk
// boundaries. Empty / zero-length secrets are ignored (passing nil or
// the zero slice yields a transparent passthrough).
//
// Each secret is registered in its raw form AND in its base64 forms (see
// base64Variants), because that is how credentials actually move through this
// system — *_b64 config fields, Kubernetes Secret values, anything a tool
// dumps from a rendered config. Registering only the raw form would mask half
// the shapes the same credential takes.
//
// The returned io.Writer also implements io.Closer; callers MUST call
// Close() at end-of-stream so the held-back tail bytes drain to w.
// Backends call Close() in their cleanup defer; tests use the test-
// helper that detects the io.Closer and calls it.
func NewRedactor(w io.Writer, secrets []string) io.Writer {
	r := &redactor{w: w}
	seen := make(map[string]bool)
	add := func(b []byte) {
		if len(b) == 0 || seen[string(b)] {
			return
		}
		seen[string(b)] = true
		r.secrets = append(r.secrets, b)
	}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		add([]byte(s))
		for _, v := range base64Variants(s) {
			add(v)
		}
	}
	// Longest first. scan tries entries in order at each position and takes the
	// first hit, so this makes the LONGEST entry win among those starting at the
	// same index — without it a short alignment fragment could match inside a
	// longer standalone encoding and leave the surrounding characters in the
	// stream.
	//
	// It does not, and cannot, fix overlap across DIFFERENT start indices: scan
	// walks left to right, so a short secret matching at i consumes those bytes
	// and a longer one starting at i+k is then only partially matched. That is
	// inherent to a single greedy pass. It does not arise from the variants of
	// one secret (they share a source string and were fuzzed for it); it would
	// need two registered secrets that genuinely interleave.
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	for i, b := range r.secrets {
		if len(b) > r.maxLen {
			r.maxLen = len(b)
		}
		r.byFirst[b[0]] = append(r.byFirst[b[0]], i)
	}
	return r
}

// minBase64Secret is the shortest secret worth generating base64 variants for.
// Encoded fragments of a very short secret are themselves short enough to
// collide with ordinary output, and redacting legitimate text is its own kind
// of failure. Real credentials — API keys, passwords, tokens — clear this by a
// wide margin.
const minBase64Secret = 8

// base64Variants returns the encoded forms of s that may appear in a stream.
//
// Secrets in this system routinely travel base64-encoded: ibmcloud.api_key_b64,
// registry.generic_password_b64, bnk.cis.bigip_password_b64,
// bnkforge.ca_b64, and every Kubernetes Secret, whose values are base64 by
// definition. A redactor that knows only the raw form passes all of those
// through untouched.
//
// Two shapes are covered:
//
//   - The standalone encodings, padded and unpadded, in both the standard and
//     URL alphabets. This is the common case: a value encoded on its own, as in
//     a Secret's data map or a *_b64 config field.
//
//   - The alignment-shifted middles. When s is encoded as part of a LARGER blob
//     (a whole kubeconfig run through base64), its encoding depends on where it
//     starts modulo 3, and none of the standalone forms appear. For each of the
//     three offsets this emits the run of characters that encode only 3-byte
//     groups lying entirely inside s — contaminated leading and trailing groups
//     dropped — which is therefore guaranteed to appear verbatim.
//
// Not covered, and worth stating plainly:
//
//   - LINE-WRAPPED base64. A wrapped encoding matches nothing, because the
//     newline lands mid-token. openssl wraps at 64 columns, GNU base64 at 76,
//     PEM at 64, and YAML block scalars at whatever the emitter chose. Closing
//     this means whitespace-tolerant matching, which costs on the hot path;
//     it is a known gap, not an oversight.
//   - An alphabet other than standard or URL.
//   - Any transformation applied before encoding (compression, encryption).
//
// The redactor is defense-in-depth against a tool that prints a credential, not
// a general exfiltration control.
func base64Variants(s string) [][]byte {
	if len(s) < minBase64Secret {
		return nil
	}
	raw := []byte(s)
	out := [][]byte{
		[]byte(base64.StdEncoding.EncodeToString(raw)),
		[]byte(base64.RawStdEncoding.EncodeToString(raw)),
		[]byte(base64.URLEncoding.EncodeToString(raw)),
		[]byte(base64.RawURLEncoding.EncodeToString(raw)),
	}
	// Both alphabets, because the middles differ whenever the secret encodes to
	// a "+" or "/" — which happens for any secret containing "~", "?", ">" or
	// "&". BIG-IP and Harbor passwords routinely contain those, and
	// bnk.cis.bigip_password_b64 / registry.generic_password_b64 are exactly the
	// fields this is meant to cover.
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		for off := 0; off < 3; off++ {
			if m := alignedMiddle(raw, off, enc); len(m) >= minBase64Secret {
				out = append(out, m)
			}
		}
	}
	return out
}

// alignedMiddle returns the base64 characters that depend only on raw, for a
// stream in which raw begins at a byte position congruent to off modulo 3.
//
// Base64 encodes in 3-byte groups. A group that also contains bytes from
// outside raw encodes to characters this function cannot predict, so the
// leading group (when off > 0) and any trailing partial group are dropped. What
// remains encodes whole groups drawn entirely from raw, and appears verbatim in
// the stream regardless of what surrounds it.
//
// The filler bytes are arbitrary — they only exist to shift the alignment, and
// every character they influence is dropped. enc selects the alphabet; it must
// be an unpadded encoding, since padding would terminate the middle early.
func alignedMiddle(raw []byte, off int, enc *base64.Encoding) []byte {
	buf := make([]byte, off, off+len(raw))
	buf = append(buf, raw...)
	encoded := enc.EncodeToString(buf)

	front := 0
	if off > 0 {
		front = 4 // the first group mixes filler with raw
	}
	tail := 0
	if rem := len(buf) % 3; rem != 0 {
		tail = rem + 1 // the last group is partial, so the next bytes change it
	}
	if front+tail >= len(encoded) {
		return nil
	}
	return []byte(encoded[front : len(encoded)-tail])
}

// Write implements io.Writer. The contract is:
//
//   - All bytes p[i] are eventually delivered to the underlying writer
//     (transformed: occurrences of any secret are replaced with the
//     redacted marker). Returns n=len(p) on success regardless of the
//     transformed-output size, matching io.Writer semantics for layered
//     filters.
//
//   - The trailing tail of the buffered output (up to maxLen-1 bytes
//     that could be the start of a secret) is held until either (a) a
//     subsequent Write disambiguates it, or (b) Close() flushes the
//     remainder unconditionally.
func (r *redactor) Write(p []byte) (int, error) {
	if len(r.secrets) == 0 {
		return r.w.Write(p)
	}
	r.buf.Write(p)

	// Scan the buffer for matches. Replace them in place. The held-back
	// region is the last (maxLen-1) bytes of the buffer — those could
	// start a secret that completes in the next Write.
	hold := r.maxLen - 1
	if hold < 0 {
		hold = 0
	}
	if r.buf.Len() <= hold {
		// Not enough buffered to make a flush safe — wait for more.
		return len(p), nil
	}

	flushable := r.buf.Bytes()[:r.buf.Len()-hold]
	transformed, kept := r.scan(flushable, false)
	if _, err := r.w.Write(transformed); err != nil {
		return 0, err
	}

	// Reconstruct the buffer: any bytes scan() decided to keep (because
	// they overlapped with the held-back tail and might still match) +
	// the held-back tail itself.
	tail := append([]byte(nil), kept...)
	tail = append(tail, r.buf.Bytes()[r.buf.Len()-hold:]...)
	r.buf.Reset()
	r.buf.Write(tail)

	return len(p), nil
}

// Close flushes the held-back tail to the underlying writer. Safe to
// call multiple times.
func (r *redactor) Close() error {
	if r.buf.Len() == 0 {
		return nil
	}
	transformed, _ := r.scan(r.buf.Bytes(), true)
	_, err := r.w.Write(transformed)
	r.buf.Reset()
	return err
}

// scan walks data left-to-right, replacing every full match of any
// secret with redactMarker. Returns the transformed bytes plus, if
// final=false, any trailing bytes that are an ambiguous prefix of one
// of the secrets and should stay in the buffer for the next Write.
//
// When final=true, every byte is emitted (no prefix held back) — this
// is the Close() path.
func (r *redactor) scan(data []byte, final bool) (out, keep []byte) {
	out = make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		// Try to match any secret starting at data[i]. Only the secrets that
		// begin with this byte are candidates; the rest cannot match here.
		matched := false
		for _, si := range r.byFirst[data[i]] {
			s := r.secrets[si]
			if i+len(s) <= len(data) && bytes.Equal(data[i:i+len(s)], s) {
				out = append(out, []byte(redactMarker)...)
				i += len(s)
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		// No full match. If we're not final and data[i:] is a strict
		// prefix of any secret, hold it for the next Write.
		if !final {
			if r.isPotentialPrefix(data[i:]) {
				keep = data[i:]
				return out, keep
			}
		}

		out = append(out, data[i])
		i++
	}
	return out, nil
}

// isPotentialPrefix reports whether tail is a non-empty strict prefix of
// any secret — i.e., tail could be the start of a secret that completes
// when more bytes arrive.
func (r *redactor) isPotentialPrefix(tail []byte) bool {
	if len(tail) == 0 {
		return false
	}
	for _, si := range r.byFirst[tail[0]] {
		s := r.secrets[si]
		if len(tail) < len(s) && bytes.HasPrefix(s, tail) {
			return true
		}
	}
	return false
}
