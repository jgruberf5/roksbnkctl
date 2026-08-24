package cli

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// #189. `bnk up` sat for the full 15-minute timeout and then failed with a
// terraform local-exec error, because the condition description `tfx wait`
// produced was only "Available=False". The scheduler's actual verdict lives in
// the condition's MESSAGE, which was dropped — so terminalWaitDiagnosis had
// nothing to read and a permanently unschedulable pod looked exactly like one
// still starting.
func TestConditionDescriptionCarriesReasonAndMessage(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{
					"type":    "Available",
					"status":  "False",
					"reason":  "Pending",
					"message": "pod f5-bnk/f5-tmm-x: 0/6 nodes are available: 4 node(s) didn't match PersistentVolume's node affinity",
				},
			},
		},
	}}
	ok, desc := conditionMatcher{typ: "Available", status: "True"}.matched(obj)
	if ok {
		t.Fatal("matched a False condition against True")
	}
	for _, want := range []string{"Available=False", "reason=Pending", "PersistentVolume's node affinity"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description is missing %q — the diagnosis reads this string.\n got: %s", want, desc)
		}
	}
}

// The pods are pinned to separate nodes by anti-affinity while their volume is
// bound to one zone. No node gains the ability to fix that, so waiting is
// pointless and should end immediately with the reason.
func TestPVNodeAffinityIsTerminal(t *testing.T) {
	desc := "Available=False reason=Pending msg=pod f5-bnk/f5-tmm-x: 0/6 nodes are available: " +
		"1 Insufficient cpu, 1 node(s) didn't match pod topology spread constraints, " +
		"4 node(s) didn't match PersistentVolume's node affinity"

	why, terminal := terminalWaitDiagnosis(desc)
	if !terminal {
		t.Fatal("a pod that can never be scheduled was not reported terminal; the wait would " +
			"burn its full timeout and then fail naming nothing useful")
	}
	// It must name the cause and a way forward, or it is no better than the timeout.
	for _, want := range []string{"PersistentVolume", "anti-affinity", "storage_class_name", "tmm_replicas", "#189"} {
		if !strings.Contains(why, want) {
			t.Errorf("diagnosis does not mention %q:\n%s", want, why)
		}
	}
	// The PV verdict must win over the co-occurring "Insufficient cpu", which
	// would otherwise send the reader after node capacity that is not the problem.
	if strings.Contains(why, "lack \"cpu\"") {
		t.Error("reported insufficient cpu; the binding constraint is the volume, and cpu is a distraction")
	}
}

// Both directions: a pod that is merely slow must NOT be failed early.
func TestSchedulableStatesAreNotTerminal(t *testing.T) {
	for _, desc := range []string{
		"Available=False reason=Pending msg=pod f5-bnk/x: 2/6 nodes are available: 4 node(s) didn't match PersistentVolume's node affinity",
		"Available=False reason=Pending msg=ContainerCreating",
		"Available=False",
		"Available=False reason=Progressing msg=waiting for rollout",
	} {
		if _, terminal := terminalWaitDiagnosis(desc); terminal {
			t.Errorf("failed early on a state that may still resolve: %q", desc)
		}
	}
}
