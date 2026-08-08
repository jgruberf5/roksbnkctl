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
	"errors"
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

// ClusterOwner identifies the Forge project currently holding a cluster.
type ClusterOwner struct {
	ProjectID   int
	ProjectName string
	ClusterID   int
}

// FindClusterOwner looks for a cluster of this name across EVERY project, not just
// the one being registered into.
//
// The project-scoped lookup cannot see a cluster held elsewhere, so registration used
// to POST straight over the top of another project's cluster — either moving it
// silently or failing with a bare exit 1 that named nothing (issue #54).
//
// Returns a zero ClusterOwner when nothing holds the name.
func (c *Client) FindClusterOwner(ctx context.Context, name string) (ClusterOwner, error) {
	data, code, err := c.do(ctx, http.MethodGet, "/api/projects", nil)
	if err != nil {
		return ClusterOwner{}, err
	}
	if !ok(code) {
		return ClusterOwner{}, httpErr("GET", "/api/projects", code, data)
	}
	type proj struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	// Three shapes have to be told apart, and the third must NOT be mistaken for the
	// second: {"projects":[…]}, a bare array, and a body that does not parse at all.
	// `{"projects": null}` is a legitimate EMPTY answer — an unmarshal into a struct
	// leaves the slice nil either way, so keying off nil-ness alone would call an
	// empty Forge unparseable and fail every registration against it.
	var probe map[string]json.RawMessage
	var list []proj
	switch {
	case json.Unmarshal(data, &probe) == nil:
		raw, has := probe["projects"]
		if !has {
			return ClusterOwner{}, fmt.Errorf("GET /api/projects returned an object with no \"projects\" key (%d bytes): %s",
				len(data), truncateBody(data))
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return ClusterOwner{}, fmt.Errorf("GET /api/projects \"projects\" was not a list (%d bytes): %s",
				len(data), truncateBody(data))
		}
	case json.Unmarshal(data, &list) == nil:
		// bare array
	default:
		// An unparseable body must not read as "nobody owns it" — that is precisely
		// the answer that lets a silent takeover through.
		return ClusterOwner{}, fmt.Errorf("GET /api/projects returned an unrecognised body (%d bytes): %s",
			len(data), truncateBody(data))
	}
	for _, pr := range list {
		id, cerr := c.projectClusterID(ctx, pr.ID, name)
		if cerr != nil {
			// One unreadable project must not be reported as "unowned" — that would
			// turn a lookup failure into a silent takeover. Surface it.
			return ClusterOwner{}, fmt.Errorf("checking project %q (%d) for cluster %q: %w", pr.Name, pr.ID, name, cerr)
		}
		if id != 0 {
			return ClusterOwner{ProjectID: pr.ID, ProjectName: pr.Name, ClusterID: id}, nil
		}
	}
	return ClusterOwner{}, nil
}

// ErrClusterOwnedElsewhere is returned when the cluster is registered to a DIFFERENT
// Forge project. Callers surface it as a refusal; --force overrides.
var ErrClusterOwnedElsewhere = errors.New("cluster is registered to another BNK Forge project")

// RegisterCluster records a cluster with a Forge project, NON-DESTRUCTIVELY.
//
// It used to DELETE any same-named cluster and re-POST it. That was described as
// idempotent, and within one project it very nearly is — except the cluster id
// CHANGES, so anything holding a reference breaks, and any scan or history attached
// to the old id is discarded. Across projects it was worse: the project-scoped lookup
// could not see a cluster held elsewhere, so the POST either moved it silently or
// failed with a bare exit 1 (issue #54).
//
// Now:
//   - held by THIS project  → update in place, preserving the id;
//   - held by ANOTHER       → refuse with ErrClusterOwnedElsewhere unless force;
//   - held by nobody        → create.
//
// The in-place update is best-effort: if the Forge build has no PUT for this
// resource, it falls back to the historical delete-and-recreate rather than failing a
// registration that used to work. The cross-project refusal has no such caveat — it
// needs no new endpoint, only the lookup above.
func (c *Client) RegisterCluster(ctx context.Context, projectID int, req RegisterRequest, force bool) (int, error) {
	// Ask THIS project directly first. It is the authoritative, single-request answer
	// for the common case, and it does not depend on /api/projects listing anything —
	// a paginated or permission-filtered project list that omitted the owner would
	// otherwise send us straight to POST, over the top of the very cluster this guard
	// exists to protect.
	owner := ClusterOwner{ProjectID: projectID}
	if id, oerr := c.projectClusterID(ctx, projectID, req.Name); oerr == nil && id != 0 {
		owner.ClusterID = id
	} else {
		// Not ours. Only now is the cross-project scan worth its cost.
		found, ferr := c.FindClusterOwner(ctx, req.Name)
		if ferr != nil {
			return 0, ferr
		}
		owner = found
	}
	switch {
	case owner.ClusterID != 0 && owner.ProjectID != projectID && !force:
		return 0, fmt.Errorf("%w: %q is held by project %q (id %d, cluster id %d). "+
			"Re-registering would move it, changing its cluster id and removing it from that project's view. "+
			"Pass --force to take it over deliberately, or unregister it there first",
			ErrClusterOwnedElsewhere, req.Name, owner.ProjectName, owner.ProjectID, owner.ClusterID)

	case owner.ClusterID != 0:
		// Ours (or a forced takeover): update in place so the id survives.
		up := fmt.Sprintf("/api/k8s/clusters/%d", owner.ClusterID)
		// The PUT body is deliberately ignored: the id we care about is the one we
		// already hold, and the point of this branch is that it does not change.
		_, code, uerr := c.do(ctx, http.MethodPut, up, req)
		if uerr == nil && ok(code) {
			return owner.ClusterID, nil
		}
		// No PUT on this build — fall back to the old behaviour rather than break a
		// registration that used to succeed. The id changes; that is the cost.
		dp := fmt.Sprintf("/api/k8s/clusters/%d", owner.ClusterID)
		d, code2, derr := c.do(ctx, http.MethodDelete, dp, nil)
		if derr != nil {
			return 0, derr
		}
		if !ok(code2) && code2 != http.StatusNotFound {
			return 0, httpErr("DELETE", dp, code2, d)
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

// ProjectIDByName returns the id of a project, or 0 when there is none of that
// name. Unlike EnsureProject it never creates one — a teardown asking "is this
// still here" must not bring it into being.
//
// Tolerates both {"projects":[…]} and a bare array response, and treats a body it
// cannot parse at all as an ERROR rather than as "no project". For EnsureProject a
// mis-parse merely creates a duplicate — loud, and noticed. Here it would print
// "nothing to unregister" and exit 0 while the cluster stayed registered, which is
// the one outcome a teardown must never produce quietly.
func (c *Client) ProjectIDByName(ctx context.Context, name string) (int, error) {
	data, code, err := c.do(ctx, http.MethodGet, "/api/projects", nil)
	if err != nil {
		return 0, err
	}
	if !ok(code) {
		return 0, httpErr("GET", "/api/projects", code, data)
	}
	type proj struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	var wrapped struct {
		Projects []proj `json:"projects"`
	}
	list := []proj(nil)
	switch {
	case json.Unmarshal(data, &wrapped) == nil && wrapped.Projects != nil:
		list = wrapped.Projects
	case json.Unmarshal(data, &list) == nil:
		// bare array fallback, matching projectClusterID
	default:
		// Neither shape parsed. Do NOT fall through to "no project": see the doc
		// comment — a silent success here leaves the cluster registered forever.
		return 0, fmt.Errorf("GET /api/projects returned an unrecognised body (%d bytes): %s",
			len(data), truncateBody(data))
	}
	for _, p := range list {
		if p.Name == name {
			return p.ID, nil
		}
	}
	return 0, nil
}

// truncateBody renders a short, single-line excerpt of a response body for an
// error message — enough to recognise the shape without dumping a page of JSON.
func truncateBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "<empty>"
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// UnregisterCluster removes a cluster from a BNK Forge project by name.
//
// The delete itself is not new — RegisterCluster has always removed a same-named
// cluster before re-POSTing, which is why re-registering churns the cluster id.
// This exposes that half on its own, so a teardown can undo a registration
// instead of a workspace being able to create one it can never remove.
//
// Returns the id that was removed, or 0 when the project holds no cluster of that
// name — absence is not an error, so a destroy can run twice.
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

// projectClusterID returns the id of a cluster named name in projectID, or 0 if
// none. Tolerates both {"clusters":[…]} and a bare array response.
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
