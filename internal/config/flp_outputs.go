package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// FLPOutputs is the persisted handoff from the optional FLP phase to the BNK
// phase — written by `roksbnkctl flp up` after apply, read by `roksbnkctl bnk up`
// when license_mode is f5licenseproxy. It carries only what the BNK License CR
// needs to point at the in-cluster F5 License Proxy: the proxy's root CA (written
// into the CWC's licenseserver-rootca Secret) and its service endpoint (the
// teem*Url base). No secrets — the CA is a public certificate.
//
// Stored at ~/.roksbnkctl/<workspace>/flp-outputs.json.
type FLPOutputs struct {
	// RootCAB64 is the base64-encoded PEM of the FLP root CA (the terraform
	// flp_root_ca output is already base64; stored verbatim).
	RootCAB64 string `json:"root_ca_b64"`
	// Endpoint is the FLP service base URL (e.g.
	// https://f5-license-proxy.<ns>.svc.cluster.local:8443). In-cluster only — that
	// name does not resolve anywhere else.
	Endpoint string `json:"endpoint"`

	// ExternalEndpoint is the address a BNK install in a DIFFERENT cluster dials,
	// e.g. https://10.240.64.5:30001. Set only when the proxy was exposed with
	// `flp up --add-node-port-access`; empty for an in-cluster-only proxy.
	//
	// Copy this (with RootCAB64) into the consuming workspace's bnk.flp.external.
	ExternalEndpoint string `json:"external_endpoint,omitempty"`

	// ExternalEndpoints lists every worker-node URL the proxy answers on. All are IP
	// SANs on its certificate, so any of them is a valid bnk.flp.external.url — use
	// another if the first node is drained.
	ExternalEndpoints []string `json:"external_endpoints,omitempty"`

	// FloatingIP is the standalone FLP VSI's operator floating IP, when one was
	// attached (bnk.flp.vsi.floating_ip, default true). It is a MANAGEMENT address —
	// `roksbnkctl flp status` prefers it so the status + web UI are reachable from a
	// machine outside the VPC. Empty for an in-cluster FLP or when opted out.
	FloatingIP string `json:"floating_ip,omitempty"`

	// Namespace the FLP was installed into.
	Namespace  string    `json:"namespace,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// ErrFLPOutputsMissing — workspace has no flp-outputs.json (FLP not deployed).
var ErrFLPOutputsMissing = errors.New("workspace has no flp-outputs.json — run `roksbnkctl flp up` first (license_mode f5licenseproxy needs a deployed F5 License Proxy)")

// ReadFLPOutputs loads the JSON for `workspace`, or ErrFLPOutputsMissing.
func ReadFLPOutputs(workspace string) (*FLPOutputs, error) {
	p, err := WorkspaceFLPOutputsPath(workspace)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrFLPOutputsMissing
		}
		return nil, err
	}
	var out FLPOutputs
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	return &out, nil
}

// WriteFLPOutputs persists the FLP handoff for `workspace` (mode 0600 — the CA
// is public, but the file lives alongside secrets, so keep the tree consistent).
func WriteFLPOutputs(workspace string, out *FLPOutputs) error {
	p, err := WorkspaceFLPOutputsPath(workspace)
	if err != nil {
		return err
	}
	out.RecordedAt = time.Now().UTC()
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, body, 0o600)
}

// DeleteFLPOutputs removes the handoff (on `flp down`). Absent file → no error.
func DeleteFLPOutputs(workspace string) error {
	p, err := WorkspaceFLPOutputsPath(workspace)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
