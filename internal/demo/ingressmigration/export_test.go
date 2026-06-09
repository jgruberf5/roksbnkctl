package ingressmigration

// This file exports internal constructors for use by the external _test package.
// It is compiled only during `go test`.

import (
	"context"
	"io"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
)

// NewScenarioForTest returns a scenario with the supplied deps injected.
// Pass a no-op helmRunner to skip real Helm network calls in unit tests.
// Only for use in tests.
func NewScenarioForTest(d VerifyDeps) scenarios.Scenario {
	return &scenario{vDeps: &d, helm: &noopHelmRunner{}}
}

// SetForgeScanFn replaces the package-level forge scan function.
// Restore the original with the returned undo func.
func SetForgeScanFn(fn func(ctx context.Context, clusterID int, out io.Writer)) func() {
	original := forgeScanFn
	forgeScanFn = fn
	return func() { forgeScanFn = original }
}
