package config

import (
	"errors"
	"testing"
)

// The BNK line is DERIVED, not configured — that is what keeps this change from
// touching config.yaml at all. If derivation is wrong, the tool silently selects
// the wrong terraform layer and the wrong CRDs.
func TestBNKLine(t *testing.T) {
	cases := []struct {
		manifest string
		want     string
		wantErr  bool
	}{
		{"2.3.0-3.2598.3-0.0.170", "2.3", false},
		{"2.4.0-1.2.3-0.0.1", "2.4", false},
		{"2.10.0-x", "2.10", false}, // two-digit minor must not truncate
		{"3.0", "3.0", false},
		// UNSET is not unknown. The field has always been optional — absent means
		// the HCL default installs — so the line is derivable and must be
		// derived. Erroring here made an optional field required and broke
		// `bnk up` for every workspace that never set it. Caught by the e2e.
		{"", "2.3", false},
		{"garbage", "", true}, // meant something, cannot be honoured — still fatal
		{"v2.3.0", "", true},  // a leading v is not the published shape
	}
	for _, c := range cases {
		ws := &Workspace{BNK: BNKCfg{ManifestVersion: c.manifest}}
		got, err := ws.BNKLine()
		if c.wantErr {
			if err == nil {
				t.Errorf("manifest %q: expected an error, got %q", c.manifest, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("manifest %q: %v", c.manifest, err)
		} else if got != c.want {
			t.Errorf("manifest %q: line = %q, want %q", c.manifest, got, c.want)
		}
	}
}

// Absence means single-nic. Every cluster built so far omits it, so any other
// default breaks every existing workspace at once.
func TestClusterNetworkModeDefaults(t *testing.T) {
	if got := (&Workspace{}).ClusterNetworkMode(); got != NetworkModeSingleNIC {
		t.Errorf("unset network_mode = %q, want %q", got, NetworkModeSingleNIC)
	}
	if got := (*Workspace)(nil).ClusterNetworkMode(); got != NetworkModeSingleNIC {
		t.Errorf("nil workspace = %q, want %q", got, NetworkModeSingleNIC)
	}
	ws := &Workspace{Cluster: ClusterCfg{NetworkMode: " multi-nic "}}
	if got := ws.ClusterNetworkMode(); got != NetworkModeMultiNIC {
		t.Errorf("whitespace not trimmed: %q", got)
	}
}

// The contract's defaults are the backward-compatibility guarantee: a file with
// no schema_version is the ORIGINAL handoff, and absence of network_mode is
// single-nic. Reading either as zero/empty would invalidate every file on disk.
func TestContractDefaults(t *testing.T) {
	old := &ClusterOutputs{ClusterID: "c1"} // as written before either field existed
	if got := old.Schema(); got != 1 {
		t.Errorf("unversioned file schema = %d, want 1", got)
	}
	if got := old.Network(); got != NetworkModeSingleNIC {
		t.Errorf("unversioned file network = %q, want %q", got, NetworkModeSingleNIC)
	}
	if old.IsMultiNIC() {
		t.Error("an unversioned file must never read as multi-nic")
	}
	if (*ClusterOutputs)(nil).Schema() != 1 || (*ClusterOutputs)(nil).Network() != NetworkModeSingleNIC {
		t.Error("nil must degrade to the oldest-compatible reading, not panic")
	}
}

// The matrix is the thing that turns "unsupported" from an apply failure into a
// plan-time refusal, so its three distinct failures must stay distinguishable.
func TestCheckSupported(t *testing.T) {
	if err := CheckSupported("2.3", NetworkModeSingleNIC, 1); err != nil {
		t.Errorf("2.3 + single-nic + schema 1 must be supported: %v", err)
	}
	if err := CheckSupported("2.4", NetworkModeMultiNIC, 2); err != nil {
		t.Errorf("2.4 + multi-nic + schema 2 must be supported: %v", err)
	}
	// 2.4 must still drive clusters built before multi-NIC existed, or adopting
	// 2.4 would force everyone to rebuild.
	if err := CheckSupported("2.4", NetworkModeSingleNIC, 1); err != nil {
		t.Errorf("2.4 must drive an existing single-nic cluster: %v", err)
	}
	if err := CheckSupported("2.3", NetworkModeMultiNIC, 2); err == nil {
		t.Error("2.3 does not express multi-nic and must refuse it")
	}
	// An unknown line is reported through a SENTINEL, because callers must be
	// able to tell "I have no information about this release" from "this pairing
	// is wrong" — the first is survivable and the second is not.
	err := CheckSupported("9.9", NetworkModeSingleNIC, 1)
	if err == nil {
		t.Error("an unknown BNK line must be reported, not silently accepted")
	} else if !errors.Is(err, ErrUnknownLine) {
		t.Errorf("unknown line must be distinguishable via ErrUnknownLine, got %v", err)
	}
	// The unsupported-PAIRING failure must NOT be that sentinel, or callers that
	// tolerate an unknown line would tolerate a known-wrong combination too.
	if err := CheckSupported("2.3", NetworkModeMultiNIC, 2); errors.Is(err, ErrUnknownLine) {
		t.Error("a known line with an unsupported mode must not read as an unknown line")
	}
}

// The matrix ships as data; a malformed or empty file would make every check
// vacuous, which is worse than failing.
func TestSupportMatrixLoads(t *testing.T) {
	lines, err := SupportedLines()
	if err != nil {
		t.Fatalf("embedded matrix must parse: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("the matrix is empty — every support check would pass vacuously")
	}
	for _, l := range lines {
		if l.BNK == "" || len(l.NetworkModes) == 0 || len(l.Contract) == 0 {
			t.Errorf("incomplete row %+v — a row missing modes or contract silently permits nothing", l)
		}
	}
}

// The exact shape the e2e hit: a workspace built by `init --non-interactive`
// from an environment that never mentioned a manifest version. That is a
// SUPPORTED configuration — the HCL default installs — and it must reach
// `bnk up`, not be refused by a version check.
func TestUnsetManifestVersionIsSupportedEndToEnd(t *testing.T) {
	ws := &Workspace{} // no bnk: block at all, as init --non-interactive writes

	line, err := ws.BNKLine()
	if err != nil {
		t.Fatalf("an unset manifest version is the default, not an error: %v", err)
	}
	if line != "2.3" {
		t.Errorf("line = %q, want the default's line 2.3", line)
	}
	// And the pairing it produces must actually be supported, or the guard still
	// refuses for a second reason.
	if err := CheckSupported(line, NetworkModeSingleNIC, ContractSchemaVersion); err != nil {
		t.Errorf("default line + single-nic + current contract must be supported: %v", err)
	}
}
