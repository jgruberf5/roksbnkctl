package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// forgeRESTServer is a minimal httptest server that serves the forge REST
// endpoints needed by RegisterREST and UnregisterREST.
type forgeRESTServer struct {
	// authFail causes the /api/auth/login endpoint to return 401.
	authFail bool
	// projectFail causes POST /api/projects to return 500.
	projectFail bool
	// clusterFail causes POST /api/projects/{id}/k8s/clusters to return 500.
	clusterFail bool
	// calls records the endpoint+method pairs called.
	calls []string
	// projectBodies records the JSON request body sent to POST /api/projects;
	// lets tests assert that fields like aws_profile / region propagate.
	projectBodies []map[string]any
}

func (s *forgeRESTServer) handler(w http.ResponseWriter, r *http.Request) {
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
		if s.authFail {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})

	case r.Method == http.MethodPost && r.URL.Path == "/api/projects":
		if s.projectFail {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.projectBodies = append(s.projectBodies, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project": map[string]any{"id": 11, "name": "awsbnkctl-default"},
			"success": true,
		})

	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/k8s/clusters"):
		if s.clusterFail {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cluster": map[string]any{"id": 99, "name": "bnk-prod"},
			"success": true,
		})

	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/k8s/clusters/"):
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/api/projects/"):
		w.WriteHeader(http.StatusNoContent)

	default:
		http.NotFound(w, r)
	}
}

func TestRegisterREST_HappyPath(t *testing.T) {
	srv := &forgeRESTServer{}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	dir := t.TempDir()
	res, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "us-east-1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST: %v", err)
	}
	if res.Link == nil {
		t.Fatal("result.Link is nil")
	}
	if res.Link.ProjectID != 11 {
		t.Errorf("ProjectID = %d, want 11", res.Link.ProjectID)
	}
	if res.Link.ClusterID != 99 {
		t.Errorf("ClusterID = %d, want 99", res.Link.ClusterID)
	}
	if res.Link.Status != "registered" {
		t.Errorf("Status = %q, want %q", res.Link.Status, "registered")
	}
	// Link file written.
	if _, err := ReadLink(dir); err != nil {
		t.Errorf("link file not written: %v", err)
	}
}

// TestRegisterREST_FlatProjectIDShape verifies the parser handles the
// localhost-forge response shape `{success, project_id, name, message}`
// (verified live on 2026-05-21).
func TestRegisterREST_FlatProjectIDShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":    true,
			"project_id": 30,
			"name":       "awsbnkctl-default",
			"message":    "Project created successfully",
		})
	})
	mux.HandleFunc("/api/projects/30/k8s/clusters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":    true,
			"cluster_id": 77,
			"name":       "bnk-prod",
			"message":    "Cluster created successfully",
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	res, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "us-east-1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST (flat project_id shape): %v", err)
	}
	if res.Link.ProjectID != 30 {
		t.Errorf("ProjectID = %d, want 30", res.Link.ProjectID)
	}
	if res.Link.ClusterID != 77 {
		t.Errorf("ClusterID = %d, want 77", res.Link.ClusterID)
	}
}

func TestRegisterREST_AuthFailure(t *testing.T) {
	srv := &forgeRESTServer{authFail: true}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  t.TempDir(),
		ClusterName:   "bnk-prod",
		Kubeconfig:    []byte("k"),
	}, RestCreds{})
	if err == nil {
		t.Fatal("expected error on auth failure, got nil")
	}
	if !strings.Contains(err.Error(), "login") {
		t.Errorf("expected login error, got: %v", err)
	}
}

func TestRegisterREST_ProjectCreationFailure(t *testing.T) {
	srv := &forgeRESTServer{projectFail: true}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  t.TempDir(),
		ClusterName:   "bnk-prod",
		Kubeconfig:    []byte("k"),
	}, RestCreds{})
	if err == nil {
		t.Fatal("expected error on project failure, got nil")
	}
}

func TestRegisterREST_ClusterCreationFailure(t *testing.T) {
	srv := &forgeRESTServer{clusterFail: true}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  t.TempDir(),
		ClusterName:   "bnk-prod",
		Kubeconfig:    []byte("k"),
	}, RestCreds{})
	if err == nil {
		t.Fatal("expected error on cluster failure, got nil")
	}
}

func TestUnregisterREST_404Tolerated(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
			return
		}
		// Simulate forge already cleaned up — return 404.
		http.NotFound(w, r)
	}))
	defer ts.Close()

	link := &Link{ProjectID: 11, ClusterID: 99}
	if err := UnregisterREST(context.Background(), ts.URL, link, RestCreds{}); err != nil {
		t.Fatalf("UnregisterREST should tolerate 404, got: %v", err)
	}
}

func TestIsMCPCatalogGap(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"tool not found: create_project", true},
		{"unknown tool create_cluster", true},
		{"method not found", true},
		{"no tool named foo", true},
		{"tool_not_found", true},
		{"connection refused", false},
		{"http 500: internal server error", false},
		{"", false},
	}
	for _, tc := range cases {
		var err error
		if tc.msg != "" {
			err = &testErr{tc.msg}
		}
		if got := IsMCPCatalogGapErr(err); got != tc.want {
			t.Errorf("IsMCPCatalogGapErr(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

// TestRegisterREST_ProjectConflict_UpsertReuses simulates the orphan-project
// case: forge already has a project with the requested name (e.g. from a
// previous run where the cluster was deleted but the project record stayed
// soft-deleted). POST /api/projects returns 409. The upsert path must:
//
//  1. GET /api/projects, find the matching name, and reuse its ID.
//  2. Continue with cluster create — which uploads a fresh kubeconfig.
//
// Verifies the user-reported failure mode (forge UI 500s if kubeconfig is
// missing) is avoided.
func TestRegisterREST_ProjectConflict_UpsertReuses(t *testing.T) {
	const orphanProjectID = 42

	var (
		clusterCreated   bool
		kubeconfigUpload string
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			http.Error(w, `{"error":"project name already exists"}`, http.StatusConflict)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"projects": []map[string]any{
					{"id": 1, "name": "some-other-project"},
					{"id": orphanProjectID, "name": "awsbnkctl-default"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/projects/42/k8s/clusters", func(w http.ResponseWriter, r *http.Request) {
		clusterCreated = true
		// Capture the kubeconfig field so the test can assert it was uploaded.
		var body struct {
			Kubeconfig string `json:"kubeconfig"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		kubeconfigUpload = body.Kubeconfig
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cluster": map[string]any{"id": 555, "name": "bnk-prod"},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	res, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "ap-southeast-2",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST upsert: %v", err)
	}
	if res.Link.ProjectID != orphanProjectID {
		t.Errorf("ProjectID = %d, want %d (orphan reuse)", res.Link.ProjectID, orphanProjectID)
	}
	if res.Link.ClusterID != 555 {
		t.Errorf("ClusterID = %d, want 555", res.Link.ClusterID)
	}
	if !clusterCreated {
		t.Error("cluster create endpoint was not called after project upsert")
	}
	if kubeconfigUpload == "" {
		t.Error("kubeconfig was not uploaded to forge after project upsert")
	}
}

// TestRegisterREST_ClusterConflict_PutsFreshKubeconfig simulates the case
// where both project AND cluster already exist in forge (e.g. a failed-mid-run
// orphan that left both records intact). The cluster-level upsert must
// PUT a fresh kubeconfig onto the existing cluster record so the forge k8s UI
// doesn't 500 on a stale/missing kubeconfig.
func TestRegisterREST_ClusterConflict_PutsFreshKubeconfig(t *testing.T) {
	const orphanProjectID = 42
	const orphanClusterID = 700

	var putKubeconfig string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			http.Error(w, `{"error":"project exists"}`, http.StatusConflict)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"projects": []map[string]any{
					{"id": orphanProjectID, "name": "awsbnkctl-default"},
				},
			})
		}
	})
	mux.HandleFunc("/api/projects/42/k8s/clusters", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			http.Error(w, `{"error":"cluster name already used in this project"}`, http.StatusConflict)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"clusters": []map[string]any{
					{"id": orphanClusterID, "name": "bnk-prod"},
				},
			})
		}
	})
	mux.HandleFunc("/api/k8s/clusters/700", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Kubeconfig string `json:"kubeconfig"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		putKubeconfig = body.Kubeconfig
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	res, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "ap-southeast-2",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\nfresh: true\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST cluster upsert: %v", err)
	}
	if res.Link.ClusterID != orphanClusterID {
		t.Errorf("ClusterID = %d, want %d (orphan reuse)", res.Link.ClusterID, orphanClusterID)
	}
	if putKubeconfig == "" {
		t.Fatal("PUT /api/k8s/clusters/{id} was never called — kubeconfig was NOT refreshed")
	}
	// Sanity: the PUT body should be base64-encoded (forge requirement).
	if !strings.HasPrefix(putKubeconfig, "YXBpVmVyc2lvbjog") { // base64("apiVersion: ")
		t.Errorf("kubeconfig PUT body did not look base64-encoded: prefix=%q", firstN(putKubeconfig, 24))
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// TestRegisterREST_SendsAWSProfile guards the slice-12 enhancement: the
// awsbnkctl-side forge registration must transmit `aws_profile` in the
// POST /api/projects body so forge's EKS-token-mint code has per-project
// AWS identity instead of falling back to the backend's process env.
// Together with the upstream bnk-forge fix that prioritises
// cluster.region, this defends against the silent-401 class of bugs
// when an operator's AWS_PROFILE differs from the backend's.
func TestRegisterREST_SendsAWSProfile(t *testing.T) {
	srv := &forgeRESTServer{}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	dir := t.TempDir()
	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "ap-southeast-2",
		AWSProfile:    "Users-123456789012",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST: %v", err)
	}
	if len(srv.projectBodies) != 1 {
		t.Fatalf("expected 1 project POST, got %d", len(srv.projectBodies))
	}
	got, ok := srv.projectBodies[0]["aws_profile"].(string)
	if !ok || got != "Users-123456789012" {
		t.Errorf("aws_profile = %v (type %T), want %q", srv.projectBodies[0]["aws_profile"], srv.projectBodies[0]["aws_profile"], "Users-123456789012")
	}
}

// TestRegisterREST_OmitsAWSProfileWhenDash verifies the "-" sentinel
// opts a caller out of sending aws_profile (forge then falls back to
// its global env, matching pre-slice-12 behaviour). Useful for tests
// or operators who explicitly do not want a per-project profile.
func TestRegisterREST_OmitsAWSProfileWhenDash(t *testing.T) {
	srv := &forgeRESTServer{}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	dir := t.TempDir()
	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "ap-southeast-2",
		AWSProfile:    "-",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST: %v", err)
	}
	if len(srv.projectBodies) != 1 {
		t.Fatalf("expected 1 project POST, got %d", len(srv.projectBodies))
	}
	if v, present := srv.projectBodies[0]["aws_profile"]; present {
		t.Errorf("aws_profile must be omitted when AWSProfile=\"-\", got %v", v)
	}
}

// ─── RestCreds threading tests ────────────────────────────────────────────────

// TestRestCreds_DefaultsWhenZero verifies the zero RestCreds value resolves to
// admin / changeme (back-compat behaviour).
func TestRestCreds_DefaultsWhenZero(t *testing.T) {
	c := RestCreds{}
	if got := c.restUsername(); got != "admin" {
		t.Errorf("restUsername() = %q, want %q", got, "admin")
	}
	if got := c.restPassword(); got != "changeme" {
		t.Errorf("restPassword() = %q, want %q", got, "changeme")
	}
}

// TestRestCreds_ExplicitValues verifies that non-zero RestCreds values are used
// as-is without any fallback.
func TestRestCreds_ExplicitValues(t *testing.T) {
	c := RestCreds{Username: "operator", Password: "s3cr3t"}
	if got := c.restUsername(); got != "operator" {
		t.Errorf("restUsername() = %q, want %q", got, "operator")
	}
	if got := c.restPassword(); got != "s3cr3t" {
		t.Errorf("restPassword() = %q, want %q", got, "s3cr3t")
	}
}

// TestRegisterREST_CredsThreadedToLogin confirms that RegisterREST forwards the
// RestCreds to the /api/auth/login call. The test server records the username +
// password sent in the login request body and asserts they match the creds.
func TestRegisterREST_CredsThreadedToLogin(t *testing.T) {
	var capturedUser, capturedPass string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedUser = body.Username
		capturedPass = body.Password
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "awsbnkctl-default"})
	})
	mux.HandleFunc("/api/projects/1/k8s/clusters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 2, "name": "bnk-prod"})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	creds := RestCreds{Username: "myuser", Password: "mypass"}
	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  t.TempDir(),
		ClusterName:   "bnk-prod",
		Region:        "us-east-1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, creds)
	if err != nil {
		t.Fatalf("RegisterREST: %v", err)
	}
	if capturedUser != "myuser" {
		t.Errorf("login username = %q, want %q", capturedUser, "myuser")
	}
	if capturedPass != "mypass" {
		t.Errorf("login password = %q, want %q", capturedPass, "mypass")
	}
}

// TestUnregisterREST_CredsThreadedToLogin confirms that UnregisterREST forwards
// the RestCreds to the /api/auth/login call.
func TestUnregisterREST_CredsThreadedToLogin(t *testing.T) {
	var capturedUser, capturedPass string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/login" {
			var body struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			capturedUser = body.Username
			capturedPass = body.Password
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
			return
		}
		// Return 404 for the cluster delete — tolerated by UnregisterREST.
		http.NotFound(w, r)
	}))
	defer ts.Close()

	creds := RestCreds{Username: "svc", Password: "hunter2"}
	link := &Link{ProjectID: 5, ClusterID: 10}
	if err := UnregisterREST(context.Background(), ts.URL, link, creds); err != nil {
		t.Fatalf("UnregisterREST: %v", err)
	}
	if capturedUser != "svc" {
		t.Errorf("login username = %q, want %q", capturedUser, "svc")
	}
	if capturedPass != "hunter2" {
		t.Errorf("login password = %q, want %q", capturedPass, "hunter2")
	}
}
