package main

import (
	"sort"
	"strings"
	"time"
)

type Phase int

const (
	PhaseQueued Phase = iota
	PhaseRunning
	PhasePassed
	PhaseFailed
	PhaseSkipped
	PhaseCancelled
	PhaseUnknown
)

func (p Phase) String() string {
	switch p {
	case PhaseQueued:
		return "queued"
	case PhaseRunning:
		return "running"
	case PhasePassed:
		return "passed"
	case PhaseFailed:
		return "failed"
	case PhaseSkipped:
		return "skipped"
	case PhaseCancelled:
		return "cancelled"
	}
	return "unknown"
}

// classifyPhase folds the GitHub Actions (status, conclusion) tuple down to
// the small enum the view layer cares about. The view never touches the raw
// GitHub strings, so any future changes to the upstream vocabulary stay
// contained here.
func classifyPhase(status, conclusion string) Phase {
	s := strings.ToLower(strings.TrimSpace(status))
	c := strings.ToLower(strings.TrimSpace(conclusion))
	switch s {
	case "queued", "waiting", "pending", "requested":
		return PhaseQueued
	case "in_progress":
		return PhaseRunning
	case "completed":
		switch c {
		case "success":
			return PhasePassed
		case "failure", "timed_out", "startup_failure":
			return PhaseFailed
		case "skipped":
			return PhaseSkipped
		case "cancelled":
			return PhaseCancelled
		case "neutral", "action_required", "stale":
			return PhaseUnknown
		}
	}
	if c == "success" {
		return PhasePassed
	}
	if c == "failure" {
		return PhaseFailed
	}
	return PhaseUnknown
}

type Step struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	Number      int       `json:"number"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
}

func (s Step) Phase() Phase { return classifyPhase(s.Status, s.Conclusion) }

func (s Step) Duration() time.Duration {
	if s.StartedAt.IsZero() {
		return 0
	}
	end := s.CompletedAt
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(s.StartedAt)
}

type Job struct {
	ID          int64     `json:"databaseId"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	URL         string    `json:"url"`
	Steps       []Step    `json:"steps"`
	// Needs is populated from the workflow YAML, not the gh JSON.
	Needs []string `json:"-"`
	// BaseName is the job name with the matrix-leg suffix stripped:
	// "test (ubuntu-latest)" -> "test". Populated by the DAG layer so the
	// view can group matrix legs into one phase cell.
	BaseName string `json:"-"`
}

func (j Job) Phase() Phase { return classifyPhase(j.Status, j.Conclusion) }

func (j Job) Duration() time.Duration {
	if j.StartedAt.IsZero() {
		return 0
	}
	end := j.CompletedAt
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(j.StartedAt)
}

// FailedStep returns the first failed step, if any. Used to surface a
// one-line failure summary on the detail card.
func (j Job) FailedStep() (Step, bool) {
	for _, s := range j.Steps {
		if s.Phase() == PhaseFailed {
			return s, true
		}
	}
	return Step{}, false
}

// StepsDone reports completed-step count and total step count, ignoring
// skipped steps so the user sees real progress.
func (j Job) StepsDone() (done, total int) {
	for _, s := range j.Steps {
		if s.Phase() == PhaseSkipped {
			continue
		}
		total++
		switch s.Phase() {
		case PhasePassed, PhaseFailed, PhaseCancelled:
			done++
		}
	}
	return
}

type WorkflowRun struct {
	ID int64 `json:"databaseId"`
	// Name is the run's display name. We use the `name` JSON field
	// (available on every gh release that supports --json) rather than
	// `workflowName` (added in gh ~2.30). On most runs they're identical;
	// they diverge only when the workflow YAML sets `run-name:`.
	Name         string    `json:"name"`
	Event        string    `json:"event"`
	HeadSHA      string    `json:"headSha"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	URL          string    `json:"url"`
	Jobs         []Job     `json:"-"`
	JobsLoadedAt time.Time `json:"-"`
	JobsErr      error     `json:"-"`
}

func (r WorkflowRun) Phase() Phase { return classifyPhase(r.Status, r.Conclusion) }

func (r WorkflowRun) IsTerminal() bool {
	switch r.Phase() {
	case PhasePassed, PhaseFailed, PhaseSkipped, PhaseCancelled:
		return true
	}
	return false
}

func (r WorkflowRun) Elapsed() time.Duration {
	if r.CreatedAt.IsZero() {
		return 0
	}
	end := r.UpdatedAt
	if !r.IsTerminal() || end.IsZero() {
		end = time.Now()
	}
	return end.Sub(r.CreatedAt)
}

func (r WorkflowRun) ShortSHA() string {
	if len(r.HeadSHA) < 7 {
		return r.HeadSHA
	}
	return r.HeadSHA[:7]
}

// SortRuns puts the most recently created run first within each workflow,
// then groups by workflow name for a stable visual order.
func SortRuns(runs []WorkflowRun) {
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].Name != runs[j].Name {
			return runs[i].Name < runs[j].Name
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
}

// Config is the resolved CLI flag bundle passed to the Model.
type Config struct {
	Root        string
	Repo        string // "owner/name" — required for gh api job calls
	SHA         string
	RunID       int64
	Workflow    string
	BaseRefresh time.Duration
}
