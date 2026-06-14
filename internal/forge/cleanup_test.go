package forge_test

// cleanup_test.go — unit tests for forge benchmark artifact cleanup.
//
// Uses a mock HTTP server (httptest) to verify:
//   - Happy-path (down scope): proxy → target → exact-name agent → exact-name
//     ssh-credential deleted; other-instance agents, forge-local, configs left alone
//   - Happy-path (full scope): proxy → target → awsbnkctl agents → awsbnkctl
//     ssh-credentials → configs deleted; non-awsbnkctl records preserved
//   - 404 idempotency: second run is a clean no-op (agent + ssh-cred 404s → nil)
//   - 401 / forge-unreachable: returns error (callers soft-fail and log)
//   - No DELETE to /api/benchmarks/runs or /api/benchmarks/results is ever issued
//   - Partial-delete error path: one delete fails, rest continue, errors aggregated
//   - 409 on ssh-credential delete is a soft warning (collected, not fatal)
//   - Empty jumphostInstanceID: skips steps 3+4, leaves agent/ssh-cred untouched

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/forge"
)

// ---------------------------------------------------------------------------
// Mock server for cleanup tests
// ---------------------------------------------------------------------------

type cleanupServer struct {
	// Configuration — 0 means use the default success code.
	listTargetsStatus   int
	listProxiesStatus   int
	deleteTargetStatus  int // 0 = 204
	deleteProxyStatus   int // 0 = 202
	deleteAgentStatus   int // 0 = 204
	deleteConfigStatus  int // 0 = 204
	deleteSSHCredStatus int // 0 = 204
	loginStatus         int // 0 = 200

	// Partial-failure control: if set, this proxy ID returns an error.
	proxyErrorID int
	proxyErrCode int // HTTP status code for the erroring proxy

	// State for enumeration responses.
	// targets is returned as a BARE JSON array — matching the shape that
	// benchmarkTargetFindByName (target.go) already uses for this endpoint.
	targets  []map[string]any
	proxies  map[int][]map[string]any // keyed by target ID
	agents   []map[string]any
	sshCreds []map[string]any
	configs  []map[string]any

	// Recorded DELETE calls.
	deletedTargetIDs  []int
	deletedProxyIDs   []int
	deletedAgentIDs   []int
	deletedSSHCredIDs []int
	deletedConfigIDs  []int
	// Track all DELETE paths to assert no runs/results are deleted.
	allDeletedPaths []string
}

func (s *cleanupServer) status(configured, successCode int) int {
	if configured != 0 {
		return configured
	}
	return successCode
}

func (s *cleanupServer) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Track every DELETE call.
	if r.Method == http.MethodDelete {
		s.allDeletedPaths = append(s.allDeletedPaths, r.URL.Path)
	}

	switch {
	// ── Auth ────────────────────────────────────────────────────────────────
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
		code := s.status(s.loginStatus, http.StatusOK)
		w.WriteHeader(code)
		if code == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
		}

	// ── Targets: GET /api/benchmarks/targets ────────────────────────────────
	// Returns a bare JSON array — matches benchmarkTargetFindByName in target.go.
	case r.Method == http.MethodGet && r.URL.Path == forge.BenchmarkTargetEndpoint:
		code := s.status(s.listTargetsStatus, http.StatusOK)
		w.WriteHeader(code)
		if code == http.StatusOK {
			list := s.targets
			if list == nil {
				list = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(list)
		}

	// ── Proxies: GET /api/benchmarks/targets/{id}/proxies ───────────────────
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/proxies") &&
		!strings.Contains(r.URL.Path, "/proxies/"):
		var targetID int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkTargetEndpoint+"/"), "%d/proxies", &targetID)
		code := s.status(s.listProxiesStatus, http.StatusOK)
		w.WriteHeader(code)
		if code == http.StatusOK {
			list := s.proxies[targetID]
			if list == nil {
				list = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(list)
		}

	// ── DELETE proxy: DELETE /api/benchmarks/targets/{id}/proxies/{pid} ─────
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/proxies/"):
		var targetID, proxyID int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkTargetEndpoint+"/"), "%d/proxies/%d", &targetID, &proxyID)
		s.deletedProxyIDs = append(s.deletedProxyIDs, proxyID)
		// Support per-proxy error injection.
		if s.proxyErrorID != 0 && proxyID == s.proxyErrorID && s.proxyErrCode != 0 {
			w.WriteHeader(s.proxyErrCode)
			return
		}
		code := s.status(s.deleteProxyStatus, http.StatusAccepted)
		w.WriteHeader(code)

	// ── DELETE target: DELETE /api/benchmarks/targets/{id} ──────────────────
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, forge.BenchmarkTargetEndpoint+"/"):
		var id int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkTargetEndpoint+"/"), "%d", &id)
		s.deletedTargetIDs = append(s.deletedTargetIDs, id)
		code := s.status(s.deleteTargetStatus, http.StatusNoContent)
		w.WriteHeader(code)

	// ── Agents: GET /api/benchmarks/agents ──────────────────────────────────
	case r.Method == http.MethodGet && r.URL.Path == forge.BenchmarkAgentEndpoint:
		list := s.agents
		if list == nil {
			list = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(list)

	// ── DELETE agent: DELETE /api/benchmarks/agents/{id} ────────────────────
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, forge.BenchmarkAgentEndpoint+"/"):
		var id int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkAgentEndpoint+"/"), "%d", &id)
		s.deletedAgentIDs = append(s.deletedAgentIDs, id)
		code := s.status(s.deleteAgentStatus, http.StatusNoContent)
		w.WriteHeader(code)

	// ── SSH credentials: GET /api/ssh-credentials ───────────────────────────
	case r.Method == http.MethodGet && r.URL.Path == forge.SSHCredentialEndpoint:
		list := s.sshCreds
		if list == nil {
			list = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(list)

	// ── DELETE ssh-credential: DELETE /api/ssh-credentials/{id} ────────────
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, forge.SSHCredentialEndpoint+"/"):
		var id int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.SSHCredentialEndpoint+"/"), "%d", &id)
		s.deletedSSHCredIDs = append(s.deletedSSHCredIDs, id)
		code := s.status(s.deleteSSHCredStatus, http.StatusNoContent)
		w.WriteHeader(code)

	// ── Configs: GET /api/benchmarks/configs ────────────────────────────────
	case r.Method == http.MethodGet && r.URL.Path == forge.BenchmarkConfigEndpoint:
		list := s.configs
		if list == nil {
			list = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(list)

	// ── DELETE config: DELETE /api/benchmarks/configs/{id} ──────────────────
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, forge.BenchmarkConfigEndpoint+"/"):
		var id int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkConfigEndpoint+"/"), "%d", &id)
		s.deletedConfigIDs = append(s.deletedConfigIDs, id)
		code := s.status(s.deleteConfigStatus, http.StatusNoContent)
		w.WriteHeader(code)

	default:
		http.NotFound(w, r)
	}
}

func startCleanupServer(s *cleanupServer) (*httptest.Server, func()) {
	ts := httptest.NewServer(http.HandlerFunc(s.handler))
	origDo := *forge.BenchmarkHTTPDoFn
	*forge.BenchmarkHTTPDoFn = ts.Client().Do
	return ts, func() {
		*forge.BenchmarkHTTPDoFn = origDo
		ts.Close()
	}
}

// ---------------------------------------------------------------------------
// Tests — down-path (DeleteClusterBenchmarkArtifacts)
// ---------------------------------------------------------------------------

// TestDeleteClusterBenchmarkArtifacts_HappyPath verifies the down-path delete
// sequence: proxy → target → exact-name agent → exact-name ssh-credential.
// Other-instance agents, forge-local, and all configs must NOT be deleted.
func TestDeleteClusterBenchmarkArtifacts_HappyPath(t *testing.T) {
	const thisInstance = "i-THIS"
	const otherInstance = "i-OTHER"
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(10), "name": "awsbnkctl-ai-rig-llama3", "cluster_id": float64(7)},
			// A target from a different cluster — must NOT be deleted.
			{"id": float64(11), "name": "other-cluster-target", "cluster_id": float64(99)},
		},
		proxies: map[int][]map[string]any{
			10: {{"id": float64(20)}},
		},
		agents: []map[string]any{
			{"id": float64(3), "name": "awsbnkctl-jumphost-" + thisInstance},  // exact match → deleted
			{"id": float64(4), "name": "awsbnkctl-jumphost-" + otherInstance}, // different instance → preserved
			{"id": float64(2), "name": "forge-local"},                         // builtin → preserved
		},
		sshCreds: []map[string]any{
			{"id": float64(5), "name": "awsbnkctl-jumphost-" + thisInstance},  // exact match → deleted
			{"id": float64(6), "name": "awsbnkctl-jumphost-" + otherInstance}, // different instance → preserved
		},
		configs: []map[string]any{
			{"id": float64(8), "name": "awsbnkctl-latency"},
			{"id": float64(9), "name": "awsbnkctl-throughput"},
			{"id": float64(1), "name": "some-other-config"},
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, thisInstance)
	if err != nil {
		t.Fatalf("DeleteClusterBenchmarkArtifacts: %v", err)
	}

	// Only the proxy for target 10 (cluster 7) deleted.
	if len(s.deletedProxyIDs) != 1 || s.deletedProxyIDs[0] != 20 {
		t.Errorf("deletedProxyIDs = %v, want [20]", s.deletedProxyIDs)
	}
	// Only target 10 (cluster 7) deleted; target 11 (cluster 99) preserved.
	if len(s.deletedTargetIDs) != 1 || s.deletedTargetIDs[0] != 10 {
		t.Errorf("deletedTargetIDs = %v, want [10]", s.deletedTargetIDs)
	}
	// Only exact-match agent (id=3) deleted; other-instance (id=4) and forge-local (id=2) preserved.
	if len(s.deletedAgentIDs) != 1 || s.deletedAgentIDs[0] != 3 {
		t.Errorf("deletedAgentIDs = %v, want [3] (exact match only)", s.deletedAgentIDs)
	}
	// Only exact-match ssh-cred (id=5) deleted; other-instance (id=6) preserved.
	if len(s.deletedSSHCredIDs) != 1 || s.deletedSSHCredIDs[0] != 5 {
		t.Errorf("deletedSSHCredIDs = %v, want [5] (exact match only)", s.deletedSSHCredIDs)
	}
	// Configs are deferred to the full purge — none deleted on down path.
	if len(s.deletedConfigIDs) != 0 {
		t.Errorf("down-path deleted configs %v — configs must not be deleted on down", s.deletedConfigIDs)
	}

	// Assert no runs or results were deleted.
	for _, path := range s.allDeletedPaths {
		if strings.Contains(path, "/runs") || strings.Contains(path, "/results") {
			t.Errorf("DELETE issued against runs/results path: %s", path)
		}
	}
}

// TestDeleteClusterBenchmarkArtifacts_EmptyInstanceID verifies that when
// jumphostInstanceID is empty, steps 3+4 (agent + ssh-credential) are skipped
// and only targets+proxies are deleted.
func TestDeleteClusterBenchmarkArtifacts_EmptyInstanceID(t *testing.T) {
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(10), "name": "awsbnkctl-target", "cluster_id": float64(7)},
		},
		proxies: map[int][]map[string]any{10: {{"id": float64(20)}}},
		agents: []map[string]any{
			{"id": float64(3), "name": "awsbnkctl-jumphost-i-abc"},
		},
		sshCreds: []map[string]any{
			{"id": float64(5), "name": "awsbnkctl-jumphost-i-abc"},
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	// Empty jumphostInstanceID → skip agent + ssh-credential deletion.
	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(s.deletedProxyIDs) != 1 || s.deletedProxyIDs[0] != 20 {
		t.Errorf("deletedProxyIDs = %v, want [20]", s.deletedProxyIDs)
	}
	if len(s.deletedTargetIDs) != 1 || s.deletedTargetIDs[0] != 10 {
		t.Errorf("deletedTargetIDs = %v, want [10]", s.deletedTargetIDs)
	}
	// Agent and ssh-cred must NOT be deleted when instance ID is empty.
	if len(s.deletedAgentIDs) != 0 {
		t.Errorf("agent deleted when instanceID is empty: %v", s.deletedAgentIDs)
	}
	if len(s.deletedSSHCredIDs) != 0 {
		t.Errorf("ssh-cred deleted when instanceID is empty: %v", s.deletedSSHCredIDs)
	}
}

// TestDeleteClusterBenchmarkArtifacts_404Idempotent verifies that 404 responses
// on all delete calls are treated as success (already gone).
func TestDeleteClusterBenchmarkArtifacts_404Idempotent(t *testing.T) {
	s := &cleanupServer{
		deleteTargetStatus:  http.StatusNotFound,
		deleteProxyStatus:   http.StatusNotFound,
		deleteAgentStatus:   http.StatusNotFound,
		deleteSSHCredStatus: http.StatusNotFound,
		targets: []map[string]any{
			{"id": float64(10), "name": "awsbnkctl-ai-rig-llama3", "cluster_id": float64(7)},
		},
		proxies: map[int][]map[string]any{
			10: {{"id": float64(20)}},
		},
		agents: []map[string]any{
			{"id": float64(3), "name": "awsbnkctl-jumphost-i-001"},
		},
		sshCreds: []map[string]any{
			{"id": float64(5), "name": "awsbnkctl-jumphost-i-001"},
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	// Must not return an error even though every DELETE returns 404.
	if err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "i-001"); err != nil {
		t.Errorf("expected nil on all-404, got: %v", err)
	}
}

// TestDeleteClusterBenchmarkArtifacts_NoArtifacts verifies that a cluster with
// no targets is a clean no-op (no agent or ssh-cred either since none in list).
func TestDeleteClusterBenchmarkArtifacts_NoArtifacts(t *testing.T) {
	s := &cleanupServer{
		targets: nil,
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 99, "i-000"); err != nil {
		t.Errorf("expected nil for empty cluster, got: %v", err)
	}
	if len(s.deletedTargetIDs)+len(s.deletedAgentIDs)+len(s.deletedConfigIDs)+len(s.deletedSSHCredIDs) != 0 {
		t.Error("expected no deletes for empty cluster with no matching records")
	}
}

// TestDeleteClusterBenchmarkArtifacts_401SoftFail verifies that a 401 from
// forge login causes an error return (callers treat this as soft-fail and log).
func TestDeleteClusterBenchmarkArtifacts_401SoftFail(t *testing.T) {
	s := &cleanupServer{loginStatus: http.StatusUnauthorized}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "i-001")
	if err == nil {
		t.Error("expected error on 401 login, got nil")
	}
}

// TestDeleteClusterBenchmarkArtifacts_NoRunsOrResultsDeleted asserts that in
// all cases no DELETE is issued to any runs or results path (down-path scope).
func TestDeleteClusterBenchmarkArtifacts_NoRunsOrResultsDeleted(t *testing.T) {
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(5), "name": "awsbnkctl-target", "cluster_id": float64(7)},
		},
		proxies:  map[int][]map[string]any{5: {{"id": float64(6)}}},
		agents:   []map[string]any{{"id": float64(7), "name": "awsbnkctl-jumphost-i-x"}},
		sshCreds: []map[string]any{{"id": float64(9), "name": "awsbnkctl-jumphost-i-x"}},
		configs:  []map[string]any{{"id": float64(8), "name": "awsbnkctl-latency"}},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	_ = forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "i-x")

	for _, path := range s.allDeletedPaths {
		if strings.Contains(path, "/runs") || strings.Contains(path, "/results") {
			t.Errorf("DELETE issued to runs/results path: %s", path)
		}
	}
}

// TestDeleteClusterBenchmarkArtifacts_PartialDeleteError verifies that when one
// proxy delete fails (500), the parent target is still deleted and the function
// returns an error mentioning the failed proxy.
func TestDeleteClusterBenchmarkArtifacts_PartialDeleteError(t *testing.T) {
	s := &cleanupServer{
		// Proxy 20 will return 500; proxy 21 and the target should still be deleted.
		proxyErrorID: 20,
		proxyErrCode: http.StatusInternalServerError,
		targets: []map[string]any{
			{"id": float64(10), "name": "awsbnkctl-ai-rig", "cluster_id": float64(7)},
		},
		proxies: map[int][]map[string]any{
			10: {{"id": float64(20)}, {"id": float64(21)}},
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "")
	if err == nil {
		t.Fatal("expected error when proxy delete fails, got nil")
	}
	if !strings.Contains(err.Error(), "20") {
		t.Errorf("error should mention failing proxy id 20, got: %v", err)
	}

	// Proxy 21 should still have been attempted despite proxy 20's failure.
	found21 := false
	for _, id := range s.deletedProxyIDs {
		if id == 21 {
			found21 = true
		}
	}
	if !found21 {
		t.Errorf("proxy 21 was not deleted after proxy 20 failed; deletedProxyIDs=%v", s.deletedProxyIDs)
	}

	// Target should still be deleted despite the proxy error.
	if len(s.deletedTargetIDs) != 1 || s.deletedTargetIDs[0] != 10 {
		t.Errorf("target 10 was not deleted after proxy error; deletedTargetIDs=%v", s.deletedTargetIDs)
	}
}

// TestDeleteClusterBenchmarkArtifacts_SSHCred409SoftWarning verifies that a 409
// on ssh-credential delete (FK conflict from a project) is treated as a soft
// warning — it is collected in the error but teardown continues.
func TestDeleteClusterBenchmarkArtifacts_SSHCred409SoftWarning(t *testing.T) {
	s := &cleanupServer{
		deleteSSHCredStatus: http.StatusConflict, // 409 = FK conflict
		targets: []map[string]any{
			{"id": float64(10), "name": "awsbnkctl-target", "cluster_id": float64(7)},
		},
		proxies:  map[int][]map[string]any{10: {{"id": float64(20)}}},
		agents:   []map[string]any{{"id": float64(3), "name": "awsbnkctl-jumphost-i-001"}},
		sshCreds: []map[string]any{{"id": float64(5), "name": "awsbnkctl-jumphost-i-001"}},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "i-001")
	// 409 is a soft warning — error is returned but target + proxy + agent still deleted.
	if err == nil {
		t.Error("expected soft-warning error on 409 ssh-cred delete, got nil")
	}
	if !strings.Contains(err.Error(), "ssh-credential") {
		t.Errorf("error should mention ssh-credential, got: %v", err)
	}

	// Target and proxy were still deleted.
	if len(s.deletedTargetIDs) != 1 {
		t.Errorf("target not deleted despite ssh-cred 409; deletedTargetIDs=%v", s.deletedTargetIDs)
	}
	if len(s.deletedProxyIDs) != 1 {
		t.Errorf("proxy not deleted despite ssh-cred 409; deletedProxyIDs=%v", s.deletedProxyIDs)
	}
	// Agent was still deleted.
	if len(s.deletedAgentIDs) != 1 || s.deletedAgentIDs[0] != 3 {
		t.Errorf("agent not deleted despite ssh-cred 409; deletedAgentIDs=%v", s.deletedAgentIDs)
	}
}

// ---------------------------------------------------------------------------
// Tests — full-path (DeleteAllClusterBenchmarkArtifacts)
// ---------------------------------------------------------------------------

// TestDeleteAllClusterBenchmarkArtifacts_HappyPath verifies the full delete
// sequence: proxy → target → awsbnkctl agent → awsbnkctl ssh-credential → config.
// Non-awsbnkctl agents, ssh-credentials, and configs must be preserved.
func TestDeleteAllClusterBenchmarkArtifacts_HappyPath(t *testing.T) {
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(10), "name": "awsbnkctl-ai-rig-llama3", "cluster_id": float64(7)},
		},
		proxies: map[int][]map[string]any{
			10: {{"id": float64(20)}},
		},
		agents: []map[string]any{
			{"id": float64(3), "name": "awsbnkctl-jumphost-i-001"},
			{"id": float64(2), "name": "forge-local"}, // builtin — must NOT be deleted
		},
		sshCreds: []map[string]any{
			{"id": float64(5), "name": "awsbnkctl-jumphost-i-001"},
			{"id": float64(6), "name": "some-external-host"}, // non-awsbnkctl — must NOT be deleted
		},
		configs: []map[string]any{
			{"id": float64(8), "name": "awsbnkctl-latency"},
			{"id": float64(9), "name": "awsbnkctl-throughput"},
			{"id": float64(1), "name": "some-other-config"}, // foreign — must NOT be deleted
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteAllClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7)
	if err != nil {
		t.Fatalf("DeleteAllClusterBenchmarkArtifacts: %v", err)
	}

	// Proxy and target deleted.
	if len(s.deletedProxyIDs) != 1 || s.deletedProxyIDs[0] != 20 {
		t.Errorf("deletedProxyIDs = %v, want [20]", s.deletedProxyIDs)
	}
	if len(s.deletedTargetIDs) != 1 || s.deletedTargetIDs[0] != 10 {
		t.Errorf("deletedTargetIDs = %v, want [10]", s.deletedTargetIDs)
	}
	// Only awsbnkctl agent deleted; forge-local (id=2) preserved.
	if len(s.deletedAgentIDs) != 1 || s.deletedAgentIDs[0] != 3 {
		t.Errorf("deletedAgentIDs = %v, want [3]", s.deletedAgentIDs)
	}
	// Only awsbnkctl ssh-cred deleted; some-external-host (id=6) preserved.
	if len(s.deletedSSHCredIDs) != 1 || s.deletedSSHCredIDs[0] != 5 {
		t.Errorf("deletedSSHCredIDs = %v, want [5]", s.deletedSSHCredIDs)
	}
	// Only awsbnkctl configs deleted; some-other-config (id=1) preserved.
	if len(s.deletedConfigIDs) != 2 {
		t.Errorf("deletedConfigIDs = %v, want [8 9]", s.deletedConfigIDs)
	}

	// Assert no runs or results were deleted.
	for _, path := range s.allDeletedPaths {
		if strings.Contains(path, "/runs") || strings.Contains(path, "/results") {
			t.Errorf("DELETE issued against runs/results path: %s", path)
		}
	}
}

// TestDeleteAllClusterBenchmarkArtifacts_BuiltinAgentPreserved verifies that
// non-awsbnkctl agents and ssh-credentials are never deleted even in the full
// cleanup path.
func TestDeleteAllClusterBenchmarkArtifacts_BuiltinAgentPreserved(t *testing.T) {
	s := &cleanupServer{
		targets: nil,
		agents: []map[string]any{
			{"id": float64(2), "name": "forge-local"},
			{"id": float64(99), "name": "some-other-agent"},
		},
		sshCreds: []map[string]any{
			{"id": float64(10), "name": "customer-provided-host"},
		},
		configs: []map[string]any{},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteAllClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(s.deletedAgentIDs) != 0 {
		t.Errorf("non-awsbnkctl agents were deleted: %v", s.deletedAgentIDs)
	}
	if len(s.deletedSSHCredIDs) != 0 {
		t.Errorf("non-awsbnkctl ssh-credentials were deleted: %v", s.deletedSSHCredIDs)
	}
}

// TestDeleteAllClusterBenchmarkArtifacts_NoRunsOrResultsDeleted asserts that the
// full cleanup path also never issues DELETE to runs or results.
func TestDeleteAllClusterBenchmarkArtifacts_NoRunsOrResultsDeleted(t *testing.T) {
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(5), "name": "awsbnkctl-target", "cluster_id": float64(7)},
		},
		proxies:  map[int][]map[string]any{5: {{"id": float64(6)}}},
		agents:   []map[string]any{{"id": float64(7), "name": "awsbnkctl-jumphost-x"}},
		sshCreds: []map[string]any{{"id": float64(9), "name": "awsbnkctl-jumphost-x"}},
		configs:  []map[string]any{{"id": float64(8), "name": "awsbnkctl-latency"}},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	_ = forge.DeleteAllClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7)

	for _, path := range s.allDeletedPaths {
		if strings.Contains(path, "/runs") || strings.Contains(path, "/results") {
			t.Errorf("DELETE issued to runs/results path: %s", path)
		}
	}
}

// ---------------------------------------------------------------------------
// Individual-helper tests
// ---------------------------------------------------------------------------

// TestDeleteBenchmarkTarget_404Idempotent verifies the individual helper.
func TestDeleteBenchmarkTarget_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteTargetStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteBenchmarkTarget(context.Background(), ts.URL, forge.RestCreds{}, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

// TestDeleteBenchmarkAgent_404Idempotent verifies the individual helper.
func TestDeleteBenchmarkAgent_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteAgentStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteBenchmarkAgent(context.Background(), ts.URL, forge.RestCreds{}, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

// TestDeleteBenchmarkConfig_404Idempotent verifies the individual helper.
func TestDeleteBenchmarkConfig_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteConfigStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteBenchmarkConfig(context.Background(), ts.URL, forge.RestCreds{}, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

// TestDeleteProxyDeployment_404Idempotent verifies the individual helper.
func TestDeleteProxyDeployment_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteProxyStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteProxyDeployment(context.Background(), ts.URL, forge.RestCreds{}, 10, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

// TestDeleteSSHCredential_404Idempotent verifies that 404 on ssh-credential
// delete is treated as success (already gone).
func TestDeleteSSHCredential_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteSSHCredStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteSSHCredential(context.Background(), ts.URL, forge.RestCreds{}, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

// TestDeleteSSHCredential_409SoftWarning verifies that 409 on ssh-credential
// delete is returned as an error (FK conflict — soft warning to caller).
func TestDeleteSSHCredential_409SoftWarning(t *testing.T) {
	s := &cleanupServer{deleteSSHCredStatus: http.StatusConflict}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteSSHCredential(context.Background(), ts.URL, forge.RestCreds{}, 5)
	if err == nil {
		t.Error("expected error on 409, got nil")
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("error should mention credential id 5, got: %v", err)
	}
}
