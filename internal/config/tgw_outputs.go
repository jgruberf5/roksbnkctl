package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// TGWOutputs is the persisted record of the optional Transit Gateway connection
// phase — written by `roksbnkctl tgw connect` after apply. It carries the gateway
// identity and this cluster's connection identity so `tgw status` and
// `cluster config` can report the attachment without a live IBM call. The live
// connection STATE (attached / pending / …) is queried separately.
//
// Stored at ~/.roksbnkctl/<workspace>/tgw-outputs.json.
type TGWOutputs struct {
	// GatewayID / GatewayName / GatewayCRN identify the shared Transit Gateway.
	// Recorded whether the operator supplied a name or an id — both are resolved.
	GatewayID   string `json:"gateway_id"`
	GatewayName string `json:"gateway_name"`
	GatewayCRN  string `json:"gateway_crn,omitempty"`

	// ConnectionID / ConnectionName identify THIS cluster's connection on the
	// gateway. Each workspace owns its own connection to a shared gateway.
	ConnectionID   string `json:"connection_id"`
	ConnectionName string `json:"connection_name"`

	// VPCID / VPCCRN identify the cluster VPC that was attached. The CRN is the
	// connection's network_id, recorded so `tgw status` can match the live
	// connection without a separate VPC lookup.
	VPCID  string `json:"vpc_id,omitempty"`
	VPCCRN string `json:"vpc_crn,omitempty"`

	RecordedAt time.Time `json:"recorded_at"`
}

// ErrTGWOutputsMissing — workspace has no tgw-outputs.json (not connected).
var ErrTGWOutputsMissing = errors.New("workspace has no tgw-outputs.json — run `roksbnkctl tgw connect <name-or-id>` first")

// ReadTGWOutputs loads the JSON for `workspace`, or ErrTGWOutputsMissing.
func ReadTGWOutputs(workspace string) (*TGWOutputs, error) {
	p, err := WorkspaceTGWOutputsPath(workspace)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrTGWOutputsMissing
		}
		return nil, err
	}
	var out TGWOutputs
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	return &out, nil
}

// WriteTGWOutputs persists the record for `workspace` (0600 — no secrets, but
// consistent with the other outputs files).
func WriteTGWOutputs(workspace string, out *TGWOutputs) error {
	p, err := WorkspaceTGWOutputsPath(workspace)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(body, '\n'), 0o600)
}

// DeleteTGWOutputs removes the record (on `tgw disconnect`). Missing is not an error.
func DeleteTGWOutputs(workspace string) error {
	p, err := WorkspaceTGWOutputsPath(workspace)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
