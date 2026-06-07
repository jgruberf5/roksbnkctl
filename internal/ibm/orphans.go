package ibm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/IBM/platform-services-go-sdk/iamidentityv1"
)

// transitGatewayHost is the global Transit Gateway API endpoint.
const transitGatewayHost = "https://transit.cloud.ibm.com"

// OrphanResource is one leftover IBM Cloud resource discovered by the sweep,
// matched against the workspace's `<prefix>-*` naming convention.
type OrphanResource struct {
	Kind   string // instance | floating_ip | public_gateway | subnet | security_group | ssh_key | vpc | transit_gateway | cluster | cos_instance | trusted_profile
	ID     string
	Name   string
	Region string // VPC-regional resources carry their region; global ones leave it ""
}

// SweepScope bounds the orphan search: the workspace prefix every resource is
// named from, the cluster ID (so the BNK trusted profile — named after the
// cluster, not the prefix — can still be found after the cluster is gone), and
// the regions to scan for regional VPC resources.
type SweepScope struct {
	Prefix    string
	ClusterID string
	Regions   []string
}

// OrphanKindOrder returns the teardown priority for a kind (lower = delete
// first). Honours the dependency chain: instances release their floating IPs
// and free subnets/SGs; the cluster is kicked off early (async) so its delete
// progresses while the rest runs; the cluster VPC goes last. Resources with the
// same order can be deleted in any sequence.
func OrphanKindOrder(kind string) int {
	switch kind {
	case "cluster":
		return 0
	case "instance":
		return 1
	case "floating_ip":
		return 2
	case "transit_gateway":
		return 3
	case "public_gateway":
		return 4
	case "subnet":
		return 5
	case "security_group":
		return 6
	case "vpc":
		return 7
	case "cos_instance":
		return 8
	case "trusted_profile":
		return 9
	default:
		return 100
	}
}

// matchesPrefix reports whether a resource name belongs to the workspace: an
// exact match (the cluster is named the bare prefix) or a `<prefix>-` child.
func matchesPrefix(name, prefix string) bool {
	if prefix == "" {
		return false
	}
	return name == prefix || strings.HasPrefix(name, prefix+"-")
}

// vpcHost is the regional VPC API endpoint for region.
func vpcHost(region string) string {
	return fmt.Sprintf("https://%s.iaas.cloud.ibm.com", region)
}

type vpcNamed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// vpcCollections maps each OrphanResource kind to its VPC collection path + the
// JSON key the items live under.
var vpcCollections = []struct{ kind, collection, key string }{
	{"instance", "instances", "instances"},
	{"floating_ip", "floating_ips", "floating_ips"},
	{"public_gateway", "public_gateways", "public_gateways"},
	{"subnet", "subnets", "subnets"},
	{"security_group", "security_groups", "security_groups"},
	{"ssh_key", "keys", "keys"},
	{"vpc", "vpcs", "vpcs"},
}

// listVPCCollection pages through a VPC collection in a region and returns the
// id+name of every item. Pagination follows the API's next.href cursor (which
// already carries the version/generation/start params).
func (c *Client) listVPCCollection(ctx context.Context, region, collection, key string) ([]vpcNamed, error) {
	url := fmt.Sprintf("%s/v1/%s?version=%s&generation=2&limit=100", vpcHost(region), collection, vpcAPIVersion)
	var out []vpcNamed
	for url != "" {
		body, err := c.authedGET(ctx, url)
		if err != nil {
			return nil, err
		}
		var page map[string]json.RawMessage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", collection, err)
		}
		if raw, ok := page[key]; ok {
			var items []vpcNamed
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, fmt.Errorf("parsing %s items: %w", collection, err)
			}
			out = append(out, items...)
		}
		url = ""
		if raw, ok := page["next"]; ok {
			var next struct {
				Href string `json:"href"`
			}
			if json.Unmarshal(raw, &next) == nil {
				url = next.Href
			}
		}
	}
	return out, nil
}

// listTransitGateways returns the account's transit gateways (id+name).
func (c *Client) listTransitGateways(ctx context.Context) ([]vpcNamed, error) {
	url := fmt.Sprintf("%s/v1/transit_gateways?version=%s", transitGatewayHost, vpcAPIVersion)
	body, err := c.authedGET(ctx, url)
	if err != nil {
		return nil, err
	}
	var page struct {
		TransitGateways []vpcNamed `json:"transit_gateways"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parsing transit_gateways: %w", err)
	}
	return page.TransitGateways, nil
}

// FindOrphans enumerates every `<prefix>-*` resource still present in the
// account across the scope's regions plus the global Transit Gateway, COS,
// cluster, and trusted-profile surfaces. Per-surface errors are swallowed
// (best-effort discovery — an inaccessible region or service shouldn't abort
// the whole sweep); only a name match contributes a result.
func (c *Client) FindOrphans(ctx context.Context, scope SweepScope) ([]OrphanResource, error) {
	var found []OrphanResource

	// Regional VPC resources.
	for _, region := range dedupeNonEmpty(scope.Regions) {
		for _, vc := range vpcCollections {
			items, err := c.listVPCCollection(ctx, region, vc.collection, vc.key)
			if err != nil {
				continue
			}
			for _, it := range items {
				if matchesPrefix(it.Name, scope.Prefix) {
					found = append(found, OrphanResource{Kind: vc.kind, ID: it.ID, Name: it.Name, Region: region})
				}
			}
		}
	}

	// Transit gateways (global).
	if tgws, err := c.listTransitGateways(ctx); err == nil {
		for _, t := range tgws {
			if matchesPrefix(t.Name, scope.Prefix) {
				found = append(found, OrphanResource{Kind: "transit_gateway", ID: t.ID, Name: t.Name})
			}
		}
	}

	// Registry COS instances (resource controller).
	if cos, err := c.ListCOSInstances(ctx); err == nil {
		for _, ci := range cos {
			if matchesPrefix(ci.Name, scope.Prefix) {
				found = append(found, OrphanResource{Kind: "cos_instance", ID: ci.CRN, Name: ci.Name})
			}
		}
	}

	// The ROKS cluster (named the bare prefix).
	if info, err := c.GetCluster(ctx, scope.Prefix); err == nil && info.ID != "" {
		found = append(found, OrphanResource{Kind: "cluster", ID: info.ID, Name: info.Name})
	}

	// BNK trusted profile — named "<clusterid>-f5-cne-controller-f5-bnk", so it
	// is only identifiable when the cluster ID is known (passed from
	// cluster-outputs.json, which survives the cluster's deletion).
	if scope.ClusterID != "" {
		if prof, err := c.findBNKTrustedProfile(ctx, scope.ClusterID); err == nil && prof != nil {
			found = append(found, *prof)
		}
	}

	return found, nil
}

// findBNKTrustedProfile locates the FLO CNE-controller trusted profile for a
// cluster ID, listing the account's profiles and matching on the id-prefixed,
// f5-bnk-suffixed name.
func (c *Client) findBNKTrustedProfile(ctx context.Context, clusterID string) (*OrphanResource, error) {
	if c.identity == nil {
		if _, err := c.Verify(ctx); err != nil {
			return nil, err
		}
	}
	accountID := c.identity.AccountID
	res, _, err := c.iam.ListProfilesWithContext(ctx, &iamidentityv1.ListProfilesOptions{
		AccountID: &accountID,
	})
	if err != nil {
		return nil, err
	}
	for _, p := range res.Profiles {
		name := ""
		if p.Name != nil {
			name = *p.Name
		}
		if strings.HasPrefix(name, clusterID) && strings.Contains(name, "f5-cne-controller") {
			return &OrphanResource{Kind: "trusted_profile", ID: *p.ID, Name: name}, nil
		}
	}
	return nil, nil
}

// DeleteOrphan deletes a single discovered resource. Idempotent (a 404 is
// success). Transit gateways have their connections removed first.
func (c *Client) DeleteOrphan(ctx context.Context, o OrphanResource) error {
	switch o.Kind {
	case "instance", "floating_ip", "public_gateway", "subnet", "security_group", "ssh_key", "vpc":
		collection := vpcCollectionFor(o.Kind)
		url := fmt.Sprintf("%s/v1/%s/%s?version=%s&generation=2", vpcHost(o.Region), collection, o.ID, vpcAPIVersion)
		return c.authedDELETE(ctx, url)
	case "transit_gateway":
		return c.deleteTransitGateway(ctx, o.ID)
	case "cluster":
		url := fmt.Sprintf("%s/global/v1/clusters/%s?deleteResources=true", containerServiceBase, o.ID)
		return c.authedDELETE(ctx, url)
	case "cos_instance":
		return c.DeleteCOSInstance(ctx, o.ID, true)
	case "trusted_profile":
		_, err := c.iam.DeleteProfileWithContext(ctx, &iamidentityv1.DeleteProfileOptions{ProfileID: &o.ID})
		return err
	default:
		return fmt.Errorf("unknown orphan kind %q", o.Kind)
	}
}

// deleteTransitGateway removes a gateway's connections then the gateway itself
// (a gateway with live connections can't be deleted).
func (c *Client) deleteTransitGateway(ctx context.Context, id string) error {
	connURL := fmt.Sprintf("%s/v1/transit_gateways/%s/connections?version=%s", transitGatewayHost, id, vpcAPIVersion)
	if body, err := c.authedGET(ctx, connURL); err == nil {
		var page struct {
			Connections []vpcNamed `json:"connections"`
		}
		if json.Unmarshal(body, &page) == nil {
			for _, conn := range page.Connections {
				delURL := fmt.Sprintf("%s/v1/transit_gateways/%s/connections/%s?version=%s", transitGatewayHost, id, conn.ID, vpcAPIVersion)
				_ = c.authedDELETE(ctx, delURL)
			}
		}
	}
	url := fmt.Sprintf("%s/v1/transit_gateways/%s?version=%s", transitGatewayHost, id, vpcAPIVersion)
	return c.authedDELETE(ctx, url)
}

func vpcCollectionFor(kind string) string {
	for _, vc := range vpcCollections {
		if vc.kind == kind {
			return vc.collection
		}
	}
	return ""
}

// SortOrphans orders resources for deletion by OrphanKindOrder (stable within a
// kind), so callers delete in dependency order.
func SortOrphans(orphans []OrphanResource) {
	sort.SliceStable(orphans, func(i, j int) bool {
		return OrphanKindOrder(orphans[i].Kind) < OrphanKindOrder(orphans[j].Kind)
	})
}

func dedupeNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
