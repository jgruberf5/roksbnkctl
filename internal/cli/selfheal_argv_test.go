package cli

import (
	"strings"
	"testing"
)

// The optional region / resource-group values are passed to `sh -c` as
// POSITIONAL parameters, never interpolated into the script text — that is what
// keeps the API key out of the literal and makes the whole command
// injection-safe. The script's "$N" references and the argv positions therefore
// have to agree exactly; a mismatch silently logs in with the wrong value, or
// with an empty one.
func TestRemoteHealCommandPositionsMatchTheScript(t *testing.T) {
	for _, tc := range []struct {
		name         string
		region, rg   string
		wantRefs     []string
		wantTrailing []string
		wantNoRefs   []string
	}{
		{
			name:         "region only",
			region:       "us-south",
			wantRefs:     []string{`-r "$3"`},
			wantTrailing: []string{"_", "cid", "s3cr3t-api-key-value", "us-south"},
			wantNoRefs:   []string{`-g "$`},
		},
		{
			name:         "resource group only",
			rg:           "default",
			wantRefs:     []string{`-g "$3"`},
			wantTrailing: []string{"_", "cid", "s3cr3t-api-key-value", "default"},
			wantNoRefs:   []string{`-r "$`},
		},
		{
			name:         "both",
			region:       "us-south",
			rg:           "default",
			wantRefs:     []string{`-r "$3"`, `-g "$4"`},
			wantTrailing: []string{"_", "cid", "s3cr3t-api-key-value", "us-south", "default"},
		},
		{
			name:         "neither",
			wantTrailing: []string{"_", "cid", "s3cr3t-api-key-value"},
			wantNoRefs:   []string{`-r "$`, `-g "$`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := remoteHealCommand("cid", "s3cr3t-api-key-value", tc.region, tc.rg)
			if len(argv) < 3 || argv[0] != "sh" || argv[1] != "-c" {
				t.Fatalf("expected an `sh -c` invocation, got %q", argv)
			}
			script, positional := argv[2], argv[3:]

			for _, ref := range tc.wantRefs {
				if !strings.Contains(script, ref) {
					t.Errorf("script is missing %q:\n%s", ref, script)
				}
			}
			for _, ref := range tc.wantNoRefs {
				if strings.Contains(script, ref) {
					t.Errorf("script should not contain %q:\n%s", ref, script)
				}
			}
			if len(positional) != len(tc.wantTrailing) {
				t.Fatalf("positional args = %q, want %q", positional, tc.wantTrailing)
			}
			for i, want := range tc.wantTrailing {
				if positional[i] != want {
					t.Errorf("$%d = %q, want %q", i, positional[i], want)
				}
			}
			// The key travels as a positional value, never in the script text.
			if strings.Contains(script, "s3cr3t-api-key-value") {
				t.Errorf("the API key must not appear in the script literal:\n%s", script)
			}
		})
	}
}
