package exec

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
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
		for _, want := range []string{`"kind":"Job"`, `"name":"the-job"`, `"uid":"abc-123"`, `"controller":true`} {
			if !strings.Contains(body, want) {
				t.Errorf("the patch should carry %s:\n%s", want, body)
			}
		}
	}
	if !patched {
		t.Error("no patch was issued, so nothing owns the Secret")
	}
}

// Review of #155, finding 1. blockOwnerDeletion is not what this code wants —
// it holds the OWNER back until dependents are gone — and on OpenShift, which
// is this project's target platform, it is the single reason the patch can be
// rejected. The OwnerReferencesPermissionEnforcement admission plugin (on by
// default there) refuses it unless the caller holds `update` on
// batch/jobs/finalizers, which this backend's ClusterRole does not grant:
//
//	Error from server (Forbidden): secrets "..." is forbidden: cannot set
//	blockOwnerDeletion if an ownerReference refers to a resource you can't
//	set finalizers on
//
// Without it the same patch is accepted and the Secret is still collected with
// the Job. The first version of this fix warned about the symptom while the
// success test asserted the flag was present, pinning the cause in place.
func TestThePatchDoesNotSetBlockOwnerDeletion(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "job-files", Namespace: "bnk-jobs"},
	})
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "the-job", UID: types.UID("abc-123")}}
	if err := setSecretOwnerRef(context.Background(), "bnk-jobs", cs, "job-files", job); err != nil {
		t.Fatal(err)
	}
	for _, a := range cs.Actions() {
		pa, ok := a.(k8stesting.PatchAction)
		if !ok {
			continue
		}
		if strings.Contains(string(pa.GetPatch()), "blockOwnerDeletion") {
			t.Errorf("the patch sets blockOwnerDeletion, which OpenShift's "+
				"OwnerReferencesPermissionEnforcement rejects without jobs/finalizers access, "+
				"and which this code does not need:\n%s", pa.GetPatch())
		}
	}
}

// Review of #155, finding 5. The warning went to the process's os.Stderr.
// internal/exec is a library: every other output path here routes through
// opts.Stderr and the credential redactor, and a caller that redirects output
// should not have one line escape behind its back.
func TestTheWarningGoesToTheCallersStderr(t *testing.T) {
	var buf bytes.Buffer
	warnOrphanedFilesSecret(&buf, "bnk-jobs", "job-files", errors.New("forbidden: cannot update"))

	got := buf.String()
	if got == "" {
		t.Fatal("the warning did not reach the caller's stderr")
	}
	for _, want := range []string{"warning:", "bnk-jobs/job-files", "forbidden", "kubectl -n bnk-jobs delete secret job-files"} {
		if !strings.Contains(got, want) {
			t.Errorf("the warning should contain %q:\n%s", want, got)
		}
	}
}

// RunOpts.Stderr is optional, so a nil sink must not panic.
func TestTheWarningToleratesANilWriter(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a nil stderr must not panic: %v", r)
		}
	}()
	warnOrphanedFilesSecret(nil, "bnk-jobs", "job-files", errors.New("boom"))
}

// Review of #155, finding 6. An interrupted run DOES delete the Secret — the
// cleanup goroutine fires on ctx cancel regardless of owner-ref state. Telling
// an operator to hand-delete something already gone is its own failure, and
// Ctrl-C on a long terraform Job is routine, so the wording is conditional.
func TestTheWarningDoesNotClaimTheSecretSurvivesACancelledRun(t *testing.T) {
	var buf bytes.Buffer
	warnOrphanedFilesSecret(&buf, "ns", "n", errors.New("boom"))
	got := buf.String()

	if strings.Contains(got, "it will NOT be auto-deleted") {
		t.Errorf("unconditional: a cancelled run deletes this Secret via the cleanup "+
			"goroutine, so the operator would be sent after a Secret that is gone:\n%s", got)
	}
	if !strings.Contains(got, "if this run completes") {
		t.Errorf("the claim should be conditioned on the run completing:\n%s", got)
	}
}

// The call site must hand the helper the CALLER's stderr. Asserted over the
// parsed AST rather than the file's text, which is the direct answer to how the
// previous guard failed review: a substring scan cannot tell live code from a
// comment, and that reviewer proved it by satisfying the old check with a
// comment and a `_ = err`. Comments are not in the AST.
func TestTheWarningIsWiredToTheCallersStderr(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "k8s.go", nil, 0) // 0 = comments discarded
	if err != nil {
		t.Fatalf("parsing k8s.go: %v", err)
	}

	var args []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "warnOrphanedFilesSecret" || len(call.Args) == 0 {
			return true
		}
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, call.Args[0]); err != nil {
			t.Fatal(err)
		}
		args = append(args, buf.String())
		return true
	})

	if len(args) == 0 {
		t.Fatal("nothing calls warnOrphanedFilesSecret, so an owner-ref failure is silent again")
	}
	for _, got := range args {
		if got != "opts.Stderr" {
			t.Errorf("the warning is written to %s, not opts.Stderr.\n"+
				"internal/exec is a library: every other output path here routes through the "+
				"caller's sink and the credential redactor, and a caller that redirects output "+
				"should not have one line escape to the process's stderr behind its back.", got)
		}
	}
}
