package ibm

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// AddressPrefix is one of a VPC's per-zone address prefixes.
type AddressPrefix struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	CIDR string `json:"cidr"`
	Zone struct {
		Name string `json:"name"`
	} `json:"zone"`
}

// ListVPCAddressPrefixes returns the address prefixes of one VPC.
//
// Needed because a Transit Gateway routes on these, not on subnets: two attached
// VPCs whose prefixes overlap make the gateway's routing ambiguous and it drops
// traffic for one of them, silently.
func (c *Client) ListVPCAddressPrefixes(ctx context.Context, region, vpcID string) ([]AddressPrefix, error) {
	url := fmt.Sprintf("%s/v1/vpcs/%s/address_prefixes?version=%s&generation=2&limit=100",
		vpcHost(region), vpcID, vpcAPIVersion)
	var out []AddressPrefix
	for url != "" {
		body, err := c.authedGET(ctx, url)
		if err != nil {
			return nil, err
		}
		var page struct {
			AddressPrefixes []AddressPrefix `json:"address_prefixes"`
			Next            struct {
				Href string `json:"href"`
			} `json:"next"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parsing address prefixes for VPC %s: %w", vpcID, err)
		}
		out = append(out, page.AddressPrefixes...)
		url = page.Next.Href
	}
	return out, nil
}

// PrefixConflict is one overlapping pair: a CIDR we intend to use against a CIDR
// already present on a VPC attached to the gateway.
type PrefixConflict struct {
	Intended string // the prefix the new cluster VPC would take
	Existing string // the prefix already attached
	VPCName  string // the VPC that already holds it
}

func (p PrefixConflict) String() string {
	return fmt.Sprintf("%s overlaps %s on VPC %q", p.Intended, p.Existing, p.VPCName)
}

// cidrsOverlap reports whether two CIDR blocks share any address. Either
// containing the other counts — a /18 inside a /16 is exactly the case that bites,
// since it is not an equality check that catches it.
func cidrsOverlap(a, b string) (bool, error) {
	_, na, err := net.ParseCIDR(strings.TrimSpace(a))
	if err != nil {
		return false, fmt.Errorf("parsing %q: %w", a, err)
	}
	_, nb, err := net.ParseCIDR(strings.TrimSpace(b))
	if err != nil {
		return false, fmt.Errorf("parsing %q: %w", b, err)
	}
	return na.Contains(nb.IP) || nb.Contains(na.IP), nil
}

// FindPrefixConflicts compares the prefixes a new VPC intends to take against the
// prefixes already in use by VPCs attached to a gateway, and returns every
// overlapping pair.
//
// Pure comparison — the caller supplies both sides, so this is testable without an
// API and reusable wherever the attachment set is already known. attached maps a
// VPC display name to its prefixes.
func FindPrefixConflicts(intended []string, attached map[string][]string) ([]PrefixConflict, error) {
	var conflicts []PrefixConflict
	// Sorted iteration would be nicer, but callers render these into an error
	// message and a stable order matters more than lexical order: keep input order
	// for `intended`, which is zone order, and let the caller sort if needed.
	for _, want := range intended {
		for vpcName, have := range attached {
			for _, got := range have {
				overlap, err := cidrsOverlap(want, got)
				if err != nil {
					return nil, err
				}
				if overlap {
					conflicts = append(conflicts, PrefixConflict{
						Intended: want, Existing: got, VPCName: vpcName,
					})
				}
			}
		}
	}
	return conflicts, nil
}

// DefaultAutoPrefixes are the per-zone prefixes IBM's "auto" address prefix
// management assigns to EVERY VPC in a region. They are what makes two
// roksbnkctl-created clusters collide on a shared gateway when neither sets
// cluster.vpc_cidr, and they are what an unset config intends to take.
//
// Not fetched from the API because they must be known BEFORE the VPC exists —
// the whole point is to refuse before creating it.
var DefaultAutoPrefixes = []string{"10.241.0.0/18", "10.241.64.0/18", "10.241.128.0/18"}

// IntendedPrefixes returns the prefixes a cluster VPC will take for a given
// cluster.vpc_cidr, mirroring the terraform arithmetic exactly:
//
//	cidr /n  →  three zone prefixes at /n+2  (cidrsubnet(cidr, 2, i))
//
// An empty cidr means IBM auto-assignment, which yields DefaultAutoPrefixes.
func IntendedPrefixes(cidr string) ([]string, error) {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return append([]string(nil), DefaultAutoPrefixes...), nil
	}
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("cluster.vpc_cidr %q is not a valid CIDR: %w", cidr, err)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("cluster.vpc_cidr %q must be IPv4", cidr)
	}
	if ones > 18 {
		return nil, fmt.Errorf("cluster.vpc_cidr %q is too small: it is split into three per-zone prefixes, so /18 is the smallest usable block", cidr)
	}
	zoneOnes := ones + 2
	out := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		ip, err := nthSubnet(network, zoneOnes, i)
		if err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%s/%d", ip, zoneOnes))
	}
	return out, nil
}

// nthSubnet returns the base address of the i-th subnet of newOnes bits within
// network — the Go equivalent of terraform's cidrsubnet(network, newOnes-ones, i).
func nthSubnet(network *net.IPNet, newOnes, i int) (net.IP, error) {
	base := network.IP.To4()
	if base == nil {
		return nil, fmt.Errorf("%s is not IPv4", network)
	}
	ones, _ := network.Mask.Size()
	shift := 32 - newOnes
	v := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	v |= uint32(i) << shift
	// Guard against an index that does not fit the widened prefix.
	if newOnes-ones < 1 || i >= 1<<(newOnes-ones) {
		return nil, fmt.Errorf("subnet index %d does not fit /%d inside /%d", i, newOnes, ones)
	}
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)), nil
}
