package demo

import (
	"fmt"
	"io"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
)

// Intent prints the "why" line shown to an operator before a use-case runs.
// Pure presentation — no kube I/O.
func Intent(out io.Writer, useCase, intent string) {
	fmt.Fprintf(out, "\n▶ %s — %s\n", useCase, intent)
}

// Proof prints the result line and the ASCII data-path diagram after a run.
// It reuses the diagram already rendered into result.EnvDiagram by
// [scenarios.Run] — it does NOT call [scenarios.Render] and does no kube I/O.
//
// Status display:
//   - "ok"      → PASS
//   - "skipped" → SKIPPED  (Red-rated use-case short-circuited before Apply)
//   - "dry-run" → DRY-RUN
//   - anything else → FAIL
func Proof(out io.Writer, useCase string, result scenarios.Result) {
	var label string
	switch result.Status {
	case "ok":
		label = "PASS"
	case "skipped":
		label = "SKIPPED"
	case "dry-run":
		label = "DRY-RUN"
	default:
		label = "FAIL"
	}
	prefix := "✔"
	if label == "FAIL" {
		prefix = "✗"
	}
	fmt.Fprintf(out, "%s %s: %s — %s\n", prefix, useCase, label, result.Summary)
	if result.EnvDiagram != "" {
		fmt.Fprintln(out, result.EnvDiagram)
	}
}
