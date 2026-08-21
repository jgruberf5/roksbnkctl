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

// The preflight must not refuse to start. This is the regression itself.
//
// Asserted by RUNNING each demo with no Forge in the environment and checking it
// reaches its LAST phase. The first version was a regex over the `[[ … ]] || die`
// form, and an adversarial review reinstated the exact regression using
// `if [ -z "$FORGE_URL" ]; then die …; fi` — one of six spellings that evaded it.
// A pattern match can always be spelled around; "does the demo still get to the
// end" cannot.
//
// The host toolchain is stubbed, so this runs anywhere: the demos check for
// roksbnkctl/terraform/helm/jq on PATH, and CI has none of them.
func TestDemosReachTheirLastPhaseWithNoForgeConfigured(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}
	if testing.Short() {
		t.Skip("drives both demos end to end; runs in the full suite")
	}
	root := repoRootForDemoTest(t)
	stub := stubToolchain(t)

	for name := range forgeGatedDemos(t) {
		t.Run(filepath.Base(name), func(t *testing.T) {
			script := filepath.Join(root, name)
			// The last phase banner the demo prints, e.g. "PHASE 6/6" or "PHASE 7/7".
			body, rerr := os.ReadFile(script)
			if rerr != nil {
				t.Fatal(rerr)
			}
			last := regexp.MustCompile(`PHASE (\d+)/(\d+)`).FindAllStringSubmatch(string(body), -1)
			if len(last) == 0 {
				t.Fatal("no phase banners found")
			}
			total := last[0][2]
			want := "PHASE " + total + "/" + total

			cmd := exec.Command(bash, script)
			cmd.Dir = filepath.Dir(script)
			cmd.Env = append(os.Environ(),
				"PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"),
				"IBMCLOUD_API_KEY=stub-key-for-the-dry-run-000",
				"DRY_RUN=1", "AUTO_ADVANCE=1",
				// The recording holds exist to make a video readable; a test does
				// not need them, and they dominate the runtime (184s -> a few).
				"PHASE_DELAY=0", "CMD_RENDER_HOLD=0", "CMD_POST_HOLD=0",
				"OUT_SETTLE_HOLD=0", "OUT_POST_HOLD=0", "PHASE_BANNER_HOLD=0",
				// Explicitly empty: the point is a demo with NO Forge anywhere.
				"FORGE_URL=", "FORGE_USER=", "FORGE_PASS=",
				"BNK_FORGE_URL=", "BNK_FORGE_USER=", "BNK_FORGE_PASSWORD=",
			)
			out, _ := cmd.CombinedOutput()

			if !strings.Contains(string(out), want) {
				t.Errorf("%s never reached %s without Forge configured.\n"+
					"Forge gates one phase; refusing in preflight makes the cluster build and the "+
					"BNK install — neither of which touches Forge — unreachable without "+
					"credentials they do not use.\n--- output tail ---\n%s",
					name, want, tail(string(out), 25))
			}
		})
	}
}

// stubToolchain puts fake roksbnkctl/terraform/helm/jq/docker on PATH so the
// demos' `command -v` preflight passes on a machine that has none of them. The
// roksbnkctl stub answers `version` in the real format, which preflight_binary
// parses.
func stubToolchain(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"terraform", "helm", "jq", "docker"} {
		write := filepath.Join(dir, tool)
		if err := os.WriteFile(write, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rbk := "#!/bin/sh\ncase \"$1\" in version) echo \"roksbnkctl v99.0.0 (commit stub, built now)\";; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "roksbnkctl"), []byte(rbk), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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
		// The names roksbnkctl itself reads (internal/cli/bnkforge.go, chapter
		// 24a). An operator who exports these has configured Forge; treating
		// them as unconfigured silently skipped the phase, and in the CI demo
		// the container would have had working credentials anyway (#164 review).
		{"BNK_FORGE_* names only", []string{
			"BNK_FORGE_URL=https://f", "BNK_FORGE_USER=u", "BNK_FORGE_PASSWORD=p"}, 0, "true"},
		{"mixed FORGE_ and BNK_FORGE_", []string{
			"FORGE_URL=https://f", "BNK_FORGE_USER=u", "BNK_FORGE_PASSWORD=p"}, 0, "true"},
		{"partial across both spellings", []string{
			"FORGE_URL=https://f", "BNK_FORGE_USER=u"}, 3, ""},
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
