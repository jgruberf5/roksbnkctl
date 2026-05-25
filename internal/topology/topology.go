// Package topology builds and renders the whole-cluster data-path topology
// from a cluster.yaml (intent) and the cluster's state.env — no AWS or
// Kubernetes API calls required.
//
// Two render formats are supported:
//   - ASCII box-drawing (envdiagram aesthetic, ~70 cols)
//   - Mermaid graph TD (for docs / forge embedding)
package topology

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// SubnetInfo carries one subnet's CIDR, AZ, and provisioned ID (empty when
// not yet provisioned).
type SubnetInfo struct {
	CIDR string
	AZ   string
	ID   string // empty before provisioning
}

// NodeGroupInfo captures the first node group's sizing + the live TMM node
// name (empty before Phase 16 runs).
type NodeGroupInfo struct {
	Name         string
	InstanceType string
	DesiredSize  int
	MinSize      int
	MaxSize      int
	TMMNode      string // live from state; empty pre-provisioning
}

// TMMInfo holds the TMM pod's data-plane identity: external VLAN self-IP +
// interface name, and internal VLAN self-IP + interface name.
type TMMInfo struct {
	ExtSelfIP string
	ExtIfname string
	IntSelfIP string
	IntIfname string
}

// JumphostInfo carries the jumphost instance ID and its two ENI IPs (mgmt
// public subnet + BNK_EXT data-plane subnet). EICE ID is for EC2 Instance
// Connect Endpoint access.
type JumphostInfo struct {
	InstanceID string
	MgmtIP     string
	BnkExtIP   string
	EICEID     string
}

// Model is the topology data model. All string fields are "" when the
// corresponding resource has not yet been provisioned. Callers should render
// "" as "(not provisioned)" so the diagram is useful before `awsbnkctl up`.
type Model struct {
	// Identity
	ClusterName string
	Region      string
	Pattern     string // e.g. "host-device"

	// VPC
	VPCCidr string
	VPCID   string // empty pre-provisioning

	// Gateways
	IGWID   string
	NatGWID string

	// Subnets
	PublicSubnets  []SubnetInfo
	PrivateSubnets []SubnetInfo
	DataExtSubnet  SubnetInfo // BNK_EXT (TMM external VLAN)
	DataIntSubnet  SubnetInfo // BNK_INT (TMM internal VLAN)

	// EKS node group (first group only for the topology view)
	NodeGroup NodeGroupInfo

	// TMM data-plane identity
	TMM TMMInfo

	// CSRC egress overlay daemonset
	// Always present on host-device clusters; note is descriptive.
	CSRCNote string

	// Jumphost
	Jumphost JumphostInfo

	// Gateway API
	GatewayClassName string
	VIPRange         string // derived from DataExtSubnet.CIDR
}

// np returns v when non-empty, otherwise "(not provisioned)".
func np(v string) string {
	if v == "" {
		return "(not provisioned)"
	}
	return v
}

// vipFromCIDR derives the default Gateway VIP from a /24 CIDR.
// Returns "" on any failure.
func vipFromCIDR(cidr string) string {
	slash := strings.IndexByte(cidr, '/')
	if slash <= 0 {
		return ""
	}
	parts := strings.Split(cidr[:slash], ".")
	if len(parts) != 4 {
		return ""
	}
	parts[3] = "100"
	return strings.Join(parts, ".") + " (default VIP)"
}

// Build populates a Model from intent first, then overlays non-empty values
// from state. State wins whenever it has a non-empty value for a field; intent
// fills in everything that is deterministic before provisioning (CIDRs, AZs,
// node group sizing). Build is pure and deterministic — no I/O.
func Build(cl *intent.Cluster, st *state.State) Model {
	// Intent-only fields.
	m := Model{
		ClusterName: cl.Metadata.Name,
		Region:      cl.Metadata.Region,
		Pattern:     cl.Pattern,
		VPCCidr:     cl.Network.VPCCidr,
	}

	// Public subnets from intent.
	for _, s := range cl.Network.Subnets.Public {
		m.PublicSubnets = append(m.PublicSubnets, SubnetInfo{CIDR: s.CIDR, AZ: s.AZ})
	}
	// Private subnets from intent.
	for _, s := range cl.Network.Subnets.Private {
		m.PrivateSubnets = append(m.PrivateSubnets, SubnetInfo{CIDR: s.CIDR, AZ: s.AZ})
	}

	// Data-path subnets from intent.
	if dp := cl.Network.DataPath; dp != nil {
		m.DataExtSubnet = SubnetInfo{CIDR: dp.External.CIDR, AZ: dp.External.AZ}
		m.DataIntSubnet = SubnetInfo{CIDR: dp.Internal.CIDR, AZ: dp.Internal.AZ}
		// Self-IPs from intent (may be auto-derived).
		if dp.SelfIPs != nil {
			m.TMM.ExtSelfIP = dp.SelfIPs.External
			m.TMM.IntSelfIP = dp.SelfIPs.Internal
		}
	}

	// Node group from intent (first group).
	if cl.ClusterSpec != nil && len(cl.ClusterSpec.NodeGroups) > 0 {
		ng := cl.ClusterSpec.NodeGroups[0]
		m.NodeGroup = NodeGroupInfo{
			Name:         ng.Name,
			InstanceType: ng.InstanceType,
			DesiredSize:  ng.DesiredSize,
			MinSize:      ng.MinSize,
			MaxSize:      ng.MaxSize,
		}
	}

	// CSRC note (always present for host-device; informative for other patterns).
	if cl.Pattern == "host-device" {
		m.CSRCNote = "(host-device: CSRC daemonset on each node)"
	}

	// VIP range derived from DataExtSubnet.
	if m.DataExtSubnet.CIDR != "" {
		m.VIPRange = vipFromCIDR(m.DataExtSubnet.CIDR)
	}

	// Overlay non-empty state values — state wins.
	if st == nil {
		return m
	}

	overlay := func(field *string, key string) {
		if v := st.Get(key); v != "" {
			*field = v
		}
	}

	overlay(&m.VPCID, "VPC_ID")
	overlay(&m.IGWID, "IGW_ID")
	overlay(&m.NatGWID, "NAT_GW_ID")

	// Public subnet IDs (state stores as comma-separated list).
	if pubIDs := st.Get("PUBLIC_SUBNETS"); pubIDs != "" {
		ids := strings.Split(pubIDs, ",")
		for i, id := range ids {
			if i < len(m.PublicSubnets) {
				m.PublicSubnets[i].ID = strings.TrimSpace(id)
			}
		}
	}
	// Private subnet IDs.
	if privIDs := st.Get("PRIVATE_SUBNETS"); privIDs != "" {
		ids := strings.Split(privIDs, ",")
		for i, id := range ids {
			if i < len(m.PrivateSubnets) {
				m.PrivateSubnets[i].ID = strings.TrimSpace(id)
			}
		}
	}

	overlay(&m.DataExtSubnet.ID, "BNK_EXT_SUBNET")
	overlay(&m.DataIntSubnet.ID, "BNK_INT_SUBNET")

	// Node group live state.
	overlay(&m.NodeGroup.Name, "NODEGROUP_DEFAULT_NAME")
	overlay(&m.NodeGroup.TMMNode, "TMM_NODE_NAME")

	// TMM state.
	overlay(&m.TMM.ExtSelfIP, "TMM_EXT_SELFIP")
	overlay(&m.TMM.IntSelfIP, "TMM_INT_SELFIP")
	overlay(&m.TMM.ExtIfname, "EXTERNAL_IFNAME")
	overlay(&m.TMM.IntIfname, "INTERNAL_IFNAME")

	// Jumphost.
	overlay(&m.Jumphost.InstanceID, "JUMPHOST_INSTANCE_ID")
	overlay(&m.Jumphost.MgmtIP, "JUMPHOST_MGMT_ENI_IP")
	overlay(&m.Jumphost.BnkExtIP, "JUMPHOST_BNK_EXT_ENI_IP")
	overlay(&m.Jumphost.EICEID, "JUMPHOST_EICE_ID")

	// Gateway API.
	overlay(&m.GatewayClassName, "GATEWAYCLASS_NAME")

	return m
}

// RenderASCII renders a single box-drawing diagram of the cluster data path
// in the envdiagram aesthetic (~70 cols). Fields that are not yet provisioned
// are shown as "(not provisioned)".
func RenderASCII(m Model) string {
	// innerW is the number of rune-columns between the left and right border
	// characters. Every row must fill exactly innerW rune-columns so that the
	// right border │ lands in the same terminal column on every line.
	const innerW = 70

	top := "┌" + strings.Repeat("─", innerW) + "┐"
	bot := "└" + strings.Repeat("─", innerW) + "┘"

	// boxLine returns "│" + content padded/truncated to exactly innerW
	// rune-columns + "│".  Content is prefixed with a single space so text
	// never touches the left border.  Multi-byte runes are counted correctly
	// via utf8.RuneCountInString; lines that would overflow are truncated with
	// an ellipsis instead.
	boxLine := func(s string) string {
		inner := " " + s // one leading space
		w := utf8.RuneCountInString(inner)
		switch {
		case w < innerW:
			inner += strings.Repeat(" ", innerW-w)
		case w > innerW:
			// Truncate to innerW-1 runes + "…" (ellipsis = 1 rune).
			runes := []rune(inner)
			inner = string(runes[:innerW-1]) + "…"
		}
		return "│" + inner + "│"
	}
	blank := boxLine("")
	sep := "├" + strings.Repeat("─", innerW) + "┤"

	// Subnet rows helper.
	subnetRows := func(subnets []SubnetInfo, label string) []string {
		var rows []string
		for i, s := range subnets {
			idStr := ""
			if s.ID != "" {
				idStr = "  " + s.ID
			}
			rows = append(rows, boxLine(fmt.Sprintf("  %s[%d]: %s (%s)%s", label, i, s.CIDR, s.AZ, idStr)))
		}
		return rows
	}

	var lines []string
	lines = append(lines, top)

	// Header: identity
	lines = append(lines, boxLine(fmt.Sprintf("cluster: %-20s  region: %s", m.ClusterName, m.Region)))
	lines = append(lines, boxLine(fmt.Sprintf("pattern: %-20s  vpc:    %s", np(m.Pattern), m.VPCCidr)))
	if m.VPCID != "" {
		lines = append(lines, boxLine(fmt.Sprintf("vpc-id: %s", m.VPCID)))
	}
	lines = append(lines, blank)
	lines = append(lines, boxLine(fmt.Sprintf("igw: %-30s  nat-gw: %s", np(m.IGWID), np(m.NatGWID))))

	// Internet / egress row
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("Internet"))
	lines = append(lines, boxLine("  │  (via IGW → public subnets / NAT GW → private subnets)"))
	lines = append(lines, sep)

	// Public subnets
	lines = append(lines, boxLine("PUBLIC SUBNETS  (map-public-ip-on-launch, route → IGW)"))
	lines = append(lines, subnetRows(m.PublicSubnets, "pub")...)

	// Jumphost
	lines = append(lines, blank)
	lines = append(lines, boxLine(fmt.Sprintf("  jumphost: %-24s  eice: %s", np(m.Jumphost.InstanceID), np(m.Jumphost.EICEID))))
	lines = append(lines, boxLine(fmt.Sprintf("    mgmt-ip:    %-20s  (primary ENI, public subnet)", np(m.Jumphost.MgmtIP))))
	lines = append(lines, boxLine(fmt.Sprintf("    bnk-ext-ip: %-20s  (secondary ENI, BNK_EXT)", np(m.Jumphost.BnkExtIP))))
	lines = append(lines, boxLine("    └─SSH+EICE──► send test traffic to TMM SelfIP"))

	// Private subnets
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("PRIVATE SUBNETS  (egress via NAT GW)"))
	lines = append(lines, subnetRows(m.PrivateSubnets, "prv")...)

	// BNK data-path subnets
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("BNK DATA-PATH SUBNETS"))
	extID := ""
	if m.DataExtSubnet.ID != "" {
		extID = "  " + m.DataExtSubnet.ID
	}
	intID := ""
	if m.DataIntSubnet.ID != "" {
		intID = "  " + m.DataIntSubnet.ID
	}
	lines = append(lines, boxLine(fmt.Sprintf("  BNK_EXT: %-18s (%s)%s", np(m.DataExtSubnet.CIDR), np(m.DataExtSubnet.AZ), extID)))
	lines = append(lines, boxLine(fmt.Sprintf("  BNK_INT: %-18s (%s)%s", np(m.DataIntSubnet.CIDR), np(m.DataIntSubnet.AZ), intID)))

	// TMM VLAN self-IPs
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("TMM VLANS"))
	lines = append(lines, boxLine(fmt.Sprintf("  ext-vlan: selfip=%-18s  ifname=%s",
		np(m.TMM.ExtSelfIP), np(m.TMM.ExtIfname))))
	lines = append(lines, boxLine(fmt.Sprintf("  int-vlan: selfip=%-18s  ifname=%s",
		np(m.TMM.IntSelfIP), np(m.TMM.IntIfname))))

	// Node group
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("EKS NODE GROUP"))
	ngName := m.NodeGroup.Name
	if ngName == "" {
		ngName = "(not provisioned)"
	}
	lines = append(lines, boxLine(fmt.Sprintf("  name: %-30s", ngName)))
	if m.NodeGroup.InstanceType != "" {
		lines = append(lines, boxLine(fmt.Sprintf("  instance: %s × %d  (min:%d max:%d)",
			m.NodeGroup.InstanceType, m.NodeGroup.DesiredSize,
			m.NodeGroup.MinSize, m.NodeGroup.MaxSize)))
	}
	lines = append(lines, boxLine(fmt.Sprintf("  tmm-node: %s", np(m.NodeGroup.TMMNode))))

	// CSRC egress overlay
	if m.CSRCNote != "" {
		lines = append(lines, blank)
		lines = append(lines, sep)
		lines = append(lines, boxLine("CSRC EGRESS OVERLAY"))
		lines = append(lines, boxLine("  "+m.CSRCNote))
	}

	// GatewayClass + VIP range
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("GATEWAY API"))
	lines = append(lines, boxLine(fmt.Sprintf("  gatewayclass: %s", np(m.GatewayClassName))))
	lines = append(lines, boxLine(fmt.Sprintf("  vip-range:    %s", np(m.VIPRange))))

	lines = append(lines, blank)
	lines = append(lines, bot)

	return strings.Join(lines, "\n")
}

// RenderMermaid renders a Mermaid graph TD diagram of the cluster data path.
// Node labels include CIDRs/IPs when known.
func RenderMermaid(m Model) string {
	var sb strings.Builder

	// sanitize makes a Mermaid-safe node ID by replacing non-alphanumeric chars.
	sanitize := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			} else {
				b.WriteRune('_')
			}
		}
		return b.String()
	}
	_ = sanitize

	label := func(id, text string) string {
		// Mermaid node: id["text"]
		return fmt.Sprintf(`%s["%s"]`, id, text)
	}

	edge := func(from, to string) string {
		return fmt.Sprintf("  %s --> %s", from, to)
	}

	sb.WriteString("graph TD\n")
	sb.WriteString("\n")
	sb.WriteString("  %% Cluster: " + m.ClusterName + "  Region: " + m.Region + "\n")
	sb.WriteString("\n")

	// Internet
	sb.WriteString("  " + label("INET", "Internet") + "\n")
	sb.WriteString("\n")

	// VPC
	vpcLabel := fmt.Sprintf("VPC\\n%s", m.VPCCidr)
	if m.VPCID != "" {
		vpcLabel += "\\n" + m.VPCID
	}
	sb.WriteString("  " + label("VPC", vpcLabel) + "\n")
	sb.WriteString(edge("INET", "VPC") + "\n")
	sb.WriteString("\n")

	// IGW
	sb.WriteString("  " + label("IGW", "Internet GW\\n"+np(m.IGWID)) + "\n")
	sb.WriteString(edge("VPC", "IGW") + "\n")

	// NAT GW
	sb.WriteString("  " + label("NATGW", "NAT Gateway\\n"+np(m.NatGWID)) + "\n")
	sb.WriteString(edge("VPC", "NATGW") + "\n")
	sb.WriteString("\n")

	// Public subnets
	for i, s := range m.PublicSubnets {
		nodeID := fmt.Sprintf("PUB%d", i)
		lbl := fmt.Sprintf("Public Subnet %d\\n%s\\n%s", i, s.CIDR, s.AZ)
		if s.ID != "" {
			lbl += "\\n" + s.ID
		}
		sb.WriteString("  " + label(nodeID, lbl) + "\n")
		sb.WriteString(edge("IGW", nodeID) + "\n")
	}
	sb.WriteString("\n")

	// Private subnets
	for i, s := range m.PrivateSubnets {
		nodeID := fmt.Sprintf("PRV%d", i)
		lbl := fmt.Sprintf("Private Subnet %d\\n%s\\n%s", i, s.CIDR, s.AZ)
		if s.ID != "" {
			lbl += "\\n" + s.ID
		}
		sb.WriteString("  " + label(nodeID, lbl) + "\n")
		sb.WriteString(edge("NATGW", nodeID) + "\n")
	}
	sb.WriteString("\n")

	// BNK_EXT subnet
	extLbl := fmt.Sprintf("BNK EXT Subnet\\n%s\\n%s", np(m.DataExtSubnet.CIDR), np(m.DataExtSubnet.AZ))
	if m.DataExtSubnet.ID != "" {
		extLbl += "\\n" + m.DataExtSubnet.ID
	}
	sb.WriteString("  " + label("BNKEXT", extLbl) + "\n")
	sb.WriteString(edge("VPC", "BNKEXT") + "\n")

	// BNK_INT subnet
	intLbl := fmt.Sprintf("BNK INT Subnet\\n%s\\n%s", np(m.DataIntSubnet.CIDR), np(m.DataIntSubnet.AZ))
	if m.DataIntSubnet.ID != "" {
		intLbl += "\\n" + m.DataIntSubnet.ID
	}
	sb.WriteString("  " + label("BNKINT", intLbl) + "\n")
	sb.WriteString(edge("VPC", "BNKINT") + "\n")
	sb.WriteString("\n")

	// Node group
	ngLbl := fmt.Sprintf("Node Group\\n%s\\n%s × %d", m.NodeGroup.Name, m.NodeGroup.InstanceType, m.NodeGroup.DesiredSize)
	sb.WriteString("  " + label("NG", ngLbl) + "\n")
	if len(m.PrivateSubnets) > 0 {
		sb.WriteString(edge("PRV0", "NG") + "\n")
	} else {
		sb.WriteString(edge("VPC", "NG") + "\n")
	}
	sb.WriteString("\n")

	// TMM node
	tmmNodeLbl := "TMM Node\\n" + np(m.NodeGroup.TMMNode)
	sb.WriteString("  " + label("TMMNODE", tmmNodeLbl) + "\n")
	sb.WriteString(edge("NG", "TMMNODE") + "\n")

	// TMM pod / VLANs
	tmmExtLbl := fmt.Sprintf("TMM Ext VLAN\\nselfip: %s\\nifname: %s",
		np(m.TMM.ExtSelfIP), np(m.TMM.ExtIfname))
	sb.WriteString("  " + label("TMMEXT", tmmExtLbl) + "\n")
	sb.WriteString(edge("TMMNODE", "TMMEXT") + "\n")
	sb.WriteString(edge("BNKEXT", "TMMEXT") + "\n")

	tmmIntLbl := fmt.Sprintf("TMM Int VLAN\\nselfip: %s\\nifname: %s",
		np(m.TMM.IntSelfIP), np(m.TMM.IntIfname))
	sb.WriteString("  " + label("TMMINT", tmmIntLbl) + "\n")
	sb.WriteString(edge("TMMNODE", "TMMINT") + "\n")
	sb.WriteString(edge("BNKINT", "TMMINT") + "\n")
	sb.WriteString("\n")

	// CSRC
	if m.CSRCNote != "" {
		sb.WriteString("  " + label("CSRC", "CSRC Egress Overlay\\n"+m.CSRCNote) + "\n")
		sb.WriteString(edge("TMMNODE", "CSRC") + "\n")
		sb.WriteString("\n")
	}

	// Jumphost
	jhLbl := fmt.Sprintf("Jumphost\\n%s\\nmgmt: %s\\nbnk-ext: %s",
		np(m.Jumphost.InstanceID), np(m.Jumphost.MgmtIP), np(m.Jumphost.BnkExtIP))
	sb.WriteString("  " + label("JH", jhLbl) + "\n")
	if len(m.PublicSubnets) > 0 {
		sb.WriteString(edge("PUB0", "JH") + "\n")
	}
	sb.WriteString(edge("BNKEXT", "JH") + "\n")
	sb.WriteString("\n")

	// EICE
	eiceLbl := "EICE\\n" + np(m.Jumphost.EICEID)
	sb.WriteString("  " + label("EICE", eiceLbl) + "\n")
	sb.WriteString(edge("JH", "EICE") + "\n")
	sb.WriteString("\n")

	// GatewayClass
	gcLbl := fmt.Sprintf("GatewayClass\\n%s\\nvip: %s", np(m.GatewayClassName), np(m.VIPRange))
	sb.WriteString("  " + label("GWC", gcLbl) + "\n")
	sb.WriteString(edge("TMMEXT", "GWC") + "\n")

	return sb.String()
}
