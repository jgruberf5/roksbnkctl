package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// restHTTPErr lets callers switch on the HTTP status code (e.g. POST /api/projects
// 409 → fall back to GET-and-reuse) without parsing the formatted error string.
type restHTTPErr struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *restHTTPErr) Error() string {
	return fmt.Sprintf("http %d from %s: %s", e.StatusCode, e.URL, e.Body)
}

// RestCreds carries the forge REST login credentials. Both fields are resolved
// by the caller (intent.ForgeSpec.ResolveUsername / ResolvePassword) before
// this package is invoked — rest.go does not read env or cluster.yaml directly.
//
// When Username is empty, "admin" is used. When Password is empty, "changeme"
// is used (the caller should have already warned the user if the default is
// in use). Zero value = all defaults, matching pre-credential-feature behaviour.
type RestCreds struct {
	Username string
	Password string
}

// restUsername returns the effective username — "admin" when unset.
func (c RestCreds) restUsername() string {
	if c.Username != "" {
		return c.Username
	}
	return "admin"
}

// restPassword returns the effective password — "changeme" when unset.
func (c RestCreds) restPassword() string {
	if c.Password != "" {
		return c.Password
	}
	return "changeme"
}

// RegisterREST mirrors Register's shape but uses forge's REST API instead of
// MCP. Used as a fallback when the MCP catalog does not expose create_project
// or create_cluster (catalog-gap detection in Phase09).
//
// Credentials are resolved by the caller via intent.ForgeSpec.ResolveUsername /
// ResolvePassword and passed in as creds. Pass a zero RestCreds for
// back-compat default behaviour (admin/changeme).
func RegisterREST(ctx context.Context, restURL string, req RegisterRequest, creds RestCreds) (RegisterResult, error) {
	if req.WorkspaceName == "" {
		return RegisterResult{}, fmt.Errorf("forge.RegisterREST: workspace name is required")
	}
	if req.WorkspaceDir == "" {
		return RegisterResult{}, fmt.Errorf("forge.RegisterREST: workspace dir is required")
	}
	if req.ClusterName == "" {
		return RegisterResult{}, fmt.Errorf("forge.RegisterREST: cluster name is required")
	}
	if len(req.Kubeconfig) == 0 {
		return RegisterResult{}, fmt.Errorf("forge.RegisterREST: kubeconfig is empty")
	}
	if req.ProjectName == "" {
		req.ProjectName = defaultProjectName(req.WorkspaceName)
	}

	base := strings.TrimRight(restURL, "/")

	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return RegisterResult{}, fmt.Errorf("forge REST login: %w", err)
	}

	proj, err := restCreateProject(ctx, base, token, req)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("forge REST create project: %w", err)
	}

	cluster, err := restCreateCluster(ctx, base, token, proj.ID, req)
	if err != nil {
		// Best-effort rollback.
		_ = restDeleteProject(ctx, base, token, proj.ID)
		return RegisterResult{}, fmt.Errorf("forge REST create cluster: %w", err)
	}

	link := &Link{
		ForgeURL:     restURL,
		ProjectID:    proj.ID,
		ProjectName:  proj.Name,
		ClusterID:    cluster.ID,
		ClusterName:  cluster.Name,
		RegisteredAt: time.Now().UTC(),
		Workspace:    req.WorkspaceName,
		Status:       "registered",
	}
	if err := WriteLink(req.WorkspaceDir, link); err != nil {
		return RegisterResult{Link: link, ForgeURL: restURL},
			fmt.Errorf("registration succeeded but writing forge_link.json failed: %w", err)
	}
	return RegisterResult{Link: link, ForgeURL: restURL}, nil
}

// UnregisterREST tears down the forge-side registration via REST: it deletes
// the cluster record AND the project shell (the project is created by
// registration and named for the cluster — after down nothing should remain).
// Tolerates 404 responses (operator may have cleaned up via forge UI).
// creds carries the forge login credentials; pass a zero RestCreds for
// back-compat default behaviour (admin/changeme).
func UnregisterREST(ctx context.Context, restURL string, link *Link, creds RestCreds) error {
	if link == nil {
		return fmt.Errorf("forge.UnregisterREST: link is nil")
	}
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return fmt.Errorf("forge REST login: %w", err)
	}

	if err := restDeleteCluster(ctx, base, token, link.ProjectID, link.ClusterID); err != nil && !is404(err) {
		return fmt.Errorf("forge REST delete cluster: %w", err)
	}
	if err := restDeleteProject(ctx, base, token, link.ProjectID); err != nil && !is404(err) {
		return fmt.Errorf("forge REST delete project: %w", err)
	}
	return nil
}

// UnregisterRESTByName removes a cluster's forge registration when no local
// forge_link.json exists (e.g. the workspace state directory was lost):
// it logs in over REST, finds the project by the canonical registration name
// ("awsbnkctl-<clusterName>"), deletes the cluster record by name within it
// (tolerating absence), and deletes the project. Returns an error wrapping
// os.ErrNotExist when no such project exists forge-side.
func UnregisterRESTByName(ctx context.Context, restURL, clusterName string, creds RestCreds) error {
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return fmt.Errorf("forge REST login: %w", err)
	}

	proj, err := restFindProjectByName(ctx, base, token, defaultProjectName(clusterName))
	if err != nil {
		// Propagates the os.ErrNotExist wrap so callers can distinguish
		// "never registered" from "lookup failed".
		return err
	}

	cluster, err := restFindClusterByName(ctx, base, token, proj.ID, clusterName)
	switch {
	case err == nil:
		if derr := restDeleteCluster(ctx, base, token, proj.ID, cluster.ID); derr != nil && !is404(derr) {
			return fmt.Errorf("forge REST delete cluster: %w", derr)
		}
	case errors.Is(err, os.ErrNotExist):
		// Cluster record already gone — still purge the project shell below.
	default:
		return err
	}

	if err := restDeleteProject(ctx, base, token, proj.ID); err != nil && !is404(err) {
		return fmt.Errorf("forge REST delete project: %w", err)
	}
	return nil
}

// defaultProjectName returns the canonical forge project name awsbnkctl
// registers a workspace under. Shared by RegisterREST, Register and
// UnregisterRESTByName so the naming convention cannot drift.
func defaultProjectName(workspace string) string {
	return "awsbnkctl-" + workspace
}

// IsMCPCatalogGapErr returns true when err indicates the MCP catalog does not
// expose the requested tool — i.e., the forge MCP server is running an older
// version that pre-dates create_project / create_cluster tools.
// Exported so Phase09 (in the phases package) can use it.
func IsMCPCatalogGapErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "tool not found") ||
		strings.Contains(s, "unknown tool") ||
		strings.Contains(s, "method not found") ||
		strings.Contains(s, "no tool named") ||
		strings.Contains(s, "tool_not_found")
}

// ── REST helpers ──────────────────────────────────────────────────────────────

func restLogin(ctx context.Context, base, username, password string) (string, error) {
	body := map[string]string{"username": username, "password": password}
	var resp struct {
		Token string `json:"token"`
	}
	if err := restPost(ctx, base+"/api/auth/login", "", body, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("forge REST login: empty token in response")
	}
	return resp.Token, nil
}

type restProject struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func restCreateProject(ctx context.Context, base, token string, req RegisterRequest) (restProject, error) {
	body := map[string]any{
		"name":           req.ProjectName,
		"project_type":   "cloud-aws",
		"cloud_provider": "aws",
		"region":         req.Region,
		"environment":    "dev",
		"description":    fmt.Sprintf("Created by awsbnkctl for workspace %q", req.WorkspaceName),
	}
	if p := sendableAWSProfile(req.AWSProfile); p != "" {
		body["aws_profile"] = p
	}
	// Forge has THREE response shapes in the wild for POST /api/projects:
	//   A. wrapped:     {project: {id, name, ...}, success: true}  (>=2.10.x MCP tool docs)
	//   B. flat-id:     {id, name, ...}
	//   C. flat-prefix: {success, project_id, name, message}       (localhost dev, verified 2026-05-21)
	// Probe all three and use whichever has a non-zero ID.
	var resp struct {
		ID        int         `json:"id"`
		ProjectID int         `json:"project_id"`
		Name      string      `json:"name"`
		Project   restProject `json:"project"`
		Success   bool        `json:"success"`
	}
	err := restPost(ctx, base+"/api/projects", token, body, &resp)
	if err != nil {
		// 409 = unique-name violation: a project with this name already exists
		// (orphan from a previous run where the cluster was deleted but the
		// project record was preserved per the soft-delete default). Fall back
		// to GET-list-and-reuse so the kubeconfig still gets re-uploaded
		// against this project on the subsequent cluster POST.
		var herr *restHTTPErr
		if errors.As(err, &herr) && herr.StatusCode == http.StatusConflict {
			fmt.Fprintf(os.Stderr, "[forge] project %q already exists (409) — reusing existing record\n", req.ProjectName)
			existing, lookupErr := restFindProjectByName(ctx, base, token, req.ProjectName)
			if lookupErr != nil {
				return restProject{}, fmt.Errorf("forge REST: 409 on create + lookup failed: %w (original: %v)", lookupErr, err)
			}
			fmt.Fprintf(os.Stderr, "[forge] reusing project id=%d name=%q\n", existing.ID, existing.Name)
			return existing, nil
		}
		return restProject{}, err
	}
	if resp.Project.ID != 0 {
		return resp.Project, nil
	}
	if resp.ID != 0 {
		return restProject{ID: resp.ID, Name: resp.Name}, nil
	}
	if resp.ProjectID != 0 {
		return restProject{ID: resp.ProjectID, Name: resp.Name}, nil
	}
	return restProject{}, fmt.Errorf("forge REST create project: no project ID in response (tried wrapped, flat-id, project_id shapes)")
}

// restFindProjectByName GETs /api/projects and returns the project whose name
// matches exactly. Used by the 409 upsert path. Returns os.ErrNotExist when
// the name is absent so callers can distinguish "lookup worked, not found"
// from "lookup failed".
func restFindProjectByName(ctx context.Context, base, token, name string) (restProject, error) {
	var resp struct {
		Projects []restProject `json:"projects"`
	}
	if err := restGet(ctx, base+"/api/projects", token, &resp); err != nil {
		return restProject{}, fmt.Errorf("list projects: %w", err)
	}
	for _, p := range resp.Projects {
		if p.Name == name {
			return p, nil
		}
	}
	return restProject{}, fmt.Errorf("project %q not found in forge: %w", name, os.ErrNotExist)
}

type restCluster struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func restCreateCluster(ctx context.Context, base, token string, projectID int, req RegisterRequest) (restCluster, error) {
	// Forge REST expects the kubeconfig BASE64-ENCODED (verified against
	// localhost forge 2026-05-21: raw YAML returns "Invalid base64
	// kubeconfig: Incorrect padding"). Encode here, not at the caller,
	// so the MCP path (which sends raw bytes per the existing client)
	// is unaffected.
	encodedKubeconfig := base64.StdEncoding.EncodeToString(req.Kubeconfig)
	body := map[string]any{
		"name":           req.ClusterName,
		"cloud_provider": "aws",
		"region":         req.Region,
		"kubeconfig":     encodedKubeconfig,
	}
	// Same three-shape tolerance as restCreateProject (wrapped, flat-id,
	// flat-prefix).
	var resp struct {
		ID        int         `json:"id"`
		ClusterID int         `json:"cluster_id"`
		Name      string      `json:"name"`
		Cluster   restCluster `json:"cluster"`
		Success   bool        `json:"success"`
	}
	url := fmt.Sprintf("%s/api/projects/%d/k8s/clusters", base, projectID)
	err := restPost(ctx, url, token, body, &resp)
	if err != nil {
		// 409 = the project already contains a cluster with this name. Find
		// it and PUT the fresh kubeconfig so the forge k8s UI doesn't 500 on
		// a stale/missing kubeconfig (user-reported failure mode 2026-05-22).
		var herr *restHTTPErr
		if errors.As(err, &herr) && herr.StatusCode == http.StatusConflict {
			fmt.Fprintf(os.Stderr, "[forge] cluster %q already exists in project %d (409) — refreshing kubeconfig\n",
				req.ClusterName, projectID)
			existing, lookupErr := restFindClusterByName(ctx, base, token, projectID, req.ClusterName)
			if lookupErr != nil {
				return restCluster{}, fmt.Errorf("forge REST: 409 on cluster create + lookup failed: %w (original: %v)", lookupErr, err)
			}
			if updateErr := restUpdateClusterKubeconfig(ctx, base, token, existing.ID, encodedKubeconfig); updateErr != nil {
				return restCluster{}, fmt.Errorf("forge REST: 409 on cluster create + kubeconfig refresh failed: %w", updateErr)
			}
			fmt.Fprintf(os.Stderr, "[forge] cluster id=%d kubeconfig refreshed\n", existing.ID)
			return existing, nil
		}
		return restCluster{}, err
	}
	if resp.Cluster.ID != 0 {
		return resp.Cluster, nil
	}
	if resp.ID != 0 {
		return restCluster{ID: resp.ID, Name: resp.Name}, nil
	}
	if resp.ClusterID != 0 {
		return restCluster{ID: resp.ClusterID, Name: resp.Name}, nil
	}
	return restCluster{}, fmt.Errorf("forge REST create cluster: no cluster ID in response (tried wrapped, flat-id, cluster_id shapes)")
}

// restFindClusterByName GETs /api/projects/{id}/k8s/clusters and returns the
// cluster whose name matches exactly.
func restFindClusterByName(ctx context.Context, base, token string, projectID int, name string) (restCluster, error) {
	var resp struct {
		Clusters []restCluster `json:"clusters"`
	}
	url := fmt.Sprintf("%s/api/projects/%d/k8s/clusters", base, projectID)
	if err := restGet(ctx, url, token, &resp); err != nil {
		return restCluster{}, fmt.Errorf("list clusters: %w", err)
	}
	for _, c := range resp.Clusters {
		if c.Name == name {
			return c, nil
		}
	}
	return restCluster{}, fmt.Errorf("cluster %q not found in project %d: %w", name, projectID, os.ErrNotExist)
}

// restUpdateClusterKubeconfig PUTs a fresh kubeconfig onto an existing cluster
// record. encodedKubeconfig must already be base64-encoded (forge requirement
// — verified against localhost 2026-05-21 + ClusterUpdateRequest schema).
func restUpdateClusterKubeconfig(ctx context.Context, base, token string, clusterID int, encodedKubeconfig string) error {
	body := map[string]any{"kubeconfig": encodedKubeconfig}
	url := fmt.Sprintf("%s/api/k8s/clusters/%d", base, clusterID)
	return restPut(ctx, url, token, body, nil)
}

func restDeleteCluster(ctx context.Context, base, token string, projectID, clusterID int) error {
	url := fmt.Sprintf("%s/api/projects/%d/k8s/clusters/%d", base, projectID, clusterID)
	return restDelete(ctx, url, token)
}

func restDeleteProject(ctx context.Context, base, token string, projectID int) error {
	url := fmt.Sprintf("%s/api/projects/%d", base, projectID)
	return restDelete(ctx, url, token)
}

// restPost sends a POST request with JSON body and decodes the JSON response
// into out. On HTTP status >= 400 returns *restHTTPErr (typed so callers can
// switch on status code, e.g. 409 → upsert fallback).
func restPost(ctx context.Context, url, token string, body, out any) error {
	return restRequest(ctx, http.MethodPost, url, token, body, out)
}

// restPut mirrors restPost but uses HTTP PUT — used by the cluster-409 upsert
// path to PUT a fresh kubeconfig onto an existing cluster record.
func restPut(ctx context.Context, url, token string, body, out any) error {
	return restRequest(ctx, http.MethodPut, url, token, body, out)
}

// restGet sends a GET request and decodes the JSON response into out.
// Status >= 400 returns *restHTTPErr.
func restGet(ctx context.Context, url, token string, out any) error {
	return restRequest(ctx, http.MethodGet, url, token, nil, out)
}

// restRequest is the shared transport for restPost / restPut / restGet.
func restRequest(ctx context.Context, method, url, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return &restHTTPErr{StatusCode: resp.StatusCode, URL: url, Body: truncateREST(string(respBytes), 400)}
	}
	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response from %s: %w", url, err)
		}
	}
	return nil
}

// restDelete sends a DELETE request and tolerates 404.
func restDelete(ctx context.Context, url, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http DELETE %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("http 404 from %s", url)
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d from %s: %s", resp.StatusCode, url, truncateREST(string(b), 400))
	}
	return nil
}

func truncateREST(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
