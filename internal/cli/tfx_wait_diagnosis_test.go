package cli

import (
	"strings"
	"testing"
)

// Waiting cannot fix a pod the scheduler has rejected for insufficient node
// resources: no node gains capacity on its own.
//
// Before this, `bnk up` sat for the full 15-minute timeout and then failed with
// "f5-bnk-f5-cne-controller not ready -- Available=False", naming neither the
// resource nor the pod. During BNK 2.4 validation that cost fifteen minutes
// twice over to learn "the nodes have no hugepages" — a fact the condition
// message had been publishing the whole time.
func TestTerminalWaitDiagnosis(t *testing.T) {
	for _, tc := range []struct {
		name     string
		desc     string
		terminal bool
		wants    []string
	}{
		{
			name:     "the real 2.4 failure",
			desc:     "Available=False pod f5-bnk/f5-tmm-7587c649f8-h2whb: 0/3 nodes are available: 3 Insufficient hugepages-2Mi.",
			terminal: true,
			wants:    []string{"hugepages-2Mi", "0 of 3", "deploymentSize", "Tiny"},
		},
		{
			name:     "insufficient cpu is equally terminal, without the hugepages advice",
			desc:     "Available=False pod x: 0/6 nodes are available: 6 Insufficient cpu.",
			terminal: true,
			wants:    []string{"cpu", "0 of 6"},
		},
		{
			// Some nodes still fit, so the scheduler may yet place it — e.g. a
			// rolling update where one node is briefly full.
			name:     "partial availability is not terminal",
			desc:     "Available=False pod x: 2/6 nodes are available: 4 Insufficient memory.",
			terminal: false,
		},
		{
			// These genuinely resolve by waiting. Failing them early would trade
			// a slow success for a fast wrong answer.
			name:     "unbound PVC is not terminal",
			desc:     "Available=False pod x: 0/6 nodes are available: pod has unbound immediate PersistentVolumeClaims.",
			terminal: false,
		},
		{
			name:     "still starting is not terminal",
			desc:     "Available=False pod f5-bnk/f5-cne-controller-x: containers with unready status: [f5-cne-controller]",
			terminal: false,
		},
		{
			name:     "empty description is not terminal",
			desc:     "",
			terminal: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why, terminal := terminalWaitDiagnosis(tc.desc)
			if terminal != tc.terminal {
				t.Fatalf("terminal=%v, want %v (desc=%q, why=%q)", terminal, tc.terminal, tc.desc, why)
			}
			for _, w := range tc.wants {
				if !strings.Contains(why, w) {
					t.Errorf("diagnosis omits %q, which is what makes it actionable:\n%s", w, why)
				}
			}
			if !terminal && why != "" {
				t.Errorf("non-terminal case returned a diagnosis anyway: %q", why)
			}
		})
	}
}

// The hugepages advice must appear ONLY for hugepages. Attaching BNK-specific
// deploymentSize guidance to an unrelated shortage would be confidently wrong.
func TestHugepagesAdviceIsScopedToHugepages(t *testing.T) {
	hp, _ := terminalWaitDiagnosis("0/3 nodes are available: 3 Insufficient hugepages-2Mi")
	cpu, _ := terminalWaitDiagnosis("0/3 nodes are available: 3 Insufficient cpu")
	if !strings.Contains(hp, "deploymentSize") {
		t.Error("hugepages diagnosis should explain deploymentSize")
	}
	if strings.Contains(cpu, "deploymentSize") || strings.Contains(cpu, "hugepages") {
		t.Errorf("cpu diagnosis must not carry hugepages advice:\n%s", cpu)
	}
}
