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

// VPC is a resolved VPC in a region.
type VPC struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	CRN    string `json:"crn"`
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
