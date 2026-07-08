package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockForge is a minimal stateful BNK Forge v3 API for tests. Preseed
// credTemplates / projects to exercise the reuse (find-existing) paths.
type mockForge struct {
	token         string
	credTemplates []map[string]any
	projects      []map[string]any
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
		body["id"] = m.id()
		m.projects = append(m.projects, body)
		writeJSON(w, 201, body)
	})
	// register: POST /api/projects/{pid}/k8s/clusters
	mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&m.registerBody)
		m.registerPath = r.URL.Path
		writeJSON(w, 201, map[string]any{"id": m.id()})
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
		Name: "ws", Provider: "IBM", ClusterID: "cid", Region: "eu-gb", TemplateID: tid,
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
		m.registerBody["region"] != "eu-gb" {
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
