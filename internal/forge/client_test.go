package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockForge is a minimal stateful BNK Forge v3 API for tests. Preseed
// credTemplates / projects / clusters to exercise the find-existing paths.
type mockForge struct {
	token         string
	credTemplates []map[string]any
	projects      []map[string]any
	clusters      []map[string]any // registered clusters (for idempotency)
	deletedIDs    []string         // cluster ids DELETEd
	nextID        int
	registerBody  map[string]any // captured POST body of the last cluster register
	registerPath  string
}

func (m *mockForge) id() int { m.nextID++; return m.nextID }

func (m *mockForge) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Username == "" || body.Password == "" {
			writeJSON(w, 401, map[string]string{"detail": "bad creds"})
			return
		}
		writeJSON(w, 200, map[string]string{"token": m.token})
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+m.token {
			writeJSON(w, 401, map[string]string{"detail": "unauthorized"})
			return
		}
		writeJSON(w, 200, map[string]any{"user": map[string]any{"username": "admin"}})
	})
	mux.HandleFunc("/api/credential-templates", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, m.credTemplates)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["id"] = m.id()
		m.credTemplates = append(m.credTemplates, body)
		writeJSON(w, 201, body)
	})
	mux.HandleFunc("/api/credential-templates/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true}) // PUT update
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"projects": m.projects})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		pid := m.id()
		m.projects = append(m.projects, map[string]any{"id": pid, "name": body["name"]})
		// Forge's create response uses project_id (not id) — regression-guards
		// the field-mapping bug fixed for v1.17.3.
		writeJSON(w, 201, map[string]any{"success": true, "project_id": pid, "name": body["name"]})
	})
	// project-scoped clusters: GET list + POST register (idempotency)
	mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/k8s/clusters") {
			if r.Method == http.MethodGet {
				writeJSON(w, 200, map[string]any{"clusters": m.clusters})
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&m.registerBody)
			m.registerPath = r.URL.Path
			cid := m.id()
			m.clusters = append(m.clusters, map[string]any{"id": cid, "name": m.registerBody["name"]})
			writeJSON(w, 201, map[string]any{"id": cid})
			return
		}
		writeJSON(w, 404, map[string]any{"detail": "not found"})
	})
	// DELETE /api/k8s/clusters/{id} — used by the idempotent re-register.
	mux.HandleFunc("/api/k8s/clusters/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeJSON(w, 200, map[string]any{})
			return
		}
		idStr := strings.TrimPrefix(r.URL.Path, "/api/k8s/clusters/")
		kept := m.clusters[:0]
		for _, cl := range m.clusters {
			if fmt.Sprint(cl["id"]) == idStr {
				m.deletedIDs = append(m.deletedIDs, idStr)
			} else {
				kept = append(kept, cl)
			}
		}
		m.clusters = kept
		w.WriteHeader(204)
	})
	return mux
}

func TestRegisterFlow_CreatesResources(t *testing.T) {
	m := &mockForge{token: "tok123", nextID: 10}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL, false)

	if err := c.Login(ctx, "admin", "pw"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if c.Token != "tok123" {
		t.Fatalf("token = %q, want tok123", c.Token)
	}
	if !c.TokenValid(ctx) {
		t.Fatal("TokenValid = false, want true")
	}
	tid, err := c.EnsureIBMCredentialTemplate(ctx, "roksbnkctl-ws", "APIKEY", "default")
	if err != nil {
		t.Fatalf("ensure cred: %v", err)
	}
	pid, err := c.EnsureProject(ctx, "roks-demo")
	if err != nil {
		t.Fatalf("ensure project: %v", err)
	}
	fid, err := c.RegisterCluster(ctx, pid, RegisterRequest{
		Name: "ws", Provider: "IBM", CloudProvider: "ibm",
		ClusterID: "cid", Region: "eu-gb", TemplateID: tid,
		Kubeconfig: "YXBpVmVyc2lvbjp2MQ==", // base64 — Forge requires it in the body
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if fid == 0 {
		t.Fatal("register returned id 0")
	}
	// The register must hit the project-scoped path and carry the right body.
	wantPath := "/api/projects/" + itoa(pid) + "/k8s/clusters"
	if m.registerPath != wantPath {
		t.Errorf("register path = %q, want %q", m.registerPath, wantPath)
	}
	if m.registerBody["cluster_id"] != "cid" || m.registerBody["provider"] != "IBM" ||
		m.registerBody["cloud_provider"] != "ibm" ||
		m.registerBody["region"] != "eu-gb" || m.registerBody["kubeconfig"] != "YXBpVmVyc2lvbjp2MQ==" {
		t.Errorf("register body = %v", m.registerBody)
	}
	if _, ok := m.registerBody["template_id"]; !ok {
		t.Errorf("register body missing template_id: %v", m.registerBody)
	}
}

func TestEnsureReusesExisting(t *testing.T) {
	m := &mockForge{
		token:         "t",
		nextID:        100,
		credTemplates: []map[string]any{{"id": 5, "name": "roksbnkctl-ws", "provider": "IBM"}},
		projects:      []map[string]any{{"id": 7, "name": "roks-demo"}},
	}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL, false)
	c.Token = "t"

	tid, err := c.EnsureIBMCredentialTemplate(ctx, "roksbnkctl-ws", "K", "rg")
	if err != nil || tid != 5 {
		t.Fatalf("reuse cred: id=%d err=%v (want 5)", tid, err)
	}
	pid, err := c.EnsureProject(ctx, "roks-demo")
	if err != nil || pid != 7 {
		t.Fatalf("reuse project: id=%d err=%v (want 7)", pid, err)
	}
	if len(m.projects) != 1 {
		t.Errorf("existing project should not have been re-created: %v", m.projects)
	}
}

func TestRegisterIdempotent_ReplacesExisting(t *testing.T) {
	// A cluster named "ws" is already registered in the project (a prior run).
	m := &mockForge{token: "t", nextID: 40, clusters: []map[string]any{{"id": 5, "name": "ws"}}}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	c := New(srv.URL, false)
	c.Token = "t"

	fid, err := c.RegisterCluster(context.Background(), 1, RegisterRequest{
		Name: "ws", Provider: "IBM", CloudProvider: "ibm", ClusterID: "cid", Region: "eu-gb", Kubeconfig: "eA==",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(m.deletedIDs) != 1 || m.deletedIDs[0] != "5" {
		t.Errorf("expected the stale cluster id 5 to be DELETEd first, got %v", m.deletedIDs)
	}
	if fid == 0 || fid == 5 {
		t.Errorf("expected a fresh cluster id, got %d", fid)
	}
}

func TestInsecureTLS(t *testing.T) {
	m := &mockForge{token: "tls-tok"}
	srv := httptest.NewTLSServer(m.handler(t))
	defer srv.Close()
	ctx := context.Background()

	// Default (verify on) must fail against the self-signed test cert.
	if err := New(srv.URL, false).Login(ctx, "a", "b"); err == nil {
		t.Fatal("expected TLS verification error with insecure=false")
	}
	// insecure=true skips verification and succeeds.
	if err := New(srv.URL, true).Login(ctx, "a", "b"); err != nil {
		t.Fatalf("insecure login: %v", err)
	}
}

func TestLoginError(t *testing.T) {
	m := &mockForge{token: "x"}
	srv := httptest.NewServer(m.handler(t))
	defer srv.Close()
	if err := New(srv.URL, false).Login(context.Background(), "", ""); err == nil {
		t.Fatal("expected login error on empty creds")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
