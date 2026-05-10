package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	listTimeout = 10 * time.Second
	viewTimeout = 15 * time.Second
	logTimeout  = 30 * time.Second
)

// ListRunsForCommit fetches recent runs and filters client-side by head
// SHA. We avoid `gh run list --commit <sha>` because that flag was added
// in gh 2.27 (2023) and we want to support older releases. The over-fetch
// (50 runs) is enough headroom that even busy repos still surface every
// run for the latest commit. Empty result is not an error — it usually
// means the commit hasn't been pushed yet.
func ListRunsForCommit(ctx context.Context, sha string, limit int) ([]WorkflowRun, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	fetch := limit * 5
	if fetch < 50 {
		fetch = 50
	}
	args := []string{
		"run", "list",
		"--limit", fmt.Sprintf("%d", fetch),
		"--json", "databaseId,name,status,conclusion,event,headSha,createdAt,updatedAt,url",
	}
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return nil, ghError("gh run list", err)
	}
	var all []WorkflowRun
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, fmt.Errorf("parse gh run list output: %w", err)
	}
	var matched []WorkflowRun
	for _, r := range all {
		if strings.EqualFold(r.HeadSHA, sha) {
			matched = append(matched, r)
		}
	}
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

// apiJob mirrors the GitHub REST shape for /actions/runs/{id}/jobs. Field
// names are snake_case in the API; we translate to our internal Job here
// so the rest of the program keeps the simpler types.
type apiJob struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	HTMLURL     string    `json:"html_url"`
	Steps       []apiStep `json:"steps"`
}

type apiStep struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	Number      int       `json:"number"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// LoadJobs fetches jobs (with steps) via `gh api`. Going through the REST
// endpoint instead of `gh run view --json jobs` buys us compatibility
// with older gh releases (the `jobs` JSON field was added in gh ~2.40).
func LoadJobs(ctx context.Context, repo string, runID int64) ([]Job, error) {
	ctx, cancel := context.WithTimeout(ctx, viewTimeout)
	defer cancel()
	path := fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, runID)
	out, err := exec.CommandContext(ctx, "gh", "api", path).Output()
	if err != nil {
		return nil, ghError("gh api runs/<id>/jobs", err)
	}
	var wrap struct {
		Jobs []apiJob `json:"jobs"`
	}
	if err := json.Unmarshal(out, &wrap); err != nil {
		return nil, fmt.Errorf("parse gh api jobs output: %w", err)
	}
	jobs := make([]Job, 0, len(wrap.Jobs))
	for _, a := range wrap.Jobs {
		j := Job{
			ID:          a.ID,
			Name:        a.Name,
			Status:      a.Status,
			Conclusion:  a.Conclusion,
			StartedAt:   a.StartedAt,
			CompletedAt: a.CompletedAt,
			URL:         a.HTMLURL,
		}
		for _, s := range a.Steps {
			j.Steps = append(j.Steps, Step{
				Name:        s.Name,
				Status:      s.Status,
				Conclusion:  s.Conclusion,
				Number:      s.Number,
				StartedAt:   s.StartedAt,
				CompletedAt: s.CompletedAt,
			})
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// FailedLogTail returns the tail of the failed-step output for one job.
// `gh run view --log-failed --job <id>` works on any reasonably recent gh
// (>= 2.20). On older releases the --job flag may not pair with
// --log-failed; in that case ghError surfaces a usable message.
func FailedLogTail(ctx context.Context, runID, jobID int64, lines int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, logTimeout)
	defer cancel()
	args := []string{
		"run", "view", fmt.Sprintf("%d", runID),
		"--log-failed",
		"--job", fmt.Sprintf("%d", jobID),
	}
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return "", ghError("gh run view --log-failed", err)
	}
	return tail(string(out), lines), nil
}

func tail(s string, n int) string {
	if n <= 0 {
		return s
	}
	all := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(all) <= n {
		return strings.Join(all, "\n")
	}
	return strings.Join(all[len(all)-n:], "\n")
}

// ghError extracts stderr from an *exec.ExitError so callers get a useful
// message instead of "exit status 1".
func ghError(label string, err error) error {
	var stderr string
	if ee, ok := err.(*exec.ExitError); ok {
		stderr = strings.TrimSpace(string(ee.Stderr))
	}
	if strings.Contains(stderr, "rate limit") {
		return fmt.Errorf("%s: GitHub API rate limit hit — retry later", label)
	}
	if strings.Contains(stderr, "Could not resolve to a Repository") {
		return fmt.Errorf("%s: no GitHub remote — set with `gh repo set-default`", label)
	}
	if stderr == "" {
		return fmt.Errorf("%s: %w", label, err)
	}
	return fmt.Errorf("%s: %s", label, stderr)
}
