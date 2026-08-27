package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// #271. On BNK 2.4.0-EA the CWC can enter "Device Registration In Progress" and
// never leave it: after obtaining its certificate it answers the SPK controllers
// ResponseCM20GetBackLater every 5s and never POSTs /entitlements/telemetry, so
// F5 TEEM is never asked. Observed to persist 25+ minutes and to survive a fresh
// CWC pod with no inherited state.
//
// The cost of not detecting it is THIRTY minutes, not fifteen: the licence wait
// burns its full 15, then the CNEInstance wait burns another 15 because the TMMs
// are blocked on the same licence — and the operator is finally told "the status
// of pod readiness gate ConfigurationDone is not True", which names neither the
// licence nor the CWC.
func TestAStalledLicenceRegistrationIsCalledStuck(t *testing.T) {
	why, stuck := stalledWaitDiagnosis(
		"k8s.f5net.com/v1/licenses",
		`status.state="Registering"`,
		licenseRegistrationStall+time.Second,
	)
	if !stuck {
		t.Fatal("a licence unchanged in Registering past the stall window was not called stuck")
	}
	for _, want := range []string{
		"entitlements/telemetry", // names the missing call, which is the actual signal
		"licensestatus",          // the recovery is spelled out, not alluded to
		"rollout restart",
		"BNK defect", // says whose bug it is
	} {
		if !strings.Contains(why, want) {
			t.Errorf("the message does not mention %q — an operator reading only this line has to\n"+
				"know what to do next:\n%s", want, why)
		}
	}
}

// Slow is not stuck. A registration that has not yet reached the stall window
// must be left alone, or the fix trades a 30-minute wrong answer for a fast one.
func TestALicenceStillWithinTheStallWindowIsLeftAlone(t *testing.T) {
	if _, stuck := stalledWaitDiagnosis(
		"k8s.f5net.com/v1/licenses",
		`status.state="Registering"`,
		licenseRegistrationStall-time.Second,
	); stuck {
		t.Error("a licence registering for less than the stall window was called stuck; " +
			"a healthy registration takes about a minute, and failing early here would turn a " +
			"slow success into a fast failure")
	}
}

// The detection is keyed on the licence, not on duration alone. Any other
// resource may legitimately sit unchanged for minutes — an image pull, a node
// coming up, a slow finalizer — and failing those early is exactly the wrong
// trade.
func TestOnlyALicenceWaitIsSubjectToTheStallCheck(t *testing.T) {
	for _, gvr := range []string{
		"k8s.f5.com/v1/cneinstances",
		"apps/v1/deployments",
		"k8s.f5net.com/v1/f5tmms",
	} {
		if _, stuck := stalledWaitDiagnosis(gvr, `status.state="Registering"`, time.Hour); stuck {
			t.Errorf("%s was called stuck. Only the licence has a known unresumable state; every "+
				"other resource may still be making progress that a GET cannot see.", gvr)
		}
	}
}

// A licence in some OTHER state is not this defect. Only the registering states
// are known to be unresumable; anything else may still resolve.
func TestALicenceInAnotherStateIsNotCalledStuck(t *testing.T) {
	for _, desc := range []string{
		`status.state="Active"`,
		`status.state="Pending"`,
		`status.state="Expired"`,
		`status.state=""`,
	} {
		if _, stuck := stalledWaitDiagnosis("k8s.f5net.com/v1/licenses", desc, time.Hour); stuck {
			t.Errorf("a licence at %s was called stuck; only the registering states are known "+
				"to be unresumable", desc)
		}
	}
}

// The predicate being right is not enough — the POLL LOOP has to reach it.
//
// The loop tracks when the observed state last changed, and a bug in that
// tracking (resetting it every iteration, say) would leave the predicate correct
// and never satisfied. This drives the real loop against a licence that never
// leaves Registering, with the stall window shrunk so the test is fast.
func TestThePollLoopFailsFastOnAStalledLicence(t *testing.T) {
	restore := licenseStallForTest(30 * time.Millisecond)
	defer restore()

	restoreGVR := flagWaitGVR
	flagWaitGVR = "k8s.f5net.com/v1/licenses"
	defer func() { flagWaitGVR = restoreGVR }()

	dc := fakeDynFor(cneObject("", "Registering"))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m, merr := parseWaitFor("jsonpath=status.state=Active")
	if merr != nil {
		t.Fatalf("parseWaitFor: %v", merr)
	}

	var buf strings.Builder
	start := time.Now()
	// A 10s budget: if the stall check does not fire, this runs the full timeout,
	// which is the 15-minute failure in miniature.
	err := runTFXWaitPoll(context.Background(), ri, "bnk", m,
		10*time.Second, 5*time.Millisecond, &buf)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a licence stuck in Registering should fail the wait, not satisfy it")
	}
	if !strings.Contains(err.Error(), "is stuck") {
		t.Errorf("the error does not identify this as stuck rather than merely timed out: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %s to give up on a stalled licence; the whole point is not to spend the "+
			"budget on a state that cannot change", elapsed)
	}
}

// The counterpart: a licence that DOES reach Active must still satisfy the wait,
// even if it takes longer than the stall window to get there. The stall check
// must key on the state being unchanged, not on elapsed time alone.
func TestALicenceThatEventuallyGoesActiveStillSatisfiesTheWait(t *testing.T) {
	restore := licenseStallForTest(30 * time.Millisecond)
	defer restore()

	restoreGVR := flagWaitGVR
	flagWaitGVR = "k8s.f5net.com/v1/licenses"
	defer func() { flagWaitGVR = restoreGVR }()

	dc := fakeDynFor(cneObject("", "Registering"))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m, merr := parseWaitFor("jsonpath=status.state=Active")
	if merr != nil {
		t.Fatalf("parseWaitFor: %v", merr)
	}

	// Move it through a DIFFERENT state before Active, so the "unchanged" clock
	// resets and the stall check does not fire on a licence that is progressing.
	go func() {
		time.Sleep(20 * time.Millisecond)
		obj := cneObject("", "Activating")
		_, _ = ri.Update(context.Background(), obj, metav1.UpdateOptions{})
		time.Sleep(20 * time.Millisecond)
		active := cneObject("", "Active")
		_, _ = ri.Update(context.Background(), active, metav1.UpdateOptions{})
	}()

	if err := runTFXWaitPoll(context.Background(), ri, "bnk", m,
		5*time.Second, 5*time.Millisecond, io.Discard); err != nil {
		t.Errorf("a licence that progressed to Active was failed as stuck: %v\n"+
			"The check must key on the state being UNCHANGED, not on elapsed time.", err)
	}
}

// licenseStallForTest shrinks the stall window so a test can exercise the real
// loop in milliseconds, and restores it afterwards.
func licenseStallForTest(d time.Duration) func() {
	prev := licenseRegistrationStall
	licenseRegistrationStall = d
	return func() { licenseRegistrationStall = prev }
}

// THE PATH PRODUCTION ACTUALLY TAKES.
//
// `tfx wait` defaults to --mode watch and terraform does not override it, so the
// licence wait runs on the watch path. A stall check added only to the poll loop
// would pass every test above and never fire in a real `bnk up` — which is the
// shape of defect this project keeps finding: correct code on a path nothing
// calls.
//
// A watch cannot notice "nothing happened" by itself: a state that never changes
// produces no events, and sitting silently is exactly what a watch is built to
// do. The watchdog is what turns that silence into a verdict.
func TestTheWatchPathFailsFastOnAStalledLicence(t *testing.T) {
	restore := licenseStallForTest(40 * time.Millisecond)
	defer restore()
	restoreGVR := flagWaitGVR
	flagWaitGVR = "k8s.f5net.com/v1/licenses"
	defer func() { flagWaitGVR = restoreGVR }()

	dc := fakeDynFor(cneObject("", "Registering"))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m, err := parseWaitFor("jsonpath=status.state=Active")
	if err != nil {
		t.Fatalf("parseWaitFor: %v", err)
	}

	var buf strings.Builder
	start := time.Now()
	// 10s budget. Without the watchdog the watch sits until it expires, which is
	// the 15-minute production failure in miniature.
	werr := runTFXWaitWatch(context.Background(), ri, "bnk", m, 10*time.Second, &buf)
	elapsed := time.Since(start)

	if werr == nil {
		t.Fatal("a licence stuck in Registering should not satisfy the watch")
	}
	if !strings.Contains(werr.Error(), "is stuck") {
		t.Errorf("the watch reported %q rather than identifying the stall.\n"+
			"A plain timeout here is the 15-minute failure: it names neither the licence state "+
			"nor what to do about it.", werr)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the watch took %s to give up; the watchdog exists so it does not spend the "+
			"budget on a state that cannot change", elapsed)
	}
}

// The counterpart on the watch path: a licence that reaches Active must still
// satisfy the watch. The watchdog must key on the state being UNCHANGED, or it
// would abandon every slow-but-progressing registration.
func TestTheWatchPathStillSucceedsWhenTheLicenceGoesActive(t *testing.T) {
	restore := licenseStallForTest(2 * time.Second)
	defer restore()
	restoreGVR := flagWaitGVR
	flagWaitGVR = "k8s.f5net.com/v1/licenses"
	defer func() { flagWaitGVR = restoreGVR }()

	dc := fakeDynFor(cneObject("", "Active"))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m, err := parseWaitFor("jsonpath=status.state=Active")
	if err != nil {
		t.Fatalf("parseWaitFor: %v", err)
	}

	if werr := runTFXWaitWatch(context.Background(), ri, "bnk", m, 3*time.Second, io.Discard); werr != nil {
		t.Errorf("an Active licence was not accepted by the watch: %v", werr)
	}
}

// A non-licence watch must never be cut short by the watchdog, however long it
// sits. Every other resource may still be making progress a GET cannot see.
func TestTheWatchdogDoesNotTouchNonLicenceWaits(t *testing.T) {
	restore := licenseStallForTest(20 * time.Millisecond)
	defer restore()
	restoreGVR := flagWaitGVR
	flagWaitGVR = "k8s.f5.com/v1/cneinstances"
	defer func() { flagWaitGVR = restoreGVR }()

	dc := fakeDynFor(cneObject("False", ""))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m, err := parseWaitFor("condition=CNEControllerAvailable=True")
	if err != nil {
		t.Fatalf("parseWaitFor: %v", err)
	}

	werr := runTFXWaitWatch(context.Background(), ri, "bnk", m, 300*time.Millisecond, io.Discard)
	if werr != nil && strings.Contains(werr.Error(), "is stuck") {
		t.Error("a CNEInstance watch was cut short as stuck. Only the licence has a known " +
			"unresumable state; ending other waits early trades a slow success for a fast " +
			"wrong answer.")
	}
}

// THE SAFETY PROPERTY: the watchdog's clock must RESTART whenever the state
// changes.
//
// A registration that moves Registering -> Activating -> Active is progressing,
// however slowly, and cutting it off would turn a slow success into a fast
// failure — the exact trade every diagnosis in this file is written to avoid.
//
// Without a reset, the watchdog fires purely on elapsed time and abandons a
// licence that was working. That mutation survived the first round of tests here:
// the poll-path progression test does not exercise the watchdog at all, and the
// watch-path success test starts from an already-Active object, so neither
// observes a state TRANSITION.
func TestTheWatchdogClockRestartsWhenTheLicenceStateChanges(t *testing.T) {
	restore := licenseStallForTest(60 * time.Millisecond)
	defer restore()
	restoreGVR := flagWaitGVR
	flagWaitGVR = "k8s.f5net.com/v1/licenses"
	defer func() { flagWaitGVR = restoreGVR }()

	dc := fakeDynFor(cneObject("", "Registering"))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m, err := parseWaitFor("jsonpath=status.state=Active")
	if err != nil {
		t.Fatalf("parseWaitFor: %v", err)
	}

	// Both intermediate states are still REGISTERING states, so the stall
	// predicate matches throughout and the ONLY thing preventing a fire is the
	// clock restarting on each change. An intermediate state outside the
	// registering set would make the test pass for the wrong reason — the
	// predicate would simply stop matching, and the mutation would survive. It
	// did, on the first attempt at this test.
	go func() {
		time.Sleep(35 * time.Millisecond)
		_, _ = ri.Update(context.Background(), cneObject("", "Registering-CPCL"), metav1.UpdateOptions{})
		time.Sleep(35 * time.Millisecond)
		_, _ = ri.Update(context.Background(), cneObject("", "Active"), metav1.UpdateOptions{})
	}()

	var buf strings.Builder
	werr := runTFXWaitWatch(context.Background(), ri, "bnk", m, 5*time.Second, &buf)
	if werr != nil {
		t.Errorf("a licence progressing Registering -> Registering-CPCL -> Active was failed: %v\n"+
			"The watchdog must restart its clock on every state CHANGE, not fire on elapsed "+
			"time alone — otherwise it abandons registrations that were working.\noutput:\n%s",
			werr, buf.String())
	}
}
