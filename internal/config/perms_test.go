package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// #121. config.yaml carries ibmcloud.api_key_b64 and
// registry.generic_password_b64 — base64, which the field docs are explicit is
// obfuscation and not encryption. The file mode is the only protection they
// have, and it was 0644 inside a 0755 directory: readable by every local
// account on the host.
func TestSaveWorkspaceWritesCredentialFilesOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go's Chmod on Windows only toggles the read-only bit")
	}
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	if err := SaveWorkspace("perms", &Workspace{Prefix: "perms"}); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}

	cfgPath, err := WorkspaceConfigPath("perms")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := WorkspaceDir("perms")
	if err != nil {
		t.Fatal(err)
	}
	stateDir, err := WorkspaceStateDir("perms")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		want os.FileMode
		what string
	}{
		{cfgPath, 0o600, "config.yaml can hold the IBM Cloud API key"},
		{dir, 0o700, "the workspace dir must not be traversable by other users"},
		{stateDir, 0o700, "terraform state holds whatever the credentials resolve to"},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.path, err)
		}
		if got := info.Mode().Perm(); got&^tc.want != 0 {
			t.Errorf("%s is mode %#o, wider than %#o — %s", tc.path, got, tc.want, tc.what)
		}
	}
}

// A fix that only applies on the next write leaves every workspace created by
// an earlier build exposed, and for a finished workspace nothing ever rewrites
// it. Reading one has to repair it.
func TestLoadWorkspaceRepairsAWorldReadableWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go's Chmod on Windows only toggles the read-only bit")
	}
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	if err := SaveWorkspace("legacy", &Workspace{Prefix: "legacy"}); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}
	cfgPath, err := WorkspaceConfigPath("legacy")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := WorkspaceDir("legacy")
	if err != nil {
		t.Fatal(err)
	}

	// Put the tree back the way every pre-fix build left it.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfgPath, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadWorkspace("legacy"); err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	for path, want := range map[string]os.FileMode{cfgPath: 0o600, dir: 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got&^want != 0 {
			t.Errorf("after load, %s is still %#o (want no wider than %#o) — "+
				"an existing workspace stays exposed", path, got, want)
		}
	}
}

// The sibling records live in the same directory and are written on their own
// paths, so they need the same mode rather than relying on the directory bit.
func TestMirrorAndClusterRecordsAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go's Chmod on Windows only toggles the read-only bit")
	}
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	if err := SaveWorkspace("records", &Workspace{Prefix: "records"}); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}
	if err := WriteRegistryMirror("records", &RegistryMirror{Target: "generic"}); err != nil {
		t.Fatalf("WriteRegistryMirror: %v", err)
	}
	dir, err := WorkspaceDir("records")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "registry-mirror.json")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got&^os.FileMode(0o600) != 0 {
		t.Errorf("%s is mode %#o, wider than 0600", p, got)
	}
}

// A tree that cannot be tightened must not take the command down with it — the
// demos run against .bootstrap-state on a DrvFs mount that cannot hold 0600,
// and a hard failure there would break every run.
func TestSecureWorkspacePermsIsSilentOnAnAbsentWorkspace(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	fixed, err := SecureWorkspacePerms("never-created")
	if err != nil {
		t.Errorf("an absent workspace is not an error, got: %v", err)
	}
	if len(fixed) != 0 {
		t.Errorf("nothing to fix, got %v", fixed)
	}
}
