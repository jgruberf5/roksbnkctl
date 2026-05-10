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

func Burndown(ctx context.Context, root string, sprintNum int, roles []string) ([]Snapshot, error) {
	var paths []string
	for _, role := range roles {
		paths = append(paths,
			filepath.Join("issues", fmt.Sprintf("issue_sprint%d_%s.md", sprintNum, role)),
			filepath.Join("issues", fmt.Sprintf("resolved_sprint%d_%s.md", sprintNum, role)),
		)
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
		open := 0
		for _, role := range roles {
			issuePath := filepath.Join("issues", fmt.Sprintf("issue_sprint%d_%s.md", sprintNum, role))
			content, err := exec.CommandContext(ctx, "git", "-C", root, "show", sha+":"+issuePath).Output()
			if err != nil {
				continue
			}
			open += countOpenInBlob(string(content))
		}
		snaps = append(snaps, Snapshot{Time: time.Unix(ts, 0), Open: open})
	}

	nowOpen := 0
	for _, role := range roles {
		path := filepath.Join(root, "issues", fmt.Sprintf("issue_sprint%d_%s.md", sprintNum, role))
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		nowOpen += countOpenInBlob(string(body))
	}
	if len(snaps) == 0 || snaps[len(snaps)-1].Open != nowOpen {
		snaps = append(snaps, Snapshot{Time: time.Now(), Open: nowOpen})
	}

	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time.Before(snaps[j].Time) })
	return snaps, nil
}

func countOpenInBlob(content string) int {
	var current *Issue
	n := 0
	flush := func() {
		if current != nil && current.OpenLike {
			n++
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## Issue ") {
			flush()
			current = &Issue{OpenLike: true} // default open until status line proves otherwise
			continue
		}
		if current == nil {
			continue
		}
		if m := statRE.FindStringSubmatch(line); m != nil {
			current.Status = strings.TrimSpace(m[1])
			current.OpenLike, current.Deferred = classifyStatus(current.Status)
		}
	}
	flush()
	return n
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
