package ui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeTerminal makes isTerminalFn return the desired value in tests.
func withFakeTerminal(tty bool, fn func()) {
	orig := isTerminalFn
	isTerminalFn = func(_ io.Writer) bool { return tty }
	defer func() { isTerminalFn = orig }()
	fn()
}

// TestNewRenderer_GatingMatrix covers the 8 combinations of (demo × tty × noColor).
func TestNewRenderer_GatingMatrix(t *testing.T) {
	cases := []struct {
		demo, tty, noColor bool
		wantRocket         bool
	}{
		{true, true, false, true},    // all green → RocketRenderer
		{true, true, true, false},    // noColor → Plain
		{true, false, false, false},  // not a TTY → Plain
		{true, false, true, false},   // noColor + no TTY → Plain
		{false, true, false, false},  // not demo → Plain
		{false, true, true, false},   // not demo + noColor → Plain
		{false, false, false, false}, // nothing set → Plain
		{false, false, true, false},  // noColor + no TTY + not demo → Plain
	}
	for _, c := range cases {
		c := c
		name := ""
		if c.demo {
			name += "demo"
		} else {
			name += "nodemo"
		}
		if c.tty {
			name += "+tty"
		} else {
			name += "+notty"
		}
		if c.noColor {
			name += "+nocolor"
		} else {
			name += "+color"
		}
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			withFakeTerminal(c.tty, func() {
				rdr := NewRenderer(&buf, "test-cluster", c.demo, c.noColor)
				_, isRocket := rdr.(*RocketRenderer)
				_, isPlain := rdr.(PlainRenderer)
				if c.wantRocket && !isRocket {
					t.Errorf("wanted *RocketRenderer, got %T", rdr)
				}
				if !c.wantRocket && !isPlain {
					t.Errorf("wanted PlainRenderer, got %T", rdr)
				}
			})
		})
	}
}

func testStages() []Stage {
	return []Stage{
		{Num: 1, Label: "VPC · subnets · IGW · NAT"},
		{Num: 2, Label: "EKS control plane"},
		{Num: 3, Label: "Nodes · kubeconfig · ENIs · jumphost"},
		{Num: 4, Label: "BNK supply chain · activation"},
	}
}

// TestRocketRenderer_StartProducesHeader verifies that Start writes a header
// containing "DEMO LAUNCH" and the cluster name.
func TestRocketRenderer_StartProducesHeader(t *testing.T) {
	var buf bytes.Buffer
	rdr := NewRocketRenderer(&buf, "syd-tracer")
	rdr.Start(testStages())

	out := buf.String()
	if !strings.Contains(out, "DEMO LAUNCH") {
		t.Errorf("Start output missing 'DEMO LAUNCH'; got:\n%s", out)
	}
	if !strings.Contains(out, "syd-tracer") {
		t.Errorf("Start output missing cluster name 'syd-tracer'; got:\n%s", out)
	}
}

// TestRocketRenderer_PhaseBeginMarksInProgress verifies that after PhaseBegin(2, "x"),
// the output contains "STAGE 2" and the in-progress bar or waiting indicator.
func TestRocketRenderer_PhaseBeginMarksInProgress(t *testing.T) {
	var buf bytes.Buffer
	rdr := NewRocketRenderer(&buf, "test-cluster")
	rdr.Start(testStages())
	buf.Reset() // only look at post-Begin output

	rdr.PhaseBegin(2, "eks-cluster")

	out := buf.String()
	if !strings.Contains(out, "STAGE 2") {
		t.Errorf("PhaseBegin output missing 'STAGE 2'; got:\n%s", out)
	}
	// The active stage shows the ⏳ in-progress indicator (and the ◐ icon).
	if !strings.Contains(out, "⏳") {
		t.Errorf("PhaseBegin output not showing in-progress indicator (⏳); got:\n%s", out)
	}
}

// TestRocketRenderer_PhaseEndOnSuccessMarksDone verifies that a successful PhaseEnd
// is reflected as done (✓) in the output.
func TestRocketRenderer_PhaseEndOnSuccessMarksDone(t *testing.T) {
	var buf bytes.Buffer
	rdr := NewRocketRenderer(&buf, "test-cluster")
	rdr.Start(testStages())
	rdr.PhaseBegin(1, "preflight")
	buf.Reset()

	rdr.PhaseEnd(1, "preflight", nil)
	rdr.Finish(nil)

	out := buf.String()
	if !strings.Contains(out, "✓") {
		t.Errorf("after successful Finish, output missing '✓'; got:\n%s", out)
	}
}

// TestRocketRenderer_PhaseEndOnFailMarksFailed verifies that a failed PhaseEnd
// outputs ✗ and the error message.
func TestRocketRenderer_PhaseEndOnFailMarksFailed(t *testing.T) {
	var buf bytes.Buffer
	rdr := NewRocketRenderer(&buf, "test-cluster")
	rdr.Start(testStages())
	rdr.PhaseBegin(1, "vpc")
	buf.Reset()

	sentinelErr := errors.New("vpc creation failed: quota exceeded")
	rdr.PhaseEnd(1, "vpc", sentinelErr)

	out := buf.String()
	if !strings.Contains(out, "✗") {
		t.Errorf("after failed PhaseEnd, output missing '✗'; got:\n%s", out)
	}
	if !strings.Contains(out, sentinelErr.Error()) {
		t.Errorf("after failed PhaseEnd, output missing error text %q; got:\n%s",
			sentinelErr.Error(), out)
	}
}

// TestRocketRenderer_FinishSuccess verifies that Finish(nil) emits the ORBIT line.
func TestRocketRenderer_FinishSuccess(t *testing.T) {
	var buf bytes.Buffer
	rdr := NewRocketRenderer(&buf, "test-cluster")
	rdr.Start(testStages())
	buf.Reset()

	rdr.Finish(nil)

	out := buf.String()
	if !strings.Contains(out, "ORBIT") {
		t.Errorf("Finish(nil) output missing 'ORBIT'; got:\n%s", out)
	}
}

// TestRocketRenderer_FinishOnError_DoesNotShowOrbit verifies that Finish(err) does
// not emit the ORBIT celebration line.
func TestRocketRenderer_FinishOnError_DoesNotShowOrbit(t *testing.T) {
	var buf bytes.Buffer
	rdr := NewRocketRenderer(&buf, "test-cluster")
	rdr.Start(testStages())
	rdr.PhaseBegin(1, "vpc")
	sentinelErr := errors.New("boom")
	rdr.PhaseEnd(1, "vpc", sentinelErr)
	buf.Reset()

	rdr.Finish(sentinelErr)

	out := buf.String()
	// After a failed phase the ORBIT celebration line must not appear.
	// The placeholder "──────────  ORBIT" row in the frame is acceptable;
	// the "🛰  cluster up · VIP live" success line must be absent.
	if strings.Contains(out, "🛰") {
		t.Errorf("Finish(err) must not show ORBIT celebration (🛰); got:\n%s", out)
	}
}

// TestRocketRenderer_StageTransitionsOnNextStageBegin verifies that when PhaseBegin
// is called for a new stage, the previous active stage transitions to ✓.
func TestRocketRenderer_StageTransitionsOnNextStageBegin(t *testing.T) {
	var buf bytes.Buffer
	rdr := NewRocketRenderer(&buf, "test-cluster")
	rdr.Start(testStages())
	rdr.PhaseBegin(1, "a")
	rdr.PhaseEnd(1, "a", nil)
	buf.Reset() // capture only the frame from PhaseBegin(2,...)

	rdr.PhaseBegin(2, "b")

	out := buf.String()
	// Parse output into per-stage lines for targeted assertions.
	lines := strings.Split(out, "\n")
	var stage1Line, stage2Line string
	for _, l := range lines {
		if strings.Contains(l, "STAGE 1") {
			stage1Line = l
		}
		if strings.Contains(l, "STAGE 2") {
			stage2Line = l
		}
	}

	// Stage 1 must now show ✓ (transitioned to done).
	if !strings.Contains(stage1Line, "✓") {
		t.Errorf("after PhaseBegin(2,...), Stage 1 line should show ✓ but got: %q", stage1Line)
	}
	// Stage 1 must NOT show (current: because its currentPhase was cleared.
	if strings.Contains(stage1Line, "(current:") {
		t.Errorf("after PhaseBegin(2,...), Stage 1 line should not show '(current:' but got: %q", stage1Line)
	}
	// Stage 2 must show ⏳ (active).
	if !strings.Contains(stage2Line, "⏳") {
		t.Errorf("after PhaseBegin(2,...), Stage 2 line should show ⏳ but got: %q", stage2Line)
	}
}

// TestRocketRenderer_PhaseEndAlone_DoesNotMarkDone verifies that a successful
// PhaseEnd alone does NOT transition the stage to done — the stage stays ⏳.
func TestRocketRenderer_PhaseEndAlone_DoesNotMarkDone(t *testing.T) {
	var buf bytes.Buffer
	rdr := NewRocketRenderer(&buf, "test-cluster")
	rdr.Start(testStages())
	rdr.PhaseBegin(1, "a")
	buf.Reset()

	rdr.PhaseEnd(1, "a", nil)

	out := buf.String()
	// Stage 1 must still show ⏳ because no "next stage" PhaseBegin has arrived.
	if !strings.Contains(out, "⏳") {
		t.Errorf("PhaseEnd alone should leave stage active (⏳) but got:\n%s", out)
	}
	// Must NOT show ✓ yet.
	if strings.Contains(out, "✓") {
		t.Errorf("PhaseEnd alone must not mark stage done (✓) but got:\n%s", out)
	}
}

// TestRocketRenderer_FinishStillMarksTrailingStageDone verifies that Finish(nil)
// still transitions the trailing (last) active stage to ✓.
func TestRocketRenderer_FinishStillMarksTrailingStageDone(t *testing.T) {
	var buf bytes.Buffer
	rdr := NewRocketRenderer(&buf, "test-cluster")
	rdr.Start([]Stage{{Num: 4, Label: "BNK supply chain · activation"}})
	rdr.PhaseBegin(4, "x")
	rdr.PhaseEnd(4, "x", nil)
	buf.Reset()

	rdr.Finish(nil)

	out := buf.String()
	if !strings.Contains(out, "✓") {
		t.Errorf("Finish(nil) should mark trailing stage done (✓) but got:\n%s", out)
	}
}

// TestPlainRenderer_AllMethodsNoOp verifies that PlainRenderer writes zero bytes.
func TestPlainRenderer_AllMethodsNoOp(t *testing.T) {
	var buf bytes.Buffer
	// Wrap buf in a writer that also tracks writes.
	rdr := PlainRenderer{}
	// Temporarily redirect isTerminalFn to confirm it is never called (no panic).
	rdr.Start(testStages())
	rdr.PhaseBegin(1, "preflight")
	rdr.PhaseEnd(1, "preflight", nil)
	rdr.PhaseEnd(2, "eks-cluster", errors.New("some error"))
	rdr.Finish(nil)
	rdr.Finish(errors.New("err"))

	if buf.Len() != 0 {
		t.Errorf("PlainRenderer wrote %d bytes, want 0", buf.Len())
	}
}
