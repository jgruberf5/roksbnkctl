package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func TestSelectOutputs_Redaction(t *testing.T) {
	outs := map[string]config.StateOutput{
		"ip":  {Value: "1.2.3.4"},
		"key": {Value: "-----BEGIN KEY-----", Sensitive: true},
		"x":   {Value: "y"},
	}
	sel := selectOutputs(outs, []string{"ip", "key"})
	if sel["ip"] != "1.2.3.4" {
		t.Errorf("ip = %v, want 1.2.3.4", sel["ip"])
	}
	if sel["key"] != "<sensitive>" {
		t.Errorf("sensitive value not redacted: %v", sel["key"])
	}
	if _, ok := sel["x"]; ok {
		t.Error("selectOutputs must only include the requested keys")
	}
}

func TestRenderPhaseStatus(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	// JSON render of a deployed phase round-trips.
	flagStatusJSON = true
	defer func() { flagStatusJSON = false }()
	ps := phaseStatus{Phase: "testing", Deployed: true, Outputs: map[string]any{"ip": "1.2.3.4"}}
	if err := renderPhaseStatus(cmd, ps); err != nil {
		t.Fatal(err)
	}
	var got phaseStatus
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if !got.Deployed || got.Phase != "testing" || got.Outputs["ip"] != "1.2.3.4" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Not-deployed human output.
	flagStatusJSON = false
	buf.Reset()
	if err := renderPhaseStatus(cmd, phaseStatus{Phase: "gateway"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "gateway: not deployed") {
		t.Errorf("not-deployed output = %q", buf.String())
	}
}
