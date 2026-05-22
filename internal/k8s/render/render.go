// Package render provides Go text/template rendering helpers for BNK manifest
// templates. Templates use {{ .Field }} syntax (not shell $VAR envsubst).
package render

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// CertChainVars holds the substitution variables for shared/bnk-cert-chain.yaml.
// All fields are derived from cluster.metadata.name at Phase 12 entry time.
type CertChainVars struct {
	SelfSignedIssuer string // <cluster>-selfsigned-cluster-issuer
	CACertName       string // <cluster>-ca
	CASecretName     string // <cluster>-ca-secret
	CAIssuer         string // <cluster>-ca-cluster-issuer
}

// CertChainVarsFromCluster derives the BNK cert chain template variables from
// the cluster intent. All variable names match aws-gpu-setup's convention so
// existing cert naming is consistent between bash and Go paths.
func CertChainVarsFromCluster(cl *intent.Cluster) CertChainVars {
	name := cl.Metadata.Name
	return CertChainVars{
		SelfSignedIssuer: name + "-selfsigned-cluster-issuer",
		CACertName:       name + "-ca",
		CASecretName:     name + "-ca-secret",
		CAIssuer:         name + "-ca-cluster-issuer",
	}
}

// Render executes a Go text/template given in tmpl with data as the dot-value
// and returns the rendered bytes. Returns a descriptive error on any parse or
// execution failure.
func Render(tmpl []byte, data interface{}) ([]byte, error) {
	t, err := template.New("manifest").Parse(string(tmpl))
	if err != nil {
		return nil, fmt.Errorf("render: parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render: execute template: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderCertChain renders the BNK cert chain template with vars derived from
// the cluster intent. Convenience wrapper over Render + CertChainVarsFromCluster.
func RenderCertChain(tmpl []byte, cl *intent.Cluster) ([]byte, error) {
	vars := CertChainVarsFromCluster(cl)
	return Render(tmpl, vars)
}

// FLOValuesVars holds the substitution variables for shared/flo-values.yaml.tmpl.
// All fields are derived from the cluster intent + slice-5 state at Phase 14 entry.
type FLOValuesVars struct {
	CAIssuer      string // <cluster>-ca-cluster-issuer
	FARSecretName string // far-secret
	JWT           string // raw JWT token contents (NOT base64)
	ClusterName   string // cl.Metadata.Name
}

// FLOValuesVarsFromCluster derives the FLO values template variables from the
// cluster intent. JWT is passed explicitly because it is file-read data, not
// derivable from the intent struct alone.
func FLOValuesVarsFromCluster(cl *intent.Cluster, jwt string) FLOValuesVars {
	cvars := CertChainVarsFromCluster(cl)
	return FLOValuesVars{
		CAIssuer:      cvars.CAIssuer,
		FARSecretName: "far-secret",
		JWT:           jwt,
		ClusterName:   cl.Metadata.Name,
	}
}

// RenderFLOValues renders the FLO values template with vars derived from
// the cluster intent and the raw JWT string.
func RenderFLOValues(tmpl []byte, cl *intent.Cluster, jwt string) ([]byte, error) {
	vars := FLOValuesVarsFromCluster(cl, jwt)
	return Render(tmpl, vars)
}

// OTELCertsVars holds the substitution variables for shared/otel-certs.yaml.
// All fields are derived from the cluster intent at Phase 15 entry.
type OTELCertsVars struct {
	OTELSvrCert     string // external-otelsvr
	OTELSvrSecret   string // external-otelsvr-secret
	OTELF5IngCert   string // external-f5ingotelsvr
	OTELF5IngSecret string // external-f5ingotelsvr-secret
	OperatorNS      string // f5-cne-core
	CAIssuer        string // <cluster>-ca-cluster-issuer
}

// OTELCertsVarsFromCluster derives the OTEL certs template variables from the
// cluster intent. Names match aws-gpu-setup's vars.env OTEL_* constants.
func OTELCertsVarsFromCluster(cl *intent.Cluster) OTELCertsVars {
	cvars := CertChainVarsFromCluster(cl)
	// #nosec G101 -- these are k8s resource names, not credential values
	return OTELCertsVars{
		OTELSvrCert:     "external-otelsvr",
		OTELSvrSecret:   "external-otelsvr-secret",
		OTELF5IngCert:   "external-f5ingotelsvr",
		OTELF5IngSecret: "external-f5ingotelsvr-secret",
		OperatorNS:      "f5-cne-core",
		CAIssuer:        cvars.CAIssuer,
	}
}

// RenderOTELCerts renders the OTEL certs template with vars derived from
// the cluster intent.
func RenderOTELCerts(tmpl []byte, cl *intent.Cluster) ([]byte, error) {
	vars := OTELCertsVarsFromCluster(cl)
	return Render(tmpl, vars)
}

// ─── cloud-network-mapping ConfigMap ─────────────────────────────────────────

// CloudNetworkMappingVars holds the substitution variables for
// shared/cloud-network-mapping.yaml.tmpl. All fields are derived from the
// cluster intent + slice-03/18 state at Phase 19 entry.
type CloudNetworkMappingVars struct {
	AZ           string // first AZ from cl.Network.AZs
	MGMTSubnet   string // MGMT_SUBNET (= first public subnet ID)
	BNKExtSubnet string // BNK_EXT_SUBNET from state
	BNKIntSubnet string // BNK_INT_SUBNET from state
	MGMTCidr     string // first public subnet CIDR
	BNKExtCidr   string // cl.Network.DataPath.External.Cidr
	BNKIntCidr   string // cl.Network.DataPath.Internal.Cidr
}

// RenderCloudNetworkMapping renders the cloud-network-mapping ConfigMap
// template. It reads subnet IDs from state.env (written by Phase 03) and CIDRs
// from the cluster intent. Returns an error if required state keys or intent
// fields are missing.
func RenderCloudNetworkMapping(tmpl []byte, cl *intent.Cluster, getter func(string) string) ([]byte, error) {
	if len(cl.Network.AZs) == 0 {
		return nil, fmt.Errorf("render: network.azs is empty")
	}
	if len(cl.Network.Subnets.Public) == 0 {
		return nil, fmt.Errorf("render: network.subnets.public is empty")
	}
	if cl.Network.DataPath == nil {
		return nil, fmt.Errorf("render: network.dataPath is nil (required for host-device pattern)")
	}
	mgmtSubnet := getter("MGMT_SUBNET")
	if mgmtSubnet == "" {
		return nil, fmt.Errorf("render: MGMT_SUBNET not in state (Phase 03 must run first)")
	}
	bnkExtSubnet := getter("BNK_EXT_SUBNET")
	if bnkExtSubnet == "" {
		return nil, fmt.Errorf("render: BNK_EXT_SUBNET not in state (Phase 03 must run first)")
	}
	bnkIntSubnet := getter("BNK_INT_SUBNET")
	if bnkIntSubnet == "" {
		return nil, fmt.Errorf("render: BNK_INT_SUBNET not in state (Phase 03 must run first)")
	}
	vars := CloudNetworkMappingVars{
		AZ:           cl.Network.AZs[0],
		MGMTSubnet:   mgmtSubnet,
		BNKExtSubnet: bnkExtSubnet,
		BNKIntSubnet: bnkIntSubnet,
		MGMTCidr:     cl.Network.Subnets.Public[0].CIDR,
		BNKExtCidr:   cl.Network.DataPath.External.CIDR,
		BNKIntCidr:   cl.Network.DataPath.Internal.CIDR,
	}
	return Render(tmpl, vars)
}

// ─── IRSA ServiceAccount ─────────────────────────────────────────────────────

// IRSASAVars holds the substitution variables for shared/irsa-sa.yaml.tmpl.
type IRSASAVars struct {
	InstanceNameCR string // <cluster>-bnk
	CneIRSARoleARN string // CNE_IRSA_ROLE_ARN from state
}

// RenderIRSASA renders the IRSA ServiceAccount template. Returns an error if
// CNE_IRSA_ROLE_ARN is not present in state (Phase 18 must have run first).
func RenderIRSASA(tmpl []byte, cl *intent.Cluster, getter func(string) string) ([]byte, error) {
	roleARN := getter("CNE_IRSA_ROLE_ARN")
	if roleARN == "" {
		return nil, fmt.Errorf("render: CNE_IRSA_ROLE_ARN not in state (Phase 18 must run first)")
	}
	vars := IRSASAVars{
		InstanceNameCR: cl.Metadata.Name + "-bnk",
		CneIRSARoleARN: roleARN,
	}
	return Render(tmpl, vars)
}

// ─── NetworkAttachmentDefinitions (host-device) ───────────────────────────────

// NADVars holds the substitution variables for
// host-device/network-attachment-defs.yaml.tmpl.
// For the host-device pattern the interface names and NAD names are
// architecture constants (Architect: not operator knobs).
type NADVars struct {
	Namespace       string // target namespace for the NADs
	ExternalNADName string // external-netdevice
	InternalNADName string // internal-netdevice
	ExternalIFName  string // ens8
	InternalIFName  string // ens7
}

// RenderNADs renders the host-device NADs template for the given namespace.
// The interface names and NAD names are constants for the host-device pattern.
func RenderNADs(tmpl []byte, namespace string) ([]byte, error) {
	vars := NADVars{
		Namespace:       namespace,
		ExternalNADName: "external-netdevice",
		InternalNADName: "internal-netdevice",
		ExternalIFName:  "ens8",
		InternalIFName:  "ens7",
	}
	return Render(tmpl, vars)
}

// ─── CNEInstance CR ────────────────────────────────────────────────────────

// cneInstanceNamespace is the k8s namespace for CNEInstance and related resources.
// Mirrors phases.InstanceNamespace — duplicated here to avoid an import cycle
// (phases imports render; render cannot import phases).
const cneInstanceNamespace = "f5-cne-system"

// CNEInstanceVars holds the substitution variables for
// shared/cneinstance.yaml.tmpl. Fields are split into three categories per
// the Architect review (slice-07 reviews/architect.md):
//   - Operator-knobs: sourced from cl.Bnk.* (set by cluster.yaml, defaults applied).
//   - State-derived: sourced from st.Get() (written by earlier phases).
//   - Hardcoded constants: baked into the template, NOT templated.
type CNEInstanceVars struct {
	// Operator-knobs (cluster.yaml bnk:)
	DeploymentSize   string // default "Small"
	StorageClassName string // default "gp3"
	ManifestVersion  string // default "2.21.13"
	TmmMtu           int    // default 9000
	TmmCpu           string // default "4"
	TmmMemory        string // default "16Gi"
	TmmHugepages     string // default "8Gi"
	PalCpuSet        string // default "0-3"

	// State-derived
	InstanceNameCR      string // <cluster>-bnk
	InstanceNS          string // f5-cne-system
	LabName             string // cluster.metadata.name
	CAIssuer            string // <cluster>-ca-cluster-issuer
	FARSecretName       string // far-secret
	VPCID               string // VPC_ID from state
	AWSRegion           string // cl.Metadata.Region
	ExternalNAD         string // external-netdevice
	InternalNAD         string // internal-netdevice
	ExternalIFName      string // ens8
	InternalIFName      string // ens7
	ExternalPCI         string // 0000:00:08.0
	InternalPCI         string // 0000:00:07.0
	CloudHostDeviceName string // ens8
	CloudHostDeviceTag  string // f5-cne-device
}

// RenderCNEInstance renders the CNEInstance CR template with vars derived
// from the cluster intent and state.
// Requires VPC_ID in state (Phase 02). All BnkSpec fields must have defaults
// applied (intent.Load does this).
func RenderCNEInstance(tmpl []byte, cl *intent.Cluster, getter func(string) string) ([]byte, error) {
	if cl.Bnk == nil {
		return nil, fmt.Errorf("render: cl.Bnk is nil — bnk: block required in cluster.yaml")
	}
	vpcID := getter("VPC_ID")
	if vpcID == "" {
		return nil, fmt.Errorf("render: VPC_ID not in state (Phase 02 must run first)")
	}
	cvars := CertChainVarsFromCluster(cl)
	vars := CNEInstanceVars{
		// Operator-knobs
		DeploymentSize:   cl.Bnk.DeploymentSize,
		StorageClassName: cl.Bnk.StorageClassName,
		ManifestVersion:  cl.Bnk.ManifestVersion,
		TmmMtu:           cl.Bnk.TmmMtu,
		TmmCpu:           cl.Bnk.TmmCpu,
		TmmMemory:        cl.Bnk.TmmMemory,
		TmmHugepages:     cl.Bnk.TmmHugepages,
		PalCpuSet:        cl.Bnk.PalCpuSet,
		// State-derived
		InstanceNameCR:      cl.Metadata.Name + "-bnk",
		InstanceNS:          cneInstanceNamespace,
		LabName:             cl.Metadata.Name,
		CAIssuer:            cvars.CAIssuer,
		FARSecretName:       "far-secret",
		VPCID:               vpcID,
		AWSRegion:           cl.Metadata.Region,
		ExternalNAD:         "external-netdevice",
		InternalNAD:         "internal-netdevice",
		ExternalIFName:      "ens8",
		InternalIFName:      "ens7",
		ExternalPCI:         "0000:00:08.0",
		InternalPCI:         "0000:00:07.0",
		CloudHostDeviceName: "ens8",
		CloudHostDeviceTag:  "f5-cne-device",
	}
	return Render(tmpl, vars)
}

// ─── License CR ───────────────────────────────────────────────────────────────

// LicenseCRVars holds the substitution variables for shared/license-cr.yaml.tmpl.
type LicenseCRVars struct {
	LabName string // cluster.metadata.name
	JWT     string // raw JWT token string (file contents, whitespace-trimmed)
}

// RenderLicenseCR renders the License CR template. jwt must be the raw token
// string (already read from disk and whitespace-trimmed by the caller).
// The caller (Phase 23) is responsible for file I/O so that dry-run mode can
// pass a placeholder string without a real file on disk.
func RenderLicenseCR(tmpl []byte, cl *intent.Cluster, jwt string) ([]byte, error) {
	vars := LicenseCRVars{
		LabName: cl.Metadata.Name,
		JWT:     jwt,
	}
	return Render(tmpl, vars)
}

// ─── F5SPKVlan + GatewayClass (slice-10) ───────────────────────────────────

// F5SPKVlanVars holds the substitution variables for host-device/f5spkvlan.yaml.tmpl.
type F5SPKVlanVars struct {
	InstanceNS         string // f5-cne-system (matches CNEInstance namespace)
	TmmExtSelfIP       string // e.g. 10.0.10.240
	TmmIntSelfIP       string // e.g. 10.0.20.240
	TmmSelfIPPrefixLen int    // typically 24
}

// RenderF5SPKVlan renders the F5SPKVlan CR template for the host-device
// pattern. Caller supplies the SelfIP values from cl.Network.DataPath.SelfIPs
// (auto-derived by intent.applyDefaults when not explicitly set).
func RenderF5SPKVlan(tmpl []byte, selfExt, selfInt string, prefixLen int) ([]byte, error) {
	vars := F5SPKVlanVars{
		InstanceNS:         cneInstanceNamespace,
		TmmExtSelfIP:       selfExt,
		TmmIntSelfIP:       selfInt,
		TmmSelfIPPrefixLen: prefixLen,
	}
	return Render(tmpl, vars)
}

// GatewayClassVars holds the substitution variables for host-device/gatewayclass.yaml.tmpl.
type GatewayClassVars struct {
	GwcName    string // <cluster>-gatewayclass
	LabName    string // cl.Metadata.Name
	InstanceNS string // f5-cne-system
}

// RenderGatewayClass renders the GatewayClass template for the host-device pattern.
func RenderGatewayClass(tmpl []byte, cl *intent.Cluster) ([]byte, error) {
	vars := GatewayClassVars{
		GwcName:    cl.Metadata.Name + "-gatewayclass",
		LabName:    cl.Metadata.Name,
		InstanceNS: cneInstanceNamespace,
	}
	return Render(tmpl, vars)
}
