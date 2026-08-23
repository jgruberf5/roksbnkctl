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

// A demo that writes its config.yaml with an UNQUOTED heredoc is running the
// shell's substitution over every line of that document — including the
// comments. The demos' comments are full of backticked command names, so
//
//	# (matching the `init` interview). Phase 5/6 runs `testing up` + `test`
//
// executed `init`, `testing up` and `test`, and substituted their output into
// the file. Found by running the demo: `init` is a real binary on this host, so
// it ran and printed "Explicit --user argument required to run as user
// manager", and `testing up` produced "testing: command not found".
//
// The heredoc has to keep expanding ${REGION} and friends, so the fix is to
// escape the backticks rather than quote the delimiter.
//
// Two things are wrong when this regresses and only one is visible: the config
// silently loses the words (they become the commands' empty stdout), and the
// demo executes whatever those words happen to name on the host running it.
// This asserts the visible half, because it is the half that can be checked
// without letting the invisible half happen.
func TestDemoConfigHeredocsDoNotRunTheirOwnComments(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}
	if testing.Short() {
		t.Skip("drives the demos; runs in the full suite")
	}
	root := repoRootForDemoTest(t)
	stub := stubToolchain(t)

	// demo script -> where it writes its config, and phrases that appear ONLY
	// inside a backticked span in that file. If substitution happens, the span
	// becomes the command's empty stdout and the phrase disappears.
	//
	// The two demos write to different places on purpose: the CLI demo stages
	// into .demo-state, the CI demo writes onto the /work volume it bind-mounts
	// into every container. Pointing both at .demo-state made the CI case report
	// "wrote no config.yaml" — which the test treats as a failure rather than a
	// pass, because a check that finds nothing has checked nothing.
	work := t.TempDir()
	cases := map[string]struct {
		configGlob string
		wants      []string
	}{
		"scripts/demos/cluster-lifecycle-cli-demo/cluster-lifecycle-cli-demo.sh": {
			configGlob: "%s/.demo-state/*-config.yaml",
			wants:      []string{"`init`", "`testing up`", "`test`"},
		},
		"scripts/demos/cluster-lifecycle-ci-demo/cluster-lifecycle-ci-demo.sh": {
			configGlob: work + "/config.yaml",
			wants:      []string{"`init`", "`testing up`", "`test`"},
		},
	}

	for script, tc := range cases {
		t.Run(filepath.Base(script), func(t *testing.T) {
			full := filepath.Join(root, script)
			dir := filepath.Dir(full)

			pattern := tc.configGlob
			if strings.Contains(pattern, "%s") {
				pattern = fmt.Sprintf(pattern, dir)
			}
			before, _ := filepath.Glob(pattern)
			for _, f := range before {
				_ = os.Remove(f)
			}

			cmd := exec.Command(bash, full)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"),
				"IBMCLOUD_API_KEY=stub-key-for-the-dry-run-000",
				"DRY_RUN=1", "AUTO_ADVANCE=1",
				"PHASE_DELAY=0", "CMD_RENDER_HOLD=0", "CMD_POST_HOLD=0",
				"OUT_SETTLE_HOLD=0", "OUT_POST_HOLD=0", "PHASE_BANNER_HOLD=0",
				"FORGE_URL=", "FORGE_USER=", "FORGE_PASS=",
				"BNK_FORGE_URL=", "BNK_FORGE_USER=", "BNK_FORGE_PASSWORD=",
				"CI_WORK="+work,
			)
			out, _ := cmd.CombinedOutput()

			// The substitution announces itself in the demo's own output too,
			// because the commands run in the foreground.
			for _, leak := range []string{"command not found", "Explicit --user argument required"} {
				if strings.Contains(string(out), leak) {
					t.Errorf("the demo executed a word from inside its own heredoc (%q in the output).\n"+
						"An unquoted heredoc substitutes backticks; escape them as \\`.", leak)
				}
			}

			found, _ := filepath.Glob(pattern)
			if len(found) == 0 {
				t.Fatalf("the demo wrote no config.yaml, so this test checked nothing.\n--- output tail ---\n%s",
					tail(string(out), 20))
			}
			body, rerr := os.ReadFile(found[0])
			if rerr != nil {
				t.Fatal(rerr)
			}
			for _, w := range tc.wants {
				if !strings.Contains(string(body), w) {
					t.Errorf("generated config lost %q — the heredoc substituted it away.\n"+
						"The words are gone from the file AND were run as commands on this host.\n"+
						"--- generated ---\n%s", w, string(body))
				}
			}
		})
	}
}

// The behavioural test above samples three phrases from one generated file.
// That is not enough: an adversarial review added a comment containing an
// unescaped `true` to a config heredoc, the demo executed it, the word vanished
// from the generated config — and the test passed, because the leak detectors
// only match "command not found" and "Explicit --user argument required".
// Any backticked word naming a real, QUIET binary (true, date, env, id, cat)
// reintroduces the bug invisibly.
//
// So this checks the structure instead: every unquoted heredoc body in every
// demo script, for an unescaped backtick or $(. It is a source scan, but of
// PARSED structure rather than sampled text — it tracks heredoc open/close and
// knows which delimiters are quoted, which is exactly the distinction that
// decides whether substitution happens.
func TestNoDemoHeredocSubstitutesUnescapedCommands(t *testing.T) {
	root := repoRootForDemoTest(t)

	scripts, err := filepath.Glob(filepath.Join(root, "scripts/demos/**/*.sh"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	more, _ := filepath.Glob(filepath.Join(root, "scripts/demos/*/*.sh"))
	scripts = append(scripts, more...)
	libs, _ := filepath.Glob(filepath.Join(root, "scripts/demos/lib/*.sh"))
	scripts = append(scripts, libs...)

	seen := map[string]bool{}
	checked := 0
	// `<<DELIM` or `<<-DELIM` with an UNQUOTED delimiter substitutes; `<<"D"`
	// and `<<'D'` do not.
	open := regexp.MustCompile(`<<-?\s*([A-Za-z_][A-Za-z0-9_]*)\s*$`)
	risky := regexp.MustCompile("(^|[^\\\\])(`|\\$\\()")

	for _, f := range scripts {
		if seen[f] {
			continue
		}
		seen[f] = true
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			continue
		}
		lines := strings.Split(string(b), "\n")
		rel, _ := filepath.Rel(root, f)
		for i := 0; i < len(lines); i++ {
			m := open.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			checked++
			delim := m[1]
			// The close line is not always bare. These demos open heredocs INSIDE a
			// quoted argument — `onvsi "cat > f <<YAML` — so the terminator is
			// `YAML"`. Requiring an exact match made the parser run past the real
			// close and report the rest of the file as heredoc body, which is how
			// three plain shell comments got flagged (and, earlier, escaped for a
			// substitution that could never have happened there).
			closeRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(delim) + `["'` + "`" + `)]*\s*$`)
			for j := i + 1; j < len(lines) && !closeRe.MatchString(lines[j]); j++ {
				line := lines[j]
				// Only COMMENT and prose lines are the hazard. Live code inside a
				// heredoc — a script being written out — needs its substitutions,
				// and flagging those would make this test unusable.
				if !strings.HasPrefix(strings.TrimSpace(line), "#") && !isProse(line) {
					continue
				}
				if risky.MatchString(line) {
					t.Errorf("%s:%d is inside an UNQUOTED heredoc (<<%s), so the shell substitutes it:\n"+
						"  %s\n"+
						"The backticked word is EXECUTED on the host running the demo and replaced by its "+
						"output, so the text silently disappears from the generated file. Escape it as \\`, "+
						"or quote the delimiter if the body needs no expansion.",
						rel, j+1, delim, strings.TrimSpace(line))
				}
				i = j
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no heredocs at all in the demo scripts; the parser broke and this test checked nothing")
	}
}

// isProse reports whether a heredoc line is human text rather than shell — the
// banner/say blocks the demos embed. Anything with an assignment, a pipe or a
// leading command word is treated as code and left alone.
func isProse(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if strings.ContainsAny(t, "|;&") || strings.Contains(t, "=") {
		return false
	}
	return strings.Contains(t, " ") && !strings.HasPrefix(t, "$")
}
