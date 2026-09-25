package tf

import "testing"

// #307 / #308 both proposed removing ENABLE_K8S_ROUTES from the 2.4 TMM env,
// and both were wrong. It is read by the LIFECYCLE OPERATOR, not by TMM:
//
//	tmm-img:v10.204.15-0.1.46 (2.4.0 GA)   0 occurrences
//	f5ingress:v14.91.12-0.4.7 (controller) 0 occurrences, all 2766 files
//	f5-lifecycle-operator:v2.30.0-0.5.2    PRESENT, with "Found ENABLE_K8S_ROUTES"
//	                                       and "error parsing ENABLE_K8S_ROUTES
//	                                       env var: %w", beside "Adding TMM
//	                                       TMM_K8S_ROUTES environment variables"
//
// Checking that needs a pull-and-grep of the operator image with `strings -a`;
// the operator ships no shell, and plain `grep` returns a confident zero on a
// 95MB static Go binary even for variables it demonstrably reads.
//
// Twice is enough. This fails if it is removed again.
func TestEnableK8sRoutesIsEmittedOn24(t *testing.T) {
	const expr = `local.adv_env["tmm"][*].name`

	on24 := consoleEnvNames(t, "bnk_line = \"2.4\"\n", expr)
	if countName(on24, "ENABLE_K8S_ROUTES") != 1 {
		t.Errorf("ENABLE_K8S_ROUTES is not emitted on 2.4: %v\n"+
			"It is READ BY FLO. Removing it changes the operator's behaviour on every "+
			"2.4 install — see #307, and #308, which was closed unmerged for this.", on24)
	}

	// The companions it is grouped with, so a future edit to the block cannot
	// silently take them either.
	for _, name := range []string{"TMM_IGNORE_GATEWAYS", "DISABLE_HT"} {
		if countName(on24, name) != 1 {
			t.Errorf("%s missing from the 2.4 TMM env: %v", name, on24)
		}
	}
}

// 2.3 must NOT carry it — the block is 2.4-only, and a gate that stopped gating
// would put it on both lines.
func TestEnableK8sRoutesIsAbsentOn23(t *testing.T) {
	const expr = `local.adv_env["tmm"][*].name`

	on23 := consoleEnvNames(t, "bnk_line = \"2.3\"\n", expr)
	if countName(on23, "ENABLE_K8S_ROUTES") != 0 {
		t.Errorf("ENABLE_K8S_ROUTES leaked onto 2.3: %v", on23)
	}
}

// TMM_K8S_ROUTES is the OTHER end of the same control, read by /opt/bin/mapres
// in the TMM image, and it is in the SHARED defaults rather than the 2.4-only
// block. PRD 18 claimed 2.4 drops it; the shipped binary says otherwise, so it
// must be present on BOTH lines.
func TestTMMK8sRoutesIsOnBothLines(t *testing.T) {
	const expr = `local.adv_env["tmm"][*].name`

	for _, line := range []string{"2.3", "2.4"} {
		got := consoleEnvNames(t, "bnk_line = \""+line+"\"\n", expr)
		if countName(got, "TMM_K8S_ROUTES") != 1 {
			t.Errorf("TMM_K8S_ROUTES missing on %s: %v\n"+
				"mapres reads it to decide whether to install the ipv4/ipv6 gateway rule.", line, got)
		}
	}
}
