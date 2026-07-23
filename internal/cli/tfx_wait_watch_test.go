package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The watch-first strategy over the fake dynamic client (which supports List +
// Watch): an already-satisfied object matches via the initial LIST/synthesized
// ADDED event, and a transition matches via the MODIFIED event.

func TestRunTFXWait_WatchAlreadyReady(t *testing.T) {
	dc := fakeDynFor(cneObject("True", ""))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m := conditionMatcher{typ: "CNEControllerAvailable", status: "True"}
	// mode=watch: the initial LIST must satisfy immediately, no fallback needed.
	if err := runTFXWait(context.Background(), ri, "bnk", m, 3*time.Second, 5*time.Millisecond, "watch", io.Discard); err != nil {
		t.Fatalf("watch on a ready object should succeed, got %v", err)
	}
}

func TestRunTFXWait_WatchBecomesReady(t *testing.T) {
	dc := fakeDynFor(cneObject("False", "")) // starts not-ready
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m := conditionMatcher{typ: "CNEControllerAvailable", status: "True"}

	go func() {
		time.Sleep(25 * time.Millisecond)
		_, _ = ri.Update(context.Background(), cneObject("True", ""), metav1.UpdateOptions{})
	}()

	if err := runTFXWait(context.Background(), ri, "bnk", m, 3*time.Second, 5*time.Millisecond, "watch", io.Discard); err != nil {
		t.Fatalf("watch should succeed once the object becomes ready (MODIFIED event), got %v", err)
	}
}

func TestRunTFXWait_WatchTimesOut(t *testing.T) {
	dc := fakeDynFor(cneObject("False", "")) // never becomes ready
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m := conditionMatcher{typ: "CNEControllerAvailable", status: "True"}
	err := runTFXWait(context.Background(), ri, "bnk", m, 60*time.Millisecond, 5*time.Millisecond, "watch", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("watch that never matches should report a timeout, got %v", err)
	}
}

func TestRunTFXWait_PollModeStillWorks(t *testing.T) {
	dc := fakeDynFor(cneObject("True", ""))
	ri := dc.Resource(cneGVR).Namespace("f5-bnk")
	m := conditionMatcher{typ: "CNEControllerAvailable", status: "True"}
	// mode=poll forces the GET loop path through the dispatcher.
	if err := runTFXWait(context.Background(), ri, "bnk", m, time.Second, 5*time.Millisecond, "poll", io.Discard); err != nil {
		t.Fatalf("poll mode on a ready object should succeed, got %v", err)
	}
}
