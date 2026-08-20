package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// The workspace tree holds live credentials in plain files: config.yaml can
// carry ibmcloud.api_key_b64, registry.generic_password_b64 and the BIG-IP /
// GTM passwords, and each state tree's terraform.tfstate carries whatever those
// resolve to. Every one of them is base64 at most, which is obfuscation and not
// encryption — the file mode is the only protection they have.
const (
	// SecretFileMode is the mode for a file that can contain a credential.
	SecretFileMode fs.FileMode = 0o600
	// SecretDirMode is the mode for a directory containing such files. Owner-only
	// traversal keeps everything beneath it unreachable regardless of its own mode.
	SecretDirMode fs.FileMode = 0o700
)

var permWarnOnce sync.Once

// ownerOnly strips every group and other bit while preserving the owner's own.
//
// Preserving them matters: forcing a flat 0600/0700 would clear the execute bit
// from anything executable that lands in the tree, and this runs over entries
// the workspace layout does not enumerate. Masking only removes access, so it
// is safe to apply to a path whose purpose is unknown.
func ownerOnly(m fs.FileMode) fs.FileMode { return m.Perm() & 0o700 }

// LooseWorkspacePaths reports which paths in the workspace tree are reachable
// by users other than the owner. Read-only: it reports, it does not repair.
//
// Scope is the workspace directory and its immediate children — which covers
// config.yaml, registry-mirror.json, cluster-outputs.json, ssh/ and every
// state-* tree, without a deep walk of tf-source/ and scratch/ on every
// workspace load. Owner-only on a state directory makes its contents
// unreachable regardless of their own modes, so the depth buys nothing the cost
// would justify.
//
// Enumerating children rather than a fixed list of directory names is
// deliberate: phases add state trees (state-cluster, state-testing,
// state-gateway, …) and a hand-maintained list is one phase away from missing
// one silently.
func LooseWorkspacePaths(name string) ([]string, error) {
	if runtime.GOOS == "windows" {
		// Go's Chmod on Windows only toggles the read-only bit, so Perm() never
		// reports an owner-only mode and every path would look loose forever.
		return nil, nil
	}
	dir, err := WorkspaceDir(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil // absent workspace: nothing to expose
	}

	var loose []string
	if info.Mode().Perm() != ownerOnly(info.Mode()) {
		loose = append(loose, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return loose, fmt.Errorf("listing %s: %w", dir, err)
	}
	for _, e := range entries {
		ei, err := e.Info()
		if err != nil {
			continue
		}
		if !ei.Mode().IsRegular() && !ei.IsDir() {
			continue // sockets, symlinks: not ours to re-mode
		}
		if ei.Mode().Perm() != ownerOnly(ei.Mode()) {
			loose = append(loose, filepath.Join(dir, e.Name()))
		}
	}
	return loose, nil
}

// SecureWorkspacePerms tightens the workspace tree to owner-only and reports the
// paths it changed.
//
// Repair matters as much as writing the right mode: every workspace created
// before this existed is on disk at 0755/0644 right now, and a fix that only
// applies on the next write leaves those exposed until something happens to
// rewrite them — which for a finished workspace is never.
//
// Best-effort by design. A tree that cannot be chmod'ed (a read-only mount, a
// filesystem with no POSIX modes — the DrvFs case the demos hit) must not stop
// the command; the caller gets the paths it fixed and the first error, and
// decides. On Windows it is a no-op.
func SecureWorkspacePerms(name string) ([]string, error) {
	loose, err := LooseWorkspacePaths(name)
	if len(loose) == 0 {
		return nil, err
	}
	var fixed []string
	for _, p := range loose {
		info, serr := os.Stat(p)
		if serr != nil {
			continue
		}
		if cerr := os.Chmod(p, ownerOnly(info.Mode())); cerr != nil {
			if err == nil {
				err = fmt.Errorf("tightening %s: %w", p, cerr)
			}
			continue
		}
		fixed = append(fixed, p)
	}
	return fixed, err
}

// warnOnLoosePerms repairs the workspace tree on load and says so once per
// process. Silent tightening would be safe but invisible, and a user whose
// credentials have been world-readable for the life of the workspace should
// hear about it — the mode is fixed from here on, the exposure that already
// happened is not.
func warnOnLoosePerms(name string) {
	fixed, err := SecureWorkspacePerms(name)
	if len(fixed) == 0 && err == nil {
		return
	}
	permWarnOnce.Do(func() {
		if len(fixed) > 0 {
			fmt.Fprintf(os.Stderr, "⚠ workspace %q was readable by other users on this host; tightened %d path(s) to owner-only.\n", name, len(fixed))
			fmt.Fprintln(os.Stderr, "  config.yaml can hold your IBM Cloud API key and registry password. If this host is shared, rotate them.")
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ workspace %q: could not tighten permissions: %v\n", name, err)
		}
	})
}
