package ibm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ClusterSummary is the subset of GET /global/v2/vpc/getClusters used to let
// the user pick an existing cluster to reuse at `roksbnkctl init`.
type ClusterSummary struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Region            string `json:"region"`
	State             string `json:"state"`
	MasterKubeVersion string `json:"masterKubeVersion"`
	Type              string `json:"type"`
}

// ListClusters returns the account's running OpenShift (ROKS) VPC clusters,
// sorted by name. Non-running and non-OpenShift clusters are filtered out — a
// reuse target must be a live ROKS cluster.
func (c *Client) ListClusters(ctx context.Context) ([]ClusterSummary, error) {
	url := containerServiceBase + "/global/v2/vpc/getClusters"
	body, err := c.authedGET(ctx, url)
	if err != nil {
		return nil, err
	}
	var all []ClusterSummary
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, fmt.Errorf("parsing getClusters: %w", err)
	}
	var out []ClusterSummary
	for _, cl := range all {
		if clusterRunning(cl.State) && clusterIsOpenShift(cl) {
			out = append(out, cl)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// clusterRunning reports whether a cluster state means "up and usable".
// "warning" is included — a degraded-but-running cluster is still reusable.
func clusterRunning(state string) bool {
	switch strings.ToLower(state) {
	case "normal", "warning":
		return true
	}
	return false
}

// clusterIsOpenShift identifies ROKS clusters. The container API reports either
// type=="openshift" or an "_openshift"-suffixed master version depending on the
// endpoint, so accept either signal.
func clusterIsOpenShift(cl ClusterSummary) bool {
	return strings.EqualFold(cl.Type, "openshift") ||
		strings.Contains(strings.ToLower(cl.MasterKubeVersion), "openshift")
}
