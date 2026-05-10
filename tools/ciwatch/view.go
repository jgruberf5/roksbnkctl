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

type mode int

const (
	modeRunList mode = iota
	modeRunDetail
	modeLogPager
)

type tickMsg time.Time

type loadedMsg struct {
	runs []WorkflowRun
	err  error
}

type jobsLoadedMsg struct {
	runID int64
	jobs  []Job
	err   error
}

type logLoadedMsg struct {
	runID int64
	jobID int64
	body  string
	err   error
}

type Model struct {
	cfg         Config
	dags        map[string]*DAG
	runs        []WorkflowRun
	mode        mode
	selected    int // index into runs (list/detail) or jobs (log selection)
	jobSelected int // index into the flat jobs list of the selected run
	logBuf      string
	logScroll   int
	logRunID    int64
	logJobID    int64
	width       int
	height      int
	lastRefresh time.Time
	err         error
	stillRun    int // number of consecutive ticks where nothing changed (for back-off)
	prevSig     string
}

func InitialModel(cfg Config) Model {
	return Model{
		cfg:  cfg,
		dags: LoadDAGs(cfg.Root),
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{loadCmd(m.cfg, nil), tickCmd(m.cfg.BaseRefresh)}
	if m.cfg.RunID != 0 {
		cmds = append(cmds, jobsCmd(m.cfg.Repo, m.cfg.RunID))
	}
	return tea.Batch(cmds...)
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// loadCmd lists runs for the configured commit. The cached runs let us
// preserve already-loaded Jobs (we re-fetch jobs only when UpdatedAt
// advances or the run is still in progress).
func loadCmd(cfg Config, cached []WorkflowRun) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runs, err := ListRunsForCommit(ctx, cfg.SHA, 20)
		if err != nil {
			return loadedMsg{err: err}
		}
		if cfg.Workflow != "" {
			filtered := runs[:0]
			for _, r := range runs {
				if strings.Contains(strings.ToLower(r.Name), strings.ToLower(cfg.Workflow)) {
					filtered = append(filtered, r)
				}
			}
			runs = filtered
		}
		// Carry forward cached Jobs where UpdatedAt hasn't moved — avoids
		// hammering `gh run view` for terminal runs on every tick.
		byID := map[int64]WorkflowRun{}
		for _, c := range cached {
			byID[c.ID] = c
		}
		for i := range runs {
			if c, ok := byID[runs[i].ID]; ok && c.UpdatedAt.Equal(runs[i].UpdatedAt) {
				runs[i].Jobs = c.Jobs
				runs[i].JobsLoadedAt = c.JobsLoadedAt
				runs[i].JobsErr = c.JobsErr
			}
		}
		SortRuns(runs)
		return loadedMsg{runs: runs}
	}
}

func jobsCmd(repo string, runID int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		jobs, err := LoadJobs(ctx, repo, runID)
		return jobsLoadedMsg{runID: runID, jobs: jobs, err: err}
	}
}

func logCmd(runID, jobID int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		body, err := FailedLogTail(ctx, runID, jobID, 500)
		return logLoadedMsg{runID: runID, jobID: jobID, body: body, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tickMsg:
		next, cmds := m.scheduleNext()
		return m, tea.Batch(append(cmds, tickCmd(next))...)
	case loadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.runs = msg.runs
		m.lastRefresh = time.Now()
		if m.selected >= len(m.runs) && len(m.runs) > 0 {
			m.selected = len(m.runs) - 1
		}
		// Sequence: after the list lands, pull jobs for any run whose
		// detail we'd render (in_progress or selected detail target) so
		// the next View() has something useful.
		var cmds []tea.Cmd
		for _, r := range m.runs {
			if r.Phase() == PhaseRunning || r.Phase() == PhaseQueued || r.JobsLoadedAt.IsZero() {
				cmds = append(cmds, jobsCmd(m.cfg.Repo, r.ID))
			}
		}
		sig := runsSignature(m.runs)
		if sig == m.prevSig {
			m.stillRun++
		} else {
			m.stillRun = 0
		}
		m.prevSig = sig
		return m, tea.Batch(cmds...)
	case jobsLoadedMsg:
		for i := range m.runs {
			if m.runs[i].ID != msg.runID {
				continue
			}
			m.runs[i].JobsLoadedAt = time.Now()
			if msg.err != nil {
				m.runs[i].JobsErr = msg.err
				continue
			}
			m.runs[i].JobsErr = nil
			if d, ok := m.dags[m.runs[i].Name]; ok {
				d.AnnotateJobs(msg.jobs)
			} else {
				// No DAG — still strip matrix suffixes so detail view groups legs.
				(&DAG{}).AnnotateJobs(msg.jobs)
			}
			m.runs[i].Jobs = msg.jobs
		}
		return m, nil
	case logLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.logBuf = msg.body
		m.logScroll = 0
		m.logRunID = msg.runID
		m.logJobID = msg.jobID
		return m, nil
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		return m, loadCmd(m.cfg, m.runs)
	}
	switch m.mode {
	case modeRunList:
		switch msg.String() {
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.runs)-1 {
				m.selected++
			}
		case "enter":
			if len(m.runs) > 0 {
				m.mode = modeRunDetail
				m.jobSelected = 0
				return m, jobsCmd(m.cfg.Repo, m.runs[m.selected].ID)
			}
		}
	case modeRunDetail:
		switch msg.String() {
		case "esc", "b":
			m.mode = modeRunList
		case "left", "h":
			if m.selected > 0 {
				m.selected--
				m.jobSelected = 0
			}
		case "right", "l":
			if m.selected < len(m.runs)-1 {
				m.selected++
				m.jobSelected = 0
			}
		case "up", "k":
			if m.jobSelected > 0 {
				m.jobSelected--
			}
		case "down", "j":
			run := m.runs[m.selected]
			if m.jobSelected < len(run.Jobs)-1 {
				m.jobSelected++
			}
		case "enter":
			run := m.runs[m.selected]
			if m.jobSelected < len(run.Jobs) {
				job := run.Jobs[m.jobSelected]
				if job.Phase() == PhaseFailed {
					m.mode = modeLogPager
					m.logBuf = "Loading failed-step log..."
					m.logScroll = 0
					return m, logCmd(run.ID, job.ID)
				}
			}
		}
	case modeLogPager:
		switch msg.String() {
		case "esc", "b":
			m.mode = modeRunDetail
		case "j", "down":
			m.logScroll++
		case "k", "up":
			if m.logScroll > 0 {
				m.logScroll--
			}
		case "g":
			m.logScroll = 0
		case "G":
			m.logScroll = m.logMaxScroll()
		case "pgdown", " ":
			m.logScroll += m.logViewportHeight()
		case "pgup":
			m.logScroll -= m.logViewportHeight()
			if m.logScroll < 0 {
				m.logScroll = 0
			}
		}
	}
	return m, nil
}

// scheduleNext picks the next tick interval and returns any commands that
// should fire alongside it (the periodic list refresh, plus job re-fetches
// for runs that are still moving).
func (m Model) scheduleNext() (time.Duration, []tea.Cmd) {
	cmds := []tea.Cmd{loadCmd(m.cfg, m.runs)}
	for _, r := range m.runs {
		if r.Phase() == PhaseRunning || r.Phase() == PhaseQueued {
			cmds = append(cmds, jobsCmd(m.cfg.Repo, r.ID))
		}
	}
	hasMoving := false
	for _, r := range m.runs {
		if r.Phase() == PhaseRunning || r.Phase() == PhaseQueued {
			hasMoving = true
			break
		}
	}
	switch {
	case m.mode == modeLogPager:
		return 10 * time.Second, cmds
	case hasMoving:
		return 3 * time.Second, cmds
	case m.stillRun >= 3:
		return 30 * time.Second, cmds
	}
	return m.cfg.BaseRefresh, cmds
}

func runsSignature(runs []WorkflowRun) string {
	var b strings.Builder
	for _, r := range runs {
		fmt.Fprintf(&b, "%d:%s:%s|", r.ID, r.Status, r.Conclusion)
	}
	return b.String()
}

// ===== style palette (mirrors sprintwatch where roles overlap) =====

var (
	stOK         = lipgloss.NewStyle().Foreground(lipgloss.Color("#00D75F")).Bold(true)
	stWarn       = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAF00")).Bold(true)
	stErr        = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF005F")).Bold(true)
	stRunning    = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FAFFF")).Bold(true)
	stCancelled  = lipgloss.NewStyle().Foreground(lipgloss.Color("#5F5F5F")).Bold(true)
	stInfo       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FAFFF"))
	stDim        = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	stHead       = lipgloss.NewStyle().Foreground(lipgloss.Color("#87D7FF")).Bold(true)
	stTitleBar   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#5F87FF")).Padding(0, 1)
	stHelp       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5F5F5F"))
	stCardBlue   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F87FF")).Padding(0, 1).MarginBottom(1)
	stCardGreen  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#00D75F")).Padding(0, 1).MarginBottom(1)
	stCardRed    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#FF005F")).Padding(0, 1).MarginBottom(1)
	stCardYellow = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#FFAF00")).Padding(0, 1).MarginBottom(1)
	stCardWhite  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#FFFFFF")).Padding(0, 1).MarginBottom(1)
)

func phaseStyle(p Phase) lipgloss.Style {
	switch p {
	case PhasePassed:
		return stOK
	case PhaseFailed:
		return stErr
	case PhaseRunning:
		return stRunning
	case PhaseQueued:
		return stWarn
	case PhaseCancelled:
		return stCancelled
	case PhaseSkipped:
		return stDim
	}
	return stDim
}

func phaseBadge(p Phase) string {
	switch p {
	case PhasePassed:
		return stOK.Render("✓ PASSED")
	case PhaseFailed:
		return stErr.Render("✗ FAILED")
	case PhaseRunning:
		return stRunning.Render("● RUNNING")
	case PhaseQueued:
		return stWarn.Render("⏳ QUEUED")
	case PhaseCancelled:
		return stCancelled.Render("⊘ CANCELLED")
	case PhaseSkipped:
		return stDim.Render("⊝ SKIPPED")
	}
	return stDim.Render("? UNKNOWN")
}

func phaseGlyph(p Phase) string {
	switch p {
	case PhasePassed:
		return stOK.Render("█")
	case PhaseFailed:
		return stErr.Render("█")
	case PhaseRunning:
		return stRunning.Render("▓")
	case PhaseQueued:
		return stWarn.Render("░")
	case PhaseCancelled:
		return stCancelled.Render("█")
	case PhaseSkipped:
		return stDim.Render("░")
	}
	return stDim.Render(" ")
}

// ===== view dispatch =====

func (m Model) View() string {
	if m.err != nil && len(m.runs) == 0 {
		return stErr.Render("ERROR: ") + m.err.Error() + "\n\n" + stHelp.Render("[q] quit  [r] retry") + "\n"
	}
	switch m.mode {
	case modeRunDetail:
		return m.viewDetail()
	case modeLogPager:
		return m.viewLog()
	}
	return m.viewList()
}

func (m Model) viewList() string {
	var sb strings.Builder
	sb.WriteString(m.header() + "\n\n")
	if len(m.runs) == 0 {
		sb.WriteString(stDim.Render(fmt.Sprintf("No workflow runs found for commit %s.\n", short(m.cfg.SHA))))
		sb.WriteString(stDim.Render("Has the commit been pushed? Try `git push`.\n"))
		sb.WriteString("\n" + stHelp.Render("[q] quit  [r] refresh"))
		return sb.String()
	}
	for i, r := range m.runs {
		sb.WriteString(m.renderRunCard(r, i == m.selected))
	}
	if m.err != nil {
		sb.WriteString("\n" + stErr.Render("warn: ") + m.err.Error())
	}
	sb.WriteString("\n" + stHelp.Render("[q] quit  [r] refresh  [↑/↓] select  [enter] detail"))
	return sb.String()
}

func (m Model) renderRunCard(r WorkflowRun, selected bool) string {
	var b strings.Builder
	header := stHead.Render(r.Name)
	header += "  " + stDim.Render(r.Event+" @ "+r.ShortSHA())
	header += "  " + phaseBadge(r.Phase())
	b.WriteString(header + "\n")

	// Phase mini-map: one block per DAG column, color = aggregate phase.
	if dag, ok := m.dags[r.Name]; ok && len(r.Jobs) > 0 {
		cols := dag.PhaseColumns(r.Jobs)
		var glyphs []string
		for _, col := range cols {
			worst := PhaseUnknown
			for _, c := range col {
				if rankPhase(c.AggregatePhase()) > rankPhase(worst) {
					worst = c.AggregatePhase()
				}
			}
			glyphs = append(glyphs, phaseGlyph(worst))
		}
		if len(glyphs) > 0 {
			b.WriteString("Phases:    " + strings.Join(glyphs, stDim.Render(" → ")) + "\n")
		}
	} else if len(r.Jobs) > 0 {
		// No DAG parsed — flat strip of per-job glyphs.
		var glyphs []string
		for _, j := range r.Jobs {
			glyphs = append(glyphs, phaseGlyph(j.Phase()))
		}
		b.WriteString("Jobs:      " + strings.Join(glyphs, " ") + "\n")
	} else if r.JobsErr != nil {
		b.WriteString(stDim.Render("Jobs:      ") + stWarn.Render(r.JobsErr.Error()) + "\n")
	} else {
		b.WriteString(stDim.Render("Jobs:      loading...") + "\n")
	}

	// Per-phase counts when jobs are loaded.
	if len(r.Jobs) > 0 {
		counts := map[Phase]int{}
		for _, j := range r.Jobs {
			counts[j.Phase()]++
		}
		b.WriteString(fmt.Sprintf("Jobs:      %s passed   %s failed   %s running   %s queued   %s skipped\n",
			boldOrDim(counts[PhasePassed], stOK),
			boldOrDim(counts[PhaseFailed], stErr),
			boldOrDim(counts[PhaseRunning], stRunning),
			boldOrDim(counts[PhaseQueued], stWarn),
			boldOrDim(counts[PhaseSkipped], stDim)))
	}

	elapsed := r.Elapsed()
	if elapsed > 0 {
		label := "Elapsed:"
		if r.IsTerminal() {
			label = "Duration:"
		}
		b.WriteString(fmt.Sprintf("%s   %s\n", label, stHead.Render(humanDuration(elapsed))))
	}

	style := stCardBlue
	switch {
	case r.Phase() == PhaseFailed && selected:
		style = stCardWhite
	case r.Phase() == PhaseFailed:
		style = stCardRed
	case r.Phase() == PhasePassed && selected:
		style = stCardWhite
	case r.Phase() == PhasePassed:
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

func rankPhase(p Phase) int {
	switch p {
	case PhaseFailed:
		return 6
	case PhaseRunning:
		return 5
	case PhaseQueued:
		return 4
	case PhaseCancelled:
		return 3
	case PhasePassed:
		return 2
	case PhaseSkipped:
		return 1
	}
	return 0
}

func (m Model) viewDetail() string {
	if len(m.runs) == 0 || m.selected >= len(m.runs) {
		return m.viewList()
	}
	var sb strings.Builder
	sb.WriteString(m.header() + "\n\n")
	r := m.runs[m.selected]

	title := stHead.Render(fmt.Sprintf("%s  ", r.Name)) + phaseBadge(r.Phase())
	sb.WriteString(title + "\n")
	sb.WriteString(stDim.Render(fmt.Sprintf("%s @ %s   %s   %s\n",
		r.Event, r.ShortSHA(), humanDuration(r.Elapsed()), r.URL)))
	sb.WriteString("\n")

	if len(r.Jobs) == 0 {
		if r.JobsErr != nil {
			sb.WriteString(stErr.Render("could not load jobs: ") + r.JobsErr.Error() + "\n")
		} else {
			sb.WriteString(stDim.Render("loading jobs...\n"))
		}
	} else {
		sb.WriteString(m.renderJobList(r))
	}

	sb.WriteString("\n" + stHelp.Render("[esc/b] back  [↑/↓] select job  [←/→] prev/next run  [enter] failed-step log  [r] refresh  [q] quit"))
	return sb.String()
}

func (m Model) renderJobList(r WorkflowRun) string {
	var b strings.Builder
	dag := m.dags[r.Name]
	if dag == nil || len(dag.Jobs) == 0 {
		// Flat list fallback.
		for i, j := range r.Jobs {
			b.WriteString(renderJobLine(j, i == m.jobSelected, m.width))
		}
		return b.String()
	}
	cols := dag.PhaseColumns(r.Jobs)
	idx := 0
	for depth, col := range cols {
		b.WriteString(stHead.Render(fmt.Sprintf("Phase %d", depth+1)) + "\n")
		for _, cell := range col {
			for _, leg := range cell.Legs {
				b.WriteString(renderJobLine(leg, idx == m.jobSelected, m.width))
				idx++
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderJobLine(j Job, selected bool, width int) string {
	cursor := "  "
	if selected {
		cursor = stHead.Render("▸ ")
	}
	done, total := j.StepsDone()
	stepInfo := stDim.Render(fmt.Sprintf("[%d/%d steps]", done, total))
	dur := stDim.Render(humanDuration(j.Duration()))
	name := j.Name
	max := width - 50
	if max < 30 {
		max = 30
	}
	if len(name) > max {
		name = name[:max-1] + "…"
	}
	line := fmt.Sprintf("%s%s %-*s  %s  %s",
		cursor,
		phaseBadge(j.Phase()),
		max, name,
		stepInfo, dur,
	)
	if fs, ok := j.FailedStep(); ok {
		line += "\n    " + stErr.Render("└ failed step: ") + fs.Name
	}
	return line + "\n"
}

func (m Model) viewLog() string {
	var sb strings.Builder
	sb.WriteString(m.header() + "\n\n")
	if len(m.runs) > 0 && m.selected < len(m.runs) {
		r := m.runs[m.selected]
		var jobName string
		for _, j := range r.Jobs {
			if j.ID == m.logJobID {
				jobName = j.Name
				break
			}
		}
		sb.WriteString(stHead.Render(fmt.Sprintf("Failed-step log — %s / %s", r.Name, jobName)) + "\n\n")
	}
	lines := strings.Split(m.logBuf, "\n")
	vh := m.logViewportHeight()
	max := len(lines) - vh
	if max < 0 {
		max = 0
	}
	if m.logScroll > max {
		m.logScroll = max
	}
	end := m.logScroll + vh
	if end > len(lines) {
		end = len(lines)
	}
	for _, ln := range lines[m.logScroll:end] {
		sb.WriteString(ln + "\n")
	}
	sb.WriteString("\n" + stHelp.Render(fmt.Sprintf(
		"[esc/b] back  [j/k] line  [pgup/pgdn|space] page  [g/G] top/bottom  [%d-%d/%d]",
		m.logScroll+1, end, len(lines))))
	return sb.String()
}

func (m Model) logViewportHeight() int {
	h := m.height - 6
	if h < 5 {
		h = 20
	}
	return h
}

func (m Model) logMaxScroll() int {
	n := len(strings.Split(m.logBuf, "\n")) - m.logViewportHeight()
	if n < 0 {
		return 0
	}
	return n
}

// ===== helpers =====

func (m Model) header() string {
	bar := stTitleBar.Render(" CI Watch ")
	repo := stDim.Render("repo: " + filepath.Base(m.cfg.Root))
	commit := stDim.Render("commit: " + short(m.cfg.SHA))
	refreshed := ""
	if !m.lastRefresh.IsZero() {
		refreshed = stDim.Render("refreshed: " + m.lastRefresh.Format("15:04:05"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, bar, "  ", repo, "  ", commit, "  ", refreshed)
}

func short(sha string) string {
	if len(sha) < 7 {
		return sha
	}
	return sha[:7]
}

func boldOrDim(n int, hot lipgloss.Style) string {
	if n == 0 {
		return stDim.Render("0")
	}
	return hot.Render(fmt.Sprintf("%d", n))
}

func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := int(d.Hours())
	mn := int(d.Minutes()) - h*60
	return fmt.Sprintf("%dh%02dm", h, mn)
}
