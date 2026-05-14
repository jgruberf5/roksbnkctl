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
	Body     string
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
	Theme  string
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
	themeRE     = regexp.MustCompile(`(?i)^\s*\*\*Theme:?\*\*:?\s*(.+?)\s*$`)
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
		sp.Theme = readTheme(filepath.Join(promptsDir, fmt.Sprintf("sprint%d", k.n), "README.md"))
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
				if err := overlayResolved(r, resolvedPath); err != nil {
					return nil, err
				}
			}
			applyRoadmapRule(r)
			sp.Roles[role] = r
		}
		sprints = append(sprints, sp)
	}
	return sprints, nil
}

// readTheme extracts the `**Theme:** <text>` line from a sprint sidecar
// README. Returns "" if the file is absent or the line is missing — sprints
// without a theme just render without one.
func readTheme(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := themeRE.FindStringSubmatch(sc.Text()); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func parseIssueFile(r *Role, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	parseBlobScanner(r, sc)
	return sc.Err()
}

// ParseBlob parses the contents of an issue or resolved markdown file (without
// touching the filesystem) into r.Issues. Reusable for git-show output.
func ParseBlob(r *Role, content string) {
	parseBlobScanner(r, bufio.NewScanner(strings.NewReader(content)))
}

func parseBlobScanner(r *Role, sc *bufio.Scanner) {
	var current *Issue
	var body strings.Builder
	foundIssue := false
	foundNoIssuesMarker := false
	flush := func() {
		if current == nil {
			return
		}
		current.Body = strings.TrimRight(body.String(), "\n")
		r.Issues = append(r.Issues, *current)
		body.Reset()
	}
	for sc.Scan() {
		line := sc.Text()
		if !foundIssue && strings.Contains(strings.ToLower(line), "no issues filed") {
			foundNoIssuesMarker = true
		}
		if strings.HasPrefix(line, "## Issue ") {
			flush()
			current = parseIssueHeader(line)
			foundIssue = true
			continue
		}
		if current == nil {
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
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
	flush()
	if !foundIssue && foundNoIssuesMarker {
		r.NoIssuesFiled = true
	}
}

// OverlayResolved is the public version of overlayResolved, taking blob
// contents directly. Resolved file's per-issue status overrides the issue
// file's; resolved-only issues are appended.
func OverlayResolved(r *Role, resolvedContent string) {
	tmp := &Role{}
	ParseBlob(tmp, resolvedContent)
	byNum := map[int]*Issue{}
	for i := range tmp.Issues {
		byNum[tmp.Issues[i].Number] = &tmp.Issues[i]
	}
	for i := range r.Issues {
		if rv, ok := byNum[r.Issues[i].Number]; ok {
			if rv.Status != "" {
				r.Issues[i].Status = rv.Status
				r.Issues[i].OpenLike = rv.OpenLike
				r.Issues[i].Deferred = rv.Deferred
			}
			if rv.Severity != "unknown" && rv.Severity != "" {
				r.Issues[i].Severity = rv.Severity
			}
			delete(byNum, r.Issues[i].Number)
		}
	}
	for _, leftover := range byNum {
		r.Issues = append(r.Issues, *leftover)
	}
}

// ApplyRoadmapRule is the exported applyRoadmapRule for use by history.go.
func ApplyRoadmapRule(r *Role) { applyRoadmapRule(r) }

func overlayResolved(r *Role, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	OverlayResolved(r, string(content))
	return nil
}

// applyRoadmapRule treats severity=roadmap as never-blocking, regardless of
// status text. Roadmap items are forward-looking notes for future sprints.
func applyRoadmapRule(r *Role) {
	for i := range r.Issues {
		if r.Issues[i].Severity == "roadmap" {
			r.Issues[i].OpenLike = false
		}
	}
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
