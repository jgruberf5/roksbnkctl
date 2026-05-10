package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	pathFlag := flag.String("path", "", "Path to the repo (default: detect via git)")
	onceFlag := flag.Bool("once", false, "Render once to stdout and exit (no TTY needed)")
	refFlag := flag.String("ref", "", "Override HEAD: any git ref/SHA whose runs to watch")
	prFlag := flag.Int("pr", 0, "Override HEAD: PR number whose head SHA to watch")
	runIDFlag := flag.Int64("run-id", 0, "Skip discovery; jump straight to this run's detail view")
	workflowFlag := flag.String("workflow", "", "Substring filter on workflow name")
	refreshFlag := flag.Duration("refresh", 5*time.Second, "Base refresh interval (adapts to run state)")
	flag.Parse()

	root, err := resolveRoot(*pathFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciwatch:", err)
		os.Exit(1)
	}
	if err := resolveGH(); err != nil {
		fmt.Fprintln(os.Stderr, "ciwatch:", err)
		os.Exit(1)
	}

	sha, err := resolveSHA(root, *refFlag, *prFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciwatch:", err)
		os.Exit(1)
	}

	repo, err := resolveRepo(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciwatch:", err)
		os.Exit(1)
	}

	cfg := Config{
		Root:        root,
		Repo:        repo,
		SHA:         sha,
		RunID:       *runIDFlag,
		Workflow:    *workflowFlag,
		BaseRefresh: *refreshFlag,
	}

	if *onceFlag {
		m := InitialModel(cfg)
		m.width = 100
		msg := loadCmd(cfg, nil)().(loadedMsg)
		if msg.err != nil {
			fmt.Fprintln(os.Stderr, msg.err)
			os.Exit(1)
		}
		m.runs = msg.runs
		// Inline the per-run job fetch so the phase mini-map renders.
		// In TTY mode the ticker drives this asynchronously; here we
		// have one shot at stdout, so block on it.
		for i := range m.runs {
			jmsg := jobsCmd(cfg.Repo, m.runs[i].ID)().(jobsLoadedMsg)
			m.runs[i].JobsLoadedAt = time.Now()
			if jmsg.err != nil {
				m.runs[i].JobsErr = jmsg.err
				continue
			}
			if d, ok := m.dags[m.runs[i].Name]; ok {
				d.AnnotateJobs(jmsg.jobs)
			} else {
				(&DAG{}).AnnotateJobs(jmsg.jobs)
			}
			m.runs[i].Jobs = jmsg.jobs
		}
		m.lastRefresh = time.Now()
		fmt.Println(m.View())
		return
	}

	p := tea.NewProgram(InitialModel(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func resolveRoot(flagVal string) (string, error) {
	if flagVal != "" {
		return filepath.Abs(flagVal)
	}
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository — pass --path /path/to/repo")
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveGH verifies that the gh CLI is installed and authenticated. Surfaced
// at startup so users get a clear message rather than a cryptic first-tick
// failure inside the TUI.
func resolveGH() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found on PATH — install from https://cli.github.com/")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh not authenticated — run `gh auth login`\n%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// resolveRepo discovers the GitHub owner/name for the repo. We need this
// because the `gh api` calls used for per-run job loading take fully
// qualified paths (`repos/{owner}/{name}/actions/runs/<id>/jobs`).
func resolveRepo(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", "--json", "nameWithOwner")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("could not detect GitHub repo (gh repo view): %w", err)
	}
	var wrap struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	if err := json.Unmarshal(out, &wrap); err != nil {
		return "", fmt.Errorf("parse gh repo view: %w", err)
	}
	if wrap.NameWithOwner == "" {
		return "", fmt.Errorf("gh repo view returned no nameWithOwner")
	}
	return wrap.NameWithOwner, nil
}

// resolveSHA picks the commit whose runs we'll watch. Precedence: --pr,
// --ref, then the current branch's HEAD.
func resolveSHA(root, ref string, pr int) (string, error) {
	switch {
	case pr > 0:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "gh", "pr", "view", fmt.Sprintf("%d", pr), "--json", "headRefOid").Output()
		if err != nil {
			return "", fmt.Errorf("gh pr view %d: %w", pr, err)
		}
		var wrap struct {
			HeadRefOid string `json:"headRefOid"`
		}
		if err := json.Unmarshal(out, &wrap); err != nil {
			return "", fmt.Errorf("parse gh pr view: %w", err)
		}
		if wrap.HeadRefOid == "" {
			return "", fmt.Errorf("PR %d has no head SHA", pr)
		}
		return wrap.HeadRefOid, nil
	case ref != "":
		out, err := exec.Command("git", "-C", root, "rev-parse", ref).Output()
		if err != nil {
			return "", fmt.Errorf("git rev-parse %s: %w", ref, err)
		}
		return strings.TrimSpace(string(out)), nil
	default:
		out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
		if err != nil {
			return "", fmt.Errorf("git rev-parse HEAD: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}
}
