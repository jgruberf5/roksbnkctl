package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunTFXReadJSON_Pairs(t *testing.T) {
	dir := t.TempDir()
	floFile := filepath.Join(dir, "flo-version.txt")
	cisFile := filepath.Join(dir, "cis-version.txt")
	if err := os.WriteFile(floFile, []byte("  2.3.10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cisFile, []byte("2.19.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// cis-version present, plus a deliberately missing file to prove the empty-string
	// fallback (mirrors the modules' `cat … 2>/dev/null`).
	flagReadJSONPairs = []string{
		"flo=" + floFile,
		"cis=" + cisFile,
		"missing=" + filepath.Join(dir, "nope.txt"),
	}
	flagReadJSONFile, flagReadJSONRaw = "", false
	t.Cleanup(func() { flagReadJSONPairs = nil })

	var buf bytes.Buffer
	cmd := tfxReadJSONCmd
	cmd.SetOut(&buf)
	if err := runTFXReadJSONCmd(cmd, nil); err != nil {
		t.Fatalf("read-json --pair: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output not valid JSON: %v (%q)", err, buf.String())
	}
	want := map[string]string{"flo": "2.3.10", "cis": "2.19.1", "missing": ""}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("result[%q] = %q want %q", k, got[k], v)
		}
	}
}
