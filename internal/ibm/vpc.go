package ibm

import (
	"context"
	"encoding/json"
	"fmt"
)

// VPC read helpers over the regional VPC endpoint, mirroring the transit-gateway
// listing. Read-only: backs the init interview's "use an existing cluster VPC"
// discovery, so the operator picks from the account's live VPCs rather than
// pasting an id by hand.

// Default IBM Cloud quotas, used for PRE-flight warnings. These are the account
// defaults; an account with an approved increase may exceed them — so callers gate
// a warning ("at the default limit"), never a hard failure. The two the user hits
// in practice are VPCs-per-region and Transit-Gateways-per-account.
const (
	VPCQuotaPerRegion  = 20 // VPCs per region (default)
	TGWQuotaPerAccount = 10 // Transit Gateways per account, global (default)
)

// VPC is a resolved VPC in a region.
type VPC struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	CRN    string `json:"crn"`
}

// CreateVPC creates a VPC named name in region with default (auto) address
// prefixes and returns it. Used by the init interview when the operator chooses
// "create a new VPC" for a standalone FLP appliance; the FLP module then adds the
// subnet, public gateway, and security group inside it. (The new VPC still needs
// to be attached to the Transit Gateway to be reachable from a cluster in another
// VPC/region — `roksbnkctl tgw connect` or the console.)
func (c *Client) CreateVPC(ctx context.Context, region, name, resourceGroupID string) (*VPC, error) {
	reqBody := map[string]any{"name": name}
	if resourceGroupID != "" {
		reqBody["resource_group"] = map[string]string{"id": resourceGroupID}
	}
	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/v1/vpcs?version=%s&generation=2", vpcHost(region), vpcAPIVersion)
	resp, err := c.authedPOST(ctx, url, bodyJSON)
	if err != nil {
		return nil, fmt.Errorf("creating VPC %q in %s: %w", name, region, err)
	}
	var v VPC
	if err := json.Unmarshal(resp, &v); err != nil {
		return nil, fmt.Errorf("parsing created VPC: %w", err)
	}
	return &v, nil
}

// ListVPCs returns every VPC in a region. Unlike transit gateways (a global
// collection), VPCs are regional, so the caller passes the cluster region.
// Pagination follows the API's next.href cursor (which carries the
// version/generation/start params).
func (c *Client) ListVPCs(ctx context.Context, region string) ([]VPC, error) {
	url := fmt.Sprintf("%s/v1/vpcs?version=%s&generation=2&limit=100", vpcHost(region), vpcAPIVersion)
	var out []VPC
	for url != "" {
		body, err := c.authedGET(ctx, url)
		if err != nil {
			return nil, err
		}
		var page struct {
			VPCs []VPC `json:"vpcs"`
			Next struct {
				Href string `json:"href"`
			} `json:"next"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parsing vpcs: %w", err)
		}
		out = append(out, page.VPCs...)
		url = page.Next.Href
	}
	return out, nil
}

// Subnet is a subnet with its parent VPC id — enough for the shared-VPC teardown
// guard to tell whose subnets are still in a VPC.
type Subnet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	VPC  struct {
		ID string `json:"id"`
	} `json:"vpc"`
}

// ListSubnets returns every subnet in a region (paginated). The caller filters by
// VPC id; the region-wide list is one call plus pagination, cheaper than N
// per-VPC calls when only one VPC matters.
func (c *Client) ListSubnets(ctx context.Context, region string) ([]Subnet, error) {
	url := fmt.Sprintf("%s/v1/subnets?version=%s&generation=2&limit=100", vpcHost(region), vpcAPIVersion)
	var out []Subnet
	for url != "" {
		body, err := c.authedGET(ctx, url)
		if err != nil {
			return nil, err
		}
		var page struct {
			Subnets []Subnet `json:"subnets"`
			Next    struct {
				Href string `json:"href"`
			} `json:"next"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parsing subnets: %w", err)
		}
		out = append(out, page.Subnets...)
		url = page.Next.Href
	}
	return out, nil
}
