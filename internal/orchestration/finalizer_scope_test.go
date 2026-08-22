package orchestration

import (
	"strings"
	"testing"
)

// The finalizer repair deletes finalizers on a live cluster, so what it does
// NOT touch matters as much as what it does.
//
// The first version wrote `metadata.finalizers: null`, which removes every
// finalizer on the object while its own doc comment promised it only touched
// F5's. A Velero backup finalizer, a Kyverno one, or kubernetes.io/pvc-protection
// dropped here leaks whatever it was protecting — and does so silently, during a
// teardown nobody is watching.
func TestOnlyF5FinalizersAreRemoved(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      []string
		keep    []string
		removed int
	}{
		{
			name:    "the two seen holding a real namespace open",
			in:      []string{"k8s.f5.com/CNEInstanceFinalizer", "k8s.f5net.com/uninstall"},
			keep:    nil,
			removed: 2,
		},
		{
			name:    "someone else's finalizer survives alongside F5's",
			in:      []string{"k8s.f5.com/CNEInstanceFinalizer", "velero.io/backup-protection"},
			keep:    []string{"velero.io/backup-protection"},
			removed: 1,
		},
		{
			name:    "a kubernetes.io protection finalizer is never ours to drop",
			in:      []string{"kubernetes.io/pvc-protection"},
			keep:    []string{"kubernetes.io/pvc-protection"},
			removed: 0,
		},
		{
			name:    "subdomains of f5.com count, lookalikes do not",
			in:      []string{"gateway.k8s.f5.com/x", "fic.f5.com/y", "notf5.com/z", "f5.com.evil.io/w"},
			keep:    []string{"notf5.com/z", "f5.com.evil.io/w"},
			removed: 2,
		},
		{
			name:    "nothing to do",
			in:      nil,
			keep:    nil,
			removed: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keep, removed := retainNonF5Finalizers(tc.in)
			if removed != tc.removed {
				t.Errorf("removed %d, want %d (in=%v)", removed, tc.removed, tc.in)
			}
			if strings.Join(keep, ",") != strings.Join(tc.keep, ",") {
				t.Errorf("kept %v, want %v", keep, tc.keep)
			}
		})
	}
}

// The repair discovers kinds through BNKCRDGroups. If that list were ever
// emptied the repair would silently become a no-op that still reports success,
// which is the failure mode the hardcoded three-entry list already had once.
func TestTheFinalizerSweepHasGroupsToSearch(t *testing.T) {
	if len(BNKCRDGroups) == 0 {
		t.Fatal("BNKCRDGroups is empty, so the finalizer sweep would find nothing and report a clean teardown")
	}
	var f5 int
	for _, g := range BNKCRDGroups {
		if strings.HasSuffix(g, "f5.com") || strings.HasSuffix(g, "f5net.com") {
			f5++
		}
	}
	if f5 != len(BNKCRDGroups) {
		t.Errorf("BNKCRDGroups contains a non-F5 group; the sweep lists and mutates every namespaced "+
			"object in each of these groups, so a foreign group here is a foreign object modified: %v",
			BNKCRDGroups)
	}
}
