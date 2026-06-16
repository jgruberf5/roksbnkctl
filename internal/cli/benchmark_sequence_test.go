package cli

// benchmark_sequence_test.go — unit tests for the scenario-sequence harness
// (Task A, PRD-11 adaptive 4-test harness).
//
// Coverage:
//   - parseScenarioKeys: exhaustive parsing and error cases
//   - resetCacheHookFn seam: invocation count, order, and args (single/two/three keys)
//   - runScenarioSequence: single path fail-closed (reset error aborts remaining)
//   - runShootoutFrontEnd: shootout reset error → RESET_FAILED, other fronts unaffected
//   - registerBenchmarkConfigFn called once per key with per-key name (not raw string)

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

// ---------------------------------------------------------------------------
// parseScenarioKeys
// ---------------------------------------------------------------------------

func TestParseScenarioKeys_SingleKey(t *testing.T) {
	keys, err := parseScenarioKeys("baseline")
	if err != nil {
		t.Fatalf("parseScenarioKeys(baseline): %v", err)
	}
	if len(keys) != 1 || keys[0] != "baseline" {
		t.Errorf("got %v, want [baseline]", keys)
	}
}

func TestParseScenarioKeys_TwoKeys(t *testing.T) {
	keys, err := parseScenarioKeys("baseline,mooncake")
	if err != nil {
		t.Fatalf("parseScenarioKeys(baseline,mooncake): %v", err)
	}
	if len(keys) != 2 || keys[0] != "baseline" || keys[1] != "mooncake" {
		t.Errorf("got %v, want [baseline mooncake]", keys)
	}
}

func TestParseScenarioKeys_WhitespaceAround(t *testing.T) {
	keys, err := parseScenarioKeys("baseline, mooncake")
	if err != nil {
		t.Fatalf("parseScenarioKeys(baseline, mooncake): %v", err)
	}
	if len(keys) != 2 || keys[0] != "baseline" || keys[1] != "mooncake" {
		t.Errorf("got %v, want [baseline mooncake]", keys)
	}
}

func TestParseScenarioKeys_WhitespacePadded(t *testing.T) {
	// Leading/trailing spaces on the whole string and per-key.
	keys, err := parseScenarioKeys("  baseline ,  mooncake  ")
	if err != nil {
		t.Fatalf("parseScenarioKeys: %v", err)
	}
	if len(keys) != 2 || keys[0] != "baseline" || keys[1] != "mooncake" {
		t.Errorf("got %v, want [baseline mooncake]", keys)
	}
}

func TestParseScenarioKeys_ThreeKeys(t *testing.T) {
	keys, err := parseScenarioKeys("baseline,mooncake,high-concurrency")
	if err != nil {
		t.Fatalf("parseScenarioKeys: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("got %d keys, want 3: %v", len(keys), keys)
	}
	if keys[2] != "high-concurrency" {
		t.Errorf("keys[2] = %q, want high-concurrency", keys[2])
	}
}

func TestParseScenarioKeys_EmptyString(t *testing.T) {
	_, err := parseScenarioKeys("")
	if err == nil {
		t.Error("expected error for empty string, got nil")
	}
}

func TestParseScenarioKeys_WhitespaceOnly(t *testing.T) {
	_, err := parseScenarioKeys("   ")
	if err == nil {
		t.Error("expected error for whitespace-only string, got nil")
	}
}

func TestParseScenarioKeys_TrailingComma(t *testing.T) {
	// "baseline," → empty second entry → error.
	_, err := parseScenarioKeys("baseline,")
	if err == nil {
		t.Error("expected error for trailing comma (baseline,), got nil")
	}
}

func TestParseScenarioKeys_LeadingComma(t *testing.T) {
	// ",baseline" → empty first entry → error.
	_, err := parseScenarioKeys(",baseline")
	if err == nil {
		t.Error("expected error for leading comma (,baseline), got nil")
	}
}

func TestParseScenarioKeys_DoubleComma(t *testing.T) {
	// "baseline,,mooncake" → empty middle entry → error.
	_, err := parseScenarioKeys("baseline,,mooncake")
	if err == nil {
		t.Error("expected error for double comma (baseline,,mooncake), got nil")
	}
}

func TestParseScenarioKeys_CommaOnly(t *testing.T) {
	_, err := parseScenarioKeys(",")
	if err == nil {
		t.Error("expected error for comma-only string, got nil")
	}
}

// ---------------------------------------------------------------------------
// resetCacheHookFn seam — invocation count, order, and args
// ---------------------------------------------------------------------------

// sequenceTestSetup configures the global seams for a runScenarioSequence test
// and returns a cleanup function.
func sequenceTestSetup(t *testing.T) func() {
	t.Helper()

	origRunAiperf := runAiperfFn
	origPushRaw := pushRawAiperfResultFn
	origRegisterConfig := registerBenchmarkConfigFn
	origResetHook := resetCacheHookFn
	origVIP := flagBenchVIP
	origModel := flagBenchModel
	origForgeURL := flagBenchForgeURL
	origEndpoint := flagBenchEndpoint

	flagBenchVIP = "10.0.10.100"
	flagBenchModel = "test-model"
	flagBenchForgeURL = "http://localhost:9999"
	flagBenchEndpoint = "/v1/chat/completions"

	runAiperfFn = func(_ context.Context, _ jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
		return stubAiperfResult(), nil
	}
	pushRawAiperfResultFn = func(_ context.Context, opts forge.RawAiperfPushOptions) (forge.RawAiperfPushResponse, error) {
		return forge.RawAiperfPushResponse{RunID: 1, Status: "ok"}, nil
	}
	registerBenchmarkConfigFn = func(_ context.Context, opts forge.BenchmarkConfigOptions) (forge.BenchmarkConfigResponse, error) {
		return forge.BenchmarkConfigResponse{ID: 1, Name: opts.Name}, nil
	}

	return func() {
		runAiperfFn = origRunAiperf
		pushRawAiperfResultFn = origPushRaw
		registerBenchmarkConfigFn = origRegisterConfig
		resetCacheHookFn = origResetHook
		flagBenchVIP = origVIP
		flagBenchModel = origModel
		flagBenchForgeURL = origForgeURL
		flagBenchEndpoint = origEndpoint
	}
}

// recordingResetHook returns a hook that records calls, and a pointer to a
// slice of (PrevKey, NextKey) pairs that accumulates on each call.
type resetCall struct {
	PrevKey string
	NextKey string
	Proxy   string
}

func recordingResetHook() (func(context.Context, resetCacheArgs) error, *[]resetCall) {
	var calls []resetCall
	hook := func(_ context.Context, args resetCacheArgs) error {
		calls = append(calls, resetCall{PrevKey: args.PrevKey, NextKey: args.NextKey, Proxy: args.Proxy})
		return nil
	}
	return hook, &calls
}

func TestResetSeam_SingleKey_ZeroCalls(t *testing.T) {
	// AC: single-key path — reset seam never called (0 invocations),
	// and registerBenchmarkConfigFn is called exactly once with
	// Name="awsbnkctl-scenario-baseline" (proves byte-identity of forge records).
	cleanup := sequenceTestSetup(t)
	defer cleanup()

	hook, calls := recordingResetHook()
	resetCacheHookFn = hook

	// Recording fake for registerBenchmarkConfigFn.
	var configNames []string
	registerBenchmarkConfigFn = func(_ context.Context, opts forge.BenchmarkConfigOptions) (forge.BenchmarkConfigResponse, error) {
		configNames = append(configNames, opts.Name)
		return forge.BenchmarkConfigResponse{ID: 1, Name: opts.Name}, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{}
	// "baseline" expands to 4 children; use it as a known single key.
	_, err := runScenarioSequence(cmd, jumphost.ProbeOptions{VIP: flagBenchVIP}, forge.RestCreds{}, "agent", graph,
		[]string{"baseline"}, "", "")
	if err != nil {
		t.Fatalf("runScenarioSequence: %v", err)
	}

	// Reset seam: must not fire for N=1.
	if len(*calls) != 0 {
		t.Errorf("reset seam called %d times for single key, want 0", len(*calls))
	}

	// registerBenchmarkConfigFn: exactly 1 call, correct per-key name.
	if len(configNames) != 1 {
		t.Fatalf("registerBenchmarkConfigFn called %d times for single key, want 1", len(configNames))
	}
	if configNames[0] != "awsbnkctl-scenario-baseline" {
		t.Errorf("configNames[0] = %q, want awsbnkctl-scenario-baseline", configNames[0])
	}
}

func TestResetSeam_TwoKeys_OneCall(t *testing.T) {
	cleanup := sequenceTestSetup(t)
	defer cleanup()

	hook, calls := recordingResetHook()
	resetCacheHookFn = hook

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{}
	_, err := runScenarioSequence(cmd, jumphost.ProbeOptions{VIP: flagBenchVIP}, forge.RestCreds{}, "agent", graph,
		[]string{"baseline", "mooncake"}, "", "")
	if err != nil {
		t.Fatalf("runScenarioSequence: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("reset seam called %d times for two keys, want 1", len(*calls))
	}
	if (*calls)[0].PrevKey != "baseline" || (*calls)[0].NextKey != "mooncake" {
		t.Errorf("reset call args = {%q, %q}, want {baseline, mooncake}", (*calls)[0].PrevKey, (*calls)[0].NextKey)
	}
}

func TestResetSeam_ThreeKeys_TwoCalls(t *testing.T) {
	cleanup := sequenceTestSetup(t)
	defer cleanup()

	hook, calls := recordingResetHook()
	resetCacheHookFn = hook

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{}
	_, err := runScenarioSequence(cmd, jumphost.ProbeOptions{VIP: flagBenchVIP}, forge.RestCreds{}, "agent", graph,
		[]string{"baseline", "mooncake", "high-concurrency"}, "", "")
	if err != nil {
		t.Fatalf("runScenarioSequence: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("reset seam called %d times for three keys, want 2", len(*calls))
	}
	// First call: baseline → mooncake
	if (*calls)[0].PrevKey != "baseline" || (*calls)[0].NextKey != "mooncake" {
		t.Errorf("calls[0] = {%q, %q}, want {baseline, mooncake}", (*calls)[0].PrevKey, (*calls)[0].NextKey)
	}
	// Second call: mooncake → high-concurrency
	if (*calls)[1].PrevKey != "mooncake" || (*calls)[1].NextKey != "high-concurrency" {
		t.Errorf("calls[1] = {%q, %q}, want {mooncake, high-concurrency}", (*calls)[1].PrevKey, (*calls)[1].NextKey)
	}
}

func TestResetSeam_ProxyPassedThrough(t *testing.T) {
	// In shootout mode, the proxy label must be populated in resetCacheArgs.
	cleanup := sequenceTestSetup(t)
	defer cleanup()

	hook, calls := recordingResetHook()
	resetCacheHookFn = hook

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{}
	_, err := runScenarioSequence(cmd, jumphost.ProbeOptions{VIP: flagBenchVIP}, forge.RestCreds{}, "agent", graph,
		[]string{"baseline", "mooncake"}, "f5-bnk", "")
	if err != nil {
		t.Fatalf("runScenarioSequence: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 reset call, got %d", len(*calls))
	}
	if (*calls)[0].Proxy != "f5-bnk" {
		t.Errorf("resetCacheArgs.Proxy = %q, want f5-bnk", (*calls)[0].Proxy)
	}
}

// ---------------------------------------------------------------------------
// registerBenchmarkConfigFn called once per key with per-key name
// ---------------------------------------------------------------------------

func TestRegisterConfigFn_PerKeyName(t *testing.T) {
	// Asserts that registerBenchmarkConfigFn is called with
	// "awsbnkctl-scenario-<key>" for each key, not the raw comma-joined string.
	cleanup := sequenceTestSetup(t)
	defer cleanup()

	// Default reset hook (no-op).
	var configNames []string
	registerBenchmarkConfigFn = func(_ context.Context, opts forge.BenchmarkConfigOptions) (forge.BenchmarkConfigResponse, error) {
		configNames = append(configNames, opts.Name)
		return forge.BenchmarkConfigResponse{ID: 1, Name: opts.Name}, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{}
	_, err := runScenarioSequence(cmd, jumphost.ProbeOptions{VIP: flagBenchVIP}, forge.RestCreds{}, "agent", graph,
		[]string{"baseline", "mooncake"}, "", "")
	if err != nil {
		t.Fatalf("runScenarioSequence: %v", err)
	}

	if len(configNames) != 2 {
		t.Fatalf("registerBenchmarkConfigFn called %d times, want 2", len(configNames))
	}
	if configNames[0] != "awsbnkctl-scenario-baseline" {
		t.Errorf("configNames[0] = %q, want awsbnkctl-scenario-baseline", configNames[0])
	}
	if configNames[1] != "awsbnkctl-scenario-mooncake" {
		t.Errorf("configNames[1] = %q, want awsbnkctl-scenario-mooncake", configNames[1])
	}
}

// ---------------------------------------------------------------------------
// Single-path fail-closed: reset error aborts remaining scenarios
// ---------------------------------------------------------------------------

func TestResetSeam_SinglePath_FailClosed(t *testing.T) {
	// When resetCacheHookFn returns an error, runScenarioSequence must:
	//   - return the wrapped "cache reset between..." error
	//   - not run the next scenario key
	cleanup := sequenceTestSetup(t)
	defer cleanup()

	resetErr := errors.New("sm redeploy failed")
	resetCalled := 0
	resetCacheHookFn = func(_ context.Context, args resetCacheArgs) error {
		resetCalled++
		return resetErr
	}

	// Count how many times runAiperfFn is called to verify mooncake was skipped.
	aiperfCalls := 0
	runAiperfFn = func(_ context.Context, _ jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
		aiperfCalls++
		return stubAiperfResult(), nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{}
	_, err := runScenarioSequence(cmd, jumphost.ProbeOptions{VIP: flagBenchVIP}, forge.RestCreds{}, "agent", graph,
		[]string{"baseline", "mooncake"}, "", "")

	if err == nil {
		t.Fatal("expected error from reset failure, got nil")
	}
	if !strings.Contains(err.Error(), "cache reset between") {
		t.Errorf("error message should contain 'cache reset between'; got: %v", err)
	}
	if !strings.Contains(err.Error(), "baseline") || !strings.Contains(err.Error(), "mooncake") {
		t.Errorf("error should name both keys; got: %v", err)
	}
	if resetCalled != 1 {
		t.Errorf("reset called %d times, want 1", resetCalled)
	}
	// baseline has 4 children; mooncake must NOT have been run.
	// aiperfCalls should be exactly 4 (baseline only).
	if aiperfCalls != 4 {
		t.Errorf("aiperf called %d times, want 4 (baseline only; mooncake skipped by fail-closed reset)", aiperfCalls)
	}
}

// TestResetSeam_ErrorType_ErrorsAs asserts that a reset failure from
// runScenarioSequence is detectable via errors.As(&resetCacheError{}), not
// string-matching. This locks the sentinel-type detection contract so that
// changes to the wrapped cause message do not silently break RESET_FAILED
// classification in runShootoutFrontEnd.
func TestResetSeam_ErrorType_ErrorsAs(t *testing.T) {
	cleanup := sequenceTestSetup(t)
	defer cleanup()

	resetCacheHookFn = func(_ context.Context, _ resetCacheArgs) error {
		// cause message does NOT contain "cache reset between" — string-match would fail.
		return errors.New("underlying sm failure")
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{}
	_, err := runScenarioSequence(cmd, jumphost.ProbeOptions{VIP: flagBenchVIP}, forge.RestCreds{}, "agent", graph,
		[]string{"baseline", "mooncake"}, "", "")
	if err == nil {
		t.Fatal("expected error from reset failure, got nil")
	}

	var rce *resetCacheError
	if !errors.As(err, &rce) {
		t.Fatalf("errors.As(*resetCacheError) = false; err = %v", err)
	}
	if rce.PrevKey != "baseline" || rce.NextKey != "mooncake" {
		t.Errorf("resetCacheError{PrevKey=%q, NextKey=%q}, want {baseline, mooncake}", rce.PrevKey, rce.NextKey)
	}
}

// ---------------------------------------------------------------------------
// Shootout path: reset error → RESET_FAILED on affected leg, others continue
// ---------------------------------------------------------------------------

func TestShootoutResetFailed_OneLegDoesNotAbortOthers(t *testing.T) {
	// When the reset seam fails for one front-end (e.g. f5-bnk), the shootout
	// must mark that leg RESET_FAILED and still run the other front-end.
	cleanup := sequenceTestSetup(t)
	defer cleanup()

	origScenario := flagBenchScenario
	origCheckServedModel := checkServedModelFn
	origProxies := flagBenchProxies
	origDirectPodIP := flagBenchDirectPodIP
	origDiscover := discoverProxiesFn
	origList := listProxyDeploymentsFn
	defer func() {
		flagBenchScenario = origScenario
		checkServedModelFn = origCheckServedModel
		flagBenchProxies = origProxies
		flagBenchDirectPodIP = origDirectPodIP
		discoverProxiesFn = origDiscover
		listProxyDeploymentsFn = origList
	}()

	flagBenchScenario = "baseline,mooncake"
	flagBenchProxies = "f5-bnk,envoy"
	flagBenchDirectPodIP = ""

	checkServedModelFn = func(_ context.Context, _ jumphost.ProbeOptions, _ string) error {
		return nil
	}

	// Stub forge discovery: envoy has an external_url.
	knownProxies := []forge.ProxyDeployment{
		{ID: 30, ProxyType: "f5-bnk"},
		{ID: 31, ProxyType: "envoy", ExternalURL: "10.0.1.55:31234"},
	}
	discoverProxiesFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) (forge.ProxyDiscoveryResult, error) {
		return forge.ProxyDiscoveryResult{DiscoveredCount: 2}, nil
	}
	listProxyDeploymentsFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) ([]forge.ProxyDeployment, error) {
		return knownProxies, nil
	}

	// The reset hook fails for f5-bnk, succeeds for envoy.
	resetCacheHookFn = func(_ context.Context, args resetCacheArgs) error {
		if args.Proxy == "f5-bnk" {
			return fmt.Errorf("f5-bnk reset failed")
		}
		return nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{targetID: 5}

	// Resolve front-ends and run each.
	frontEnds, resolveErr := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, graph.targetID)
	if resolveErr != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", resolveErr)
	}

	outcomes := make([]shootoutOutcome, 0, len(frontEnds))
	for _, fe := range frontEnds {
		if fe.ResolveErr != nil {
			outcomes = append(outcomes, shootoutOutcome{
				ProxyType: fe.ProxyType,
				Status:    "RESOLVE_FAILED",
				Err:       fe.ResolveErr,
			})
			continue
		}
		g := graph
		g.proxyDeploymentID = fe.ProxyDeploymentID
		feProbeOpts := jumphost.ProbeOptions{VIP: flagBenchVIP}
		if fe.VIP != "" {
			feProbeOpts.VIP = fe.VIP
		}
		oc := runShootoutFrontEnd(cmd, feProbeOpts, forge.RestCreds{}, "agent", g, fe.ProxyType, fe.VIP, nil)
		outcomes = append(outcomes, oc)
	}

	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(outcomes))
	}

	// f5-bnk: should be RESET_FAILED (reset seam errored).
	bnkOc := outcomes[0]
	if bnkOc.ProxyType != "f5-bnk" {
		t.Errorf("outcomes[0].ProxyType = %q, want f5-bnk", bnkOc.ProxyType)
	}
	if bnkOc.Status != "RESET_FAILED" {
		t.Errorf("f5-bnk Status = %q, want RESET_FAILED", bnkOc.Status)
	}
	if bnkOc.Err == nil {
		t.Error("f5-bnk Err should be set for RESET_FAILED")
	}

	// envoy: should have run successfully (reset hook returned nil for envoy).
	envoyOc := outcomes[1]
	if envoyOc.ProxyType != "envoy" {
		t.Errorf("outcomes[1].ProxyType = %q, want envoy", envoyOc.ProxyType)
	}
	if envoyOc.Status != "OK" {
		t.Errorf("envoy Status = %q, want OK (err=%v)", envoyOc.Status, envoyOc.Err)
	}
}
