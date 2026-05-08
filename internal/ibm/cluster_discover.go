package ibm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/IBM/go-sdk-core/v5/core"
)

// ClusterInfo is the subset of `GET /global/v2/getCluster` fields
// roksctl uses for cluster registration / display. Not exhaustive — only
// what ends up in ClusterOutputs or shown to the user.
type ClusterInfo struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Region            string   `json:"region"`
	ResourceGroupID   string   `json:"resourceGroup"`
	ResourceGroupName string   `json:"resourceGroupName"`
	State             string   `json:"state"`
	Status            string   `json:"status"`
	MasterURL         string   `json:"masterURL"`
	MasterKubeVersion string   `json:"masterKubeVersion"`
	CRN               string   `json:"crn"`
	VPCs              []string `json:"vpcs"`
	WorkerZones       []string `json:"workerZones"`
	Provider          string   `json:"provider"`
}

// VPCID returns the cluster's primary VPC ID, or "" if the cluster has
// none reported. ROKS clusters created by this TF always have exactly
// one VPC, so callers can treat the slice as a singleton.
func (ci *ClusterInfo) VPCID() string {
	if ci == nil || len(ci.VPCs) == 0 {
		return ""
	}
	return ci.VPCs[0]
}

// ErrClusterNotFound — the container service has no cluster matching
// the given name or ID. Sentinel so callers can distinguish "no such
// cluster" from auth or transport errors.
var ErrClusterNotFound = errors.New("cluster not found")

// GetCluster fetches cluster metadata from the IBM Container Service.
// Hits the same endpoint the `ibmcloud ks cluster get` CLI uses.
//
// Returns ErrClusterNotFound on 404 — `roksctl cluster register` uses
// that to give a clean "no such cluster" error.
func (c *Client) GetCluster(ctx context.Context, idOrName string) (*ClusterInfo, error) {
	if idOrName == "" {
		return nil, errors.New("cluster name/id is empty")
	}

	auth := &core.IamAuthenticator{ApiKey: c.apiKey}
	token, err := auth.GetToken()
	if err != nil {
		return nil, fmt.Errorf("getting IAM token: %w", err)
	}

	url := fmt.Sprintf("%s/global/v2/getCluster?cluster=%s&v1-compatible",
		containerServiceBase, idOrName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "roksctl")
	if c.region != "" {
		req.Header.Set("X-Region", c.region)
	}

	resp, err := kubeconfigHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling container service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %q", ErrClusterNotFound, idOrName)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("getCluster returned %s: %s",
			resp.Status, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading getCluster response: %w", err)
	}
	var info ClusterInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parsing getCluster response: %w", err)
	}
	return &info, nil
}
