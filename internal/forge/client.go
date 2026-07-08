// Package forge is a small REST client for BNK Forge v3 (FastAPI). It replaces
// the legacy shell-out to the v1 `bnk-forge` CLI, which does not exist in v3.
//
// The register flow is credential-backed: an IBM credential template holding the
// IBM Cloud API key is stored in Forge (marked default), so Forge re-derives the
// cluster's cert-based kubeconfig on demand rather than storing a perishable one.
package forge

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a BNK Forge v3 server. Zero value is not usable — use New.
type Client struct {
	BaseURL string
	Token   string
	http    *http.Client
}

// New returns a Client for baseURL. When insecure is true, TLS verification is
// skipped (self-signed certs, common in lab Forge installs).
func New(baseURL string, insecure bool) *Client {
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for self-signed lab certs
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 60 * time.Second, Transport: tr},
	}
}

// do issues a request; body (if non-nil) is JSON-encoded. Returns the raw
// response body and status code.
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("encoding request body: %w", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "roksbnkctl")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading response: %w", err)
	}
	return data, resp.StatusCode, nil
}

func ok(code int) bool { return code >= 200 && code < 300 }

func httpErr(method, path string, code int, body []byte) error {
	s := strings.TrimSpace(string(body))
	if len(s) > 2048 {
		s = s[:2048]
	}
	return fmt.Errorf("%s %s → HTTP %d: %s", method, path, code, s)
}

// numField returns the first of keys in m whose value is a non-zero JSON number.
// Forge's create responses are inconsistent about the id field name
// (`id` vs `project_id` vs `cluster_id`), so callers pass all plausible names.
func numField(m map[string]any, keys ...string) int {
	for _, k := range keys {
		if f, ok := m[k].(float64); ok && f != 0 {
			return int(f)
		}
	}
	return 0
}

// createdID extracts the created object's id from a decoded response body,
// checking the top level then a nested wrapper object (e.g. {"project":{...}}).
func createdID(body []byte, wrapper string, keys ...string) int {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return 0
	}
	if id := numField(m, keys...); id != 0 {
		return id
	}
	if wrapper != "" {
		if w, ok := m[wrapper].(map[string]any); ok {
			return numField(w, keys...)
		}
	}
	return 0
}

// Login exchanges username/password for a session token and stores it on c.
func (c *Client) Login(ctx context.Context, username, password string) error {
	data, code, err := c.do(ctx, http.MethodPost, "/api/auth/login",
		map[string]string{"username": username, "password": password})
	if err != nil {
		return err
	}
	if !ok(code) {
		return httpErr("POST", "/api/auth/login", code, data)
	}
	var r struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("parsing login response: %w", err)
	}
	if r.Token == "" {
		return fmt.Errorf("login succeeded but returned no token")
	}
	c.Token = r.Token
	return nil
}

// TokenValid reports whether c.Token is currently accepted (GET /api/auth/me).
func (c *Client) TokenValid(ctx context.Context) bool {
	if c.Token == "" {
		return false
	}
	_, code, err := c.do(ctx, http.MethodGet, "/api/auth/me", nil)
	return err == nil && ok(code)
}

// Version returns the Forge server version (GET /api/system/version).
func (c *Client) Version(ctx context.Context) (string, error) {
	data, code, err := c.do(ctx, http.MethodGet, "/api/system/version", nil)
	if err != nil {
		return "", err
	}
	if !ok(code) {
		return "", httpErr("GET", "/api/system/version", code, data)
	}
	var r struct {
		CurrentVersion string `json:"current_version"`
	}
	_ = json.Unmarshal(data, &r)
	return r.CurrentVersion, nil
}

type credentialTemplate struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// EnsureIBMCredentialTemplate returns the id of a default IBM credential
// template named name, creating it (or updating the matching one) so it holds
// the given API key + resource group and is marked default. Forge uses it to
// derive the cluster kubeconfig on demand.
func (c *Client) EnsureIBMCredentialTemplate(ctx context.Context, name, apiKey, resourceGroup string) (int, error) {
	data, code, err := c.do(ctx, http.MethodGet, "/api/credential-templates", nil)
	if err != nil {
		return 0, err
	}
	if !ok(code) {
		return 0, httpErr("GET", "/api/credential-templates", code, data)
	}
	var existing []credentialTemplate
	_ = json.Unmarshal(data, &existing)

	fields := map[string]any{
		"ibmcloud_api_key":        apiKey,
		"ibmcloud_resource_group": resourceGroup,
		"is_default":              true,
	}
	for _, t := range existing {
		if t.Name == name {
			p := fmt.Sprintf("/api/credential-templates/%d", t.ID)
			d, code, err := c.do(ctx, http.MethodPut, p, fields)
			if err != nil {
				return 0, err
			}
			if !ok(code) {
				return 0, httpErr("PUT", p, code, d)
			}
			return t.ID, nil
		}
	}

	create := map[string]any{"name": name, "provider": "IBM"}
	for k, v := range fields {
		create[k] = v
	}
	d, code, err := c.do(ctx, http.MethodPost, "/api/credential-templates", create)
	if err != nil {
		return 0, err
	}
	if !ok(code) {
		return 0, httpErr("POST", "/api/credential-templates", code, d)
	}
	if id := createdID(d, "credential_template", "id", "template_id", "credential_template_id"); id != 0 {
		return id, nil
	}
	return 0, fmt.Errorf("create credential-template returned no id: %s", strings.TrimSpace(string(d)))
}

// EnsureProject returns the id of the project named name, creating it if absent.
func (c *Client) EnsureProject(ctx context.Context, name string) (int, error) {
	data, code, err := c.do(ctx, http.MethodGet, "/api/projects", nil)
	if err != nil {
		return 0, err
	}
	if !ok(code) {
		return 0, httpErr("GET", "/api/projects", code, data)
	}
	var lr struct {
		Projects []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	_ = json.Unmarshal(data, &lr)
	for _, p := range lr.Projects {
		if p.Name == name {
			return p.ID, nil
		}
	}
	d, code, err := c.do(ctx, http.MethodPost, "/api/projects",
		map[string]string{"name": name, "description": "roksbnkctl"})
	if err != nil {
		return 0, err
	}
	if !ok(code) {
		return 0, httpErr("POST", "/api/projects", code, d)
	}
	if id := createdID(d, "project", "id", "project_id"); id != 0 {
		return id, nil
	}
	return 0, fmt.Errorf("create project returned no id: %s", strings.TrimSpace(string(d)))
}

// RegisterRequest is the body for registering a cluster into a project. Forge
// requires the cluster kubeconfig (it connects immediately); TemplateID links
// the IBM credential template so Forge can re-derive the kubeconfig later.
type RegisterRequest struct {
	Name string `json:"name"`
	// Provider is the credential provider ("IBM"). CloudProvider is the
	// platform Forge displays (lowercase "ibm"); without it Forge stores the
	// "on-prem" default and the UI shows the platform as Unknown.
	Provider      string `json:"provider"`
	CloudProvider string `json:"cloud_provider"`
	ClusterID     string `json:"cluster_id"`
	Region        string `json:"region"`
	TemplateID    int    `json:"template_id"`
	Kubeconfig    string `json:"kubeconfig"`
}

// RegisterCluster registers a cluster under projectID and returns the Forge
// cluster id.
func (c *Client) RegisterCluster(ctx context.Context, projectID int, req RegisterRequest) (int, error) {
	p := fmt.Sprintf("/api/projects/%d/k8s/clusters", projectID)
	d, code, err := c.do(ctx, http.MethodPost, p, req)
	if err != nil {
		return 0, err
	}
	if !ok(code) {
		return 0, httpErr("POST", p, code, d)
	}
	if id := createdID(d, "cluster", "id", "cluster_id", "registered_cluster_id"); id != 0 {
		return id, nil
	}
	return 0, fmt.Errorf("register returned no cluster id: %s", strings.TrimSpace(string(d)))
}
