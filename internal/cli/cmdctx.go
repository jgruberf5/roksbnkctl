package cli

import (
	"context"

	"github.com/spf13/cobra"
)

// cmdContext returns the command's context, or a background context when cmd is
// nil.
//
// Production callers always have a command — cobra sets the context in
// ExecuteContext, and root.go builds it with signal.NotifyContext so it cancels
// on Ctrl-C. The nil case is tests: a dozen of them invoke a RunE body directly
// as `runX(nil, args)` to exercise its validation without standing up the cobra
// tree.
//
// Before this existed, whether that panicked depended on whether the body
// happened to return before its first cmd.Context() — registry_target_test.go
// documents exactly that constraint ("a nil cobra.Command is safe here"), which
// is a landmine for the next person who moves a guard earlier in a function.
//
// This does NOT detach a production call from cancellation: cmd is non-nil
// there, so the caller's context is returned unchanged.
func cmdContext(cmd *cobra.Command) context.Context {
	if cmd == nil {
		return context.Background()
	}
	return cmd.Context()
}
