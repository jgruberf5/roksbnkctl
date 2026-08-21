package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #164. Both lifecycle demos died in their PREFLIGHT without FORGE_URL/USER/PASS,
// while BNK Forge is used by exactly one phase — `bnkforge register`. Nothing
// else touches it: internal/tf/vars.go carries no Forge fields, and two sibling
// demos (shared-licensing-cli-demo, disconnected-cluster-cli-demo) already run
// `bnk up` with no Forge at all.
//
// So the gate locked five of six phases — the entire terraform half — behind a
// credential they do not use. Validating the v1.51.0 terraform change meant
// either obtaining Forge access or driving the commands by hand.
//
// The demos that need this treatment are the ones that RUN a Forge phase. Found
// by scanning rather than hard-coded, so a new lifecycle demo is covered on the
// day it lands rather than whenever someone remembers this file.
func forgeGatedDemos(t *testing.T) map[string]string {
	t.Helper()
	root := repoRootForDemoTest(t)
	out := map[string]string{}
	err := filepath.Walk(filepath.Join(root, "scripts", "demos"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".sh") {
			return err
		}
		if strings.Contains(filepath.ToSlash(path), "/demos/lib/") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if !strings.Contains(string(body), "bnkforge register") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no demo runs `bnkforge register` — this guard can no longer see what it guards")
	}
	return out
}

// The preflight must not refuse to start. This is the regression itself: a
// `die` on a missing FORGE_* before phase 1 is what made the Forge-free
// majority of each demo unreachable.
func TestNoDemoDiesInPreflightForAMissingForge(t *testing.T) {
	// A `die` whose message mentions FORGE_URL/USER/PASS. The partial-config
	// die inside forge_mode is fine and lives in lib/, which is excluded.
	dieOnForge := regexp.MustCompile(`(?m)^\s*\[\[[^\n]*FORGE_(URL|USER|PASS)[^\n]*\]\]\s*\|\|\s*die`)

	for name, body := range forgeGatedDemos(t) {
		if m := dieOnForge.FindString(body); m != "" {
			t.Errorf("%s refuses to start without BNK Forge:\n  %s\n\n"+
				"Forge gates one phase, not the demo. Killing the run in preflight makes the "+
				"cluster build and the BNK install — neither of which touches Forge — "+
				"unreachable without credentials they do not use. Call forge_mode instead.",
				name, strings.TrimSpace(m))
		}
	}
}

// Deciding the mode is not enough; the register phase has to respect it. Scoped
// to the phase BLOCK rather than the file, because FORGE_ENABLED legitimately
// appears in the preflight summary too — a whole-file Contains check stayed
// green when the register call was made unconditional, since the preflight
// occurrence still satisfied it.
func TestTheForgePhaseIsConditionalOnTheResolvedMode(t *testing.T) {
	for name, body := range forgeGatedDemos(t) {
		if !strings.Contains(body, "forge_mode") {
			t.Errorf("%s runs `bnkforge register` but never calls forge_mode, so nothing "+
				"decides whether Forge is available", name)
			continue
		}

		// The block is the register PHASE: from its banner to the next endphase.
		//
		// Anchored on the phase banner, not on the first "bnkforge register" in
		// the file — that is a header comment, and anchoring there walked back to
		// phase 1 and reported the wrong block entirely.
		banner := regexp.MustCompile(`(?m)^\s*(?:pause;\s*)?phase\s+P\d+\s+"[^"]*bnkforge register[^"]*"`)
		loc := banner.FindStringIndex(body)
		if loc == nil {
			t.Errorf("%s: could not find the `bnkforge register` phase banner; this guard can no "+
				"longer see the block it checks", name)
			continue
		}
		rest := body[loc[0]:]
		end := strings.Index(rest, "endphase")
		if end < 0 {
			t.Errorf("%s: could not find the end of the Forge phase", name)
			continue
		}
		block := rest[:end]

		if !strings.Contains(block, "FORGE_ENABLED") {
			t.Errorf("%s: the `bnkforge register` phase does not branch on FORGE_ENABLED, so it "+
				"runs regardless — and fails AFTER the cluster is built, which is worse than "+
				"failing in preflight.\n--- phase block ---\n%s", name, block)
		}
		// A skipped phase must say so on screen. These demos are recorded; a
		// silently absent phase is indistinguishable from one that ran.
		if !strings.Contains(block, "forge_skip_note") {
			t.Errorf("%s: the Forge phase can be skipped without telling the viewer. These demos "+
				"are recorded — an unexplained gap reads as a phase that ran.", name)
		}
	}
}

// A partial configuration must still fail, and fail EARLY. Someone who set
// FORGE_URL and mistyped FORGE_USER meant to use Forge; silently skipping the
// phase would let them watch the demo believing registration happened.
//
// This RUNS forge_mode rather than scanning for its error text. The first
// version looked for the string "half-configured" in the file and stayed green
// when the die was replaced with a silent fallback — because the word also
// appears in the comment above it. A scan cannot tell code from prose.
func TestForgeModeResolvesTheThreeStates(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}
	lib := filepath.Join(repoRootForDemoTest(t), "scripts", "demos", "lib", "forge-mode.sh")

	// A stub die, so the lib is exercised without pulling in demo-format.sh.
	const prelude = `die(){ echo "DIED: $*" >&2; exit 3; }; source "%s"; `

	for _, tc := range []struct {
		name     string
		env      []string
		wantCode int
		wantOut  string
	}{
		{"all three set", []string{"FORGE_URL=https://f", "FORGE_USER=u", "FORGE_PASS=p"}, 0, "true"},
		{"none set", nil, 0, "false"},
		{"url only", []string{"FORGE_URL=https://f"}, 3, ""},
		{"missing password", []string{"FORGE_URL=https://f", "FORGE_USER=u"}, 3, ""},
		{"password only", []string{"FORGE_PASS=p"}, 3, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bash, "-c", fmt.Sprintf(prelude, lib)+`forge_mode; echo "$FORGE_ENABLED"`)
			cmd.Env = append(os.Environ(), tc.env...)
			// Clear anything inherited, so the "none set" case is genuinely none.
			cmd.Env = append(cmd.Env, "FORGE_URL=", "FORGE_USER=", "FORGE_PASS=")
			cmd.Env = append(cmd.Env, tc.env...)
			out, _ := cmd.CombinedOutput()
			code := cmd.ProcessState.ExitCode()

			if code != tc.wantCode {
				t.Errorf("exit %d, want %d\n%s", code, tc.wantCode, out)
			}
			if tc.wantOut != "" && !strings.Contains(string(out), tc.wantOut) {
				t.Errorf("FORGE_ENABLED should be %q:\n%s", tc.wantOut, out)
			}
			if tc.wantCode == 3 {
				// The error must name what is missing, or the operator is left
				// guessing which of three variables they got wrong.
				if !strings.Contains(string(out), "FORGE_") {
					t.Errorf("the refusal should name the missing variables:\n%s", out)
				}
			}
		})
	}
}

// The enabled path must export the BNK_FORGE_* names the tooling reads by name
// — that is what the removed preflight block did, and the register phase and the
// CI runner both depend on it.
func TestForgeModeExportsTheRuntimeNames(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}
	lib := filepath.Join(repoRootForDemoTest(t), "scripts", "demos", "lib", "forge-mode.sh")

	cmd := exec.Command(bash, "-c",
		`die(){ exit 3; }; source "`+lib+`"; forge_mode; `+
			`echo "$BNK_FORGE_URL|$BNK_FORGE_USER|$BNK_FORGE_PASSWORD"`)
	cmd.Env = append(os.Environ(),
		"FORGE_URL=https://forge.example.com", "FORGE_USER=demo", "FORGE_PASS=secret-pass")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("forge_mode: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "https://forge.example.com|demo|secret-pass" {
		t.Errorf("BNK_FORGE_* not exported as expected, got %q", got)
	}
}
