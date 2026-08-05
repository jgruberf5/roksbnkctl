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

// EnsureProject returns the id of the project named name, creating it if absent,
// and sets its target platform to IBM ROKS so the Forge UI doesn't show
// "Target Platform: Unknown".
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
	id := 0
	for _, p := range lr.Projects {
		if p.Name == name {
			id = p.ID
			break
		}
	}
	if id == 0 {
		d, code, err := c.do(ctx, http.MethodPost, "/api/projects",
			map[string]string{"name": name, "description": "roksbnkctl"})
		if err != nil {
			return 0, err
		}
		if !ok(code) {
			return 0, httpErr("POST", "/api/projects", code, d)
		}
		id = createdID(d, "project", "id", "project_id")
		if id == 0 {
			return 0, fmt.Errorf("create project returned no id: %s", strings.TrimSpace(string(d)))
		}
	}
	c.setProjectPlatform(ctx, id)
	return id, nil
}

// setProjectPlatform marks the project as targeting IBM ROKS. A project's target
// platform is configured, not derived from its clusters' detected platform, so
// without this the Forge UI shows "Target Platform: Unknown". Best-effort: the
// platform label is cosmetic and older Forge versions may lack these fields, so
// a failure here must not fail registration.
func (c *Client) setProjectPlatform(ctx context.Context, id int) {
	_, _, _ = c.do(ctx, http.MethodPut, fmt.Sprintf("/api/projects/%d", id), map[string]string{
		"target_platform_profile": "roks",
		"platform_provider":       "ibm",
		"cloud_provider":          "ibm",
	})
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

// RegisterCluster is idempotent: if a cluster with the same name already exists
// in the project (from a prior run — a re-record or a re-triggered CI pipeline),
// it is removed first so the fresh registration (with a current kubeconfig)
// doesn't conflict. Returns the Forge cluster id.
func (c *Client) RegisterCluster(ctx context.Context, projectID int, req RegisterRequest) (int, error) {
	if existing, err := c.projectClusterID(ctx, projectID, req.Name); err == nil && existing != 0 {
		dp := fmt.Sprintf("/api/k8s/clusters/%d", existing)
		d, code, derr := c.do(ctx, http.MethodDelete, dp, nil)
		if derr != nil {
			return 0, derr
		}
		if !ok(code) && code != http.StatusNotFound {
			return 0, httpErr("DELETE", dp, code, d)
		}
	}
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

// projectClusterID returns the id of a cluster named name in projectID, or 0 if
// none. Tolerates both {"clusters":[…]} and a bare array response.
// UnregisterCluster removes a cluster from a BNK Forge project by name.
//
// The delete itself is not new — RegisterCluster has always removed a
// same-named cluster before re-POSTing, which is why re-registering churns the
// cluster id. This exposes that half on its own, so a teardown can undo a
// registration instead of a workspace being able to create one it can never
// remove.
//
// Returns the id that was removed, or 0 when the project holds no cluster of
// that name — absence is not an error, so a destroy can run twice.
// ProjectIDByName returns the id of a project, or 0 when there is none of that
// name. Unlike EnsureProject it never creates one — a teardown asking "is this
// still here" must not bring it into being.
func (c *Client) ProjectIDByName(ctx context.Context, name string) (int, error) {
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
	return 0, nil
}

func (c *Client) UnregisterCluster(ctx context.Context, projectID int, name string) (int, error) {
	id, err := c.projectClusterID(ctx, projectID, name)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, nil
	}
	p := fmt.Sprintf("/api/k8s/clusters/%d", id)
	d, code, derr := c.do(ctx, http.MethodDelete, p, nil)
	if derr != nil {
		return 0, derr
	}
	// 404 means someone else got there first; the end state is the one we want.
	if !ok(code) && code != http.StatusNotFound {
		return 0, httpErr("DELETE", p, code, d)
	}
	return id, nil
}

func (c *Client) projectClusterID(ctx context.Context, projectID int, name string) (int, error) {
	p := fmt.Sprintf("/api/projects/%d/k8s/clusters", projectID)
	data, code, err := c.do(ctx, http.MethodGet, p, nil)
	if err != nil {
		return 0, err
	}
	if !ok(code) {
		return 0, httpErr("GET", p, code, data)
	}
	type clu struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	var wrapped struct {
		Clusters []clu `json:"clusters"`
	}
	_ = json.Unmarshal(data, &wrapped)
	list := wrapped.Clusters
	if len(list) == 0 {
		_ = json.Unmarshal(data, &list) // bare array fallback
	}
	for _, cl := range list {
		if cl.Name == name {
			return cl.ID, nil
		}
	}
	return 0, nil
}
