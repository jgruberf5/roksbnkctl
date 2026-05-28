package demo

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
)

func TestIntent_Output(t *testing.T) {
	var buf bytes.Buffer
	Intent(&buf, "http2-demo", "demonstrates HTTP/2 h2c backend")
	got := buf.String()
	if !strings.Contains(got, "http2-demo") {
		t.Errorf("Intent output missing use-case name: %q", got)
	}
	if !strings.Contains(got, "demonstrates HTTP/2 h2c backend") {
		t.Errorf("Intent output missing intent string: %q", got)
	}
	if !strings.HasPrefix(got, "\n▶") {
		t.Errorf("Intent output should start with newline+▶, got: %q", got)
	}
}

func TestProof_Pass(t *testing.T) {
	var buf bytes.Buffer
	res := scenarios.Result{
		Status:     "ok",
		Summary:    "all assertions passed",
		EnvDiagram: "ascii-diagram",
	}
	Proof(&buf, "http2-demo", res)
	got := buf.String()
	if !strings.Contains(got, "PASS") {
		t.Errorf("Proof: status ok should display PASS, got: %q", got)
	}
	if !strings.Contains(got, "all assertions passed") {
		t.Errorf("Proof: summary missing, got: %q", got)
	}
	if !strings.Contains(got, "ascii-diagram") {
		t.Errorf("Proof: EnvDiagram should be printed, got: %q", got)
	}
	// Verify the correct prefix is used for a passing result.
	if !strings.Contains(got, "✔") {
		t.Errorf("Proof: PASS output must use ✔ prefix, got: %q", got)
	}
}

func TestProof_Fail(t *testing.T) {
	var buf bytes.Buffer
	res := scenarios.Result{
		Status:     "failed",
		Summary:    "apply error",
		Assertions: []scenarios.Assertion{{Description: "check", OK: false}},
	}
	Proof(&buf, "http2-demo", res)
	got := buf.String()
	if !strings.Contains(got, "FAIL") {
		t.Errorf("Proof: status failed should display FAIL, got: %q", got)
	}
	// Cosmetic guard: FAIL lines must use ✗, not ✔, to avoid misleading
	// operators reading a live demo walkthrough terminal output.
	if strings.Contains(got, "✔") {
		t.Errorf("Proof: FAIL output must not use ✔ prefix, got: %q", got)
	}
	if !strings.Contains(got, "✗") {
		t.Errorf("Proof: FAIL output must use ✗ prefix, got: %q", got)
	}
}

func TestProof_Skipped(t *testing.T) {
	var buf bytes.Buffer
	res := scenarios.Result{
		Status:  "skipped",
		Summary: "rated red — not testable",
	}
	Proof(&buf, "red-uc", res)
	got := buf.String()
	if !strings.Contains(got, "SKIPPED") {
		t.Errorf("Proof: status skipped should display SKIPPED, got: %q", got)
	}
}

func TestProof_DryRun(t *testing.T) {
	var buf bytes.Buffer
	res := scenarios.Result{
		Status:  "dry-run",
		Summary: "2 manifest(s) rendered; nothing applied",
	}
	Proof(&buf, "http2-demo", res)
	got := buf.String()
	if !strings.Contains(got, "DRY-RUN") {
		t.Errorf("Proof: status dry-run should display DRY-RUN, got: %q", got)
	}
}

func TestProof_EmptyDiagram(t *testing.T) {
	var buf bytes.Buffer
	res := scenarios.Result{
		Status:     "skipped",
		Summary:    "rated red",
		EnvDiagram: "", // empty — should not print blank line
	}
	Proof(&buf, "red-uc", res)
	got := buf.String()
	// Should have exactly one line ending in summary + newline, no trailing blank
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("Proof with empty EnvDiagram: expected 1 line, got %d: %q", len(lines), got)
	}
}
