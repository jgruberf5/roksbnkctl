package config

import (
	"strings"
	"testing"
)

// #177. The release line is derived from bnk.manifest_version, so a manifest bump
// across lines flips it — and that is not an in-place upgrade. The damage is
// off-cluster (GTM objects on an external BIG-IP), abandoned rather than migrated
// (the whole F5SPK* surface), and survives teardown (~30 CWC license secrets), so
// this refuses rather than warns.

func ws(manifest string) *Workspace {
	return &Workspace{BNK: BNKCfg{ManifestVersion: manifest}}
}

func TestALineFlipIsRefused(t *testing.T) {
	err := CheckLineChange(ws("2.4.0-EA"), map[string]string{"bnk_line": `"2.3"`})
	if err == nil {
		t.Fatal("moving a 2.3 install to a 2.4 manifest must be refused: the GTM objects it " +
			"leaves on the external BIG-IP are outside the cluster and outside terraform state")
	}
	// The refusal has to say WHY, or it reads as the tool being fussy about a
	// version string. Each reason is something the operator cannot discover from
	// a plan.
	for _, want := range []string{"2.3", "2.4", "GTM", "external BIG-IP", "license secrets", "NEW workspace"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should mention %q:\n%v", want, err)
		}
	}
}

// The reverse direction is no safer. A 2.4 install moved back to a 2.3 manifest
// abandons Infra/GatewaySettings and expects F5SPK* objects that were never made.
func TestTheReverseFlipIsAlsoRefused(t *testing.T) {
	if err := CheckLineChange(ws("2.3.0-3.2598.3-0.0.170"), map[string]string{"bnk_line": `"2.4"`}); err == nil {
		t.Fatal("2.4 -> 2.3 must be refused too")
	}
}

// Same line, different build. This MUST be allowed — patching within a line is
// the ordinary upgrade path, and a guard that blocked it would be worse than the
// bug it prevents.
func TestAPatchWithinTheSameLineIsAllowed(t *testing.T) {
	if err := CheckLineChange(ws("2.3.9-9.9999.9-0.0.999"), map[string]string{"bnk_line": `"2.3"`}); err != nil {
		t.Errorf("a patch within 2.3 is the normal upgrade and must not be refused:\n%v", err)
	}
}

// Nothing installed yet: there is no prior line to contradict. A first run must
// not fail because a guard invented a disagreement.
func TestNoSnapshotIsSilent(t *testing.T) {
	if err := CheckLineChange(ws("2.4.0-EA"), nil); err != nil {
		t.Errorf("an absent snapshot means nothing is installed; stay silent:\n%v", err)
	}
	if err := CheckLineChange(ws("2.4.0-EA"), map[string]string{}); err != nil {
		t.Errorf("an empty snapshot means the same:\n%v", err)
	}
}

// Installed before bnk_line was rendered. The snapshot cannot say which line it
// was, and inferring it from a manifest that may itself have changed is how a
// guard produces a false accusation.
func TestASnapshotWithoutTheLineIsSilent(t *testing.T) {
	applied := map[string]string{"flo_namespace": `"f5-bnk"`}
	if err := CheckLineChange(ws("2.4.0-EA"), applied); err != nil {
		t.Errorf("a pre-bnk_line snapshot must not be accused of anything:\n%v", err)
	}
}

// A malformed manifest is BNKLine's error to report. This guard answering it too
// would give the same mistake two different messages.
func TestAMalformedManifestIsNotThisGuardsError(t *testing.T) {
	if err := CheckLineChange(ws("not-a-version"), map[string]string{"bnk_line": `"2.3"`}); err != nil {
		t.Errorf("a malformed manifest belongs to BNKLine, not here:\n%v", err)
	}
}
