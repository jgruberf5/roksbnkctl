package forge

// target.go — register a benchmark target (the LLM endpoint behind BNK) with
// forge via POST /api/benchmarks/targets.
//
// Forge's target endpoint does NOT support upsert.  On conflict, fall back to
// GET /api/benchmarks/targets and match by name.  Mirrors the accessmethod.go
// and agent.go patterns.
//
// cluster_id is required by the forge schema.  When unavailable (e.g. the
// workspace has no forge_link.json), the caller should skip target registration
// gracefully (best-effort, non-fatal).
//
// Uses the same benchmarkHTTPDoFn transport seam as PushBenchmarkResult.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// BenchmarkTargetEndpoint is the forge REST path for benchmark targets.
const BenchmarkTargetEndpoint = "/api/benchmarks/targets"

// BenchmarkTargetOptions carries all data for registering a benchmark target.
type BenchmarkTargetOptions struct {
	// RestURL is the forge REST base URL (e.g. "http://localhost:8000").
	RestURL string
	// Creds are the forge REST login credentials.
	Creds RestCreds
	// Name is the target name as stored in forge (e.g. "ai-rig-llama3").
	Name string
	// ClusterID is the forge cluster FK (required by schema).
	// When zero, registration is skipped — callers receive ErrTargetNoClusterID.
	ClusterID int
	// LLMBaseURL is the HTTP base URL of the LLM endpoint (e.g. "http://10.0.10.100").
	LLMBaseURL string
	// LLMModel is the model name served by the endpoint (e.g. "meta-llama/Llama-3.1-8B-Instruct").
	LLMModel string
	// LLMNamespace is the k8s namespace where the inference pod runs.
	LLMNamespace string
	// LLMEndpoint is the service/endpoint name in that namespace.
	LLMEndpoint string
	// ProxyNamespace is the k8s namespace where the BNK proxy runs.
	ProxyNamespace string
	// Tags is an optional free-form tag map.
	Tags map[string]string
}

// BenchmarkTargetResponse is the subset of forge's BenchmarkTarget fields
// that callers need.
type BenchmarkTargetResponse struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ClusterID int    `json:"cluster_id"`
}

// ErrTargetNoClusterID is returned by RegisterBenchmarkTarget when ClusterID
// is zero.  Callers should treat this as a soft skip, not a hard failure.
var ErrTargetNoClusterID = errors.New("forge.RegisterBenchmarkTarget: ClusterID is required (workspace may not have a forge link)")

// RegisterBenchmarkTarget creates or reuses a forge BenchmarkTarget record.
//
// Idempotent: on name conflict, falls back to GET list + match by name.
// Returns ErrTargetNoClusterID when opts.ClusterID is zero (caller should skip).
// Best-effort: callers should log on error rather than aborting the run.
func RegisterBenchmarkTarget(ctx context.Context, opts BenchmarkTargetOptions) (BenchmarkTargetResponse, error) {
	if opts.RestURL == "" {
		return BenchmarkTargetResponse{}, fmt.Errorf("forge.RegisterBenchmarkTarget: RestURL is required")
	}
	if opts.Name == "" {
		return BenchmarkTargetResponse{}, fmt.Errorf("forge.RegisterBenchmarkTarget: Name is required")
	}
	if opts.ClusterID == 0 {
		return BenchmarkTargetResponse{}, ErrTargetNoClusterID
	}

	base := strings.TrimRight(opts.RestURL, "/")

	token, err := bmkRestLogin(ctx, base, opts.Creds.restUsername(), opts.Creds.restPassword())
	if err != nil {
		return BenchmarkTargetResponse{}, fmt.Errorf("forge benchmark target: login: %w", err)
	}

	body := map[string]any{
		"name":       opts.Name,
		"cluster_id": opts.ClusterID,
	}
	if opts.LLMBaseURL != "" {
		body["llm_base_url"] = opts.LLMBaseURL
	}
	if opts.LLMModel != "" {
		body["llm_model"] = opts.LLMModel
	}
	if opts.LLMNamespace != "" {
		body["llm_namespace"] = opts.LLMNamespace
	}
	if opts.LLMEndpoint != "" {
		body["llm_endpoint"] = opts.LLMEndpoint
	}
	if opts.ProxyNamespace != "" {
		body["proxy_namespace"] = opts.ProxyNamespace
	}
	if len(opts.Tags) > 0 {
		body["tags"] = opts.Tags
	}

	var created BenchmarkTargetResponse
	postErr := bmkRestPost(ctx, base+BenchmarkTargetEndpoint, token, body, &created)
	if postErr == nil {
		return created, nil
	}

	// 409 or 400-with-"already exists": fall back to list-and-match.
	var herr *restHTTPErr
	if !errors.As(postErr, &herr) {
		return BenchmarkTargetResponse{}, fmt.Errorf("forge benchmark target: create: %w", postErr)
	}
	isConflict := herr.StatusCode == http.StatusConflict ||
		(herr.StatusCode == http.StatusBadRequest && strings.Contains(herr.Body, "already exists"))
	if !isConflict {
		return BenchmarkTargetResponse{}, fmt.Errorf("forge benchmark target: create: %w", postErr)
	}

	existing, lookupErr := benchmarkTargetFindByName(ctx, base, token, opts.Name)
	if lookupErr != nil {
		return BenchmarkTargetResponse{}, fmt.Errorf("forge benchmark target: conflict + list failed: %w (original: %v)", lookupErr, postErr)
	}
	return existing, nil
}

// benchmarkTargetFindByName GETs /api/benchmarks/targets and returns the record
// whose name matches exactly.
func benchmarkTargetFindByName(ctx context.Context, base, token, name string) (BenchmarkTargetResponse, error) {
	var list []BenchmarkTargetResponse
	if err := bmkRestGet(ctx, base+BenchmarkTargetEndpoint, token, &list); err != nil {
		return BenchmarkTargetResponse{}, fmt.Errorf("list benchmark targets: %w", err)
	}
	for _, r := range list {
		if r.Name == name {
			return r, nil
		}
	}
	return BenchmarkTargetResponse{}, fmt.Errorf("benchmark target %q not found in forge", name)
}
