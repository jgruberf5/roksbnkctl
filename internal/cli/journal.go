package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The journal is the append-only coordination substrate for agentic mode: the
// personas hand off to each other through it (see AGENTS.md). It's plain
// markdown under <workspace>/journal/ — these commands just make appending,
// listing, and assembling a report convenient. Nothing here applies or
// destroys infrastructure.

var journalCmd = &cobra.Command{
	Use:   "journal",
	Short: "Append-only timeline + report for agentic-mode handoffs",
	Long: `Manage the workspace journal (<workspace>/journal/), the append-only
timeline the agentic-mode personas use to hand off to one another.

  roksbnkctl journal add "<note>"   Append a note to today's journal
  roksbnkctl journal list           List entries chronologically with summaries
  roksbnkctl journal report         Assemble report.md from decisions + journal`,
}

var journalAddCmd = &cobra.Command{
	Use:   "add <message>",
	Short: "Append a note to today's journal entry",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runJournalAdd,
}

var journalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List journal entries (chronological) with one-line summaries",
	Args:  cobra.NoArgs,
	RunE:  runJournalList,
}

var journalReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Assemble report.md from decisions.md + the journal timeline",
	Args:  cobra.NoArgs,
	RunE:  runJournalReport,
}

func init() {
	journalCmd.AddCommand(journalAddCmd, journalListCmd, journalReportCmd)
	rootCmd.AddCommand(journalCmd)
}

// journalDir returns <workspace>/journal, creating it on demand.
func journalDir() (string, error) {
	dir, err := config.WorkspaceDir(resolvedWorkspaceName())
	if err != nil {
		return "", err
	}
	jd := filepath.Join(dir, "journal")
	if err := os.MkdirAll(jd, 0o755); err != nil {
		return "", fmt.Errorf("creating journal dir: %w", err)
	}
	return jd, nil
}

func runJournalAdd(cmd *cobra.Command, args []string) error {
	jd, err := journalDir()
	if err != nil {
		return err
	}
	msg := strings.Join(args, " ")
	date := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(jd, date+"-notes.md")

	// Create with a date header the first time; append thereafter.
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if werr := os.WriteFile(path, []byte("# Journal — "+date+"\n"), 0o644); werr != nil {
			return werr
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	stamp := time.Now().UTC().Format("15:04 UTC")
	if _, err := fmt.Fprintf(f, "\n## %s\n\n%s\n", stamp, msg); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "appended to journal/%s\n", date+"-notes.md")
	return nil
}

func runJournalList(cmd *cobra.Command, _ []string) error {
	jd, err := journalDir()
	if err != nil {
		return err
	}
	entries, err := journalEntries(jd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintln(out, "no journal entries yet — `roksbnkctl journal add \"<note>\"` to start")
		return nil
	}
	for _, e := range entries {
		fmt.Fprintf(out, "%-28s %s\n", e.name, e.summary)
	}
	return nil
}

func runJournalReport(cmd *cobra.Command, _ []string) error {
	dir, err := config.WorkspaceDir(resolvedWorkspaceName())
	if err != nil {
		return err
	}
	jd := filepath.Join(dir, "journal")
	entries, err := journalEntries(jd)
	if err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s — deployment report\n\n_Generated %s — seeded from decisions.md + the journal; the doc-specialist persona refines this._\n",
		resolvedWorkspaceName(), time.Now().UTC().Format("2006-01-02 15:04 UTC"))

	if dec, derr := os.ReadFile(filepath.Join(dir, "decisions.md")); derr == nil {
		fmt.Fprintf(&b, "\n---\n\n## Decisions\n\n%s\n", strings.TrimSpace(string(dec)))
	}

	fmt.Fprintf(&b, "\n---\n\n## Timeline\n")
	for _, e := range entries {
		body, rerr := os.ReadFile(filepath.Join(jd, e.name))
		if rerr != nil {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", e.name, strings.TrimSpace(string(body)))
	}

	reportPath := filepath.Join(dir, "report.md")
	if err := os.WriteFile(reportPath, []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✓ Wrote %s (%d journal entr%s)\n",
		reportPath, len(entries), plural(len(entries)))
	return nil
}

type journalEntry struct{ name, summary string }

// journalEntries lists *.md under jd sorted by filename (date-prefixed names
// sort chronologically), each with a one-line summary (first heading/line).
func journalEntries(jd string) ([]journalEntry, error) {
	des, err := os.ReadDir(jd)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []journalEntry
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") {
			continue
		}
		out = append(out, journalEntry{name: de.Name(), summary: firstLine(filepath.Join(jd, de.Name()))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// firstLine returns the first non-empty, de-marked line of a file (a quick
// summary for `journal list`). Empty string on any read error.
func firstLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		return strings.TrimSpace(strings.TrimLeft(line, "#"))
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
