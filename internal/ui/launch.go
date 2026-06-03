package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// isTerminalFn is a package-level var so tests can override it.
var isTerminalFn = func(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}

// Stage describes one high-level grouping of phases shown in the rocket UI.
type Stage struct {
	Num   int
	Label string // e.g. "VPC · subnets · IGW · NAT"
}

// Renderer is the minimal event interface consumed by runPhasedUp.
type Renderer interface {
	Start(stages []Stage)
	PhaseBegin(stage int, name string)
	PhaseEnd(stage int, name string, err error)
	Finish(err error)
}

// NewRenderer returns a RocketRenderer when demo && IsTerminal(out) && !noColor;
// otherwise it returns a PlainRenderer (no-op). out is typically os.Stderr.
func NewRenderer(out io.Writer, clusterName string, demo bool, noColor bool) Renderer {
	if demo && isTerminalFn(out) && !noColor {
		return NewRocketRenderer(out, clusterName)
	}
	return PlainRenderer{}
}

// NewDescentRenderer returns a landing-scene RocketRenderer (rocket descends
// orbit → pad) when demo && IsTerminal(out) && !noColor; otherwise a
// PlainRenderer. Used by `down` on a demo cluster.
func NewDescentRenderer(out io.Writer, clusterName string, demo bool, noColor bool) Renderer {
	if demo && isTerminalFn(out) && !noColor {
		r := NewRocketRenderer(out, clusterName)
		r.descending = true
		return r
	}
	return PlainRenderer{}
}

// PlainRenderer is a no-op — all four methods return without writing anything.
// Normal/CI output is byte-for-byte unchanged when PlainRenderer is in use.
type PlainRenderer struct{}

func (PlainRenderer) Start(_ []Stage)                   {}
func (PlainRenderer) PhaseBegin(_ int, _ string)        {}
func (PlainRenderer) PhaseEnd(_ int, _ string, _ error) {}
func (PlainRenderer) Finish(_ error)                    {}

// stageState tracks rendering state for a single stage.
type stageState struct {
	stage        Stage
	status       string // "idle" | "active" | "done" | "failed"
	currentPhase string
	elapsed      time.Duration
	startTime    time.Time
	failedErr    error
}

// RocketRenderer renders a rocket-launch progress UI to a TTY: a persistent
// launch scene in the left gutter (the rocket climbs pad → orbit as stages
// complete) beside a stage checklist on the right. Redraws in place via ANSI
// cursor-up + line-clear escapes.
type RocketRenderer struct {
	out         io.Writer
	clusterName string
	stages      []stageState
	started     time.Time
	frameLines  int // number of lines last written (for cursor rewind)
	frame       int // redraw counter (drives exhaust animation)
	finished    bool
	failed      bool
	descending  bool // true for the `down` landing scene (orbit → pad)
}

// NewRocketRenderer constructs a RocketRenderer writing to out.
// Exported so tests can instantiate it directly without going through NewRenderer.
func NewRocketRenderer(out io.Writer, clusterName string) *RocketRenderer {
	return &RocketRenderer{out: out, clusterName: clusterName}
}

func (r *RocketRenderer) Start(stages []Stage) {
	r.started = time.Now()
	r.stages = make([]stageState, len(stages))
	for i, s := range stages {
		r.stages[i] = stageState{stage: s, status: "idle"}
	}
	r.drawFrame()
}

func (r *RocketRenderer) PhaseBegin(stageNum int, name string) {
	// Mark any other active stage as done — control has moved on to a new stage.
	for i := range r.stages {
		if r.stages[i].stage.Num != stageNum && r.stages[i].status == "active" {
			r.stages[i].status = "done"
			if !r.stages[i].startTime.IsZero() {
				r.stages[i].elapsed = time.Since(r.stages[i].startTime)
			}
			r.stages[i].currentPhase = ""
		}
	}
	idx := r.stageIndex(stageNum)
	if idx < 0 {
		return
	}
	ss := &r.stages[idx]
	if ss.status == "idle" {
		ss.status = "active"
		ss.startTime = time.Now()
	}
	ss.currentPhase = name
	r.drawFrame()
}

func (r *RocketRenderer) PhaseEnd(stageNum int, name string, err error) {
	idx := r.stageIndex(stageNum)
	if idx < 0 {
		return
	}
	ss := &r.stages[idx]
	if err != nil {
		ss.status = "failed"
		ss.failedErr = err
		ss.currentPhase = name
		if !ss.startTime.IsZero() {
			ss.elapsed = time.Since(ss.startTime)
		}
		r.failed = true
		r.drawFrame()
		// Surface the error on its own line, clearly visible below the frame.
		fmt.Fprintf(r.out, "✗ STAGE %d — %s failed: %v\n", stageNum, name, err)
		return
	}
	// On success: stage stays "active" until a PhaseBegin for a DIFFERENT stage
	// transitions it to "done" (or until Finish for the final stage).
	ss.currentPhase = name
	r.drawFrame()
}

func (r *RocketRenderer) Finish(err error) {
	if r.finished {
		return
	}
	r.finished = true
	if err != nil {
		// Failure was already surfaced (boom frame + error line) by PhaseEnd.
		// Leave that frozen frame in place; don't redraw.
		r.failed = true
		return
	}
	// Mark all active/idle stages as done.
	for i := range r.stages {
		if r.stages[i].status == "active" || r.stages[i].status == "idle" {
			r.stages[i].status = "done"
			if !r.stages[i].startTime.IsZero() {
				r.stages[i].elapsed = time.Since(r.stages[i].startTime)
			}
			r.stages[i].currentPhase = ""
		}
	}
	r.drawFrame()
}

// stageIndex returns the slice index for the given stage number, or -1.
func (r *RocketRenderer) stageIndex(num int) int {
	for i := range r.stages {
		if r.stages[i].stage.Num == num {
			return i
		}
	}
	return -1
}

// clearFrame rewinds the cursor to the top of the last frame.
func (r *RocketRenderer) clearFrame() {
	if r.frameLines > 0 {
		fmt.Fprintf(r.out, "\033[%dA", r.frameLines)
	}
}

const (
	leftWidth = 20 // display width of the left launch-scene gutter
	bodyRows  = 8  // fixed number of body rows (keeps the in-place redraw clean)
	glyphRkt  = "🚀"
	glyphSat  = "🛰"
	glyphBoom = "💥"
)

// drawFrame (re)draws the full launch UI block: header + bodyRows of
// "<launch scene> │ <stage checklist>".
func (r *RocketRenderer) drawFrame() {
	r.clearFrame()
	r.frame++

	left := r.buildLeft()
	if r.descending {
		left = r.buildLeftDescent()
	}
	right := r.buildRight()

	mode := "DEMO LAUNCH"
	if r.descending {
		mode = "DEMO LANDING"
	}
	lines := 0
	fmt.Fprintf(r.out, "\033[2K   awsbnkctl ▸ %s ▸ %s   T+%s\n",
		r.clusterName, mode, formatElapsed(time.Since(r.started)))
	lines++
	for i := 0; i < bodyRows; i++ {
		fmt.Fprintf(r.out, "\033[2K %s │ %s\n", left[i], right[i])
		lines++
	}
	r.frameLines = lines
}

// buildLeft renders the launch scene: ORBIT band at the top, a diagonal
// trajectory the rocket climbs (pad → orbit) as stages complete, and the pad
// at the bottom. Each returned row is padded to leftWidth display cells so the
// divider lines up.
func (r *RocketRenderer) buildLeft() [bodyRows]string {
	var rows [bodyRows]string

	completed := 0
	for _, ss := range r.stages {
		if ss.status == "done" {
			completed++
		}
	}

	// Trajectory waypoints (one per altitude). Altitude == completed stages.
	const maxAlt = 3
	colForAlt := func(a int) int { return 2 + 3*a } // 2, 5, 8, 11
	rowForAlt := func(a int) int { return 6 - a }   // 6, 5, 4, 3

	rocketAlt := completed
	if rocketAlt > maxAlt {
		rocketAlt = maxAlt
	}
	satellite := r.finished && !r.failed
	glyph := glyphRkt
	if r.failed {
		glyph = glyphBoom
	}
	// On a clean finish the rocket has reached orbit: draw the full contrail.
	trailUpTo := rocketAlt
	if satellite {
		trailUpTo = maxAlt + 1
	}

	// Orbit band + the planned path leading up-right to it.
	if satellite {
		rows[0] = "   · ⋆ ✦ ORBIT 🛰"
	} else {
		rows[0] = "   · ⋆ ✦ ORBIT"
	}
	rows[1] = strings.Repeat(" ", 15) + "·"
	rows[2] = strings.Repeat(" ", 13) + "·"

	// Trajectory rows (rocket + contrail).
	for a := 0; a <= maxAlt; a++ {
		row := rowForAlt(a)
		col := colForAlt(a)
		switch {
		case !satellite && a == rocketAlt:
			rows[row] = strings.Repeat(" ", col) + glyph
		case a < trailUpTo:
			rows[row] = strings.Repeat(" ", col) + "⋅"
		default:
			rows[row] = ""
		}
	}

	// Launch pad / base.
	rows[7] = " ◢█◣ ░▒▓"

	for i := range rows {
		rows[i] = padCol(rows[i], leftWidth)
	}
	return rows
}

// buildLeftDescent renders the landing scene (the `down` "reverse launch"):
// the rocket starts in orbit and descends the trajectory pad-ward as teardown
// stages complete, firing a retro-burn on the way down, then touches down on
// the pad with legs deployed.
func (r *RocketRenderer) buildLeftDescent() [bodyRows]string {
	var rows [bodyRows]string

	completed, started := 0, false
	for _, ss := range r.stages {
		if ss.status == "done" {
			completed++
		}
		if ss.status != "idle" {
			started = true
		}
	}

	const maxAlt = 3
	colForAlt := func(a int) int { return 2 + 3*a } // 2, 5, 8, 11
	rowForAlt := func(a int) int { return 6 - a }   // 6, 5, 4, 3

	landed := r.finished && !r.failed
	// Rocket altitude descends: starts high (alt3) and falls to the pad (alt0).
	rocketAlt := maxAlt - completed
	if rocketAlt < 0 {
		rocketAlt = 0
	}
	glyph := glyphRkt
	if r.failed {
		glyph = glyphBoom
	}
	// Before teardown begins the cluster is still "in orbit" (satellite shown).
	inOrbit := !started && !r.finished && !r.failed
	showRocket := started && !landed

	if inOrbit {
		rows[0] = "   · ⋆ ✦ ORBIT 🛰"
	} else {
		rows[0] = "   · ⋆ ✦ ORBIT"
	}
	rows[1] = strings.Repeat(" ", 15) + "·"
	rows[2] = strings.Repeat(" ", 13) + "·"

	// Trajectory: dots ABOVE the rocket mark the path it has already descended.
	for a := 0; a <= maxAlt; a++ {
		row := rowForAlt(a)
		col := colForAlt(a)
		switch {
		case showRocket && a == rocketAlt:
			rows[row] = strings.Repeat(" ", col) + glyph
		case started && a > rocketAlt:
			rows[row] = strings.Repeat(" ", col) + "⋅"
		default:
			rows[row] = ""
		}
	}

	// Retro-burn flare directly below the descending rocket.
	if showRocket && !r.failed {
		br := rowForAlt(rocketAlt) + 1
		bc := colForAlt(rocketAlt)
		if br > 0 && br < 7 && strings.TrimSpace(rows[br]) == "" {
			rows[br] = strings.Repeat(" ", bc) + "≈"
		}
	}

	// Pad / touchdown.
	if landed {
		rows[6] = "   ▕🚀▏"
		rows[7] = " ◢██◣ ░▒░ ✓"
	} else {
		rows[7] = " ◢█◣ ░▒▓"
	}

	for i := range rows {
		rows[i] = padCol(rows[i], leftWidth)
	}
	return rows
}

// buildRight renders the stage checklist (rows 1..N), the active sub-step line,
// and a footer (exhaust while climbing, orbit summary on success, abort on
// failure).
func (r *RocketRenderer) buildRight() [bodyRows]string {
	var rows [bodyRows]string

	activePhase := ""
	for i := range r.stages {
		ss := r.stages[i]
		row := 1 + i
		if row > bodyRows-2 { // leave the last row for the footer
			break
		}
		var icon, status string
		switch ss.status {
		case "done":
			icon, status = "✓", formatElapsed(ss.elapsed)
		case "active":
			icon, status = "◐", "⏳ "+formatElapsed(time.Since(ss.startTime))
			activePhase = ss.currentPhase
		case "failed":
			icon, status = "✗", "FAILED"
		default:
			icon, status = "○", "queued"
		}
		rows[row] = fmt.Sprintf("%s STAGE %d  %-38s %s", icon, ss.stage.Num, ss.stage.Label, status)
	}

	// Active sub-step, e.g. "secondary-enis".
	if activePhase != "" {
		rows[bodyRows-3] = "     └ " + activePhase
	}

	// Footer (mode-aware: launch vs landing).
	switch {
	case r.failed && r.descending:
		rows[bodyRows-1] = "💥 ABORT — teardown failed"
	case r.failed:
		rows[bodyRows-1] = "💥 MISSION ABORT"
	case r.finished && r.descending:
		rows[bodyRows-1] = "🚀 TOUCHDOWN · cluster down · T+" + formatElapsed(time.Since(r.started))
	case r.finished:
		rows[bodyRows-1] = "🛰 cluster up · VIP live · T+" + formatElapsed(time.Since(r.started))
	case r.descending:
		rows[bodyRows-1] = descentPlume(r.frame)
	default:
		rows[bodyRows-1] = exhaust(r.frame)
	}
	return rows
}

// descentPlume returns an animated retro-burn plume for the landing scene.
func descentPlume(frame int) string {
	switch frame % 3 {
	case 0:
		return "▼ retro-burn ▼"
	case 1:
		return " ▼ retro-burn"
	default:
		return "▼  retro-burn ▼"
	}
}

// exhaust returns an animated exhaust plume keyed off the redraw counter.
func exhaust(frame int) string {
	switch frame % 3 {
	case 0:
		return "~ ~ exhaust ~ ~"
	case 1:
		return " ~ ~ exhaust ~"
	default:
		return "~  ~ exhaust ~ ~"
	}
}

// dispW returns the display width of s in terminal cells, counting the wide
// emoji glyphs this renderer paints into the left gutter as 2 cells.
func dispW(s string) int {
	w := 0
	for _, r := range s {
		switch r {
		case '🚀', '🛰', '💥':
			w += 2
		default:
			w++
		}
	}
	return w
}

// padCol right-pads s with spaces to exactly n display cells so the column
// divider aligns regardless of any wide glyphs in the row.
func padCol(s string, n int) string {
	if w := dispW(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// formatElapsed formats a duration as a compact human-readable string.
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}
