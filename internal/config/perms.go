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
// GTM passwords, and state/terraform.tfstate carries whatever those resolve to.
// Every one of them is base64 at most, which is obfuscation and not encryption
// — the file mode is the only thing protecting them, so it is owner-only, and
// so is the directory holding it.
const (
	// SecretFileMode is the mode for a file that can contain a credential.
	SecretFileMode fs.FileMode = 0o600
	// SecretDirMode is the mode for a directory containing such files. Owner-only
	// traversal keeps everything beneath it unreachable regardless of its own mode.
	SecretDirMode fs.FileMode = 0o700
)

var permWarnOnce sync.Once

// SecureWorkspacePerms tightens an existing workspace tree to owner-only and
// reports the paths it changed.
//
// Repair matters as much as writing the right mode: every workspace created
// before this existed is on disk at 0755/0644 right now, and a fix that only
// applies on the next write leaves those exposed until something happens to
// rewrite them — which for a finished workspace is never.
//
// Best-effort by design. A tree that cannot be chmod'ed (a read-only mount, a
// filesystem with no POSIX modes — the .bootstrap-state case the demos hit on
// DrvFs) must not stop the command; the caller gets the list and the error and
// decides. On Windows it is a no-op: Go's Chmod there only toggles the
// read-only bit, so a mode comparison would report a repair that can never land
// and warn on every single run.
func SecureWorkspacePerms(name string) ([]string, error) {
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	dir, err := WorkspaceDir(name)
	if err != nil {
		return nil, err
	}
	cfgPath, err := WorkspaceConfigPath(name)
	if err != nil {
		return nil, err
	}
	stateDir, err := WorkspaceStateDir(name)
	if err != nil {
		return nil, err
	}

	var fixed []string
	var firstErr error
	tighten := func(path string, want fs.FileMode) {
		info, serr := os.Stat(path)
		if serr != nil {
			return // absent is not a problem: nothing to expose
		}
		if info.Mode().Perm()&^want == 0 {
			return // already no broader than want
		}
		if cerr := os.Chmod(path, want); cerr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("tightening %s to %#o: %w", path, want, cerr)
			}
			return
		}
		fixed = append(fixed, path)
	}

	tighten(dir, SecretDirMode)
	tighten(stateDir, SecretDirMode)
	tighten(cfgPath, SecretFileMode)
	// The workspace dir is now owner-only, so its other files are unreachable by
	// other users regardless of their own mode. Tighten them anyway: the dir
	// mode is one `chmod` away from being widened again, and defence that
	// depends on a single directory bit is defence that fails silently.
	for _, base := range []string{"registry-mirror.json", "cluster-outputs.json"} {
		tighten(filepath.Join(dir, base), SecretFileMode)
	}
	return fixed, firstErr
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
