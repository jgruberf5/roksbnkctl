package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #309 layer 1. `helm_release.flo` installs from a locally-staged archive, so
// `chart` is a file path rather than a repo+name. With `version` unset it is a
// COMPUTED attribute: terraform carries the prior state value into the plan, the
// provider then loads the newly-staged archive and returns a different version, and
// the apply dies with
//
//	Provider produced inconsistent final plan ... .version:
//	was cty.StringVal("v2.30.0-0.1.27"), but now cty.StringVal("v2.30.0-0.5.2")
//
// which blames the helm provider for an unpredictable planned value. It made a
// bnk.manifest_version bump impossible to apply, and re-running never converged
// because the stale value lives in terraform STATE, not on disk.
//
// Verified in production: with the version pinned, the 2.4.0 GA FLO operator
// deployed (f5-lifecycle-operator:v2.30.0-0.5.2, Running).

func floMainTF(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "terraform", "modules", "flo", "modules", "flo", "main.tf")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// floReleaseBlock returns the `resource "helm_release" "flo"` body.
func floReleaseBlock(t *testing.T, src string) string {
	t.Helper()
	i := strings.Index(src, `resource "helm_release" "flo" {`)
	if i < 0 {
		t.Fatal(`resource "helm_release" "flo" not found`)
	}
	// bound at the next top-level resource/data/locals declaration
	rest := src[i+1:]
	j := regexp.MustCompile(`(?m)^(resource|data|locals|output|variable)\s`).FindStringIndex(rest)
	if j == nil {
		return src[i:]
	}
	return src[i : i+1+j[0]]
}

func TestFLOReleasePinsItsChartVersion(t *testing.T) {
	block := floReleaseBlock(t, floMainTF(t))

	if !regexp.MustCompile(`(?m)^\s*version\s*=\s*local\.flo_chart_version\s*$`).MatchString(block) {
		t.Error("helm_release.flo does not pin version = local.flo_chart_version; " +
			"version stays computed and a manifest bump cannot apply (#309)")
	}
}

// TestFLOVersionAndArchiveShareOneSource is the assertion that makes the pin
// meaningful. Pinning version to some OTHER value would satisfy the test above
// while letting the planned version and the staged chart disagree — which is the
// same class of failure, just with a different pair of values.
func TestFLOVersionAndArchiveShareOneSource(t *testing.T) {
	src := floMainTF(t)

	// The archive path is built from flo_chart_version...
	if !regexp.MustCompile(`flo_chart_archive\s*=\s*"\$\{var\.manifest_download_dir\}/f5-lifecycle-operator-\$\{local\.flo_chart_version\}\.tgz"`).MatchString(src) {
		t.Error("flo_chart_archive is no longer built from local.flo_chart_version; " +
			"the pinned version and the staged chart could disagree")
	}
	// ...and the release must use that same local, not a separate expression.
	block := floReleaseBlock(t, src)
	for _, bad := range []string{
		`version          = var.`,
		`version = var.`,
	} {
		if strings.Contains(block, bad) {
			t.Errorf("helm_release.flo pins version from %q rather than local.flo_chart_version; "+
				"it can then disagree with the staged archive", strings.TrimSpace(bad))
		}
	}
}

// The chart must still install from the staged archive. The tempting "fix" for the
// original error is to install from the OCI repo and let helm resolve the version —
// which reintroduces the OCI login this deliberately avoids (the credential-store
// step fails on Windows).
func TestFLOReleaseStillInstallsFromTheStagedArchive(t *testing.T) {
	block := floReleaseBlock(t, floMainTF(t))
	if !regexp.MustCompile(`(?m)^\s*chart\s*=\s*local\.flo_chart_archive\s*$`).MatchString(block) {
		t.Error("helm_release.flo no longer installs from the staged archive; " +
			"an oci:// chart reintroduces the helm registry login that fails on Windows")
	}
}
