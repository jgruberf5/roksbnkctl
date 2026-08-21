package exec

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// #149. The k8s backend creates a per-Job Secret from opts.Files and owner-refs
// it to the Job so it is garbage-collected with the Job. That patch's error was
// discarded with `_ =`.
//
// Nothing else deletes the Secret. The happy path relies entirely on
// ttlSecondsAfterFinished plus the owner-ref cascade, and the explicit cleanup
// goroutine only fires on ctx cancel. So a failed patch on an OTHERWISE
// SUCCESSFUL run leaves credential material in the cluster indefinitely, with
// nothing on screen to suggest it.

// The patch failure has to reach the caller for the caller to be able to report
// it. Driven against a fake clientset with a reactor that rejects the patch.
func TestSetSecretOwnerRefReportsAFailedPatch(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "job-files", Namespace: "bnk-jobs"},
	})
	boom := errors.New("secrets \"job-files\" is forbidden: cannot update")
	cs.PrependReactor("patch", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, boom
	})

	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "job", UID: types.UID("abc-123")}}
	err := setSecretOwnerRef(context.Background(), "bnk-jobs", cs, "job-files", job)
	if err == nil {
		t.Fatal("a rejected patch must surface as an error, or the caller cannot warn about it")
	}
	if !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("the underlying cause should survive, got: %v", err)
	}
}

// And the success path must actually attach the reference — a test that only
// proves failures propagate would pass against a function that never works.
func TestSetSecretOwnerRefAttachesTheReference(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "job-files", Namespace: "bnk-jobs"},
	})
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "the-job", UID: types.UID("abc-123")}}

	if err := setSecretOwnerRef(context.Background(), "bnk-jobs", cs, "job-files", job); err != nil {
		t.Fatalf("setSecretOwnerRef: %v", err)
	}

	var patched bool
	for _, a := range cs.Actions() {
		pa, ok := a.(k8stesting.PatchAction)
		if !ok {
			continue
		}
		patched = true
		body := string(pa.GetPatch())
		for _, want := range []string{`"kind":"Job"`, `"name":"the-job"`, `"uid":"abc-123"`, `"blockOwnerDeletion":true`} {
			if !strings.Contains(body, want) {
				t.Errorf("the patch should carry %s:\n%s", want, body)
			}
		}
	}
	if !patched {
		t.Error("no patch was issued, so nothing owns the Secret")
	}
}

// The wiring guard. opts.Files has no caller today, so this branch cannot be
// reached at runtime and no functional test can cover the reporting itself —
// the same situation that made #119's guard a source scan. What it pins is that
// the error is not discarded, which is the whole defect.
func TestTheOwnerRefFailureIsReportedNotDiscarded(t *testing.T) {
	src, err := os.ReadFile("k8s.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	if m := regexp.MustCompile(`_\s*=\s*setSecretOwnerRef\(`).FindString(body); m != "" {
		t.Errorf("the owner-ref failure is discarded (%q).\n"+
			"Nothing else deletes this Secret: ttlSecondsAfterFinished removes the Job and the "+
			"cleanup goroutine only runs on ctx cancel, so without the owner-ref a SUCCESSFUL run "+
			"leaves credential material in the cluster with no signal.", m)
	}

	// The report has to be actionable and must not fail the run — the Job is
	// already created and the work should proceed.
	idx := strings.Index(body, "setSecretOwnerRef(ctx")
	if idx < 0 {
		t.Fatal("setSecretOwnerRef is no longer called from runJob")
	}
	window := body[idx:min(idx+600, len(body))]
	for _, want := range []string{"warning:", "kubectl", "delete secret"} {
		if !strings.Contains(window, want) {
			t.Errorf("the failure report should mention %q so the Secret can be removed by hand:\n%s", want, window)
		}
	}
	if strings.Contains(window, "return k8sExitFailedToStart") {
		t.Error("a failed owner-ref must not fail the run: the Job exists and the work should proceed")
	}
}
