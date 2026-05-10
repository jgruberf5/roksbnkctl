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
	repoRoot    string
	sprints     []Sprint
	burndowns   map[int][]Snapshot
	selected    int
	detailMode  bool
	err         error
	width       int
	height      int
	lastRefresh time.Time
}

func InitialModel(repoRoot string) Model {
	return Model{repoRoot: repoRoot, burndowns: map[int][]Snapshot{}}
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
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, loadCmd(m.repoRoot)
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.sprints)-1 {
				m.selected++
			}
		case "left", "h":
			if m.detailMode && m.selected > 0 {
				m.selected--
			}
		case "right", "l":
			if m.detailMode && m.selected < len(m.sprints)-1 {
				m.selected++
			}
		case "enter":
			m.detailMode = !m.detailMode
		case "esc":
			m.detailMode = false
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
	}
	return m, nil
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
		sb.WriteString(m.renderDetail(m.sprints[m.selected]))
	} else {
		for i, sp := range m.sprints {
			sb.WriteString(m.renderCard(sp, i == m.selected))
		}
	}

	if m.detailMode {
		sb.WriteString("\n" + stHelp.Render("[esc] back  [q] quit  [r] refresh  [←/→] prev/next sprint"))
	} else {
		sb.WriteString("\n" + stHelp.Render("[q] quit  [r] refresh  [↑/↓] select  [enter] detail"))
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

func (m Model) renderDetail(sp Sprint) string {
	var b strings.Builder
	title := fmt.Sprintf("Sprint %d — issue detail", sp.Number)
	b.WriteString(stHead.Render(title))
	if sp.Theme != "" {
		b.WriteString(stDim.Render("   (" + sp.Theme + ")"))
	}
	b.WriteString("\n\n")

	for _, n := range sp.RoleNames() {
		r := sp.Roles[n]
		head := stHead.Render(n)
		switch {
		case r.NotStarted:
			b.WriteString(head + "  " + stDim.Render("(no issue file yet — agent has not reported)") + "\n\n")
			continue
		case r.NoIssuesFiled:
			b.WriteString(head + "  " + stOK.Render("(no issues filed)") + "\n\n")
			continue
		case len(r.Issues) == 0:
			b.WriteString(head + "  " + stDim.Render("(empty)") + "\n\n")
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
		b.WriteString(head + "  " + summary + "\n")
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
			max := m.width - 18
			if max < 30 {
				max = 60
			}
			if len(title) > max {
				title = title[:max-1] + "…"
			}
			b.WriteString(fmt.Sprintf("  %s %s  %s\n", icon, sev, title))
		}
		if r.ResolvedFile != "" {
			b.WriteString("    " + stDim.Render("└─ "+filepath.Base(r.ResolvedFile)) + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
