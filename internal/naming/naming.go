// Package naming derives every account-scoped IBM Cloud resource name a
// workspace needs from a single workspace prefix, and validates that prefix
// against the per-resource-type length/charset limits BEFORE terraform ever
// runs — so an over-long or malformed prefix is rejected at `init` time
// rather than mid-`apply` by IBM Cloud.
//
// Sprint 26 (issues/issue_sprint26_staff.md): a single workspace prefix is
// the base for the cluster, cluster VPC, registry COS instance, transit
// gateway, client VPC, and the two jumphost name surfaces. Cluster name ==
// prefix (no suffix) is intentional: it makes the prefix-length limit equal
// the tightest resource limit (the ROKS cluster name), so a valid prefix
// guarantees every other derived name fits. Do NOT "tidy" the cluster name
// into "<prefix>-cluster" — that silently shrinks the usable prefix length.
package naming

import (
	"fmt"
	"regexp"
	"strings"
)

// constraint is one row of the per-resource-type length/charset table.
type constraint struct {
	maxLen   int
	pattern  *regexp.Regexp
	describe string
}

// Constraint table — the per-resource-type IBM Cloud length/charset limits.
//
// Sprint 26: verify against architect's pinned constraint table in
// issues/issue_sprint26_architect.md. The architect owns the authoritative
// values (cited from the IBM Terraform provider's validators); these are the
// dispatch-time starting values. Any verified delta is a one-line change
// here so the integrator can reconcile.
//
//	Kind                                  MaxLen  Charset rule
//	------------------------------------  ------  --------------------------------
//	ROKS/IKS cluster name                  35     ^[a-z][-a-z0-9]*[a-z0-9]$ (or [a-z])
//	IS resource (VPC, VSI, TGW, SSH key)   63     same IS label rule
//	COS resource instance                 180     permissive; reuse lowercase subset
var (
	// clusterCharset / isCharset: start with a lowercase letter, then
	// lowercase-alnum + hyphen, end with lowercase-alnum, no trailing
	// hyphen. A single bare [a-z] is also valid (the alternation second
	// arm). This is the shared "cluster + IS label" rule.
	labelCharset = regexp.MustCompile(`^([a-z]|[a-z][-a-z0-9]*[a-z0-9])$`)

	constraintCluster = constraint{
		maxLen:   35,
		pattern:  labelCharset,
		describe: "ROKS/IKS cluster name",
	}
	constraintIS = constraint{
		maxLen:   63,
		pattern:  labelCharset,
		describe: "IS resource name (VPC / VSI / transit gateway / SSH key)",
	}
	constraintCOS = constraint{
		// COS resource-instance names are permissive (up to 180 chars).
		// Because every derived COS name comes from a lowercase prefix we
		// reuse the same lowercase-label subset — safe and predictable.
		maxLen:   180,
		pattern:  labelCharset,
		describe: "COS resource instance name",
	}
)

// Suffix constants — the compact scheme Derive applies. Kept as named
// constants so ValidatePrefix can compute the max-allowable prefix length
// from the table without hard-coding a number.
const (
	suffixClusterVPC      = "-cluster-vpc"
	suffixRegistryCOS     = "-registry-cos"
	suffixTransitGateway  = "-tgw"
	suffixClientVPC       = "-client-vpc"
	suffixTGWJumphost     = "-jh-tgw"
	suffixClusterJumphost = "-jh"

	// clusterJumphostZoneSuffix is the longest "-<zone>" the upstream
	// module appends to the cluster-jumphost name prefix (e.g.
	// "us-south-1"). The validated name for the cluster-jumphost prefix is
	// "<prefix>-jh-<zone>", so its effective length budget includes this.
	clusterJumphostZoneSuffix = "-us-south-1"

	// maxLabelLen is the workspace-name cap SanitizeToPrefix applies. It
	// matches ValidateName's 64-char workspace-name ceiling but is also
	// bounded below by the binding cluster limit at validation time, so a
	// sanitized default still has to pass ValidatePrefix.
	maxLabelLen = 64
)

// Plan is the full set of account-scoped resource names derived from a
// prefix. Each field maps 1:1 onto a tfvars variable the full render emits.
type Plan struct {
	ClusterName           string // openshift_cluster_name / roks_cluster_id_or_name
	ClusterVPCName        string // roks_cluster_vpc_name
	COSInstanceName       string // roks_cos_instance_name
	TransitGatewayName    string // roks_transit_gateway_name
	ClientVPCName         string // testing_client_vpc_name
	TGWJumphostName       string // testing_tgw_jumphost_name
	ClusterJumphostPrefix string // testing_cluster_jumphost_name_prefix (module appends -<zone>)
}

// Derive returns the Plan for prefix. It is a pure string derivation; it
// does NOT validate — call ValidatePrefix first (or alongside) to guarantee
// every derived name fits its constraint.
func Derive(prefix string) Plan {
	return Plan{
		ClusterName:           prefix,
		ClusterVPCName:        prefix + suffixClusterVPC,
		COSInstanceName:       prefix + suffixRegistryCOS,
		TransitGatewayName:    prefix + suffixTransitGateway,
		ClientVPCName:         prefix + suffixClientVPC,
		TGWJumphostName:       prefix + suffixTGWJumphost,
		ClusterJumphostPrefix: prefix + suffixClusterJumphost,
	}
}

// derivedName pairs a derived name with the constraint it must satisfy and
// any extra suffix the consuming module appends after validation (the
// cluster-jumphost zone suffix).
type derivedName struct {
	value      string // the name as it appears in tfvars
	effective  string // the name actually created (value + module-appended suffix)
	constraint constraint
	suffix     string // the prefix suffix this name adds (for the max-prefix hint)
	zoneExtra  string // module-appended suffix included in length budget
}

// derivedNames returns each derived name paired with its constraint, in a
// stable order, so ValidatePrefix can check all of them and compute the
// tightest max-allowable-prefix bound.
func derivedNames(prefix string) []derivedName {
	p := Derive(prefix)
	return []derivedName{
		{value: p.ClusterName, effective: p.ClusterName, constraint: constraintCluster, suffix: ""},
		{value: p.ClusterVPCName, effective: p.ClusterVPCName, constraint: constraintIS, suffix: suffixClusterVPC},
		{value: p.COSInstanceName, effective: p.COSInstanceName, constraint: constraintCOS, suffix: suffixRegistryCOS},
		{value: p.TransitGatewayName, effective: p.TransitGatewayName, constraint: constraintIS, suffix: suffixTransitGateway},
		{value: p.ClientVPCName, effective: p.ClientVPCName, constraint: constraintIS, suffix: suffixClientVPC},
		{value: p.TGWJumphostName, effective: p.TGWJumphostName, constraint: constraintIS, suffix: suffixTGWJumphost},
		{
			value:      p.ClusterJumphostPrefix,
			effective:  p.ClusterJumphostPrefix + clusterJumphostZoneSuffix,
			constraint: constraintIS,
			suffix:     suffixClusterJumphost,
			zoneExtra:  clusterJumphostZoneSuffix,
		},
	}
}

// maxPrefixLen computes the largest prefix length for which EVERY derived
// name fits its constraint, from the table (not hard-coded). It is the
// minimum over all resources of (constraint.maxLen - len(suffix) -
// len(zoneExtra)).
func maxPrefixLen() int {
	best := -1
	for _, d := range derivedNames("") {
		budget := d.constraint.maxLen - len(d.suffix) - len(d.zoneExtra)
		if best < 0 || budget < best {
			best = budget
		}
	}
	if best < 0 {
		best = 0
	}
	return best
}

// ValidatePrefix validates the prefix label itself (lowercase, start with a
// letter, [a-z0-9-], no trailing hyphen) AND that every Derive'd name passes
// its constraint. On overflow it returns one actionable error naming the
// offending resource, its computed length, its limit, and the max allowable
// prefix length (computed from the table). On a charset/label violation it
// returns a clear label-rule error.
func ValidatePrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("workspace prefix is empty: it must start with a lowercase letter and contain only [a-z0-9-] (no trailing hyphen)")
	}
	if !labelCharset.MatchString(prefix) {
		return fmt.Errorf("workspace prefix %q is invalid: it must start with a lowercase letter, contain only [a-z0-9-], and not end with a hyphen", prefix)
	}
	// Length: check every derived name (including the module-appended zone
	// suffix on the cluster-jumphost prefix). Report the FIRST overflow with
	// the table-computed max prefix length so the operator knows how much to
	// trim.
	for _, d := range derivedNames(prefix) {
		if len(d.effective) > d.constraint.maxLen {
			return fmt.Errorf(
				"workspace prefix %q is too long: derived %s %q is %d chars, over the %d-char limit. "+
					"Maximum prefix length for this naming scheme is %d (currently %d).",
				prefix, d.constraint.describe, d.effective, len(d.effective), d.constraint.maxLen,
				maxPrefixLen(), len(prefix),
			)
		}
	}
	return nil
}

// MaxPrefixLen is the exported max-allowable prefix length, for callers that
// want to surface it in help text or a prompt label.
func MaxPrefixLen() int { return maxPrefixLen() }

// sanitizeStripRE removes any character that is not lowercase-alnum or a
// hyphen (after the lowercase + _/. mapping).
var sanitizeStripRE = regexp.MustCompile(`[^a-z0-9-]+`)

// collapseHyphenRE collapses runs of hyphens to a single hyphen.
var collapseHyphenRE = regexp.MustCompile(`-{2,}`)

// leadingNonLetterRE strips leading characters until the first lowercase
// letter (the label rule requires a leading [a-z]).
var leadingNonLetterRE = regexp.MustCompile(`^[^a-z]+`)

// SanitizeToPrefix derives a default, valid-shaped prefix from a workspace
// name: lowercase, map `_`/`.`→`-`, strip any other non-[a-z0-9-] char,
// collapse hyphen runs, strip leading non-letters, trim trailing hyphens,
// and cap the length. Idempotent: SanitizeToPrefix(SanitizeToPrefix(x)) ==
// SanitizeToPrefix(x).
//
// The result is a best-effort default the interview offers; it is NOT
// guaranteed to be non-empty (a name with no usable letters sanitizes to ""),
// and the caller still runs it through ValidatePrefix. The length cap here is
// the workspace-name ceiling; ValidatePrefix enforces the tighter
// naming-scheme bound.
func SanitizeToPrefix(workspaceName string) string {
	s := strings.ToLower(workspaceName)
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = sanitizeStripRE.ReplaceAllString(s, "-")
	s = collapseHyphenRE.ReplaceAllString(s, "-")
	s = leadingNonLetterRE.ReplaceAllString(s, "")
	s = strings.TrimRight(s, "-")
	if len(s) > maxLabelLen {
		s = s[:maxLabelLen]
		// A length cap can re-expose a trailing hyphen; trim again so the
		// result still satisfies the no-trailing-hyphen label rule and the
		// function stays idempotent.
		s = strings.TrimRight(s, "-")
	}
	return s
}
