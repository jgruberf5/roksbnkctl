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

	// 56 rows carry no description today. The ratchet only ever goes DOWN.
	const ceiling = 56

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
