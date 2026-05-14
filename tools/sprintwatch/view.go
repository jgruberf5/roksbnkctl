package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tickMsg time.Time

type loadedMsg struct {
	sprints   []Sprint
	burndowns map[int][]Snapshot
	err       error
}

type Model struct {
	repoRoot     string
	sprints      []Sprint
	burndowns    map[int][]Snapshot
	selected     int
	offset       int // index of the topmost visible card; window scrolls to keep `selected` visible
	detailMode   bool
	issueSel     int  // flattened index of the selected issue within the current sprint (detail mode)
	detailOffset int  // line offset into renderDetail output; scrolls to keep selected issue visible
	issueDetail  bool // showing the body of a single issue
	bodyOffset   int  // line offset into renderIssueDetail output for PgUp/PgDn scrolling
	err          error
	width        int
	height       int
	lastRefresh  time.Time
}

// flatIssueRef points at one issue rendered in detail mode. Iteration
// order matches renderDetail's role-then-issue traversal so the index
// the user navigates with maps 1:1 onto on-screen rows.
type flatIssueRef struct {
	role  *Role
	issue *Issue
}

func flattenIssues(sp Sprint) []flatIssueRef {
	var out []flatIssueRef
	for _, n := range sp.RoleNames() {
		r := sp.Roles[n]
		for i := range r.Issues {
			out = append(out, flatIssueRef{role: r, issue: &r.Issues[i]})
		}
	}
	return out
}

func InitialModel(repoRoot string) Model {
	return Model{repoRoot: repoRoot, burndowns: map[int][]Snapshot{}}
}

// flatIssueCount returns the number of issues in the currently-selected
// sprint, or 0 when no sprint is selected. Used by issueSel navigation
// to know how far ↓ may go.
func (m Model) flatIssueCount() int {
	if m.selected < 0 || m.selected >= len(m.sprints) {
		return 0
	}
	return len(flattenIssues(m.sprints[m.selected]))
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadCmd(m.repoRoot), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func loadCmd(root string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sprints, err := LoadSprints(root)
		if err != nil {
			return loadedMsg{err: err}
		}
		bd := map[int][]Snapshot{}
		for _, s := range sprints {
			snaps, err := Burndown(ctx, root, s.Number, s.RoleNames())
			if err == nil {
				bd[s.Number] = snaps
			}
		}
		return loadedMsg{sprints: sprints, burndowns: bd}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.scrollToSelected()
		m.scrollToSelectedIssue()
		m.clampBodyOffset()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, loadCmd(m.repoRoot)
		case "up", "k":
			if m.detailMode {
				if m.issueSel > 0 {
					m.issueSel--
					m.scrollToSelectedIssue()
					m.bodyOffset = 0
				}
			} else if m.selected > 0 {
				m.selected--
				m.scrollToSelected()
			}
		case "down", "j":
			if m.detailMode {
				if n := m.flatIssueCount(); m.issueSel < n-1 {
					m.issueSel++
					m.scrollToSelectedIssue()
					m.bodyOffset = 0
				}
			} else if m.selected < len(m.sprints)-1 {
				m.selected++
				m.scrollToSelected()
			}
		case "home", "g":
			if m.issueDetail {
				m.bodyOffset = 0
			} else if m.detailMode {
				m.issueSel = 0
				m.scrollToSelectedIssue()
			} else {
				m.selected = 0
				m.scrollToSelected()
			}
		case "end", "G":
			if m.issueDetail {
				// Snap body offset to the maxOffset clamp.
				m.bodyOffset = 1 << 30
				m.clampBodyOffset()
			} else if m.detailMode {
				if n := m.flatIssueCount(); n > 0 {
					m.issueSel = n - 1
					m.scrollToSelectedIssue()
				}
			} else if len(m.sprints) > 0 {
				m.selected = len(m.sprints) - 1
				m.scrollToSelected()
			}
		case "pgup":
			if m.issueDetail {
				avail := m.availDetailHeight()
				if avail <= 0 {
					avail = 10
				}
				m.bodyOffset -= avail
				m.clampBodyOffset()
			}
		case "pgdown", " ":
			if m.issueDetail {
				avail := m.availDetailHeight()
				if avail <= 0 {
					avail = 10
				}
				m.bodyOffset += avail
				m.clampBodyOffset()
			}
		case "left", "h":
			if m.detailMode && m.selected > 0 {
				m.selected--
				m.issueSel = 0
				m.issueDetail = false
				m.detailOffset = 0
				m.bodyOffset = 0
				m.scrollToSelectedIssue()
			}
		case "right", "l":
			if m.detailMode && m.selected < len(m.sprints)-1 {
				m.selected++
				m.issueSel = 0
				m.issueDetail = false
				m.detailOffset = 0
				m.bodyOffset = 0
				m.scrollToSelectedIssue()
			}
		case "enter":
			if !m.detailMode {
				m.detailMode = true
				m.issueSel = 0
				m.issueDetail = false
				m.detailOffset = 0
				m.bodyOffset = 0
				m.scrollToSelectedIssue()
			} else if m.flatIssueCount() > 0 {
				m.issueDetail = !m.issueDetail
				m.bodyOffset = 0
			}
		case "esc":
			if m.issueDetail {
				m.issueDetail = false
				m.bodyOffset = 0
			} else if m.detailMode {
				m.detailMode = false
				m.issueSel = 0
				m.detailOffset = 0
			}
		}
	case tickMsg:
		return m, tea.Batch(loadCmd(m.repoRoot), tickCmd())
	case loadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.sprints = msg.sprints
		m.burndowns = msg.burndowns
		m.lastRefresh = time.Now()
		if m.selected >= len(m.sprints) && len(m.sprints) > 0 {
			m.selected = len(m.sprints) - 1
		}
		if n := m.flatIssueCount(); m.issueSel >= n {
			if n > 0 {
				m.issueSel = n - 1
			} else {
				m.issueSel = 0
				m.issueDetail = false
			}
		}
		m.scrollToSelected()
		m.scrollToSelectedIssue()
		m.clampBodyOffset()
	}
	return m, nil
}

// chromeHeight is the number of rows View() spends on non-card chrome:
// title bar (1) + blank line after title (1) + blank line before help
// (1) + help line (1). Subtracted from m.height to size the card area.
const chromeHeight = 4

// indicatorSlack reserves 2 rows for the "▲ N above" / "▼ N below"
// scroll markers. Always reserved (even when no marker is showing) so
// the layout doesn't reflow on every scroll — wastes at most 1–2 lines.
const indicatorSlack = 2

// availCardHeight returns the vertical room left for sprint cards after
// chrome + indicator slack. Returns 0 when m.height isn't known yet
// (e.g. before the first WindowSizeMsg, or in --once mode) — callers
// treat 0 as "render everything, no scrolling."
func (m Model) availCardHeight() int {
	if m.height <= 0 {
		return 0
	}
	h := m.height - chromeHeight - indicatorSlack
	if h < 1 {
		h = 1
	}
	return h
}

// scrollToSelected slides the visible window so m.sprints[m.selected]
// is on-screen. Walks backward from selected accumulating card heights
// until the budget is exhausted; that becomes the new offset.
func (m *Model) scrollToSelected() {
	if len(m.sprints) == 0 {
		m.offset = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.sprints) {
		m.selected = len(m.sprints) - 1
	}
	avail := m.availCardHeight()
	if avail <= 0 {
		// Height unknown → no scrolling; show everything from the top.
		m.offset = 0
		return
	}
	// Snap up if selected scrolled above the window.
	if m.selected < m.offset {
		m.offset = m.selected
		return
	}
	// Walk backward from selected, adding card heights until we run
	// out of budget. The last index that still fits becomes offset.
	used := 0
	for i := m.selected; i >= 0; i-- {
		h := lipgloss.Height(m.renderCard(m.sprints[i], i == m.selected))
		if used+h > avail && i != m.selected {
			// Selected itself always renders even if it overflows by
			// a row — better that than a blank pane.
			m.offset = i + 1
			return
		}
		used += h
	}
	m.offset = 0
}

var (
	stOK         = lipgloss.NewStyle().Foreground(lipgloss.Color("#00D75F")).Bold(true)
	stWarn       = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAF00")).Bold(true)
	stErr        = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF005F")).Bold(true)
	stInfo       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FAFFF"))
	stDim        = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	stHead       = lipgloss.NewStyle().Foreground(lipgloss.Color("#87D7FF")).Bold(true)
	stTitleBar   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#5F87FF")).Padding(0, 1)
	stHelp       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5F5F5F"))
	stSpark      = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FAFFF"))
	stCardBlue   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F87FF")).Padding(0, 1).MarginBottom(1)
	stCardGreen  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#00D75F")).Padding(0, 1).MarginBottom(1)
	stCardYellow = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#FFAF00")).Padding(0, 1).MarginBottom(1)
	stCardWhite  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#FFFFFF")).Padding(0, 1).MarginBottom(1)
)

func (m Model) View() string {
	if m.err != nil {
		return stErr.Render("ERROR: ") + m.err.Error() + "\n\n" + stHelp.Render("[q] quit  [r] retry") + "\n"
	}
	if len(m.sprints) == 0 {
		return stDim.Render("Loading sprint data...\n")
	}

	var sb strings.Builder
	bar := stTitleBar.Render(" Sprint Watch ")
	repo := stDim.Render("repo: " + filepath.Base(m.repoRoot))
	refreshed := stDim.Render("refreshed: " + m.lastRefresh.Format("2006-01-02 15:04:05"))
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Center, bar, "  ", repo, "  ", refreshed) + "\n\n")

	if m.detailMode && m.selected < len(m.sprints) {
		sp := m.sprints[m.selected]
		if m.issueDetail {
			sb.WriteString(m.renderIssueDetail(sp))
		} else {
			sb.WriteString(m.renderDetail(sp))
		}
	} else {
		avail := m.availCardHeight()
		if m.offset > 0 {
			// Trailing "\n" must live OUTSIDE stDim.Render — lipgloss
			// pads the styled span's trailing empty line to the first
			// line's visible width, which would shift the next card
			// (the new top of the viewport) right by ~16 columns.
			sb.WriteString(stDim.Render(fmt.Sprintf("  ▲ %d more above", m.offset)) + "\n")
		}
		used := 0
		lastShown := m.offset - 1
		for i := m.offset; i < len(m.sprints); i++ {
			card := m.renderCard(m.sprints[i], i == m.selected)
			h := lipgloss.Height(card)
			if avail > 0 && used+h > avail && i != m.selected && i > m.offset {
				break
			}
			sb.WriteString(card)
			used += h
			lastShown = i
		}
		if hidden := len(m.sprints) - 1 - lastShown; hidden > 0 {
			sb.WriteString(stDim.Render(fmt.Sprintf("  ▼ %d more below", hidden)) + "\n")
		}
	}

	if m.issueDetail {
		sb.WriteString("\n" + stHelp.Render("[esc] back  [q] quit  [r] refresh  [↑/↓] prev/next issue  [pgup/pgdn] scroll  [←/→] prev/next sprint"))
	} else if m.detailMode {
		sb.WriteString("\n" + stHelp.Render("[esc] back  [q] quit  [r] refresh  [↑/↓] select issue  [enter] open  [←/→] prev/next sprint"))
	} else {
		sb.WriteString("\n" + stHelp.Render("[q] quit  [r] refresh  [↑/↓] select  [g/G] top/bottom  [enter] detail"))
	}
	return sb.String()
}

func (m Model) renderCard(sp Sprint, selected bool) string {
	var b strings.Builder

	var badge string
	hardOpen := sp.HardOpen()
	done, total, ns := sp.RoleSummary()
	switch {
	case sp.IsComplete():
		badge = stOK.Render("✓ COMPLETE")
	case ns > 0 && hardOpen == 0:
		badge = stWarn.Render(fmt.Sprintf("⏳ IN PROGRESS  (%d/%d agents reported)", done, total))
	default:
		badge = stWarn.Render(fmt.Sprintf("⏳ IN PROGRESS  (%d open)", hardOpen))
	}

	header := stHead.Render(fmt.Sprintf("Sprint %d", sp.Number))
	if sp.Theme != "" {
		header += stDim.Render(" — " + sp.Theme)
	}
	header += "   " + badge
	b.WriteString(header + "\n")

	var roleParts []string
	for _, n := range sp.RoleNames() {
		r := sp.Roles[n]
		var icon string
		switch {
		case r.NotStarted:
			icon = stDim.Render("?")
		case r.IsDone():
			icon = stOK.Render("✓")
		case r.HardOpen() > 0:
			icon = stErr.Render("✗") + stWarn.Render(fmt.Sprintf(" %d", r.HardOpen()))
		default:
			icon = stOK.Render("✓")
		}
		roleParts = append(roleParts, fmt.Sprintf("%-12s %s", n, icon))
	}
	b.WriteString(strings.Join(roleParts, "    ") + "\n")

	totalFiled, totalResolved, totalDeferred, totalOpen := 0, 0, 0, 0
	sevCounts := map[string]int{}
	for _, r := range sp.Roles {
		for _, i := range r.Issues {
			totalFiled++
			if !i.OpenLike {
				if i.Deferred {
					totalDeferred++
				} else {
					totalResolved++
				}
			} else {
				totalOpen++
				sevCounts[i.Severity]++
			}
		}
	}
	b.WriteString(fmt.Sprintf("Issues:      %d filed   %s resolved   %s deferred   %s open\n",
		totalFiled,
		stOK.Render(fmt.Sprintf("%d", totalResolved)),
		stInfo.Render(fmt.Sprintf("%d", totalDeferred)),
		boldOrDim(totalOpen, stWarn)))

	if totalOpen > 0 {
		b.WriteString(fmt.Sprintf("Severity:    %s blocker   %s high   %s medium   %s low\n",
			boldOrDim(sevCounts["blocker"], stErr),
			boldOrDim(sevCounts["high"], stErr),
			boldOrDim(sevCounts["medium"], stWarn),
			boldOrDim(sevCounts["low"], stDim)))
	}

	snaps := m.burndowns[sp.Number]
	if len(snaps) > 0 {
		spark := Sparkline(snaps, 16)
		last := snaps[len(snaps)-1]
		b.WriteString(fmt.Sprintf("Burn-down:   %s → %d open\n", stSpark.Render(spark), last.Open))
	}

	if sp.IsComplete() {
		if t, ok := ClosedAt(snaps); ok {
			b.WriteString(fmt.Sprintf("Closed:      %s\n", stOK.Render(t.Format("2006-01-02"))))
		}
	} else if len(snaps) > 0 {
		eta, vel, ok := ETA(snaps)
		if ok && eta.After(time.Now()) {
			days := time.Until(eta).Hours() / 24
			b.WriteString(fmt.Sprintf("ETA:         %s  (%s, velocity %.2f issues/day)\n",
				stHead.Render("~"+eta.Format("2006-01-02")),
				humanRemaining(days),
				vel))
		} else if totalOpen > 0 {
			b.WriteString(stDim.Render("ETA:         no recent burn-down to project from\n"))
		}
	}

	style := stCardBlue
	switch {
	case sp.IsComplete() && selected:
		style = stCardWhite
	case sp.IsComplete():
		style = stCardGreen
	case selected:
		style = stCardYellow
	}
	w := m.width - 2
	if w < 40 {
		w = 78
	}
	return style.Width(w).Render(strings.TrimRight(b.String(), "\n")) + "\n"
}

func boldOrDim(n int, hot lipgloss.Style) string {
	if n == 0 {
		return stDim.Render("0")
	}
	return hot.Render(fmt.Sprintf("%d", n))
}

func humanRemaining(days float64) string {
	if days < 0 {
		days = 0
	}
	if days < 1 {
		return fmt.Sprintf("%.0fh", days*24)
	}
	if days < 14 {
		return fmt.Sprintf("%.0f days", days)
	}
	return fmt.Sprintf("%.1f weeks", days/7)
}

// detailLines builds the flat list of rendered lines for the detail view
// and records the line index of each flat-indexed issue's row. Splitting
// rendering from windowing keeps scrollToSelectedIssue and renderDetail
// in sync on which line a given issueSel lives on.
func (m Model) detailLines(sp Sprint) (lines []string, issueRows []int) {
	title := stHead.Render(fmt.Sprintf("Sprint %d — issue detail", sp.Number))
	if sp.Theme != "" {
		title += stDim.Render("   (" + sp.Theme + ")")
	}
	lines = append(lines, title, "")

	flatIdx := 0
	for _, n := range sp.RoleNames() {
		r := sp.Roles[n]
		head := stHead.Render(n)
		switch {
		case r.NotStarted:
			lines = append(lines, head+"  "+stDim.Render("(no issue file yet — agent has not reported)"), "")
			continue
		case r.NoIssuesFiled:
			lines = append(lines, head+"  "+stOK.Render("(no issues filed)"), "")
			continue
		case len(r.Issues) == 0:
			lines = append(lines, head+"  "+stDim.Render("(empty)"), "")
			continue
		}
		open, closed, def := 0, 0, 0
		for _, i := range r.Issues {
			switch {
			case i.OpenLike:
				open++
			case i.Deferred:
				def++
			default:
				closed++
			}
		}
		summary := fmt.Sprintf("(%s open  %s deferred  %s resolved)",
			boldOrDim(open, stWarn),
			boldOrDim(def, stInfo),
			boldOrDim(closed, stOK))
		lines = append(lines, head+"  "+summary)
		for _, i := range r.Issues {
			var icon string
			switch {
			case i.OpenLike:
				icon = stErr.Render("✗")
			case i.Deferred:
				icon = stInfo.Render("⏸")
			default:
				icon = stOK.Render("✓")
			}
			sev := stDim.Render(fmt.Sprintf("[%-7s]", i.Severity))
			title := i.Title
			max := m.width - 20
			if max < 30 {
				max = 60
			}
			if len(title) > max {
				title = title[:max-1] + "…"
			}
			marker := "  "
			if flatIdx == m.issueSel {
				marker = stWarn.Render("▶ ")
				title = stWarn.Render(title)
			}
			issueRows = append(issueRows, len(lines))
			lines = append(lines, fmt.Sprintf("%s%s %s  %s", marker, icon, sev, title))
			flatIdx++
		}
		if r.ResolvedFile != "" {
			lines = append(lines, "    "+stDim.Render("└─ "+filepath.Base(r.ResolvedFile)))
		}
		lines = append(lines, "")
	}
	return lines, issueRows
}

// availDetailHeight returns the line budget for the detail view's
// scrolling window. Mirrors availCardHeight: same chrome + slack budget,
// returns 0 when height isn't known so the caller renders everything.
func (m Model) availDetailHeight() int {
	if m.height <= 0 {
		return 0
	}
	h := m.height - chromeHeight - indicatorSlack
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) renderDetail(sp Sprint) string {
	lines, _ := m.detailLines(sp)
	avail := m.availDetailHeight()
	if avail <= 0 || len(lines) <= avail {
		return strings.Join(lines, "\n") + "\n"
	}
	offset := m.detailOffset
	if offset < 0 {
		offset = 0
	}
	maxOffset := len(lines) - avail
	if offset > maxOffset {
		offset = maxOffset
	}
	end := offset + avail
	if end > len(lines) {
		end = len(lines)
	}
	var sb strings.Builder
	if offset > 0 {
		// Match the card-view marker style; trailing "\n" stays outside
		// stDim.Render so lipgloss doesn't pad the next line's prefix.
		sb.WriteString(stDim.Render(fmt.Sprintf("  ▲ %d lines above", offset)) + "\n")
	}
	sb.WriteString(strings.Join(lines[offset:end], "\n") + "\n")
	if hidden := len(lines) - end; hidden > 0 {
		sb.WriteString(stDim.Render(fmt.Sprintf("  ▼ %d lines below", hidden)) + "\n")
	}
	return sb.String()
}

// scrollToSelectedIssue slides the detail-view window so the row for
// m.issueSel stays visible. Mirrors scrollToSelected's contract for the
// card list.
func (m *Model) scrollToSelectedIssue() {
	if m.selected < 0 || m.selected >= len(m.sprints) {
		m.detailOffset = 0
		return
	}
	lines, rows := m.detailLines(m.sprints[m.selected])
	avail := m.availDetailHeight()
	if avail <= 0 || len(lines) <= avail {
		m.detailOffset = 0
		return
	}
	if len(rows) == 0 {
		// No issues — clamp offset to a valid range but don't move.
		maxOffset := len(lines) - avail
		if m.detailOffset > maxOffset {
			m.detailOffset = maxOffset
		}
		if m.detailOffset < 0 {
			m.detailOffset = 0
		}
		return
	}
	idx := m.issueSel
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	selectedRow := rows[idx]
	if selectedRow < m.detailOffset {
		m.detailOffset = selectedRow
	}
	if selectedRow >= m.detailOffset+avail {
		m.detailOffset = selectedRow - avail + 1
	}
	maxOffset := len(lines) - avail
	if m.detailOffset > maxOffset {
		m.detailOffset = maxOffset
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
}

// issueDetailLines builds the lines for the currently-selected issue's
// body view. Split out so renderIssueDetail and the bodyOffset clamp in
// Update agree on what's there to scroll through.
func (m Model) issueDetailLines(sp Sprint) []string {
	flat := flattenIssues(sp)
	if len(flat) == 0 {
		return []string{stDim.Render("(no issues to show)")}
	}
	idx := m.issueSel
	if idx < 0 {
		idx = 0
	}
	if idx >= len(flat) {
		idx = len(flat) - 1
	}
	ref := flat[idx]
	i := ref.issue

	var lines []string
	lines = append(lines, stHead.Render(fmt.Sprintf("Sprint %d — %s — Issue %d of %d", sp.Number, ref.role.Name, idx+1, len(flat))))
	lines = append(lines, "")

	var statusStyled string
	switch {
	case i.OpenLike:
		statusStyled = stErr.Render(i.Status)
	case i.Deferred:
		statusStyled = stInfo.Render(i.Status)
	default:
		statusStyled = stOK.Render(i.Status)
	}
	if statusStyled == "" {
		statusStyled = stDim.Render("(unset)")
	}
	lines = append(lines, stHead.Render(fmt.Sprintf("Issue %d: ", i.Number))+i.Title)
	lines = append(lines, stDim.Render("Severity: ")+i.Severity+"    "+stDim.Render("Status: ")+statusStyled)
	if ref.role.IssueFile != "" {
		lines = append(lines, stDim.Render("Source:   "+filepath.Base(ref.role.IssueFile)))
	}
	lines = append(lines, "")

	body := strings.TrimSpace(i.Body)
	if body == "" {
		lines = append(lines, stDim.Render("(no further detail recorded)"))
	} else {
		lines = append(lines, strings.Split(body, "\n")...)
	}
	return lines
}

func (m Model) renderIssueDetail(sp Sprint) string {
	lines := m.issueDetailLines(sp)
	avail := m.availDetailHeight()
	if avail <= 0 || len(lines) <= avail {
		return strings.Join(lines, "\n") + "\n"
	}
	offset := m.bodyOffset
	if offset < 0 {
		offset = 0
	}
	maxOffset := len(lines) - avail
	if offset > maxOffset {
		offset = maxOffset
	}
	end := offset + avail
	if end > len(lines) {
		end = len(lines)
	}
	var sb strings.Builder
	if offset > 0 {
		sb.WriteString(stDim.Render(fmt.Sprintf("  ▲ %d lines above", offset)) + "\n")
	}
	sb.WriteString(strings.Join(lines[offset:end], "\n") + "\n")
	if hidden := len(lines) - end; hidden > 0 {
		sb.WriteString(stDim.Render(fmt.Sprintf("  ▼ %d lines below", hidden)) + "\n")
	}
	return sb.String()
}

// clampBodyOffset keeps m.bodyOffset within the renderable range for the
// current sprint/issue/terminal-height combo. Called after anything that
// changes the body content (issue switch, sprint switch) or the budget
// (WindowSizeMsg, loadedMsg).
func (m *Model) clampBodyOffset() {
	if !m.issueDetail || m.selected < 0 || m.selected >= len(m.sprints) {
		m.bodyOffset = 0
		return
	}
	lines := m.issueDetailLines(m.sprints[m.selected])
	avail := m.availDetailHeight()
	if avail <= 0 || len(lines) <= avail {
		m.bodyOffset = 0
		return
	}
	maxOffset := len(lines) - avail
	if m.bodyOffset > maxOffset {
		m.bodyOffset = maxOffset
	}
	if m.bodyOffset < 0 {
		m.bodyOffset = 0
	}
}
