package ibm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/IBM/go-sdk-core/v5/core"
)

// authToken fetches an IAM bearer token for the client's API key. Factors out
// the token dance shared by every raw-REST helper (GetCluster, ListRegions,
// ListClusters, the orphan sweep).
func (c *Client) authToken() (string, error) {
	auth := &core.IamAuthenticator{ApiKey: c.apiKey}
	token, err := auth.GetToken()
	if err != nil {
		return "", fmt.Errorf("getting IAM token: %w", err)
	}
	return token, nil
}

// authedGET performs an authenticated GET against an IBM Cloud REST endpoint
// and returns the body on 2xx. Mirrors the manual request construction in
// GetCluster so the region/cluster/orphan helpers share one transport + the
// 60s-timeout client.
func (c *Client) authedGET(ctx context.Context, url string) ([]byte, error) {
	token, err := c.authToken()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "roksbnkctl")

	resp, err := kubeconfigHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(body)
		if len(snippet) > 4096 {
			snippet = snippet[:4096]
		}
		return nil, fmt.Errorf("GET %s returned %s: %s", url, resp.Status, strings.TrimSpace(snippet))
	}
	return body, nil
}

// authedDELETE performs an authenticated DELETE. A 404 is treated as success so
// the orphan sweep is idempotent (deleting something already gone is fine).
func (c *Client) authedDELETE(ctx context.Context, url string) error {
	token, err := c.authToken()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "roksbnkctl")

	resp, err := kubeconfigHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("DELETE %s returned %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
