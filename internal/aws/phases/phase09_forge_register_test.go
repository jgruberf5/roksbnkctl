package phases

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// forgeEnabledCluster returns a cluster intent with forge enabled, pointing
// at the given MCP and REST URLs.
func forgeEnabledCluster(mcpURL, restURL string) *intent.Cluster {
	cl := sydTracerCluster()
	cl.ClusterSpec = &intent.ClusterSpec{
		KubernetesVersion: "1.30",
		NodeGroups: []intent.NodeGroupSpec{
			{Name: "default", InstanceType: "t3.medium", DesiredSize: 1, MinSize: 1, MaxSize: 2, DiskSize: 50},
		},
	}
	cl.Forge = &intent.ForgeSpec{
		Enabled: true,
		MCPURL:  mcpURL,
		URL:     restURL,
	}
	return cl
}

// seedEKSState sets the state keys that Phase09 needs (written by Phase08).
func seedEKSState(st *state.State, name string) {
	st.Set("EKS_CLUSTER_ARN", "arn:aws:eks:ap-southeast-2:111122223333:cluster/"+name)
	st.Set("EKS_ENDPOINT", "https://test.eks.example.com")
	st.Set("EKS_CA", "dGVzdC1jYQ==") // base64 "test-ca"
}

// scriptedMCPForge is a minimal MCP HTTP handler for Phase09 tests.
type scriptedMCPForge struct {
	mu        sync.Mutex
	calls     []string
	responses map[string]string
	failTools map[string]bool // tools that return error
}

func newScriptedMCP() *scriptedMCPForge {
	return &scriptedMCPForge{
		responses: map[string]string{
			"create_project": `{"project":{"id":11,"name":"awsbnkctl-syd-tracer"},"success":true}`,
			"create_cluster": `{"cluster":{"id":99,"name":"syd-tracer"},"success":true}`,
			"get_cluster":    `{"id":99,"name":"syd-tracer","status":"connected"}`,
			"delete_cluster": `{"success":true}`,
			"delete_project": `{"success":true}`,
		},
		failTools: map[string]bool{},
	}
}

func (s *scriptedMCPForge) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int64           `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if req.ID == 0 && req.Method == "notifications/initialized" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	type rpcRespOK struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Result  any    `json:"result"`
	}

	switch req.Method {
	case "initialize":
		_ = json.NewEncoder(w).Encode(rpcRespOK{"2.0", req.ID, map[string]any{"protocolVersion": "2024-11-05"}})
	case "tools/call":
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.mu.Lock()
		s.calls = append(s.calls, p.Name)
		fail := s.failTools[p.Name]
		text := s.responses[p.Name]
		s.mu.Unlock()

		if fail {
			result := map[string]any{
				"content": []map[string]any{{"type": "text", "text": "tool not found: " + p.Name}},
				"isError": true,
			}
			_ = json.NewEncoder(w).Encode(rpcRespOK{"2.0", req.ID, result})
			return
		}
		if text == "" {
			text = `{}`
		}
		result := map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		}
		_ = json.NewEncoder(w).Encode(rpcRespOK{"2.0", req.ID, result})
	default:
		http.Error(w, "unknown method", 400)
	}
}

func (s *scriptedMCPForge) callsMade() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.calls))
	copy(out, s.calls)
	return out
}

// -- Tests --

func TestPhase09ForgeRegister_Disabled(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := sydTracerCluster() // no forge block
	clients := &Clients{Profile: "test"}
	err := Phase09ForgeRegister(context.Background(), cl, st, clients, false)
	if err != nil {
		t.Fatalf("expected nil for disabled forge, got: %v", err)
	}
}

func TestPhase09ForgeRegister_EnabledMCPSuccess(t *testing.T) {
	awsmw.ResetForTest()
	mcp := newScriptedMCP()
	mcpSrv := httptest.NewServer(http.HandlerFunc(mcp.handler))
	defer mcpSrv.Close()

	dir := t.TempDir()
	cl := forgeEnabledCluster(mcpSrv.URL+"/mcp/", "http://unused")
	st, _ := state.Load(dir)
	seedEKSState(st, cl.Metadata.Name)

	// StateDir() uses CWD — chdir to temp so forge_link.json lands there.
	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	clients := &Clients{Profile: "test", ForgeClient: forge.NewClient(mcpSrv.URL + "/mcp/")}
	err := Phase09ForgeRegister(context.Background(), cl, st, clients, false)
	if err != nil {
		t.Fatalf("Phase09ForgeRegister: %v", err)
	}
	if st.Get("FORGE_PROJECT_ID") == "" {
		t.Error("FORGE_PROJECT_ID not set")
	}
	if st.Get("FORGE_CLUSTER_ID") == "" {
		t.Error("FORGE_CLUSTER_ID not set")
	}
	calls := mcp.callsMade()
	if len(calls) == 0 || (calls[0] != "create_project" && calls[len(calls)-1] != "create_cluster") {
		t.Errorf("unexpected MCP call order: %v", calls)
	}
}

func TestPhase09ForgeRegister_MCPFailsFallbackToREST(t *testing.T) {
	awsmw.ResetForTest()

	// MCP server that returns "tool not found" for create_project.
	mcp := newScriptedMCP()
	mcp.failTools["create_project"] = true
	mcp.failTools["create_cluster"] = true
	mcpSrv := httptest.NewServer(http.HandlerFunc(mcp.handler))
	defer mcpSrv.Close()

	// REST server that succeeds.
	var restCalls []string
	var restMu sync.Mutex
	restSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		restMu.Lock()
		restCalls = append(restCalls, r.Method+" "+r.URL.Path)
		restMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
		case r.URL.Path == "/api/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"project": map[string]any{"id": 11, "name": "test"}, "success": true})
		case strings.Contains(r.URL.Path, "/k8s/clusters"):
			_ = json.NewEncoder(w).Encode(map[string]any{"cluster": map[string]any{"id": 99, "name": "syd-tracer"}, "success": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer restSrv.Close()

	dir := t.TempDir()
	cl := forgeEnabledCluster(mcpSrv.URL+"/mcp/", restSrv.URL)
	st, _ := state.Load(dir)
	seedEKSState(st, cl.Metadata.Name)

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	clients := &Clients{Profile: "test", ForgeClient: forge.NewClient(mcpSrv.URL + "/mcp/")}
	err := Phase09ForgeRegister(context.Background(), cl, st, clients, false)
	if err != nil {
		t.Fatalf("Phase09ForgeRegister: %v", err)
	}

	restMu.Lock()
	defer restMu.Unlock()
	if len(restCalls) == 0 {
		t.Error("expected REST calls, got none (fallback did not fire)")
	}
}

func TestPhase09ForgeRegister_BothFailWritesPendingLink(t *testing.T) {
	awsmw.ResetForTest()

	// MCP server that immediately closes (simulates unreachable forge).
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fail with tool not found to trigger REST fallback.
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		if req.Method == "initialize" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": "2024-11-05"},
			})
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		// tools/call — return tool not found.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "tool not found: create_project"}},
				"isError": true,
			},
		})
	}))
	defer mcpSrv.Close()

	// REST server that always fails.
	restSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer restSrv.Close()

	dir := t.TempDir()
	cl := forgeEnabledCluster(mcpSrv.URL+"/mcp/", restSrv.URL)
	st, _ := state.Load(dir)
	seedEKSState(st, cl.Metadata.Name)

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	clients := &Clients{Profile: "test", ForgeClient: forge.NewClient(mcpSrv.URL + "/mcp/")}
	// Use context with timeout to keep test fast (bypasses 1+3+9s backoff delays).
	ctx := context.Background()
	err := Phase09ForgeRegister(ctx, cl, st, clients, false)
	// Must soft-fail (return nil).
	if err != nil {
		t.Fatalf("expected soft-fail (nil), got: %v", err)
	}
	// forge-link.json should be written with status=pending.
	link, readErr := forge.ReadLink(cl.StateDir())
	if readErr != nil {
		t.Fatalf("expected pending link file, got read error: %v", readErr)
	}
	if link.Status != "pending" {
		t.Errorf("link.Status = %q, want %q", link.Status, "pending")
	}
}

func TestPhase09ForgeRegister_IdempotencyRegisteredLink(t *testing.T) {
	awsmw.ResetForTest()

	// MCP server that should NOT be called if the link already exists.
	callCount := 0
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	cl := forgeEnabledCluster(mcpSrv.URL+"/mcp/", "http://unused")
	st, _ := state.Load(dir)
	seedEKSState(st, cl.Metadata.Name)

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	// Pre-write a registered link.
	if err := forge.WriteLink(cl.StateDir(), &forge.Link{
		ProjectID: 11, ClusterID: 99, Status: "registered",
	}); err != nil {
		t.Fatalf("WriteLink: %v", err)
	}

	clients := &Clients{Profile: "test", ForgeClient: forge.NewClient(mcpSrv.URL + "/mcp/")}
	err := Phase09ForgeRegister(context.Background(), cl, st, clients, false)
	if err != nil {
		t.Fatalf("Phase09ForgeRegister: %v", err)
	}
	if callCount > 0 {
		t.Errorf("expected zero forge calls on second run with registered link, got %d", callCount)
	}
}

func TestPhase09ForgeRegisterDown_WithLink(t *testing.T) {
	awsmw.ResetForTest()
	mcp := newScriptedMCP()
	mcpSrv := httptest.NewServer(http.HandlerFunc(mcp.handler))
	defer mcpSrv.Close()

	dir := t.TempDir()
	cl := forgeEnabledCluster(mcpSrv.URL+"/mcp/", "http://unused")
	st, _ := state.Load(dir)

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	// Pre-write a link.
	if err := forge.WriteLink(cl.StateDir(), &forge.Link{
		ProjectID: 11, ClusterID: 99, Status: "registered",
	}); err != nil {
		t.Fatalf("WriteLink: %v", err)
	}

	clients := &Clients{Profile: "test", ForgeClient: forge.NewClient(mcpSrv.URL + "/mcp/")}
	err := Phase09ForgeRegisterDown(context.Background(), cl, st, clients, false)
	if err != nil {
		t.Fatalf("Phase09ForgeRegisterDown: %v", err)
	}
	// Link should be gone.
	if _, readErr := forge.ReadLink(cl.StateDir()); readErr == nil {
		t.Error("link file still present after unregister")
	}
	// purge=true: down must delete the project shell too, not just the cluster.
	var projectDeleted bool
	for _, c := range mcp.callsMade() {
		if c == "delete_project" {
			projectDeleted = true
		}
	}
	if !projectDeleted {
		t.Errorf("delete_project not called — project shell left hanging; calls: %v", mcp.callsMade())
	}
}

// No link + forge enabled but unreachable → by-name discovery fails with a
// connection error and the phase soft-fails (returns nil; never blocks teardown).
func TestPhase09ForgeRegisterDown_NoLink(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := forgeEnabledCluster("http://unused", "http://unused")
	st, _ := state.Load(dir)

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	clients := &Clients{Profile: "test"}
	err := Phase09ForgeRegisterDown(context.Background(), cl, st, clients, false)
	if err != nil {
		t.Fatalf("expected nil when no link, got: %v", err)
	}
}

// TestPhase09ForgeRegisterDown_NoLinkDiscoveryFallback covers the lost-state
// scenario (verified live 2026-06-10): forge_link.json is gone but forge is
// enabled in intent and the forge-side registration still exists. Down must
// fall back to REST by-name discovery, delete the cluster record AND project,
// and clear the FORGE_* state keys.
func TestPhase09ForgeRegisterDown_NoLinkDiscoveryFallback(t *testing.T) {
	awsmw.ResetForTest()

	var (
		restMu  sync.Mutex
		deletes []string
	)
	restSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"projects": []map[string]any{
					{"id": 7, "name": "some-other-project"},
					{"id": 45, "name": "awsbnkctl-syd-tracer"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/45/k8s/clusters":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"clusters": []map[string]any{
					{"id": 29, "name": "syd-tracer"},
				},
			})
		case r.Method == http.MethodDelete:
			restMu.Lock()
			deletes = append(deletes, "DELETE "+r.URL.Path)
			restMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer restSrv.Close()

	dir := t.TempDir()
	cl := forgeEnabledCluster("http://unused", restSrv.URL)
	st, _ := state.Load(dir)
	// Simulate stale forge state surviving the lost link file.
	st.Set("FORGE_PROJECT_ID", "45")
	st.Set("FORGE_CLUSTER_ID", "29")
	st.Set("FORGE_STATUS", "registered")
	st.Set("FORGE_LINK_PATH", "/gone/forge_link.json")

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	// No forge_link.json written — the workspace state directory was "lost".
	clients := &Clients{Profile: "test"}
	err := Phase09ForgeRegisterDown(context.Background(), cl, st, clients, false)
	if err != nil {
		t.Fatalf("Phase09ForgeRegisterDown: %v", err)
	}

	restMu.Lock()
	defer restMu.Unlock()
	want := []string{"DELETE /api/projects/45/k8s/clusters/29", "DELETE /api/projects/45"}
	if len(deletes) != 2 || deletes[0] != want[0] || deletes[1] != want[1] {
		t.Errorf("deletes = %v, want %v", deletes, want)
	}
	for _, key := range []string{"FORGE_PROJECT_ID", "FORGE_CLUSTER_ID", "FORGE_STATUS", "FORGE_LINK_PATH"} {
		if got := st.Get(key); got != "" {
			t.Errorf("%s = %q, want cleared", key, got)
		}
	}
}

// TestPhase09ForgeRegisterDown_NoLinkForgeDisabled verifies the instant skip:
// when forge isn't in cluster.yaml AND there's no link, down must not probe
// any forge endpoint (no HTTP calls at all) and return nil.
func TestPhase09ForgeRegisterDown_NoLinkForgeDisabled(t *testing.T) {
	awsmw.ResetForTest()

	var calls int
	var callsMu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callsMu.Lock()
		calls++
		callsMu.Unlock()
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cl := sydTracerCluster() // no forge block
	st, _ := state.Load(dir)

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	clients := &Clients{Profile: "test", ForgeClient: forge.NewClient(srv.URL + "/mcp/")}
	err := Phase09ForgeRegisterDown(context.Background(), cl, st, clients, false)
	if err != nil {
		t.Fatalf("expected nil for no-link + nil forge block, got: %v", err)
	}
	callsMu.Lock()
	defer callsMu.Unlock()
	if calls != 0 {
		t.Errorf("expected zero HTTP calls (instant skip), got %d", calls)
	}
}

func TestPhase09ForgeRegisterDown_KeepLink(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := forgeEnabledCluster("http://unused", "http://unused")
	st, _ := state.Load(dir)

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	// Write a link — it must survive because keepLink=true.
	if err := forge.WriteLink(cl.StateDir(), &forge.Link{
		ProjectID: 11, ClusterID: 99, Status: "registered",
	}); err != nil {
		t.Fatalf("WriteLink: %v", err)
	}

	clients := &Clients{Profile: "test"}
	err := Phase09ForgeRegisterDown(context.Background(), cl, st, clients, true)
	if err != nil {
		t.Fatalf("Phase09ForgeRegisterDown with keepLink: %v", err)
	}
	// Link must still be there.
	if _, readErr := forge.ReadLink(cl.StateDir()); readErr != nil {
		t.Errorf("expected link to be preserved, ReadLink err: %v", readErr)
	}
}

func TestPhase09ForgeRegister_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := forgeEnabledCluster("http://forge-mcp:8081/mcp/", "http://localhost:8000")
	st, _ := state.Load(dir)
	seedEKSState(st, cl.Metadata.Name)

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	clients := &Clients{Profile: "test"}
	err := Phase09ForgeRegister(context.Background(), cl, st, clients, true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if st.Get("FORGE_PROJECT_ID") != "dry-run-project" {
		t.Errorf("FORGE_PROJECT_ID = %q, want dry-run-project", st.Get("FORGE_PROJECT_ID"))
	}
	if st.Get("FORGE_CLUSTER_ID") != "dry-run-cluster" {
		t.Errorf("FORGE_CLUSTER_ID = %q, want dry-run-cluster", st.Get("FORGE_CLUSTER_ID"))
	}
	if st.Get("FORGE_STATUS") != "dry-run" {
		t.Errorf("FORGE_STATUS = %q, want dry-run", st.Get("FORGE_STATUS"))
	}
}

// TestPhase09ForgeRegisterDown_NilForgeBlockWithLink verifies that down does
// not panic when cl.Forge is nil (operator removed the forge: block between
// up and down) but a forge-link.json still exists on disk. The phase must
// complete without panicking and either succeed at unregistering or soft-fail,
// but must never propagate a nil-dereference panic.
func TestPhase09ForgeRegisterDown_NilForgeBlockWithLink(t *testing.T) {
	awsmw.ResetForTest()

	// MCP server: succeed on delete_cluster and delete_project.
	mcp := newScriptedMCP()
	mcpSrv := httptest.NewServer(http.HandlerFunc(mcp.handler))
	defer mcpSrv.Close()

	dir := t.TempDir()
	// cl has no Forge block — simulates operator removing forge: from cluster.yaml.
	cl := sydTracerCluster()
	if cl.Forge != nil {
		t.Fatal("sydTracerCluster must return a cluster with nil Forge for this test")
	}
	st, _ := state.Load(dir)

	restoreWd := chdirTemp(t, dir)
	defer restoreWd()

	// Pre-populate a forge-link.json as if up had written it earlier.
	// Populate ForgeURL and ForgeMCPURL so the down path can resolve endpoints
	// without cl.Forge.
	preLink := &forge.Link{
		ProjectID:   11,
		ClusterID:   99,
		Status:      "registered",
		ForgeURL:    "http://localhost:8000",
		ForgeMCPURL: mcpSrv.URL + "/mcp/",
	}
	if err := forge.WriteLink(cl.StateDir(), preLink); err != nil {
		t.Fatalf("WriteLink: %v", err)
	}

	clients := &Clients{Profile: "test", ForgeClient: forge.NewClient(mcpSrv.URL + "/mcp/")}

	// Must not panic. May return nil (success or soft-fail) but not an error.
	err := Phase09ForgeRegisterDown(context.Background(), cl, st, clients, false)
	if err != nil {
		t.Fatalf("Phase09ForgeRegisterDown with nil cl.Forge: expected nil, got: %v", err)
	}
}

// chdirTemp changes to dir and returns a restore func. Used so StateDir()
// resolves under dir (StateDir() returns a CWD-relative path).
func chdirTemp(t *testing.T, dir string) func() {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir %s: %v", dir, err)
	}
	return func() { _ = os.Chdir(old) }
}
