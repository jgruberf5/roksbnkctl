package cli

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
)

// A dozen tests invoke a RunE body directly as runX(nil, args). Whether that
// panicked used to depend on whether the body returned before its first
// cmd.Context() — registry_target_test.go documents that constraint in a
// comment, which is a landmine for the next person who moves a guard earlier in
// a function.
func TestCmdContextToleratesANilCommand(t *testing.T) {
	if got := cmdContext(nil); got == nil {
		t.Fatal("cmdContext(nil) must return a usable context, not nil")
	}
	if err := cmdContext(nil).Err(); err != nil {
		t.Errorf("the fallback context must not start cancelled, got: %v", err)
	}
}

// And it must not quietly replace a real command's context — that would detach
// every command from Ctrl-C, which is the defect #116 exists to fix.
func TestCmdContextReturnsTheCommandsOwnContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "carried")

	cmd := &cobra.Command{Use: "x"}
	cmd.SetContext(ctx)

	if got := cmdContext(cmd).Value(key{}); got != "carried" {
		t.Errorf("cmdContext returned a different context than the command's (value = %v)", got)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(cancelCtx)
	cancel()
	if err := cmdContext(cmd).Err(); err == nil {
		t.Error("a cancelled command context must stay cancelled through cmdContext — " +
			"otherwise Ctrl-C stops propagating")
	}
}
