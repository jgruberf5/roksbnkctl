package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The contract's own version. Bump ONLY when adding fields; a change that makes
// an existing file unreadable is not a bump, it is a break, and there is no
// migration path because clusters are never converted in place.
//
//	1 — the original handoff (no schema_version field written)
//	2 — adds network_mode, node_interfaces, vpc_cidr
const ContractSchemaVersion = 2

// Worker network attachment modes.
const (
	NetworkModeSingleNIC = "single-nic"
	NetworkModeMultiNIC  = "multi-nic"
)

// NodeInterface is one worker network attachment on a multi-NIC cluster.
type NodeInterface struct {
	Name     string `json:"name"`              // e.g. eth1
	SubnetID string `json:"subnet_id"`         // the subnet it attaches to
	Zone     string `json:"zone"`              // read from the subnet, not assumed
	Purpose  string `json:"purpose,omitempty"` // e.g. dataplane
}

// ClusterOutputs is the persisted identity of a ROKS cluster that
// roksbnkctl is tracking — written by `roksbnkctl cluster up` (after a fresh
// create) or `roksbnkctl cluster register` (after discovering an
// already-existing cluster), and read by `roksbnkctl up` to deploy BNK
// trials onto an existing cluster without re-specifying everything in
// each trial's tfvars.
//
// Stored at ~/.roksbnkctl/<workspace>/cluster-outputs.json. Treated as
// authoritative for downstream commands that need to reference the
// cluster — but explicit tfvars values always win over these.
type ClusterOutputs struct {
	ClusterName      string   `json:"cluster_name"`
	ClusterID        string   `json:"cluster_id"`
	Region           string   `json:"region"`
	ResourceGroupID  string   `json:"resource_group_id"`
	VPCID            string   `json:"vpc_id"`
	VPCName          string   `json:"vpc_name,omitempty"`
	SubnetIDs        []string `json:"subnet_ids"`
	TransitGatewayID string   `json:"transit_gateway_id,omitempty"`
	// TransitGatewayName is the cluster's transit gateway NAME (not id).
	// Sprint 28: the Testing phase's `module.testing` looks the gateway up
	// by name (data.ibm_tg_gateway.transit_gateway, name = var
	// testing_transit_gateway_name), so the standalone testing-phase run
	// needs the name in the handoff. Populated from the cluster phase's
	// roks_transit_gateway_name root output; may be empty on a
	// `cluster register` (the testing phase then falls back to the
	// config.yaml-rendered testing_transit_gateway_name).
	TransitGatewayName string `json:"transit_gateway_name,omitempty"`

	// SchemaVersion is the version of THIS contract, not of anything it
	// describes. Absent (0) means the file predates versioning and is read as
	// schema 1 — see ContractSchemaVersion.
	//
	// This file is the handoff between the cluster phase and every phase that
	// consumes a cluster. The two version axes it has to survive move
	// independently: the BNK release (which terraform layer and F5 CRDs) and the
	// IBM platform capability (single- vs multi-NIC ROKS). Extending it must
	// therefore always be ADDITIVE — a field that becomes mandatory invalidates
	// every cluster-outputs.json already on disk, all at once, with no migration
	// path, because clusters are never converted in place.
	SchemaVersion int `json:"schema_version,omitempty"`

	// NetworkMode is how the cluster's worker nodes are attached: NetworkModeSingleNIC
	// or NetworkModeMultiNIC. EMPTY MEANS SINGLE-NIC — every cluster built before
	// multi-NIC existed omits it, and they must keep working untouched.
	//
	// Decided once, at creation, and never changed: converting a cluster between
	// modes is not supported, so a workspace asking for a different mode than the
	// cluster was built with is refused rather than planned (it would be a silent
	// destroy-and-recreate of a running cluster).
	NetworkMode string `json:"network_mode,omitempty"`

	// VPCCIDR is the address block this tool actually used to CREATE the cluster
	// VPC. Empty when the VPC was adopted — the setting is ignored on that path, so
	// recording the configured value would record something that never applied —
	// and empty on every record written before schema 2.
	//
	// Recorded so the create-time warning fires on a real disagreement rather than
	// on the mere presence of the setting: a workspace that set vpc_cidr, built its
	// cluster, and has changed nothing since is the normal steady state, and
	// warning at it on every run trains people to ignore the warning.
	VPCCIDR string `json:"vpc_cidr,omitempty"`

	// NOT YET POPULATED BY ANY WRITER. Declared as part of contract v2 so the
	// shape is fixed before there is anything to put in it — multi-NIC ROKS has
	// not shipped, so no code path can fill this in honestly yet. Readers must
	// treat empty as "unknown", never as "this cluster has no extra interfaces":
	// on every cluster that exists today both are true, but on the first
	// multi-NIC cluster written by a build that still lacks the writer, only the
	// first would be.
	//
	// NodeInterfaces describes the worker network attachments a multi-NIC cluster
	// exposes, which the BNK phase needs to render F5SPKVlan attachments and
	// CNEInstance options against. Empty on single-NIC clusters, where the single
	// attachment is implied.
	NodeInterfaces   []NodeInterface `json:"node_interfaces,omitempty"`
	RegistryCOSCRN   string          `json:"registry_cos_crn,omitempty"`
	RegistryCOSName  string          `json:"registry_cos_name,omitempty"`
	MasterURL        string          `json:"master_url,omitempty"`
	OpenShiftVersion string          `json:"openshift_version,omitempty"`
	Source           string          `json:"source"` // "cluster-up" or "cluster-register"
	RecordedAt       time.Time       `json:"recorded_at"`
}

// ErrClusterOutputsMissing — workspace has no cluster-outputs.json yet.
// Sentinel so callers can distinguish "not yet registered" from a real
// I/O error.
var ErrClusterOutputsMissing = errors.New("workspace has no cluster-outputs.json — run `roksbnkctl cluster up` or `roksbnkctl cluster register` first")

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
	// Stamp the contract version on the way out. Callers construct ClusterOutputs
	// literals in several places; centralising it here means a new call site
	// cannot forget, and an older file is upgraded in place the next time the
	// cluster phase writes.
	if out != nil && out.SchemaVersion == 0 {
		out.SchemaVersion = ContractSchemaVersion
	}
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
	if err := os.MkdirAll(filepath.Dir(p), SecretDirMode); err != nil {
		return err
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.WriteFile(p, body, SecretFileMode); err != nil {
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

// Schema returns the contract version this record was written at.
//
// A file with no schema_version predates versioning and is schema 1 — that is
// every cluster-outputs.json on disk before multi-NIC existed. Reading it as 0
// would make the oldest, most numerous files look like the invalid case.
func (c *ClusterOutputs) Schema() int {
	if c == nil || c.SchemaVersion == 0 {
		return 1
	}
	return c.SchemaVersion
}

// Network reports how the cluster's workers are attached.
//
// The default is deliberate and load-bearing: absence means single-NIC, because
// every cluster built before multi-NIC omits the field. If this ever returned ""
// or an error for those, every existing workspace would break at once.
func (c *ClusterOutputs) Network() string {
	if c == nil || c.NetworkMode == "" {
		return NetworkModeSingleNIC
	}
	return c.NetworkMode
}

// IsMultiNIC is the readable form of the check callers actually want.
func (c *ClusterOutputs) IsMultiNIC() bool { return c.Network() == NetworkModeMultiNIC }
