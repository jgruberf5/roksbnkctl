package exec

import (
	"bytes"
	"io"
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
}

// NewRedactor wraps w; any byte sequence matching one of the non-empty
// secrets is replaced with [REDACTED] before reaching the underlying
// writer. Buffering across writes catches secrets split across chunk
// boundaries. Empty / zero-length secrets are ignored (passing nil or
// the zero slice yields a transparent passthrough).
//
// The returned io.Writer also implements io.Closer; callers MUST call
// Close() at end-of-stream so the held-back tail bytes drain to w.
// Backends call Close() in their cleanup defer; tests use the test-
// helper that detects the io.Closer and calls it.
func NewRedactor(w io.Writer, secrets []string) io.Writer {
	r := &redactor{w: w}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		r.secrets = append(r.secrets, []byte(s))
		if len(s) > r.maxLen {
			r.maxLen = len(s)
		}
	}
	return r
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
		// Try to match any secret starting at data[i].
		matched := false
		for _, s := range r.secrets {
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
	for _, s := range r.secrets {
		if len(tail) < len(s) && bytes.HasPrefix(s, tail) {
			return true
		}
	}
	return false
}
