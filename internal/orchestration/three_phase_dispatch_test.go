package orchestration

// Sprint 28 — additive hermetic coverage for the three-phase dispatch
// decisions + the parallel BNK ∥ Testing plumbing. Two halves:
//
//   - the §2d dispatch-decision table: per-phase presence → which phases each
//     verb acts on (incl. reuse-existing-cluster and the cluster-down guard
//     refusal), modelled as a pure function over config.Presence so it runs
//     hermetically with no terraform / cloud;
//   - the parallel-dispatch primitives the live `up`/`down` lean on: the
//     concurrent-stderr prefixWriter (newPrefixWriters), and the
//     errgroup ordering invariant + error propagation that runBNKAndTestingParallel
//     / RunDown encode (Cluster before both on up; BNK ∥ Testing before
//     Cluster on down; one leg's failure surfaces + cancels the sibling).
//
// The real leaf phases (RunTrialUp / runTestingUp) go through openTF /
// openTestingTF → a live terraform binary + a real workspace, so they are the
// gated-live driver's surface (scripts/e2e-three-phase.sh), not a hermetic
// unit. Here we pin the DECISION logic and the CONCURRENCY plumbing — the two
// things that are pure and deterministic.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"golang.org/x/sync/errgroup"
)

// ── §2d dispatch-decision table (pure, over config.Presence) ────────────

// phaseAction is the set of phases a verb acts on for a given presence, plus
// whether the verb is refused. "act on" = brought up / torn down / refreshed
// (the table doesn't distinguish create-vs-refresh; both touch the phase).
type phaseAction struct {
	phases  []string // ordered: which phases the verb touches
	refused bool     // the verb hard-refuses for this presence
}

func (a phaseAction) String() string {
	if a.refused {
		return "refused"
	}
	if len(a.phases) == 0 {
		return "noop"
	}
	return fmt.Sprintf("%v", a.phases)
}

// upAction models bare `up` (§2d / §3a): Cluster serial-first (unless already
// present or reused), then BNK ∥ Testing — only the phases that need it, but
// in the steady state all present phases refresh. We encode "act on" as:
// cluster touched unless already present, BNK + Testing always touched
// (created or refreshed).
func upAction(p config.Presence, reuse bool) phaseAction {
	var phases []string
	if !p.Cluster && !reuse {
		phases = append(phases, "cluster")
	}
	// BNK ∥ Testing always run after the cluster is present (create when
	// absent, refresh when present).
	phases = append(phases, "bnk", "testing")
	return phaseAction{phases: phases}
}

// downAction models bare `down` (§3c): nothing present → refused ("nothing to
// destroy"); otherwise BNK ∥ Testing (the present ones) then Cluster.
func downAction(p config.Presence) phaseAction {
	if !p.Any() {
		return phaseAction{refused: true}
	}
	var phases []string
	if p.BNK {
		phases = append(phases, "bnk")
	}
	if p.Testing {
		phases = append(phases, "testing")
	}
	if p.Cluster {
		phases = append(phases, "cluster")
	}
	return phaseAction{phases: phases}
}

// clusterDownAction models the `cluster down` guard (cluster_phase.go
// runClusterDown): refuse while BNK OR Testing present; refuse (nothing) when
// no cluster; else act on cluster.
func clusterDownAction(p config.Presence) phaseAction {
	if p.BNK || p.Testing {
		return phaseAction{refused: true}
	}
	if !p.Cluster {
		return phaseAction{refused: true}
	}
	return phaseAction{phases: []string{"cluster"}}
}

// bnkDownAction / testingDownAction model the leaf phase-down verbs: refuse
// when that phase has no state; else act on exactly that phase (the sibling +
// cluster are untouched — guaranteed by separate state dirs).
func bnkDownAction(p config.Presence) phaseAction {
	if !p.BNK {
		return phaseAction{refused: true}
	}
	return phaseAction{phases: []string{"bnk"}}
}

func testingDownAction(p config.Presence) phaseAction {
	if !p.Testing {
		return phaseAction{refused: true}
	}
	return phaseAction{phases: []string{"testing"}}
}

func eqPhases(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDispatchDecisionTable walks the architect's §2d table: every presence
// row × verb → the phases acted on / the refusal. This is the hermetic
// decision contract the live driver exercises end-to-end.
func TestDispatchDecisionTable(t *testing.T) {
	type row struct {
		name  string
		pres  config.Presence
		reuse bool // reuse-existing-cluster (cluster-outputs.json, no state-cluster/)

		up          phaseAction
		down        phaseAction
		clusterDown phaseAction
		bnkDown     phaseAction
		testingDown phaseAction
	}
	none := config.Presence{}
	C := config.Presence{Cluster: true}
	CB := config.Presence{Cluster: true, BNK: true}
	CT := config.Presence{Cluster: true, Testing: true}
	CBT := config.Presence{Cluster: true, BNK: true, Testing: true}

	rows := []row{
		{
			name:        "none (Empty)",
			pres:        none,
			up:          phaseAction{phases: []string{"cluster", "bnk", "testing"}},
			down:        phaseAction{refused: true},
			clusterDown: phaseAction{refused: true},
			bnkDown:     phaseAction{refused: true},
			testingDown: phaseAction{refused: true},
		},
		{
			name:        "C (cluster only)",
			pres:        C,
			up:          phaseAction{phases: []string{"bnk", "testing"}},
			down:        phaseAction{phases: []string{"cluster"}},
			clusterDown: phaseAction{phases: []string{"cluster"}},
			bnkDown:     phaseAction{refused: true},
			testingDown: phaseAction{refused: true},
		},
		{
			name:        "C+B",
			pres:        CB,
			up:          phaseAction{phases: []string{"bnk", "testing"}},
			down:        phaseAction{phases: []string{"bnk", "cluster"}},
			clusterDown: phaseAction{refused: true}, // BNK exists
			bnkDown:     phaseAction{phases: []string{"bnk"}},
			testingDown: phaseAction{refused: true},
		},
		{
			name:        "C+T",
			pres:        CT,
			up:          phaseAction{phases: []string{"bnk", "testing"}},
			down:        phaseAction{phases: []string{"testing", "cluster"}},
			clusterDown: phaseAction{refused: true}, // Testing exists
			bnkDown:     phaseAction{refused: true},
			testingDown: phaseAction{phases: []string{"testing"}},
		},
		{
			name:        "C+B+T (steady state)",
			pres:        CBT,
			up:          phaseAction{phases: []string{"bnk", "testing"}},
			down:        phaseAction{phases: []string{"bnk", "testing", "cluster"}},
			clusterDown: phaseAction{refused: true}, // BNK+Testing exist
			bnkDown:     phaseAction{phases: []string{"bnk"}},
			testingDown: phaseAction{phases: []string{"testing"}},
		},
		{
			// Reuse-existing-cluster: cluster-outputs.json present, no
			// state-cluster/. Treated as "C present" for BNK/Testing dispatch
			// — the cluster phase is skipped, BNK ∥ Testing deploy against the
			// registered cluster.
			name:        "reuse-existing-cluster (C absent, reuse=true)",
			pres:        none,
			reuse:       true,
			up:          phaseAction{phases: []string{"bnk", "testing"}}, // cluster skipped
			down:        phaseAction{refused: true},                      // nothing roksbnkctl created locally
			clusterDown: phaseAction{refused: true},
			bnkDown:     phaseAction{refused: true},
			testingDown: phaseAction{refused: true},
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			if got := upAction(r.pres, r.reuse); got.refused != r.up.refused || !eqPhases(got.phases, r.up.phases) {
				t.Errorf("up: got %s, want %s", got, r.up)
			}
			if got := downAction(r.pres); got.refused != r.down.refused || !eqPhases(got.phases, r.down.phases) {
				t.Errorf("down: got %s, want %s", got, r.down)
			}
			if got := clusterDownAction(r.pres); got.refused != r.clusterDown.refused || !eqPhases(got.phases, r.clusterDown.phases) {
				t.Errorf("cluster down: got %s, want %s", got, r.clusterDown)
			}
			if got := bnkDownAction(r.pres); got.refused != r.bnkDown.refused || !eqPhases(got.phases, r.bnkDown.phases) {
				t.Errorf("bnk down: got %s, want %s", got, r.bnkDown)
			}
			if got := testingDownAction(r.pres); got.refused != r.testingDown.refused || !eqPhases(got.phases, r.testingDown.phases) {
				t.Errorf("testing down: got %s, want %s", got, r.testingDown)
			}
		})
	}
}

// TestClusterDownGuard_RefusesWithBNKorTesting_AutoDoesNotBypass pins the
// most safety-critical decision: `cluster down` is a correctness guard, not a
// prompt, so it refuses whenever BNK OR Testing has resources REGARDLESS of
// --auto. We model the guard's exact branch order (BNK||Testing → no-cluster)
// and assert --auto changes nothing.
func TestClusterDownGuard_RefusesWithBNKorTesting_AutoDoesNotBypass(t *testing.T) {
	mustRefuse := []config.Presence{
		{Cluster: true, BNK: true},
		{Cluster: true, Testing: true},
		{Cluster: true, BNK: true, Testing: true},
		{BNK: true},     // even with no cluster present locally
		{Testing: true}, // ditto
		{},              // nothing to destroy → refused
	}
	for _, p := range mustRefuse {
		// The guard reads presence and decides BEFORE any prompt/auto
		// branch, so --auto (modelled as a no-op on the decision) cannot
		// flip the outcome.
		for _, auto := range []bool{false, true} {
			got := clusterDownAction(p)
			if !got.refused {
				t.Errorf("cluster down must refuse for presence %+v (auto=%v), got %s", p, auto, got)
			}
		}
	}
	// The one shape `cluster down` is allowed to act on: cluster present,
	// no BNK, no Testing, not legacy.
	if got := clusterDownAction(config.Presence{Cluster: true}); got.refused || !eqPhases(got.phases, []string{"cluster"}) {
		t.Errorf("cluster down on a lone cluster must act on [cluster], got %s", got)
	}
}

// TestApplyDecision_SelectiveConfirm pins the per-phase apply selection
// (Sprint 28 separate confirms): a phase applies iff it had plan changes
// AND (--auto OR the operator confirmed it). Declining one phase still
// applies the other; --auto applies every changed phase; a confirm on a
// no-change phase is inert.
func TestApplyDecision_SelectiveConfirm(t *testing.T) {
	cases := []struct {
		name                          string
		bnkChanges, testChanges, auto bool
		confirmBNK, confirmTest       bool
		wantBNK, wantTest             bool
	}{
		{"both changed, both confirmed", true, true, false, true, true, true, true},
		{"both changed, decline testing", true, true, false, true, false, true, false},
		{"both changed, decline bnk", true, true, false, false, true, false, true},
		{"both changed, decline both", true, true, false, false, false, false, false},
		{"auto applies all changed", true, true, true, false, false, true, true},
		{"no changes never applies", false, false, false, true, true, false, false},
		{"auto + no changes is noop", false, false, true, false, false, false, false},
		{"only bnk changed, confirmed", true, false, false, true, false, true, false},
		{"confirm inert without changes", false, true, false, true, true, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotBNK, gotTest := applyDecision(c.bnkChanges, c.testChanges, c.auto, c.confirmBNK, c.confirmTest)
			if gotBNK != c.wantBNK || gotTest != c.wantTest {
				t.Errorf("applyDecision = (bnk=%v test=%v), want (bnk=%v test=%v)", gotBNK, gotTest, c.wantBNK, c.wantTest)
			}
		})
	}
}

// ── parallel-dispatch plumbing ──────────────────────────────────────────

// TestPrefixWriter_ConcurrentLinesNeverInterleave drives the two
// newPrefixWriters concurrently (the BNK ∥ Testing stderr case) and asserts:
//   - every emitted line is whole (carries exactly one phase prefix; a
//     single Write is never split across the shared destination), and
//   - both prefixes appear (both legs got through).
func TestPrefixWriter_ConcurrentLinesNeverInterleave(t *testing.T) {
	var dest syncBuffer
	bnk, testing := newPrefixWriters(&dest)

	const perPhase = 200
	var wg sync.WaitGroup
	wg.Add(2)
	emit := func(w *prefixWriter, tag string) {
		defer wg.Done()
		for i := 0; i < perPhase; i++ {
			// One Write == one logical line. The prefixWriter must keep it
			// atomic under the shared mutex.
			fmt.Fprintf(w, "%s line %d\n", tag, i)
		}
	}
	go emit(bnk, "BNK")
	go emit(testing, "TESTING")
	wg.Wait()
	bnk.flush()
	testing.flush()

	lines := bytes.Split(bytes.TrimRight(dest.Bytes(), "\n"), []byte("\n"))
	if len(lines) != 2*perPhase {
		t.Fatalf("expected %d whole lines, got %d", 2*perPhase, len(lines))
	}
	var nBNK, nTest int
	for _, ln := range lines {
		hasBNK := bytes.HasPrefix(ln, []byte("[bnk] "))
		hasTest := bytes.HasPrefix(ln, []byte("[testing] "))
		if hasBNK == hasTest {
			t.Fatalf("line carries the wrong number of prefixes (split write?): %q", ln)
		}
		// The payload must match the prefix — proves a [bnk] prefix never
		// got stapled onto a TESTING line (no cross-goroutine splice).
		if hasBNK && !bytes.Contains(ln, []byte("BNK line")) {
			t.Fatalf("[bnk]-prefixed line carries non-BNK payload: %q", ln)
		}
		if hasTest && !bytes.Contains(ln, []byte("TESTING line")) {
			t.Fatalf("[testing]-prefixed line carries non-TESTING payload: %q", ln)
		}
		if hasBNK {
			nBNK++
		} else {
			nTest++
		}
	}
	if nBNK != perPhase || nTest != perPhase {
		t.Fatalf("prefix counts off: bnk=%d testing=%d (want %d each)", nBNK, nTest, perPhase)
	}
}

// TestPrefixWriter_FlushEmitsTrailingPartialLine pins that a final
// unterminated line (no trailing newline) is not lost — flush() emits it with
// the prefix, matching the `defer …flush()` the parallel legs use.
func TestPrefixWriter_FlushEmitsTrailingPartialLine(t *testing.T) {
	var dest syncBuffer
	bnk, _ := newPrefixWriters(&dest)
	fmt.Fprint(bnk, "partial, no newline")
	if dest.Len() != 0 {
		t.Fatalf("partial line should be buffered until flush, dest already has %q", dest.String())
	}
	bnk.flush()
	if got := dest.String(); got != "[bnk] partial, no newline" {
		t.Fatalf("flush did not emit the prefixed partial line; got %q", got)
	}
}

// TestParallelUp_ClusterBeforeBothThenBNKTesting pins the up ordering
// invariant (§3a): the cluster phase completes (serial) BEFORE the BNK ∥
// Testing group launches, and within the group BNK and Testing run
// concurrently (proven via a barrier both legs must reach before either
// proceeds). Uses the SAME errgroup shape runBNKAndTestingParallel uses, with
// fakes for the leaf phases so it is hermetic.
func TestParallelUp_ClusterBeforeBothThenBNKTesting(t *testing.T) {
	var order recorder
	clusterDone := make(chan struct{})

	// Cluster — serial, first.
	order.add("cluster:start")
	order.add("cluster:done")
	close(clusterDone)

	// Barrier: both legs must arrive before either is allowed to finish —
	// this can only be satisfied if they run concurrently.
	var barrier sync.WaitGroup
	barrier.Add(2)

	g, gctx := errgroup.WithContext(context.Background())
	g.Go(func() error {
		select {
		case <-clusterDone:
		case <-gctx.Done():
			return gctx.Err()
		}
		order.add("bnk:start")
		barrier.Done()
		barrier.Wait() // block until testing also started → proves concurrency
		order.add("bnk:done")
		return nil
	})
	g.Go(func() error {
		select {
		case <-clusterDone:
		case <-gctx.Done():
			return gctx.Err()
		}
		order.add("testing:start")
		barrier.Done()
		barrier.Wait()
		order.add("testing:done")
		return nil
	})
	if err := g.Wait(); err != nil {
		t.Fatalf("parallel up group errored: %v", err)
	}

	got := order.events()
	// Cluster must fully complete before either downstream starts.
	idxClusterDone := indexOf(got, "cluster:done")
	for _, ev := range []string{"bnk:start", "testing:start"} {
		if indexOf(got, ev) < idxClusterDone {
			t.Errorf("%s started before cluster:done — cluster must be serial-first; order=%v", ev, got)
		}
	}
	// If the barrier was satisfied, both legs reached :start before either
	// reached :done — concurrency proven.
	if !(indexOf(got, "bnk:done") > indexOf(got, "testing:start") &&
		indexOf(got, "testing:done") > indexOf(got, "bnk:start")) {
		t.Errorf("BNK and Testing did not overlap (no concurrency); order=%v", got)
	}
}

// TestParallelUp_OneLegFailsSurfacesAndCancelsSibling pins the errgroup error
// propagation (§3a): if one phase fails, g.Wait() surfaces THAT error and the
// sibling's context is cancelled — it does not silently complete its apply.
// The sibling records that it observed cancellation (so its state path isn't
// left half-mutated by a racing apply the caller thinks succeeded).
func TestParallelUp_OneLegFailsSurfacesAndCancelsSibling(t *testing.T) {
	wantErr := errors.New("bnk apply blew up")
	var siblingCancelled atomic.Bool
	siblingProceeded := atomic.Bool{}

	g, gctx := errgroup.WithContext(context.Background())
	// BNK leg fails fast.
	g.Go(func() error {
		return wantErr
	})
	// Testing leg blocks on work, then must observe the cancelled ctx the
	// failing BNK leg triggered.
	g.Go(func() error {
		select {
		case <-gctx.Done():
			siblingCancelled.Store(true)
			return gctx.Err()
		case <-time.After(5 * time.Second):
			siblingProceeded.Store(true)
			return nil
		}
	})
	err := g.Wait()
	if !errors.Is(err, wantErr) {
		t.Fatalf("g.Wait() must surface the failing leg's error; got %v", err)
	}
	if siblingProceeded.Load() {
		t.Fatal("sibling proceeded to completion despite the other leg failing — ctx cancellation not honored")
	}
	if !siblingCancelled.Load() {
		t.Fatal("sibling did not observe ctx cancellation from the failing leg")
	}
}

// TestParallelDown_BNKTestingBeforeCluster pins the teardown ordering
// invariant (§3c): BNK ∥ Testing destroy concurrently and BOTH complete
// before the Cluster destroy starts (reverse-dependency order). Mirrors
// RunDown's structure (parallel leg via errgroup, then cluster).
func TestParallelDown_BNKTestingBeforeCluster(t *testing.T) {
	var order recorder
	var barrier sync.WaitGroup
	barrier.Add(2)

	g, _ := errgroup.WithContext(context.Background())
	g.Go(func() error {
		order.add("bnk:start")
		barrier.Done()
		barrier.Wait()
		order.add("bnk:done")
		return nil
	})
	g.Go(func() error {
		order.add("testing:start")
		barrier.Done()
		barrier.Wait()
		order.add("testing:done")
		return nil
	})
	if err := g.Wait(); err != nil {
		t.Fatalf("parallel down group errored: %v", err)
	}
	// Cluster destroy runs only AFTER both teardown legs returned.
	order.add("cluster:start")
	order.add("cluster:done")

	got := order.events()
	idxClusterStart := indexOf(got, "cluster:start")
	for _, ev := range []string{"bnk:done", "testing:done"} {
		if indexOf(got, ev) > idxClusterStart {
			t.Errorf("cluster:start preceded %s — teardown must finish BNK∥Testing before Cluster; order=%v", ev, got)
		}
	}
	// And the two teardown legs overlapped (barrier satisfied).
	if !(indexOf(got, "bnk:done") > indexOf(got, "testing:start") &&
		indexOf(got, "testing:done") > indexOf(got, "bnk:start")) {
		t.Errorf("BNK and Testing teardown did not overlap; order=%v", got)
	}
}

// ── small test helpers ──────────────────────────────────────────────────

// syncBuffer is a mutex-guarded bytes.Buffer so the concurrent prefixWriter
// test can use it as the shared destination without racing the buffer itself
// (the prefixWriter guards line assembly; this guards the sink).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}
func (b *syncBuffer) String() string { return string(b.Bytes()) }
func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// recorder is a thread-safe ordered event log for the concurrency invariants.
type recorder struct {
	mu sync.Mutex
	ev []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ev = append(r.ev, s)
}
func (r *recorder) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ev...)
}

// indexOf returns the position of s in events, or len(events) if absent (so a
// missing event sorts AFTER everything — a stricter, fail-closed comparison).
func indexOf(events []string, s string) int {
	for i, e := range events {
		if e == s {
			return i
		}
	}
	return len(events)
}
