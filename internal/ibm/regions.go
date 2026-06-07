package ibm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// vpcRegionsHost — any regional VPC endpoint returns the same account-wide
// /v1/regions collection, so a stable default host avoids the chicken-and-egg
// "pick a region to list regions" problem.
const vpcRegionsHost = "https://us-south.iaas.cloud.ibm.com"

// vpcAPIVersion — the VPC API is date-versioned; any recent date is accepted.
const vpcAPIVersion = "2024-04-30"

// Region is an available IBM Cloud VPC region (where a ROKS cluster can run).
type Region struct {
	Name   string
	Status string
}

// ListRegions returns the account's available VPC regions. Returns an error on
// transport/parse failure — callers (init) fall back to CuratedRegions so the
// interview never hard-fails when the API is unreachable.
func (c *Client) ListRegions(ctx context.Context) ([]Region, error) {
	url := fmt.Sprintf("%s/v1/regions?version=%s&generation=2", vpcRegionsHost, vpcAPIVersion)
	body, err := c.authedGET(ctx, url)
	if err != nil {
		return nil, err
	}
	var out struct {
		Regions []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"regions"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing regions: %w", err)
	}
	var regions []Region
	for _, r := range out.Regions {
		if r.Status == "available" {
			regions = append(regions, Region{Name: r.Name, Status: r.Status})
		}
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].Name < regions[j].Name })
	return regions, nil
}

// CuratedRegions is the built-in fallback list of IBM Cloud multi-zone regions,
// used when the live regions API is unreachable.
func CuratedRegions() []Region {
	names := []string{
		"au-syd", "br-sao", "ca-tor", "eu-de", "eu-es", "eu-gb",
		"jp-osa", "jp-tok", "us-east", "us-south",
	}
	out := make([]Region, len(names))
	for i, n := range names {
		out[i] = Region{Name: n, Status: "available"}
	}
	return out
}
