package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ClusterOutputs is the persisted identity of a ROKS cluster that
// roksctl is tracking — written by `roksctl cluster up` (after a fresh
// create) or `roksctl cluster register` (after discovering an
// already-existing cluster), and read by `roksctl up` to deploy BNK
// trials onto an existing cluster without re-specifying everything in
// each trial's tfvars.
//
// Stored at ~/.roksctl/<workspace>/cluster-outputs.json. Treated as
// authoritative for downstream commands that need to reference the
// cluster — but explicit tfvars values always win over these.
type ClusterOutputs struct {
	ClusterName      string    `json:"cluster_name"`
	ClusterID        string    `json:"cluster_id"`
	Region           string    `json:"region"`
	ResourceGroupID  string    `json:"resource_group_id"`
	VPCID            string    `json:"vpc_id"`
	VPCName          string    `json:"vpc_name,omitempty"`
	SubnetIDs        []string  `json:"subnet_ids"`
	TransitGatewayID string    `json:"transit_gateway_id,omitempty"`
	RegistryCOSCRN   string    `json:"registry_cos_crn,omitempty"`
	RegistryCOSName  string    `json:"registry_cos_name,omitempty"`
	MasterURL        string    `json:"master_url,omitempty"`
	OpenShiftVersion string    `json:"openshift_version,omitempty"`
	Source           string    `json:"source"` // "cluster-up" or "cluster-register"
	RecordedAt       time.Time `json:"recorded_at"`
}

// ErrClusterOutputsMissing — workspace has no cluster-outputs.json yet.
// Sentinel so callers can distinguish "not yet registered" from a real
// I/O error.
var ErrClusterOutputsMissing = errors.New("workspace has no cluster-outputs.json — run `roksctl cluster up` or `roksctl cluster register` first")

// ReadClusterOutputs loads the JSON for `workspace`. Returns
// ErrClusterOutputsMissing if the file does not exist.
func ReadClusterOutputs(workspace string) (*ClusterOutputs, error) {
	p, err := WorkspaceClusterOutputsPath(workspace)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrClusterOutputsMissing
		}
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	var out ClusterOutputs
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	return &out, nil
}

// WriteClusterOutputs persists `out` for `workspace`. Stamps RecordedAt
// to now if zero. Creates the workspace dir if missing.
func WriteClusterOutputs(workspace string, out *ClusterOutputs) error {
	if out == nil {
		return errors.New("nil ClusterOutputs")
	}
	if out.RecordedAt.IsZero() {
		out.RecordedAt = time.Now().UTC()
	}
	p, err := WorkspaceClusterOutputsPath(workspace)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.WriteFile(p, body, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", p, err)
	}
	return nil
}

// DeleteClusterOutputs removes the workspace's cluster-outputs.json.
// No-op if the file is already absent.
func DeleteClusterOutputs(workspace string) error {
	p, err := WorkspaceClusterOutputsPath(workspace)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", p, err)
	}
	return nil
}
