package config

import (
	"strings"
	"testing"
)

func bumpWS(v string) *Workspace {
	w := &Workspace{}
	w.BNK.ManifestVersion = v
	return w
}

// The defect: a within-line bump renames the CNEManifest, terraform plans that
// as an in-place update, and the apply fails PARTWAY with BNK left down (#309).
func TestManifestBumpIsRefused(t *testing.T) {
	err := CheckManifestVersionChange(bumpWS("2.4.0"), map[string]string{
		"f5_bigip_k8s_manifest_version": `"2.4.0-EA"`,
	})
	if err == nil {
		t.Fatal("a 2.4.0-EA -> 2.4.0 bump was allowed; terraform cannot apply it and the failed apply leaves BNK down")
	}
	for _, want := range []string{"2.4.0-EA", "2.4.0", "bnk down", "bnk up"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal message is missing %q:\n%v", want, err)
		}
	}
}

// Unchanged version must be silent — otherwise every ordinary `bnk up` on an
// existing workspace would refuse.
func TestUnchangedManifestIsSilent(t *testing.T) {
	if err := CheckManifestVersionChange(bumpWS("2.4.0"), map[string]string{
		"f5_bigip_k8s_manifest_version": `"2.4.0"`,
	}); err != nil {
		t.Errorf("an unchanged manifest was refused, which would block every re-apply: %v", err)
	}
}

// No snapshot means nothing is installed. A first run must not fail.
func TestManifestBumpNoSnapshotIsSilent(t *testing.T) {
	if err := CheckManifestVersionChange(bumpWS("2.4.0"), nil); err != nil {
		t.Errorf("a first run with no snapshot was refused: %v", err)
	}
	if err := CheckManifestVersionChange(bumpWS("2.4.0"), map[string]string{}); err != nil {
		t.Errorf("an empty snapshot was refused: %v", err)
	}
}

// Installed before the version was recorded: the snapshot cannot say what was
// installed, and guessing produces a false accusation.
func TestMissingPriorVersionIsSilent(t *testing.T) {
	if err := CheckManifestVersionChange(bumpWS("2.4.0"), map[string]string{
		"some_other_var": `"x"`,
	}); err != nil {
		t.Errorf("a snapshot without the manifest version was refused: %v", err)
	}
}

// An empty configured version means "take the HCL default" — it is not a bump,
// and refusing would turn an optional field into a required one.
func TestEmptyConfiguredVersionIsSilent(t *testing.T) {
	if err := CheckManifestVersionChange(bumpWS(""), map[string]string{
		"f5_bigip_k8s_manifest_version": `"2.4.0-EA"`,
	}); err != nil {
		t.Errorf("an unset manifest version was refused: %v", err)
	}
}

// A CROSS-line change belongs to CheckLineChange, whose message explains the
// off-cluster GTM and licence damage. Two refusals for one edit is noise, and
// this one would bury the more important explanation.
func TestCrossLineChangeIsLeftToCheckLineChange(t *testing.T) {
	err := CheckManifestVersionChange(bumpWS("2.4.0"), map[string]string{
		"f5_bigip_k8s_manifest_version": `"2.3.1"`,
	})
	if err != nil {
		t.Errorf("a 2.3 -> 2.4 change was refused here; CheckLineChange owns it:\n%v", err)
	}

	// And prove CheckLineChange really does own it, so this silence is a
	// handoff rather than a hole.
	if lineErr := CheckLineChange(bumpWS("2.4.0"), map[string]string{
		"bnk_line": `"2.3"`,
	}); lineErr == nil {
		t.Error("CheckLineChange did not refuse the cross-line change either, so the handoff drops it entirely")
	}
}

// Values in the snapshot are HCL-quoted; a bare comparison would see
// `"2.4.0"` != `2.4.0` and refuse every re-apply.
func TestQuotedSnapshotValuesAreUnwrapped(t *testing.T) {
	if err := CheckManifestVersionChange(bumpWS("2.4.0"), map[string]string{
		"f5_bigip_k8s_manifest_version": `"2.4.0"`,
	}); err != nil {
		t.Errorf("quoted snapshot value was not unwrapped: %v", err)
	}
}
