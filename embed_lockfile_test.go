package roksbnkctl

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// #147. The committed terraform/.terraform.lock.hcl pins every provider to an
// exact version and records the checksums `terraform init` verifies downloads
// against. It never reached users: `//go:embed terraform` skips dotfiles, so
// released binaries extracted 86 files without it and resolved providers from
// the ">=" constraints in versions.tf at deploy time — unpinned, unverified,
// and different from the set CI tested.
//
// Nothing about that failure is visible at runtime. `terraform init` succeeds
// either way; the only difference is WHICH providers it picks and whether it
// checks them. So the guard has to be a test.
func TestLockfileIsEmbedded(t *testing.T) {
	body, err := fs.ReadFile(EmbeddedTerraform, "terraform/.terraform.lock.hcl")
	if err != nil {
		t.Fatalf("the provider lockfile is not in the embedded FS: %v\n"+
			"A plain //go:embed directory pattern skips dotfiles, so the lockfile needs its own\n"+
			"explicit `//go:embed terraform/.terraform.lock.hcl` line in embedded.go.", err)
	}
	if !strings.Contains(string(body), "provider \"registry.terraform.io/") {
		t.Error("the embedded lockfile has no provider blocks — it is not a lockfile")
	}
}

// The embedded copy must BE the committed one. A lockfile that has drifted from
// the repo is worse than none: CI would validate one provider set and users
// would get another, which is the exact failure #147 describes.
func TestEmbeddedLockfileMatchesTheCommittedOne(t *testing.T) {
	embedded, err := fs.ReadFile(EmbeddedTerraform, "terraform/.terraform.lock.hcl")
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile("terraform/.terraform.lock.hcl")
	if err != nil {
		t.Fatalf("reading the committed lockfile: %v", err)
	}
	if string(embedded) != string(onDisk) {
		t.Error("the embedded lockfile differs from terraform/.terraform.lock.hcl")
	}
}

// The reason the directive is two explicit lines rather than `all:terraform`.
// `all:` would also pull in the gitignored .terraform/ provider cache a local
// `terraform init` leaves behind — ~400MB of plugin binaries that bloated the
// binary to ~670MB and, extracted at 0644, shipped non-executable providers.
//
// This fails on a dev machine that has run terraform locally, which is exactly
// where the mistake would be made and exactly where it would otherwise go
// unnoticed until someone downloaded a release.
func TestProviderCacheIsNotEmbedded(t *testing.T) {
	var bundled []string
	var total int64
	err := fs.WalkDir(EmbeddedTerraform, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		if strings.Contains(p, "/.terraform/") || strings.HasPrefix(p, ".terraform/") {
			bundled = append(bundled, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundled) > 0 {
		show := bundled
		if len(show) > 5 {
			show = show[:5]
		}
		t.Errorf("the .terraform/ provider cache is embedded (%d files, e.g. %v).\n"+
			"Use two explicit //go:embed lines, not `all:terraform`.", len(bundled), show)
	}
	// A blunt backstop on the same failure: the HCL tree is well under a
	// megabyte, and a bundled provider cache is hundreds of them.
	if total > 4<<20 {
		t.Errorf("the embedded tree is %d bytes — far larger than the HCL source, "+
			"which means something unintended is bundled", total)
	}
}

// Every provider must be bounded. The lockfile governs what installs, but a
// `terraform init -upgrade`, or any run reaching this config without the
// lockfile, falls back to these constraints — and a bare ">=" accepts a
// breaking major.
func TestProviderConstraintsAreBounded(t *testing.T) {
	body, err := os.ReadFile("terraform/versions.tf")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "version") || !strings.Contains(trimmed, "=") {
			continue
		}
		if strings.Contains(trimmed, "~>") || strings.Contains(trimmed, "<") {
			continue
		}
		t.Errorf("unbounded provider constraint: %q\n"+
			"Use \"~> MAJOR.MINOR\" so a breaking major is not silently accepted.", trimmed)
	}
}
