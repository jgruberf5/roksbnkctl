package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// cmdWithOut returns a context-bearing cobra.Command whose stdout is captured.
func cmdWithOut() (*cobra.Command, *bytes.Buffer) {
	c := newCmd()
	var buf bytes.Buffer
	c.SetOut(&buf)
	return c, &buf
}

func TestJournalAddListReport(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	pointWorkspaceFlag(t, "jdemo")

	// add two notes
	for _, msg := range []string{"cloud-operator: cluster up started", "cloud-operator: nodes Ready"} {
		c, _ := cmdWithOut()
		if err := runJournalAdd(c, []string{msg}); err != nil {
			t.Fatalf("journal add: %v", err)
		}
	}

	// the notes file exists and contains both messages
	dir, _ := config.WorkspaceDir("jdemo")
	matches, _ := filepath.Glob(filepath.Join(dir, "journal", "*-notes.md"))
	if len(matches) != 1 {
		t.Fatalf("expected one notes file, got %v", matches)
	}
	body, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(body), "nodes Ready") || !strings.Contains(string(body), "cluster up started") {
		t.Errorf("notes file missing appended messages:\n%s", body)
	}

	// list shows the entry
	c, buf := cmdWithOut()
	if err := runJournalList(c, nil); err != nil {
		t.Fatalf("journal list: %v", err)
	}
	if !strings.Contains(buf.String(), "-notes.md") {
		t.Errorf("journal list missing the notes entry:\n%s", buf.String())
	}

	// report assembles report.md including a journal note
	c, _ = cmdWithOut()
	if err := runJournalReport(c, nil); err != nil {
		t.Fatalf("journal report: %v", err)
	}
	report, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatalf("report.md not written: %v", err)
	}
	if !strings.Contains(string(report), "nodes Ready") {
		t.Errorf("report.md missing journal content:\n%s", report)
	}
	if !strings.Contains(string(report), "## Timeline") {
		t.Errorf("report.md missing Timeline section")
	}
}

func TestJournalListEmpty(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	pointWorkspaceFlag(t, "empty")
	c, buf := cmdWithOut()
	if err := runJournalList(c, nil); err != nil {
		t.Fatalf("journal list (empty): %v", err)
	}
	if !strings.Contains(buf.String(), "no journal entries") {
		t.Errorf("empty journal list should say so, got: %s", buf.String())
	}
}
