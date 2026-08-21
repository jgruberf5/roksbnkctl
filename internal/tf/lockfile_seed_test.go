package tf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review of #153 (D1). extractEmbeddedTF re-extracts on EVERY invocation and
// writes with a truncating os.WriteFile. Every other file in the tree is ours
// and should be rewritten so a binary upgrade picks up new HCL — but the
// lockfile is terraform's, and after the first `init` the copy in the extract
// directory is the workspace's own, recording exactly what that workspace
// installed.
//
// Overwriting it would downgrade a live workspace's providers on the first run
// after an upgrade, and terraform.go inits with Upgrade(false) so it could
// never self-heal. Downgrading below the provider that wrote the state is a
// hard terraform failure with no way out from inside the tool.

func TestTheEmbeddedLockfileSeedsAFreshWorkspace(t *testing.T) {
	base := t.TempDir()
	dest, err := extractEmbeddedTF(base, "")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dest, ".terraform.lock.hcl"))
	if err != nil {
		t.Fatalf("a fresh workspace must be seeded with the pinned lockfile: %v", err)
	}
	if !strings.Contains(string(body), `provider "registry.terraform.io/`) {
		t.Error("the seeded file is not a lockfile")
	}
}

func TestReExtractDoesNotClobberAWorkspaceLockfile(t *testing.T) {
	base := t.TempDir()
	dest, err := extractEmbeddedTF(base, "")
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dest, ".terraform.lock.hcl")

	// Stand in for what `terraform init` leaves behind: the workspace's own
	// lock, recording what IT installed.
	const workspaceLock = "# written by terraform init in this workspace\n" +
		"provider \"registry.terraform.io/hashicorp/kubernetes\" {\n  version = \"3.2.1\"\n}\n"
	if err := os.WriteFile(lock, []byte(workspaceLock), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second run — the shape of every subsequent command, and of the first
	// one after a binary upgrade.
	if _, err := extractEmbeddedTF(base, ""); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != workspaceLock {
		t.Errorf("re-extracting clobbered the workspace's own lockfile.\n"+
			"This downgrades providers on the first run after a binary upgrade, and\n"+
			"terraform inits with Upgrade(false) so it cannot recover.\n"+
			"got %d bytes starting %q", len(got), firstLine(string(got)))
	}
}

// The HCL around it must still refresh, or the seed rule would have frozen the
// whole tree and a binary upgrade would stop shipping new terraform.
func TestReExtractStillRefreshesTheHCL(t *testing.T) {
	base := t.TempDir()
	dest, err := extractEmbeddedTF(base, "")
	if err != nil {
		t.Fatal(err)
	}
	versions := filepath.Join(dest, "versions.tf")
	original, err := os.ReadFile(versions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versions, []byte("# stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractEmbeddedTF(base, ""); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(versions)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Error("versions.tf was not refreshed on re-extract; the seed rule must apply " +
			"to the lockfile ALONE, or a binary upgrade stops shipping new HCL")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
