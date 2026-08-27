package orchestration

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// webhookFor builds a ValidatingWebhookConfiguration served from ns, which is
// what makes it a sweep target.
func webhookFor(name, ns string) *admissionregistrationv1.ValidatingWebhookConfiguration {
	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name: "f5validate.f5net.com",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{
					Name:      "f5-validation-svc",
					Namespace: ns,
				},
			},
		}},
	}
}

// THE DEFECT (#241). #208 removed the webhook once and #235 moved that single
// removal before the drain. Ten seconds later FLO -- which #217 deliberately
// keeps alive through the drain, because it is what finalizes the CRs -- puts
// the controller back, and the controller puts the webhook back. Every delete
// from then until its endpoint is ready is refused, which is the exact error
// both earlier fixes existed to eliminate.
//
// So the property under test is NOT "the webhook is removed". Both previous
// versions did that. It is "the webhook is removed AGAIN after something
// re-creates it", which only a sweep that runs more than once can satisfy.
func TestTheSweepRemovesAWebhookThatIsRecreatedMidTeardown(t *testing.T) {
	cs := fake.NewSimpleClientset(webhookFor("f5validate-f5-bnk", "f5-bnk"))

	var buf bytes.Buffer
	stop := runTeardownWebhookSweep(context.Background(), cs, "f5-bnk", 10*time.Millisecond, &buf)

	// The first sweep is synchronous, so by here it is already gone -- the
	// guarantee the drain depends on.
	if _, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().
		Get(context.Background(), "f5validate-f5-bnk", metav1.GetOptions{}); err == nil {
		stop()
		t.Fatal("the first sweep did not remove the webhook before returning; the drain would " +
			"issue its first delete against a live failurePolicy: Fail webhook")
	}

	// Now do what FLO does: put it back.
	if _, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().
		Create(context.Background(), webhookFor("f5validate-f5-bnk", "f5-bnk"), metav1.CreateOptions{}); err != nil {
		stop()
		t.Fatalf("re-creating the webhook: %v", err)
	}

	gone := waitForWebhookGone(t, cs, "f5validate-f5-bnk", 2*time.Second)
	stop()

	if !gone {
		t.Error("the re-created webhook was still present: the sweep ran once and stopped.\n" +
			"That is #241 -- FLO re-creates f5validate-<ns> about ten seconds into the drain, " +
			"and every delete is refused until it is removed again.")
	}
	if out := buf.String(); !strings.Contains(out, "re-created") {
		t.Errorf("a mid-teardown re-creation should be reported so a refused delete can be explained; got:\n%s", out)
	}
}

// The counterpart: nothing re-creates it, so the sweep must not invent a
// re-creation in its report. A sweep that always claims the webhook came back
// would make the #241 signature useless for diagnosing the next teardown.
func TestASweepWithNoRecreationDoesNotReportOne(t *testing.T) {
	cs := fake.NewSimpleClientset(webhookFor("f5validate-f5-bnk", "f5-bnk"))

	var buf bytes.Buffer
	stop := runTeardownWebhookSweep(context.Background(), cs, "f5-bnk", 10*time.Millisecond, &buf)
	time.Sleep(60 * time.Millisecond) // several ticks, all finding nothing
	stop()

	if out := buf.String(); strings.Contains(out, "re-created") {
		t.Errorf("no webhook was re-created, but the sweep reported one:\n%s", out)
	}
}

// The sweep must never touch a webhook served from a namespace that is not being
// destroyed. It runs on a LIVE cluster for the whole teardown, so a mis-scoped
// delete here disables admission control on something nobody asked to tear down
// -- a far worse bug than the stall being fixed.
func TestTheSweepLeavesOtherNamespacesWebhooksAlone(t *testing.T) {
	cs := fake.NewSimpleClientset(
		webhookFor("f5validate-f5-bnk", "f5-bnk"),
		webhookFor("f5validate-other", "other-ns"),
		webhookFor("unrelated-operator", "kube-system"),
	)

	stop := runTeardownWebhookSweep(context.Background(), cs, "f5-bnk", 10*time.Millisecond, nil)
	time.Sleep(50 * time.Millisecond)
	stop()

	for _, name := range []string{"f5validate-other", "unrelated-operator"} {
		if _, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().
			Get(context.Background(), name, metav1.GetOptions{}); err != nil {
			t.Errorf("the sweep deleted %s, which is served from another namespace: %v", name, err)
		}
	}
}

// stop() must actually stop it. A goroutine still deleting webhooks after the
// destroy has returned would outlive the command and act on a cluster the user
// has moved on from.
func TestStopEndsTheSweep(t *testing.T) {
	cs := fake.NewSimpleClientset()
	var mu sync.Mutex
	lists := 0
	cs.PrependReactor("list", "validatingwebhookconfigurations",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			mu.Lock()
			lists++
			mu.Unlock()
			return false, nil, nil
		})

	stop := runTeardownWebhookSweep(context.Background(), cs, "f5-bnk", 5*time.Millisecond, nil)
	time.Sleep(40 * time.Millisecond)
	stop()

	mu.Lock()
	atStop := lists
	mu.Unlock()

	time.Sleep(40 * time.Millisecond)

	mu.Lock()
	after := lists
	mu.Unlock()

	if after != atStop {
		t.Errorf("the sweep kept running after stop(): %d lists at stop, %d after", atStop, after)
	}
}

// Cancelling the parent context must also stop it, and stop() must still be safe
// to call afterwards -- it is on a defer, so it always runs.
func TestACancelledContextEndsTheSweepAndStopIsStillSafe(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(context.Background())

	stop := runTeardownWebhookSweep(ctx, cs, "f5-bnk", 5*time.Millisecond, nil)
	cancel()

	done := make(chan struct{})
	go func() { stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() did not return after the parent context was cancelled — the destroy would hang on its defer")
	}
}

// A workspace with no BNK namespace has nothing to sweep, and must not start a
// goroutine or panic on the returned stop func.
func TestSweepWithNoNamespaceIsANoOp(t *testing.T) {
	stop := startTeardownWebhookSweep(context.Background(), nil, nil, nil)
	stop() // must not panic
}

func waitForWebhookGone(t *testing.T, cs kubernetes.Interface, name string, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		_, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().
			Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// The sweep runs every 3s for as long as the destroy takes, which can be ten
// minutes. A persistent API error must therefore be reported ONCE: at one line
// per tick it would print two hundred copies of the same warning and bury the
// rest of the teardown output, which is how a real failure becomes invisible.
func TestAPersistentSweepErrorIsReportedOnce(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "validatingwebhookconfigurations",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("the API server is unreachable")
		})

	var buf bytes.Buffer
	stop := runTeardownWebhookSweep(context.Background(), cs, "f5-bnk", 5*time.Millisecond, &buf)
	time.Sleep(60 * time.Millisecond) // ~12 ticks
	stop()

	if n := strings.Count(buf.String(), "could not remove the admission webhook"); n != 1 {
		t.Errorf("the sweep reported the same error %d times; want 1.\n%s", n, buf.String())
	}
}
