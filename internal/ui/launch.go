package ui

import (
	"fmt"
	"io"
	"os"
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
	Num        int
	Label      string // e.g. "VPC · subnets · IGW · NAT"
	PhaseRange string // e.g. "[Phase 00–07]"
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

// RocketRenderer renders a rocket-themed staged progress UI to a TTY using
// ANSI cursor-up + line-clear escapes between redraws.
type RocketRenderer struct {
	out         io.Writer
	clusterName string
	stages      []stageState
	started     time.Time
	frameLines  int // number of lines last written (for cursor rewind)
	finished    bool
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
		r.drawFrame()
		// Surface the error on its own line, clearly visible.
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
		// Failure already surfaced by PhaseEnd; freeze the frame and exit.
		r.clearFrame()
		r.drawFrame()
		return
	}
	// Mark all active/idle stages as done.
	for i := range r.stages {
		if r.stages[i].status == "active" || r.stages[i].status == "idle" {
			r.stages[i].status = "done"
			if !r.stages[i].startTime.IsZero() {
				r.stages[i].elapsed = time.Since(r.stages[i].startTime)
			}
		}
	}
	r.drawFrame()
	// ORBIT summary line.
	fmt.Fprintf(r.out, "   ──────────  ORBIT    🛰  cluster up · VIP live · T+%s\n",
		formatElapsed(time.Since(r.started)))
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

// drawFrame (re)draws the full rocket UI block.
func (r *RocketRenderer) drawFrame() {
	r.clearFrame()

	lines := 0
	elapsed := time.Since(r.started)

	// Header line.
	fmt.Fprintf(r.out, "\033[2K   awsbnkctl ▸ %s ▸ DEMO LAUNCH   T+%s\n",
		r.clusterName, formatElapsed(elapsed))
	lines++

	for _, ss := range r.stages {
		fmt.Fprintf(r.out, "\033[2K%s\n", renderStage(ss))
		lines++
	}

	// ORBIT placeholder (blank until Finish).
	fmt.Fprintf(r.out, "\033[2K   ──────────  ORBIT\n")
	lines++

	r.frameLines = lines
}

// renderStage formats a single stage row.
func renderStage(ss stageState) string {
	var bar, annotation string
	switch ss.status {
	case "idle":
		bar = "──────────"
		annotation = ""
	case "active":
		bar = "██████░░░░"
		phase := ss.currentPhase
		elapsed := ""
		if !ss.startTime.IsZero() {
			elapsed = "  ⏳ " + formatElapsed(time.Since(ss.startTime))
		}
		if phase != "" {
			annotation = fmt.Sprintf("  (current: %s)%s", phase, elapsed)
		} else {
			annotation = elapsed
		}
	case "done":
		bar = "██████████"
		if ss.elapsed > 0 {
			annotation = "  ✓ " + formatElapsed(ss.elapsed)
		} else {
			annotation = "  ✓"
		}
	case "failed":
		bar = "██░░░░░░░░"
		annotation = fmt.Sprintf("  (FAILED at %s)  ✗", ss.currentPhase)
	}

	return fmt.Sprintf("   %s  STAGE %d  %-44s %s%s",
		bar, ss.stage.Num, ss.stage.Label, ss.stage.PhaseRange, annotation)
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
