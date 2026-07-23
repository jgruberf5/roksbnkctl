package tf

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// Terraform failures used to reach the user as walls of text: the IBM provider
// emits a multi-line `Error: --- … summary: … ---` block per failed resource, and
// the same blocks get re-streamed on every retry and re-printed as the wrapped
// error. diagCapture tees terraform's stderr (so live progress is unchanged) into
// a bounded tail buffer, and summarizeTerraformDiagnostics collapses that buffer
// into a short, deduplicated summary appended to the returned error.

// diagCapture is an io.Writer that passes writes through to an underlying writer
// (live streaming) while retaining the last `max` bytes for post-failure parsing.
type diagCapture struct {
	w   io.Writer
	max int

	mu  sync.Mutex
	buf []byte
}

func newDiagCapture(w io.Writer, max int) *diagCapture {
	return &diagCapture{w: w, max: max}
}

func (d *diagCapture) Write(p []byte) (int, error) {
	d.mu.Lock()
	d.buf = append(d.buf, p...)
	if len(d.buf) > d.max {
		d.buf = d.buf[len(d.buf)-d.max:]
	}
	d.mu.Unlock()
	if d.w != nil {
		return d.w.Write(p)
	}
	return len(p), nil
}

// String returns the captured tail. Reset clears it — called at the start of each
// apply/destroy so a summary reflects only the LAST attempt, not every retry.
func (d *diagCapture) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.buf)
}

func (d *diagCapture) Reset() {
	d.mu.Lock()
	d.buf = d.buf[:0]
	d.mu.Unlock()
}

// summarizeTerraformDiagnostics parses captured terraform stderr into a compact,
// deduplicated error summary. It understands both the IBM provider's YAML-ish
// block (`summary:`/`resource:`/`operation:` fields) and a plain terraform
// `Error: <title>` line. Identical messages are collapsed to `×N`. Returns "" when
// no error diagnostics are found (so the caller leaves the raw error untouched).
func summarizeTerraformDiagnostics(raw string) string {
	type block struct{ resource, operation, summary, title string }
	var blocks []block
	var cur *block
	flush := func() {
		if cur != nil {
			blocks = append(blocks, *cur)
			cur = nil
		}
	}

	for _, ln := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "Error:"):
			flush()
			cur = &block{}
			// The IBM provider opens the block with a bare `Error: ---`; a plain
			// terraform error carries its title on this same line.
			if title := strings.TrimSpace(strings.TrimPrefix(t, "Error:")); title != "" && title != "---" {
				cur.title = title
			}
		case strings.HasPrefix(t, "Warning:"):
			flush() // warnings don't belong in the error summary
		case cur == nil:
			// outside a block
		case strings.HasPrefix(t, "summary:"):
			cur.summary = unquoteYAMLScalar(strings.TrimSpace(strings.TrimPrefix(t, "summary:")))
		case strings.HasPrefix(t, "resource:"):
			cur.resource = strings.TrimSpace(strings.TrimPrefix(t, "resource:"))
		case strings.HasPrefix(t, "operation:"):
			cur.operation = strings.TrimSpace(strings.TrimPrefix(t, "operation:"))
		}
	}
	flush()

	counts := map[string]int{}
	var order []string
	for _, b := range blocks {
		msg := b.summary
		if msg == "" {
			msg = b.title
		}
		if msg == "" {
			continue
		}
		key := msg
		if b.resource != "" {
			prefix := b.resource
			if b.operation != "" {
				prefix += " " + b.operation
			}
			key = prefix + " -- " + msg
		}
		if counts[key] == 0 {
			order = append(order, key)
		}
		counts[key]++
	}
	if len(order) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "-- terraform diagnostics (deduplicated) --\nterraform failed with %d distinct error(s):", len(order))
	for _, k := range order {
		msg := k
		if len(msg) > 320 {
			msg = msg[:317] + "..."
		}
		fmt.Fprintf(&sb, "\n  x%d  %s", counts[k], msg)
	}
	return sb.String()
}

// unquoteYAMLScalar strips a single layer of matching single/double quotes from a
// YAML scalar (the IBM provider single-quotes its summary strings), and undoes
// YAML's doubled-single-quote escaping.
func unquoteYAMLScalar(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			inner := s[1 : len(s)-1]
			if s[0] == '\'' {
				inner = strings.ReplaceAll(inner, "''", "'")
			}
			return inner
		}
	}
	return s
}
