package roksbnkctl

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	version "github.com/hashicorp/go-version"
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

// Review of #153 (D3/D4) replaced two weak tests with these two.
//
// The one that compared the embedded bytes against the file on disk could not
// fail: go:embed materialises from disk at compile time and `go test` compiles
// immediately before running, so both sides were the same bytes by
// construction. And the one that scanned versions.tf for a "~>" was evadable by
// the one-line style already used at terraform/modules/flp_vsi/providers.tf,
// and structurally could not see tls/time/local/external, which are declared
// only in submodules and were genuinely unbounded.
//
// These two assert the invariants that actually matter, both derived from the
// lockfile rather than from a text pattern.

// providerRE matches a required_providers entry in either style used in this
// tree: the block form in terraform/versions.tf, and the one-line form in
// terraform/modules/flp_vsi/providers.tf.
var providerRE = regexp.MustCompile(`source\s*=\s*"([^"]+)"[^}]*?version\s*=\s*"([^"]+)"`)

// lockProviderRE matches a provider block header and its pinned version.
var lockProviderRE = regexp.MustCompile(`provider\s+"registry\.terraform\.io/([^"]+)"\s*\{\s*\n\s*version\s*=\s*"([^"]+)"`)

func rootConstraints(t *testing.T) map[string]string {
	t.Helper()
	body, err := os.ReadFile("terraform/versions.tf")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range providerRE.FindAllStringSubmatch(string(body), -1) {
		out[strings.ToLower(m[1])] = m[2]
	}
	if len(out) == 0 {
		t.Fatal("parsed no providers out of terraform/versions.tf")
	}
	return out
}

func lockedVersions(t *testing.T) map[string]string {
	t.Helper()
	body, err := fs.ReadFile(EmbeddedTerraform, "terraform/.terraform.lock.hcl")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range lockProviderRE.FindAllStringSubmatch(string(body), -1) {
		out[strings.ToLower(m[1])] = m[2]
	}
	if len(out) == 0 {
		t.Fatal("parsed no providers out of the embedded lockfile")
	}
	return out
}

// The failure this guards is fatal and unrecoverable from inside the tool: if a
// constraint edit excludes the version the lockfile pins, `terraform init`
// stops with "locked provider ... does not match configured version constraint
// ... must use terraform init -upgrade", and internal/tf/terraform.go inits
// with Upgrade(false). Every user hits it; none can get past it.
//
// Nothing else catches this. The lockfile and the constraints live in separate
// files and are edited by separate actions — `terraform providers lock` writes
// one, a human writes the other.
func TestEveryLockedVersionSatisfiesItsRootConstraint(t *testing.T) {
	constraints := rootConstraints(t)
	for provider, locked := range lockedVersions(t) {
		raw, ok := constraints[provider]
		if !ok {
			t.Errorf("%s is pinned in the lockfile at %s but has no constraint in "+
				"terraform/versions.tf, so nothing bounds it", provider, locked)
			continue
		}
		cs, err := version.NewConstraint(raw)
		if err != nil {
			t.Errorf("%s: cannot parse constraint %q: %v", provider, raw, err)
			continue
		}
		v, err := version.NewVersion(locked)
		if err != nil {
			t.Errorf("%s: cannot parse locked version %q: %v", provider, locked, err)
			continue
		}
		if !cs.Check(v) {
			t.Errorf("%s: the lockfile pins %s but versions.tf says %q.\n"+
				"terraform init would fail with \"locked provider does not match configured "+
				"version constraint\", and it inits with Upgrade(false), so no user can recover.\n"+
				"Re-run `terraform providers lock` after changing a constraint.", provider, locked, raw)
		}
	}
}

// Every provider the lockfile pins must be bounded AT THE ROOT. terraform
// intersects constraints across the whole tree, so a root bound governs
// everywhere — but only for providers the root names. tls, time, local and
// external are declared only in submodules and, before this, only with bare
// ">=", which left them unbounded across the entire configuration.
func TestEveryProviderIsBoundedAtTheRoot(t *testing.T) {
	constraints := rootConstraints(t)
	for provider := range lockedVersions(t) {
		raw, ok := constraints[provider]
		if !ok {
			t.Errorf("%s is used by this configuration but is not declared in "+
				"terraform/versions.tf, so no bound governs it anywhere", provider)
			continue
		}
		if !strings.Contains(raw, "~>") && !strings.Contains(raw, "<") {
			t.Errorf("%s has an unbounded constraint %q — a breaking major would be accepted.\n"+
				"Use \"~> MAJOR.MINOR\", which preserves the floor and caps the major.", provider, raw)
		}
	}
}
