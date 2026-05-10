package remote

import (
	"errors"
	"fmt"
	"sort"

	"golang.org/x/crypto/ssh"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Target is the runtime form of a `targets:` entry. Constructed by
// LoadTarget (which reads the workspace config), enriched with a
// resolved Signer + HostKeyCallback before Connect is called.
//
// Signer and HostKeyCallback are populated by the caller after Load:
// keys.go's ResolveSigner fills Signer, hostkeys.go's HostKeyCallback
// fills HostKeyCallback. Keeping those off the on-disk shape avoids
// dragging the whole crypto/ssh API surface into config.
type Target struct {
	Name    string
	Host    string
	Port    int
	User    string
	KeyPath string
	// KeySource is the on-disk hint for ResolveSigner — one of:
	// "" (use KeyPath), "agent", "tf-output:<name>".
	KeySource string

	Signer          ssh.Signer
	HostKeyCallback ssh.HostKeyCallback
}

// errTargetNotFound is returned when the named target isn't in the
// workspace's targets map. Wrapped by LoadTarget so callers can
// errors.Is it without importing this constant.
var errTargetNotFound = errors.New("target not found")

// ErrTargetNotFound is the sentinel callers use with errors.Is.
var ErrTargetNotFound = errTargetNotFound

// LoadTarget reads ~/.roksbnkctl/<workspace>/config.yaml and returns the
// named target, with Signer / HostKeyCallback unset (caller fills those
// before Connect). Returns ErrTargetNotFound if the workspace config
// has no entry for `name`.
func LoadTarget(workspace string, name string) (*Target, error) {
	if name == "" {
		return nil, errors.New("target name is empty")
	}
	ws, err := config.LoadWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	cfg, ok := ws.Targets[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q in workspace %q", ErrTargetNotFound, name, workspace)
	}
	return targetFromCfg(name, cfg), nil
}

// ListTargets returns all targets in the workspace, sorted by name.
// Empty slice (not error) when the workspace has no `targets:` block.
func ListTargets(workspace string) ([]*Target, error) {
	ws, err := config.LoadWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	if len(ws.Targets) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(ws.Targets))
	for n := range ws.Targets {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*Target, 0, len(names))
	for _, n := range names {
		out = append(out, targetFromCfg(n, ws.Targets[n]))
	}
	return out, nil
}

// SetTarget upserts the named target in the workspace config. Creates
// the targets map if absent. Persists immediately.
func SetTarget(workspace string, name string, cfg config.TargetCfg) error {
	if name == "" {
		return errors.New("target name is empty")
	}
	if cfg.Host == "" {
		return errors.New("target host is empty")
	}
	if cfg.User == "" {
		return errors.New("target user is empty")
	}
	ws, err := config.LoadWorkspace(workspace)
	if err != nil {
		return err
	}
	if ws.Targets == nil {
		ws.Targets = map[string]config.TargetCfg{}
	}
	ws.Targets[name] = cfg
	return config.SaveWorkspace(workspace, ws)
}

// RemoveTarget deletes the named target. No-op (no error) when absent —
// matches `roksbnkctl targets remove` user expectations.
func RemoveTarget(workspace string, name string) error {
	if name == "" {
		return errors.New("target name is empty")
	}
	ws, err := config.LoadWorkspace(workspace)
	if err != nil {
		return err
	}
	if _, ok := ws.Targets[name]; !ok {
		return nil
	}
	delete(ws.Targets, name)
	return config.SaveWorkspace(workspace, ws)
}

// targetFromCfg lifts a TargetCfg into a runtime Target (no Signer /
// HostKeyCallback yet — the caller fills those before Connect).
func targetFromCfg(name string, cfg config.TargetCfg) *Target {
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	return &Target{
		Name:      name,
		Host:      cfg.Host,
		Port:      port,
		User:      cfg.User,
		KeyPath:   cfg.KeyPath,
		KeySource: cfg.KeySource,
	}
}

// KeySourceDescription returns a one-token human string for table
// rendering ("agent", "file:~/.ssh/id_ed25519", "tf-output:<name>").
func (t *Target) KeySourceDescription() string {
	if t == nil {
		return ""
	}
	switch {
	case t.KeyPath != "":
		return "file:" + t.KeyPath
	case t.KeySource != "":
		return t.KeySource
	default:
		return "(unset)"
	}
}
