package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Snapshot struct {
	Time time.Time
	Open int
}

// issueBases lists the issue-dir candidates Burndown/openAt probe, in the
// order they should be tried. Includes both the live and archived locations
// so a sprint that moved to .archive still picks up its full pre-move git
// history without losing the post-move commit.
var issueBases = []string{"issues", ".archive/issues"}

func issuePathsFor(sprintNum int, role string) []string {
	var out []string
	for _, base := range issueBases {
		out = append(out,
			filepath.Join(base, fmt.Sprintf("issue_sprint%d_%s.md", sprintNum, role)),
			filepath.Join(base, fmt.Sprintf("resolved_sprint%d_%s.md", sprintNum, role)),
		)
	}
	return out
}

func Burndown(ctx context.Context, root string, sprintNum int, roles []string, archived bool) ([]Snapshot, error) {
	var paths []string
	for _, role := range roles {
		paths = append(paths, issuePathsFor(sprintNum, role)...)
	}

	args := []string{"-C", root, "log", "--reverse", "--format=%H %ct", "--"}
	args = append(args, paths...)
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	var snaps []Snapshot
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		sha := parts[0]
		var ts int64
		fmt.Sscanf(parts[1], "%d", &ts)
		snaps = append(snaps, Snapshot{Time: time.Unix(ts, 0), Open: openAt(ctx, root, sha, sprintNum, roles)})
	}

	nowOpen := openNow(root, sprintNum, roles, archived)
	if len(snaps) == 0 || snaps[len(snaps)-1].Open != nowOpen {
		snaps = append(snaps, Snapshot{Time: time.Now(), Open: nowOpen})
	}

	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time.Before(snaps[j].Time) })
	return snaps, nil
}

// showAtSha returns the first non-empty content found at any of `paths`
// for the given commit. Paths are tried in order so the caller controls
// precedence (typically live location before archive).
func showAtSha(ctx context.Context, root, sha string, paths []string) []byte {
	for _, p := range paths {
		out, _ := exec.CommandContext(ctx, "git", "-C", root, "show", sha+":"+p).Output()
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func openAt(ctx context.Context, root, sha string, sprintNum int, roles []string) int {
	open := 0
	for _, role := range roles {
		var issuePaths, resolvedPaths []string
		for _, base := range issueBases {
			issuePaths = append(issuePaths, filepath.Join(base, fmt.Sprintf("issue_sprint%d_%s.md", sprintNum, role)))
			resolvedPaths = append(resolvedPaths, filepath.Join(base, fmt.Sprintf("resolved_sprint%d_%s.md", sprintNum, role)))
		}
		issue := showAtSha(ctx, root, sha, issuePaths)
		if len(issue) == 0 {
			continue
		}
		resolved := showAtSha(ctx, root, sha, resolvedPaths)
		open += countRoleOpen(string(issue), string(resolved))
	}
	return open
}

func openNow(root string, sprintNum int, roles []string, archived bool) int {
	base := filepath.Join(root, "issues")
	if archived {
		base = filepath.Join(root, ".archive", "issues")
	}
	open := 0
	for _, role := range roles {
		issuePath := filepath.Join(base, fmt.Sprintf("issue_sprint%d_%s.md", sprintNum, role))
		issue, err := os.ReadFile(issuePath)
		if err != nil {
			continue
		}
		resolvedPath := filepath.Join(base, fmt.Sprintf("resolved_sprint%d_%s.md", sprintNum, role))
		resolved, _ := os.ReadFile(resolvedPath) // ignore missing
		open += countRoleOpen(string(issue), string(resolved))
	}
	return open
}

func countRoleOpen(issueContent, resolvedContent string) int {
	r := &Role{}
	ParseBlob(r, issueContent)
	if resolvedContent != "" {
		OverlayResolved(r, resolvedContent)
	}
	ApplyRoadmapRule(r)
	return r.HardOpen()
}

// ETA uses a trailing-window slope of the burndown to project when Open hits 0.
// Returns ok=false if there isn't enough downward movement to project.
func ETA(snaps []Snapshot) (eta time.Time, velocityPerDay float64, ok bool) {
	if len(snaps) < 2 {
		return time.Time{}, 0, false
	}
	last := snaps[len(snaps)-1]
	if last.Open == 0 {
		return last.Time, 0, true
	}
	cutoff := time.Now().Add(-14 * 24 * time.Hour)
	var window []Snapshot
	for _, s := range snaps {
		if s.Time.After(cutoff) {
			window = append(window, s)
		}
	}
	if len(window) < 2 {
		window = snaps
	}
	first := window[0]
	closed := first.Open - last.Open
	days := last.Time.Sub(first.Time).Hours() / 24
	if days <= 0 || closed <= 0 {
		return time.Time{}, 0, false
	}
	velocityPerDay = float64(closed) / days
	daysRemaining := float64(last.Open) / velocityPerDay
	return time.Now().Add(time.Duration(daysRemaining * 24 * float64(time.Hour))), velocityPerDay, true
}

// ClosedAt returns the timestamp the burndown first reached 0, if ever.
func ClosedAt(snaps []Snapshot) (time.Time, bool) {
	for _, s := range snaps {
		if s.Open == 0 {
			return s.Time, true
		}
	}
	return time.Time{}, false
}

func Sparkline(snaps []Snapshot, width int) string {
	if len(snaps) == 0 || width <= 0 {
		return ""
	}
	chars := []rune("▁▂▃▄▅▆▇█")
	samples := make([]int, 0, width)
	if len(snaps) <= width {
		for _, s := range snaps {
			samples = append(samples, s.Open)
		}
	} else {
		start := len(snaps) - width
		for i := 0; i < width; i++ {
			samples = append(samples, snaps[start+i].Open)
		}
	}
	max := 1
	for _, s := range samples {
		if s > max {
			max = s
		}
	}
	var b strings.Builder
	for _, s := range samples {
		idx := int(float64(s) / float64(max) * float64(len(chars)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(chars) {
			idx = len(chars) - 1
		}
		b.WriteRune(chars[idx])
	}
	return b.String()
}
