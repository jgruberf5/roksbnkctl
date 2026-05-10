package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// workflowFile mirrors the subset of GitHub Actions workflow YAML we care
// about: the workflow name and per-job needs[]. Everything else is
// ignored (steps, env, services, on, etc.) so we stay forgiving of
// schema evolution.
type workflowFile struct {
	Name string                 `yaml:"name"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Name  string     `yaml:"name"`
	Needs needsValue `yaml:"needs"`
}

// needsValue handles the three legal shapes for `needs:`:
//
//	needs: a
//	needs: [a, b]
//	needs:
//	  - a
//	  - b
type needsValue []string

func (n *needsValue) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*n = []string{value.Value}
	case yaml.SequenceNode:
		out := make([]string, 0, len(value.Content))
		for _, c := range value.Content {
			if c.Kind == yaml.ScalarNode {
				out = append(out, c.Value)
			}
		}
		*n = out
	}
	return nil
}

// DAG represents one parsed workflow's dependency graph keyed by job ID
// (the key under `jobs:` in the YAML, which is what GitHub uses to
// resolve `needs:`). The Display name comes from the optional `name:`
// override and falls back to the ID.
type DAG struct {
	Workflow string
	Jobs     map[string]workflowJob
	Depth    map[string]int
}

// LoadDAGs scans .github/workflows/*.yml and returns one DAG per workflow,
// keyed by the workflow's user-facing name (matches WorkflowRun.Name).
// Files that don't parse yield no entry — callers degrade to a flat list.
func LoadDAGs(repoRoot string) map[string]*DAG {
	out := map[string]*DAG{}
	dir := filepath.Join(repoRoot, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		d, err := parseWorkflow(filepath.Join(dir, name))
		if err != nil || d == nil {
			continue
		}
		out[d.Workflow] = d
	}
	return out
}

func parseWorkflow(path string) (*DAG, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wf workflowFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return nil, fmt.Errorf("yaml parse %s: %w", path, err)
	}
	if wf.Name == "" {
		wf.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	depth, _ := topoDepth(wf.Jobs)
	return &DAG{Workflow: wf.Name, Jobs: wf.Jobs, Depth: depth}, nil
}

// topoDepth runs Kahn's algorithm and returns each node's depth in the
// dependency DAG (0 = no needs). Cycles — impossible in valid GHA but
// possible in a malformed file — leave their members at depth 0 so the
// view still renders something.
func topoDepth(jobs map[string]workflowJob) (map[string]int, error) {
	depth := map[string]int{}
	indeg := map[string]int{}
	rev := map[string][]string{}
	for id, j := range jobs {
		depth[id] = 0
		indeg[id] = len(j.Needs)
		for _, dep := range j.Needs {
			rev[dep] = append(rev[dep], id)
		}
	}
	var queue []string
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	processed := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		processed++
		for _, child := range rev[id] {
			if depth[child] < depth[id]+1 {
				depth[child] = depth[id] + 1
			}
			indeg[child]--
			if indeg[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if processed < len(jobs) {
		return depth, fmt.Errorf("dependency cycle detected")
	}
	return depth, nil
}

// matrixSuffix matches the trailing "(leg)" GitHub appends to matrix-job
// names. "test (ubuntu-latest, 1.21)" → base "test".
var matrixSuffix = regexp.MustCompile(`\s+\([^)]+\)\s*$`)

// AnnotateJobs fills in BaseName and Needs on each Job by matching against
// the parsed DAG. Jobs whose base name has no DAG entry get BaseName=Name
// and empty Needs — they still render at depth 0.
func (d *DAG) AnnotateJobs(jobs []Job) {
	for i := range jobs {
		jobs[i].BaseName = matrixSuffix.ReplaceAllString(jobs[i].Name, "")
		if d == nil {
			continue
		}
		if id, ok := d.findJobID(jobs[i].BaseName); ok {
			jobs[i].Needs = append([]string(nil), d.Jobs[id].Needs...)
		}
	}
}

// findJobID resolves a display name (e.g. "vet + fmt + staticcheck + test")
// back to its YAML key (e.g. "test"). Tries the literal match first, then
// the per-job `name:` override.
func (d *DAG) findJobID(displayName string) (string, bool) {
	if _, ok := d.Jobs[displayName]; ok {
		return displayName, true
	}
	for id, j := range d.Jobs {
		if j.Name == displayName {
			return id, true
		}
	}
	return "", false
}

// PhaseColumns groups jobs by topological depth into ordered columns. Jobs
// with the same BaseName (matrix legs) collapse to a single column entry
// holding all legs, so the view can stack them inside one phase cell.
func (d *DAG) PhaseColumns(jobs []Job) [][]JobCell {
	depthOf := func(j Job) int {
		if d == nil {
			return 0
		}
		if id, ok := d.findJobID(j.BaseName); ok {
			return d.Depth[id]
		}
		return 0
	}
	byDepth := map[int]map[string][]Job{}
	maxDepth := 0
	for _, j := range jobs {
		depth := depthOf(j)
		if depth > maxDepth {
			maxDepth = depth
		}
		if byDepth[depth] == nil {
			byDepth[depth] = map[string][]Job{}
		}
		byDepth[depth][j.BaseName] = append(byDepth[depth][j.BaseName], j)
	}
	cols := make([][]JobCell, maxDepth+1)
	for depth := 0; depth <= maxDepth; depth++ {
		groups := byDepth[depth]
		var names []string
		for n := range groups {
			names = append(names, n)
		}
		sort.Strings(names)
		var cells []JobCell
		for _, n := range names {
			legs := groups[n]
			sort.Slice(legs, func(i, j int) bool { return legs[i].Name < legs[j].Name })
			cells = append(cells, JobCell{Base: n, Legs: legs})
		}
		cols[depth] = cells
	}
	return cols
}

// JobCell is one logical job in the DAG — either a single job or a matrix
// fan-out grouped under one BaseName.
type JobCell struct {
	Base string
	Legs []Job
}

// AggregatePhase collapses the legs into a single phase using
// worst-state-wins ordering: failed > running > queued > cancelled >
// passed > skipped. This matches at-a-glance scanning expectations.
func (c JobCell) AggregatePhase() Phase {
	rank := func(p Phase) int {
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
	best := PhaseUnknown
	for _, l := range c.Legs {
		if rank(l.Phase()) > rank(best) {
			best = l.Phase()
		}
	}
	return best
}
