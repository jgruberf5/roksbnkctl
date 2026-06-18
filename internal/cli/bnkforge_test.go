package cli

import (
	"context"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// TestTryRegisterBNKForge_GatingNoop pins that the hook is a safe no-op (no
// panic, no external call attempt) whenever the workspace hasn't opted in —
// nil context, nil workspace, absent block, or register:false. The opt-in /
// happy path execs the `bnk-forge` CLI and is exercised by the integration
// driver, not here.
func TestTryRegisterBNKForge_GatingNoop(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		cctx *config.Context
	}{
		{"nil context", nil},
		{"nil workspace", &config.Context{WorkspaceName: "ws"}},
		{"absent bnkforge block", &config.Context{WorkspaceName: "ws", Workspace: &config.Workspace{}}},
		{"register false", &config.Context{WorkspaceName: "ws", Workspace: &config.Workspace{BNKForge: &config.BNKForgeCfg{Register: false}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Must return immediately without panicking or invoking anything.
			tryRegisterBNKForge(ctx, tc.cctx)
		})
	}
}

// TestBNKForgeEnableDisable pins that `bnkforge enable`/`disable` write the
// config block for the operator — the whole point of the command group is that
// turning the feature on never requires hand-editing config.yaml.
func TestBNKForgeEnableDisable(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("bf", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("bf"); err != nil {
		t.Fatal(err)
	}
	oldWS, oldURL, oldProj := flagWorkspace, flagBNKForgeURL, flagBNKForgeProject
	flagWorkspace, flagBNKForgeURL, flagBNKForgeProject = "", "", ""
	defer func() { flagWorkspace, flagBNKForgeURL, flagBNKForgeProject = oldWS, oldURL, oldProj }()

	// enable --url --project → register:true plus the overrides, all persisted.
	flagBNKForgeURL, flagBNKForgeProject = "https://forge.local", "9"
	if err := runBNKForgeEnable(nil, nil); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ws, err := config.LoadWorkspace("bf")
	if err != nil {
		t.Fatal(err)
	}
	if ws.BNKForge == nil || !ws.BNKForge.Register {
		t.Fatalf("after enable, BNKForge = %+v; want register:true", ws.BNKForge)
	}
	if ws.BNKForge.URL != "https://forge.local" || ws.BNKForge.Project != "9" {
		t.Errorf("overrides not persisted: %+v", *ws.BNKForge)
	}

	// disable flips register off but leaves the (still-useful) url/project.
	flagBNKForgeURL, flagBNKForgeProject = "", ""
	if err := runBNKForgeDisable(nil, nil); err != nil {
		t.Fatalf("disable: %v", err)
	}
	ws, err = config.LoadWorkspace("bf")
	if err != nil {
		t.Fatal(err)
	}
	if ws.BNKForge == nil || ws.BNKForge.Register {
		t.Errorf("after disable, BNKForge = %+v; want register:false", ws.BNKForge)
	}
	if ws.BNKForge.URL != "https://forge.local" {
		t.Errorf("disable shouldn't drop url: %+v", *ws.BNKForge)
	}
}
