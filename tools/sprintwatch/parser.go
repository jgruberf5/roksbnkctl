package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Issue struct {
	Number   int
	Title    string
	Severity string
	Status   string
	OpenLike bool
	Deferred bool
}

type Role struct {
	Name          string
	IssueFile     string
	ResolvedFile  string
	Issues        []Issue
	NoIssuesFiled bool
	NotStarted    bool
}

type Sprint struct {
	Number int
	Roles  map[string]*Role
}

func (r *Role) HardOpen() int {
	n := 0
	for _, i := range r.Issues {
		if i.OpenLike {
			n++
		}
	}
	return n
}

func (r *Role) IsDone() bool {
	if r.NotStarted {
		return false
	}
	if r.NoIssuesFiled {
		return true
	}
	if r.ResolvedFile != "" {
		return true
	}
	return r.HardOpen() == 0
}

func (s *Sprint) HardOpen() int {
	n := 0
	for _, r := range s.Roles {
		n += r.HardOpen()
	}
	return n
}

func (s *Sprint) RoleSummary() (done, total, notStarted int) {
	for _, r := range s.Roles {
		total++
		if r.NotStarted {
			notStarted++
		}
		if r.IsDone() {
			done++
		}
	}
	return
}

func (s *Sprint) IsComplete() bool {
	done, total, ns := s.RoleSummary()
	return ns == 0 && done == total
}

func (s *Sprint) RoleNames() []string {
	out := make([]string, 0, len(s.Roles))
	for n := range s.Roles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

var (
	sprintDirRE = regexp.MustCompile(`^sprint(\d+)$`)
	sevRE       = regexp.MustCompile(`(?i)^\s*\*\*Severity\*\*\s*:\s*(.+?)\s*$`)
	statRE      = regexp.MustCompile(`(?i)^\s*\*\*Status\*\*\s*:\s*(.+?)\s*$`)
)

func LoadSprints(root string) ([]Sprint, error) {
	promptsDir := filepath.Join(root, "prompts")
	entries, err := os.ReadDir(promptsDir)
	if err != nil {
		return nil, fmt.Errorf("read prompts/: %w", err)
	}

	type key struct {
		n     int
		roles []string
	}
	var keys []key

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := sprintDirRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		roleEntries, err := os.ReadDir(filepath.Join(promptsDir, e.Name()))
		if err != nil {
			continue
		}
		var roles []string
		for _, re := range roleEntries {
			if re.IsDir() {
				continue
			}
			name := re.Name()
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			if name == "README.md" {
				continue
			}
			roles = append(roles, strings.TrimSuffix(name, ".md"))
		}
		sort.Strings(roles)
		keys = append(keys, key{n: n, roles: roles})
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i].n < keys[j].n })

	var sprints []Sprint
	for _, k := range keys {
		sp := Sprint{Number: k.n, Roles: map[string]*Role{}}
		for _, role := range k.roles {
			r := &Role{Name: role}
			issuePath := filepath.Join(root, "issues", fmt.Sprintf("issue_sprint%d_%s.md", k.n, role))
			resolvedPath := filepath.Join(root, "issues", fmt.Sprintf("resolved_sprint%d_%s.md", k.n, role))
			if _, err := os.Stat(issuePath); err == nil {
				r.IssueFile = issuePath
				if err := parseIssueFile(r, issuePath); err != nil {
					return nil, err
				}
			} else {
				r.NotStarted = true
			}
			if _, err := os.Stat(resolvedPath); err == nil {
				r.ResolvedFile = resolvedPath
			}
			sp.Roles[role] = r
		}
		sprints = append(sprints, sp)
	}
	return sprints, nil
}

func parseIssueFile(r *Role, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var current *Issue
	foundIssue := false
	foundNoIssuesMarker := false

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !foundIssue && strings.Contains(strings.ToLower(line), "no issues filed") {
			foundNoIssuesMarker = true
		}
		if strings.HasPrefix(line, "## Issue ") {
			if current != nil {
				r.Issues = append(r.Issues, *current)
			}
			current = parseIssueHeader(line)
			foundIssue = true
			continue
		}
		if current == nil {
			continue
		}
		if m := sevRE.FindStringSubmatch(line); m != nil {
			current.Severity = normalizeSeverity(m[1])
			continue
		}
		if m := statRE.FindStringSubmatch(line); m != nil {
			current.Status = strings.TrimSpace(m[1])
			current.OpenLike, current.Deferred = classifyStatus(current.Status)
			continue
		}
	}
	if current != nil {
		r.Issues = append(r.Issues, *current)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !foundIssue && foundNoIssuesMarker {
		r.NoIssuesFiled = true
	}
	return nil
}

func parseIssueHeader(line string) *Issue {
	rest := strings.TrimPrefix(line, "## Issue ")
	var num int
	fmt.Sscanf(rest, "%d", &num)
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	title := strings.TrimSpace(rest[i:])
	title = strings.TrimLeft(title, ": -")
	if idx := strings.Index(title, " — "); idx >= 0 {
		title = strings.TrimSpace(title[:idx])
	}
	if strings.HasPrefix(title, "(") && strings.HasSuffix(title, ")") {
		title = strings.TrimSuffix(strings.TrimPrefix(title, "("), ")")
	}
	return &Issue{Number: num, Title: title, Severity: "unknown"}
}

func normalizeSeverity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, sev := range []string{"blocker", "high", "medium", "low", "roadmap"} {
		if strings.Contains(s, sev) {
			return sev
		}
	}
	return "unknown"
}

// classifyStatus returns (openLike, deferred) for a raw status string.
// openLike means the issue still blocks sprint completion.
func classifyStatus(raw string) (bool, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(s, "open (informational") {
		return false, false
	}
	if strings.HasPrefix(s, "open") || strings.HasPrefix(s, "in-progress") || strings.HasPrefix(s, "in progress") {
		return true, false
	}
	if strings.Contains(s, "for the integrator") || strings.Contains(s, "⚠") {
		return true, false
	}
	if strings.Contains(s, "⏸") || strings.Contains(s, "deferred") {
		return false, true
	}
	if strings.Contains(s, "✅") || strings.Contains(s, "resolved") || strings.Contains(s, "wontfix") || strings.Contains(s, "won't fix") {
		return false, false
	}
	if strings.Contains(s, "informational") {
		return false, false
	}
	return true, false
}
