package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The book's two reference chapters are GENERATED, and until now nothing checked
// that the checked-in copies matched their generators.
//
// Chapter 28 was worse than un-generated: it was written by hand against a
// 190-field struct, so a field could ship undocumented or documented wrongly and
// nothing noticed. That is the same shape as the defect that let
// cneinstance_advanced_env be documented in three places while no terraform read
// it — documentation and code agreeing only by someone remembering.
//
// These regenerate and diff. A stale chapter is now a failing test rather than a
// reader's problem.
func TestGeneratedBookChaptersAreCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the generators; part of the full suite")
	}
	root := repoRootForDemoTest(t)

	for _, tc := range []struct {
		gen     string
		chapter string
	}{
		{"./tools/refgen/config-md", "book/src/28-configuration-reference.md"},
		{"./tools/refgen/tfvars-md", "book/src/29-terraform-variable-reference.md"},
		// Chapter 27 was generated and then hand-edited, with nothing checking it,
		// so it drifted: flag names gained backticks the generator does not emit
		// and rows were reordered. Its own header says "Re-run on every CLI surface
		// change", which is only true if something enforces it. Adding a command
		// without regenerating left the command reference silently missing it.
		{"./tools/refgen/cobra-md", "book/src/27-command-reference.md"},
		// The config.yaml cheatsheet. Generated for the same reason the chapters
		// are: a hand-written copy of a 189-field schema is a second source of
		// truth, and this codebase has now shipped that defect three times. It
		// also carries the version badge, which the release commit rolls forward
		// -- so this test is what makes "update it as part of the build" true
		// rather than a line in a checklist.
		{"./tools/refgen/config-cheatsheet", "scripts/demos/config-cheatsheet.html"},
	} {
		t.Run(filepath.Base(tc.chapter), func(t *testing.T) {
			cmd := exec.Command("go", "run", tc.gen)
			cmd.Dir = root
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("running %s: %v", tc.gen, err)
			}
			if len(out) == 0 {
				t.Fatalf("%s produced no output, so this comparison would pass vacuously", tc.gen)
			}
			onDisk, err := os.ReadFile(filepath.Join(root, tc.chapter))
			if err != nil {
				t.Fatalf("read %s: %v", tc.chapter, err)
			}
			if string(onDisk) != string(out) {
				t.Errorf("%s is stale.\n  Regenerate with:  go run %s > %s\n"+
					"  (on disk %d bytes, generated %d)\n"+
					"  Edit the SOURCE — the struct doc comments or variables.tf — not the chapter.",
					tc.chapter, tc.gen, tc.chapter, len(onDisk), len(out))
			}
		})
	}
}

// A ratchet, not a wall. 73 of 190 config fields have no doc comment, so the
// generated reference has 73 blank description cells. Requiring all of them at
// once would mean writing 73 descriptions blind, which produces worse
// documentation than none. This just stops the number growing: a new field
// arrives documented, and the count comes down as fields are touched.
func TestUndocumentedConfigFieldsDoNotGrow(t *testing.T) {
	root := repoRootForDemoTest(t)
	b, err := os.ReadFile(filepath.Join(root, "book/src/28-configuration-reference.md"))
	if err != nil {
		t.Skipf("chapter unreadable: %v", err)
	}

	// ZERO rows carry no description. Every config field the reference publishes
	// now says what it does. The ceiling is the floor: any new field without a doc
	// comment on the struct fails the build.
	const ceiling = 0

	lines := strings.Split(string(b), "\n")

	// Find the description column from the HEADER rather than hard-coding an
	// index. This test previously hard-coded 5 and was reading `required`, which
	// is never blank — so it reported 0 undocumented fields out of 215 and passed
	// vacuously, for exactly as long as the table has had a `default` column. A
	// guard that returns a confident answer about the wrong column is worse than
	// no guard, because it converts "nobody checked" into "something checked and
	// said yes".
	descCol := -1
	for _, line := range lines {
		if !strings.HasPrefix(line, "| key |") {
			continue
		}
		for i, h := range strings.Split(line, "|") {
			if strings.TrimSpace(h) == "description" {
				descCol = i
			}
		}
		break
	}
	if descCol < 0 {
		t.Fatal("no `| key |` header row with a `description` column found in the reference; " +
			"this test cannot locate the column it grades and would otherwise pass vacuously")
	}

	blank, total := 0, 0
	for _, line := range lines {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		total++
		cols := strings.Split(line, "|")
		if len(cols) > descCol && strings.TrimSpace(cols[descCol]) == "" {
			blank++
		}
	}
	if total == 0 {
		t.Fatal("parsed no field rows from the reference; this test would pass vacuously")
	}
	if blank > ceiling {
		t.Errorf("%d of %d config fields have no doc comment, up from %d.\n"+
			"A new field needs a comment on the struct in internal/config/workspace.go — "+
			"the reference is generated from it, so an undocumented field is an undocumented "+
			"row in the book.", blank, total, ceiling)
	}
	if blank < ceiling {
		t.Logf("undocumented config fields down to %d (ceiling %d) — lower the ceiling in this test.", blank, ceiling)
	}
}

// The generated chapters carry text the generators do not control -- Go doc
// comments off the Workspace struct, and `description` strings out of
// terraform/variables.tf. Markdown passes raw HTML straight through, so a
// comment mentioning ~/.roksbnkctl/<name>/config.yaml emitted a live <name> tag
// and the browser rendered NOTHING (#239). The published book read "Workspace is
// ~/.roksbnkctl//config.yaml": the one word telling you the segment was a
// placeholder was the word that disappeared.
//
// mdbook reported this on every build, eleven times, and the warnings scrolled
// past. This turns the same property into a failing test.
//
// It asserts on the CHECKED-IN chapters rather than on mdesc's unit tests,
// because those cover the escaper in isolation and this covers the thing a
// reader actually gets. A generator that stopped calling mdesc entirely would
// pass every test in that package and fail this one.
func TestGeneratedChaptersHaveNoRawHTMLTags(t *testing.T) {
	root := repoRootForDemoTest(t)

	for _, chapter := range []string{
		"book/src/28-configuration-reference.md",
		"book/src/29-terraform-variable-reference.md",
		"book/src/27-command-reference.md",
	} {
		t.Run(filepath.Base(chapter), func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, chapter))
			if err != nil {
				t.Fatalf("read %s: %v", chapter, err)
			}

			inFence := false
			for i, line := range strings.Split(string(b), "\n") {
				// A fenced block is literal text, brackets and all -- chapter 27 is
				// mostly usage blocks full of `cmd <name>`, and every one of them is
				// correct. Missing this was the first version's false positive.
				if strings.HasPrefix(strings.TrimSpace(line), "```") {
					inFence = !inFence
					continue
				}
				if inFence {
					continue
				}
				for _, tag := range rawTagsOutsideCode(line) {
					t.Errorf("%s:%d: %q is outside a code span, so markdown emits it as an HTML tag "+
						"and it renders as nothing.\n"+
						"  The generators route description text through tools/refgen/mdesc; this line did not go through it.\n"+
						"  line: %s",
						chapter, i+1, tag, strings.TrimSpace(line))
				}
			}
		})
	}
}

// rawTagsOutsideCode returns every <...> in line that markdown would emit as raw
// HTML: that is, every one not sitting inside an inline backtick code span.
// Escaped brackets (&lt;) and backtick-wrapped placeholders are both fine and are
// what the generators now produce. Callers skip fenced blocks before calling.
func rawTagsOutsideCode(line string) []string {
	var out []string
	inCode := false

	for i := 0; i < len(line); {
		if line[i] == '`' {
			n := 0
			for i+n < len(line) && line[i+n] == '`' {
				n++
			}
			inCode = !inCode
			i += n
			continue
		}
		if line[i] == '<' && !inCode {
			if close := strings.IndexByte(line[i:], '>'); close > 0 {
				tag := line[i : i+close+1]
				// Only flag things markdown treats as a tag: '<' then a letter or '/'.
				if c := tag[1]; c == '/' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
					out = append(out, tag)
				}
			}
		}
		i++
	}

	return out
}
