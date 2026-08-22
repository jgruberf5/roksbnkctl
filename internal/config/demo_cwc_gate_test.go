package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #169. The cwc Multi-Attach guard works around an F5 defect on BNK 2.3: the
// f5-spk-cwc Deployment mounts a ReadWriteOnce PVC but ships RollingUpdate, so
// an FLO rollover deadlocks the new pod on the volume and licensing never
// activates.
//
// 2.4 ships `strategy: Recreate`, which makes the deadlock structurally
// impossible. The guard's own header named that as its removal condition.
//
// It must be GATED, not deleted: 2.3 still ships RollingUpdate and is still the
// default manifest version, and dropping it there reintroduces a hang that
// surfaces as bnk up's 15-minute license gate timing out with no useful error.
func TestTheCWCGuardIsGatedOnTheLineNotDeleted(t *testing.T) {
	root := repoRootForDemoTest(t)

	guard := filepath.Join(root, "scripts", "demos", "disconnected-cluster-cli-demo", "cwc-guard.sh")
	if _, err := os.Stat(guard); err != nil {
		t.Fatalf("cwc-guard.sh is gone: %v\n"+
			"2.3 still ships RollingUpdate and is still the default manifest, so removing the "+
			"guard reintroduces the licensing hang on every 2.3 reused-cluster install.", err)
	}

	demo := filepath.Join(root, "scripts", "demos", "disconnected-cluster-cli-demo",
		"disconnected-cluster-cli-demo.sh")
	body, err := os.ReadFile(demo)
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)

	// The GATE, not a mention of it. A plain Contains for "bnk_line_of" is
	// satisfied by the comment above the gate — checked by mutation: removing the
	// conditional left this test green, because the explanatory comment still
	// named the function.
	gate := regexp.MustCompile(`(?m)^\s*\[\[[^\n]*bnk_line_of[^\n]*\]\]\s*\|\|`)
	if !gate.MatchString(src) {
		t.Error("the demo launches the cwc guard unconditionally; it must gate the launch on " +
			"the manifest line, because the guard is a 2.3-only workaround. Expected a " +
			"conditional of the form:\n" +
			`  [[ "$DRY_RUN" == "1" || "$(bnk_line_of "$MANIFEST_VERSION")" != "2.3" ]] || { ... }`)
	}
	// And it must be able to REACH the helper. This demo inlines its own format
	// helpers rather than sourcing demo-format.sh, so a function defined there is
	// invisible to it — the #164 failure, where a call was added without a source
	// and became a silent `command not found`.
	if !strings.Contains(src, "bnk-line.sh") {
		t.Error("the demo calls bnk_line_of but never sources bnk-line.sh. This script does not " +
			"source demo-format.sh, so the call would be a silent `command not found` and the " +
			"guard would never run — on a script that runs without `set -e`.")
	}
}

// The derivation must handle 2.4's shape. `2.4.0-EA` carries no build suffix
// where 2.3 has four dot/dash segments, so anything keyed on segment COUNT
// rather than prefix gets 2.4 wrong — and would run a 2.3 workaround against a
// 2.4 cluster.
func TestTheDemoLineDerivationMatchesTheProduct(t *testing.T) {
	root := repoRootForDemoTest(t)
	body, err := os.ReadFile(filepath.Join(root, "scripts", "demos", "lib", "bnk-line.sh"))
	if err != nil {
		t.Fatalf("lib/bnk-line.sh missing: %v", err)
	}
	src := string(body)

	// Prefix matching on the major.minor, the same rule config.BNKLine uses.
	for _, want := range []string{"2.3*", "2.4*"} {
		if !strings.Contains(src, want) {
			t.Errorf("bnk_line_of should match on the %q prefix", want)
		}
	}
	// An unknown version must not silently read as 2.3 — that would run the
	// workaround against a release nobody has characterised.
	if !strings.Contains(src, "should not assume 2.3") {
		t.Error("an unrecognised manifest must return empty rather than defaulting to a line")
	}
}
