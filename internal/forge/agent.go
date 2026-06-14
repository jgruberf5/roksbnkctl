package forge

// agent.go — register a benchmark agent (the jumphost that runs aiperf) with
// forge via POST /api/benchmarks/agents.
//
// Upsert semantics: if the name already exists (409 / 400-with-"already exists"),
// fall back to GET /api/benchmarks/agents and return the existing record by
// exact name match.  Mirrors the accessmethod.go upsert pattern.
//
// Uses the same benchmarkHTTPDoFn transport seam as PushBenchmarkResult so
// tests can inject a mock without touching http.DefaultClient.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// BenchmarkAgentEndpoint is the forge REST path for benchmark agents.
const BenchmarkAgentEndpoint = "/api/benchmarks/agents"

// BenchmarkAgentOptions carries all data for registering a benchmark agent.
type BenchmarkAgentOptions struct {
	// RestURL is the forge REST base URL (e.g. "http://localhost:8000").
	RestURL string
	// Creds are the forge REST login credentials.
	Creds RestCreds
	// Name is the agent name as stored in forge.  MUST match the agent_name
	// sent on run pushes — forge resolves runs to agents by name.
	Name string
	// Hostname is the EC2 instance-id or DNS name of the jumphost.
	Hostname string
	// IPAddress is the jumphost's private IP (optional).
	IPAddress string
	// Tags is an optional free-form tag map attached to the agent record.
	Tags map[string]string
	// Capabilities is an optional list of capability strings (e.g. ["aiperf"]).
	Capabilities []string
}

// BenchmarkAgentResponse is the subset of forge's BenchmarkAgent fields
// that callers need.
type BenchmarkAgentResponse struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
}

// RegisterBenchmarkAgent creates or reuses a forge BenchmarkAgent record.
//
// Idempotent: on name conflict, falls back to GET list + match by name.
// Best-effort: callers should log on error rather than aborting the run.
func RegisterBenchmarkAgent(ctx context.Context, opts BenchmarkAgentOptions) (BenchmarkAgentResponse, error) {
	if opts.RestURL == "" {
		return BenchmarkAgentResponse{}, fmt.Errorf("forge.RegisterBenchmarkAgent: RestURL is required")
	}
	if opts.Name == "" {
		return BenchmarkAgentResponse{}, fmt.Errorf("forge.RegisterBenchmarkAgent: Name is required")
	}

	base := strings.TrimRight(opts.RestURL, "/")

	token, err := bmkRestLogin(ctx, base, opts.Creds.restUsername(), opts.Creds.restPassword())
	if err != nil {
		return BenchmarkAgentResponse{}, fmt.Errorf("forge benchmark agent: login: %w", err)
	}

	body := map[string]any{
		"name":     opts.Name,
		"hostname": opts.Hostname,
	}
	if opts.IPAddress != "" {
		body["ip_address"] = opts.IPAddress
	}
	if len(opts.Tags) > 0 {
		body["tags"] = opts.Tags
	}
	if len(opts.Capabilities) > 0 {
		body["capabilities"] = opts.Capabilities
	}

	var created BenchmarkAgentResponse
	postErr := bmkRestPost(ctx, base+BenchmarkAgentEndpoint, token, body, &created)
	if postErr == nil {
		return created, nil
	}

	// 409 or 400-with-"already exists": fall back to list-and-match.
	var herr *restHTTPErr
	if !errors.As(postErr, &herr) {
		return BenchmarkAgentResponse{}, fmt.Errorf("forge benchmark agent: create: %w", postErr)
	}
	isConflict := herr.StatusCode == http.StatusConflict ||
		(herr.StatusCode == http.StatusBadRequest && strings.Contains(herr.Body, "already exists"))
	if !isConflict {
		return BenchmarkAgentResponse{}, fmt.Errorf("forge benchmark agent: create: %w", postErr)
	}

	existing, lookupErr := benchmarkAgentFindByName(ctx, base, token, opts.Name)
	if lookupErr != nil {
		return BenchmarkAgentResponse{}, fmt.Errorf("forge benchmark agent: conflict + list failed: %w (original: %v)", lookupErr, postErr)
	}
	return existing, nil
}

// benchmarkAgentFindByName GETs /api/benchmarks/agents and returns the record
// whose name matches exactly.
func benchmarkAgentFindByName(ctx context.Context, base, token, name string) (BenchmarkAgentResponse, error) {
	var list []BenchmarkAgentResponse
	if err := bmkRestGet(ctx, base+BenchmarkAgentEndpoint, token, &list); err != nil {
		return BenchmarkAgentResponse{}, fmt.Errorf("list benchmark agents: %w", err)
	}
	for _, r := range list {
		if r.Name == name {
			return r, nil
		}
	}
	return BenchmarkAgentResponse{}, fmt.Errorf("benchmark agent %q not found in forge", name)
}
