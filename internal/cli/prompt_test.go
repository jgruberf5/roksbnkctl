package cli

import "testing"

// Under `go test` stdin is not a TTY, so promptSelect must return its default
// option without reading — the same non-interactive contract as
// promptString/promptInt/promptYesNo.
func TestPromptSelect_NonTTY(t *testing.T) {
	if got := promptSelect("pick", []string{"a", "b", "c"}, 1); got != 1 {
		t.Errorf("promptSelect non-TTY = %d, want default 1", got)
	}
	// An out-of-range default clamps to 0.
	if got := promptSelect("pick", []string{"a"}, 5); got != 0 {
		t.Errorf("promptSelect out-of-range default = %d, want clamped 0", got)
	}
	// Empty options returns 0.
	if got := promptSelect("pick", nil, 3); got != 0 {
		t.Errorf("promptSelect empty options = %d, want 0", got)
	}
}
