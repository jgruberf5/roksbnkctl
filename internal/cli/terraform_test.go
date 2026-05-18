package cli

import (
	"strings"
	"testing"
)

// TestValidateTerraformReadOnly_AllowlistMatrix pins the accept/reject
// matrix for the Sprint 13 Issue 2 read-only gate.
func TestValidateTerraformReadOnly_AllowlistMatrix(t *testing.T) {
	accept := [][]string{
		{"output"},
		{"output", "testing_cluster_jumphost_ssh_commands"},
		{"show"},
		{"providers"},
		{"version"},
		{"graph"},
		{"validate"},
		{"state", "list"},
		{"state", "show", "module.x.res"},
		{"state", "pull"},
		{"fmt", "-check"},
		{"fmt", "-check", "-recursive"},
		{"output", "-json"},
	}
	for _, argv := range accept {
		if err := validateTerraformReadOnly(argv); err != nil {
			t.Errorf("validateTerraformReadOnly(%v) = %v, want nil (read-only)", argv, err)
		}
	}

	reject := [][]string{
		{},                      // no subcommand
		{"apply"},               // mutating top-level
		{"destroy"},             //
		{"init"},                //
		{"plan"},                // not in allowlist (use lifecycle verbs)
		{"import", "a", "b"},    //
		{"taint", "x"},          //
		{"untaint", "x"},        //
		{"state"},               // bare state needs read-only sub-verb
		{"state", "rm", "addr"}, // sub-verb guard
		{"state", "mv", "a", "b"},
		{"state", "replace-provider", "a", "b"},
		{"fmt"},                         // fmt without -check rewrites
		{"fmt", "-recursive"},           // still rewrites
		{"output", "-auto-approve"},     // mutation-flag scrub
		{"show", "-destroy"},            //
		{"validate", "-target=res.x"},   //
		{"providers", "-replace=res.y"}, //
	}
	for _, argv := range reject {
		err := validateTerraformReadOnly(argv)
		if err == nil {
			t.Errorf("validateTerraformReadOnly(%v) = nil, want rejection", argv)
			continue
		}
		if !strings.Contains(err.Error(), "roksbnkctl up") {
			t.Errorf("validateTerraformReadOnly(%v) error %q must point at the lifecycle verbs", argv, err)
		}
	}
}

func TestExtractPhaseFlag(t *testing.T) {
	cases := []struct {
		in        []string
		wantPhase string
		wantArgv  []string
	}{
		{[]string{"output"}, "", []string{"output"}},
		{[]string{"--phase", "cluster", "state", "list"}, "cluster", []string{"state", "list"}},
		{[]string{"--phase=cluster", "show"}, "cluster", []string{"show"}},
		{[]string{"--phase", "cluster", "--", "output"}, "cluster", []string{"output"}},
	}
	for _, c := range cases {
		gotPhase, gotArgv := extractPhaseFlag(c.in)
		if gotPhase != c.wantPhase {
			t.Errorf("extractPhaseFlag(%v) phase = %q, want %q", c.in, gotPhase, c.wantPhase)
		}
		if strings.Join(gotArgv, " ") != strings.Join(c.wantArgv, " ") {
			t.Errorf("extractPhaseFlag(%v) argv = %v, want %v", c.in, gotArgv, c.wantArgv)
		}
	}
}

// TestTerraformReadOnlyStateDir_RoutesByPhase: default phase → state/,
// --phase cluster → state-cluster/, unknown → error.
func TestTerraformReadOnlyStateDir_RoutesByPhase(t *testing.T) {
	dir, label, err := terraformReadOnlyStateDir("ws", "")
	if err != nil || label != "default" || !strings.HasSuffix(dir, "state") {
		t.Errorf("default phase: dir=%q label=%q err=%v", dir, label, err)
	}
	cdir, clabel, err := terraformReadOnlyStateDir("ws", "cluster")
	if err != nil || clabel != "cluster" || !strings.HasSuffix(cdir, "state-cluster") {
		t.Errorf("cluster phase: dir=%q label=%q err=%v", cdir, clabel, err)
	}
	if _, _, err := terraformReadOnlyStateDir("ws", "bogus"); err == nil {
		t.Errorf("unknown phase should error")
	}
}

// TestRunTerraformPassthrough_RejectsOn: --on is rejected before any
// workspace is opened, with a pointer explaining state is local-only.
func TestRunTerraformPassthrough_RejectsOn(t *testing.T) {
	prev := flagOn
	t.Cleanup(func() { flagOn = prev })
	flagOn = "jumphost"
	err := runTerraformPassthrough(terraformCmd, []string{"output"})
	if err == nil {
		t.Fatalf("runTerraformPassthrough with --on should error")
	}
	if !strings.Contains(err.Error(), "workstation-local") {
		t.Errorf("--on rejection %q should explain state is workstation-local", err)
	}
}
