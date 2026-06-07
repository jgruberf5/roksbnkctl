package config

import (
	"errors"
	"fmt"
)

// Context is the resolved roksbnkctl runtime context for a single command:
// which workspace we're operating on, plus the loaded global and (if the
// workspace exists) workspace config.
//
// CLI commands acquire a Context near their start and use it for the rest
// of their execution. `roksbnkctl init` is the one command that may run with
// Workspace == nil — every other command should treat that as "needs init".
type Context struct {
	WorkspaceName string
	Global        *Global
	Workspace     *Workspace // nil if the workspace hasn't been initialised yet
}

// ErrNoWorkspaceSelected is returned (via WorkspaceNotReady) when a command
// needs a workspace but none was resolved: no -w flag and no current-workspace
// pointer. There is intentionally NO "default" fallback — once the user has
// deleted every workspace, commands must say so rather than silently operate
// on a phantom "default".
var ErrNoWorkspaceSelected = errors.New("no workspace selected; create one with `roksbnkctl init` or pick one with `roksbnkctl ws use <name>`")

// WorkspaceNotReady is the standard error for "this command needs a loaded
// workspace and there isn't one". An empty name (nothing selected) yields
// ErrNoWorkspaceSelected; a named-but-uninitialised workspace yields the
// run-init hint. Commands use it for their `cctx.Workspace == nil` guard.
func WorkspaceNotReady(name string) error {
	if name == "" {
		return ErrNoWorkspaceSelected
	}
	return fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", name)
}

// New resolves the workspace name from (in priority order):
//
//  1. workspaceFlag (the -w/--workspace value, may be "")
//  2. Global.CurrentWorkspace
//
// There is no "default" fallback: when neither is set, WorkspaceName is left
// empty and Workspace is nil. It then loads the workspace config if a name
// resolved. Missing/absent workspace is not propagated as an error — the
// caller decides whether that's OK (`roksbnkctl init` is fine with it;
// everything else uses WorkspaceNotReady).
func New(workspaceFlag string) (*Context, error) {
	g, err := LoadGlobal()
	if err != nil {
		return nil, fmt.Errorf("loading global config: %w", err)
	}

	name := workspaceFlag
	if name == "" {
		name = g.CurrentWorkspace
	}

	ctx := &Context{WorkspaceName: name, Global: g}
	if name == "" {
		// Nothing selected — no phantom "default". Callers that require a
		// workspace surface ErrNoWorkspaceSelected via WorkspaceNotReady.
		return ctx, nil
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}

	ws, err := LoadWorkspace(name)
	switch {
	case err == nil:
		ctx.Workspace = ws
	case errors.Is(err, ErrWorkspaceNotFound):
		// Fine — caller is expected to handle (init creates; others error).
	default:
		return nil, err
	}
	return ctx, nil
}

// SetCurrent persists the workspace pointer in ~/.roksbnkctl/config.yaml so
// later commands without -w default to it. Refuses if the workspace
// doesn't exist on disk yet — pointing at a phantom would just produce
// confusing "workspace not found" errors on every subsequent command.
func SetCurrent(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if !WorkspaceExists(name) {
		return fmt.Errorf("workspace %q does not exist; create it with `roksbnkctl init -w %s`", name, name)
	}
	g, err := LoadGlobal()
	if err != nil {
		return err
	}
	g.CurrentWorkspace = name
	return SaveGlobal(g)
}

// ClearCurrent removes the current-workspace pointer. Used when the last
// workspace is deleted — there is intentionally no fallback "default", so the
// pointer is simply unset and the next command reports ErrNoWorkspaceSelected.
func ClearCurrent() error {
	g, err := LoadGlobal()
	if err != nil {
		return err
	}
	g.CurrentWorkspace = ""
	return SaveGlobal(g)
}
