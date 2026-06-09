package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestInitExample_PrintsEmbeddedTFVars(t *testing.T) {
	var out bytes.Buffer
	initExampleCmd.SetOut(&out)
	t.Cleanup(func() { initExampleCmd.SetOut(nil) })

	if err := runInitExample(initExampleCmd, nil); err != nil {
		t.Fatalf("runInitExample: %v", err)
	}
	got := out.String()
	if len(got) < 200 {
		t.Fatalf("init example output too short (%d bytes) — embedded example not found?", len(got))
	}
	if !strings.Contains(got, "terraform.tfvars") {
		t.Errorf("output does not look like the tfvars example:\n%.120s", got)
	}
}
