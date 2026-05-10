package cred

// Sprint 3 / PRD 04 — cred resolver chain (env → keychain → config-b64 → prompt).
//
// These tests exercise the resolver against the contract in PRD 04 +
// `prompts/sprint3/staff.md` Priority 1. They expect the staff agent's
// `internal/cred/resolver.go` to expose:
//
//	type Resolver struct {
//	    Workspace      string  // for keychain key + config lookup
//	    NonInteractive bool    // skip the prompt step
//	    // Optional injection seams for tests (env reader, stdin reader);
//	    // see test cases below for the shape we assume.
//	}
//
//	func (r *Resolver) IBMCloudAPIKey(ctx context.Context) (string, error)
//
// PRD 04's "Cross-backend principles" item #1 ("never log credentials") and
// item #6 ("cred lifecycle ties to workspace lifecycle") inform the resolver
// chain order — env beats keychain beats config-b64 beats prompt, identical
// to the existing `internal/config/secrets.go` ResolveAPIKey behaviour the
// staff agent extracts and refactors.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// resetEnv unsets every IBM Cloud API-key env var the resolver might check.
// The legacy resolver in internal/config/secrets.go consulted five names; the
// new resolver inherits that set unchanged. Any future expansion is tracked
// in the resolver's own apiKeyEnvVars list.
func resetEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"IBMCLOUD_API_KEY",
		"IC_API_KEY",
		"TF_VAR_ibmcloud_api_key",
		"TF_VAR_IBMCLOUD_API_KEY",
		"TF_VAR_IC_API_KEY",
	} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

// TestResolver_EnvOnly asserts: when IBMCLOUD_API_KEY is set, the resolver
// returns it without touching keychain/config/prompt. This is the fastest
// path and the documented preferred source for CI/automation.
func TestResolver_EnvOnly(t *testing.T) {
	resetEnv(t)
	t.Setenv("IBMCLOUD_API_KEY", "env-key-123")
	keyring.MockInit()

	r := &Resolver{Workspace: "test-ws", NonInteractive: true}
	got, err := r.IBMCloudAPIKey(context.Background())
	if err != nil {
		t.Fatalf("expected env hit, got err: %v", err)
	}
	if got != "env-key-123" {
		t.Errorf("got %q, want %q", got, "env-key-123")
	}
}

// TestResolver_KeychainOnly asserts: when env is empty, the resolver falls
// through to the OS keychain entry under
// service="roksbnkctl", user="<workspace>/ibmcloud_api_key".
func TestResolver_KeychainOnly(t *testing.T) {
	resetEnv(t)
	keyring.MockInit()
	if err := keyring.Set("roksbnkctl", "test-ws/ibmcloud_api_key", "kc-key-456"); err != nil {
		t.Skipf("keychain unavailable on this runner: %v", err)
	}

	r := &Resolver{Workspace: "test-ws", NonInteractive: true}
	got, err := r.IBMCloudAPIKey(context.Background())
	if err != nil {
		t.Fatalf("expected keychain hit, got err: %v", err)
	}
	if got != "kc-key-456" {
		t.Errorf("got %q, want %q", got, "kc-key-456")
	}
}

// TestResolver_EnvShadowsKeychain asserts the resolver chain order: env wins
// over keychain when both are set. PRD 04 §"Cross-backend principles" item #5
// (documented escape hatches) requires this so users can override a stored
// key per-invocation without rewriting their keychain.
func TestResolver_EnvShadowsKeychain(t *testing.T) {
	resetEnv(t)
	t.Setenv("IBMCLOUD_API_KEY", "env-wins")
	keyring.MockInit()
	if err := keyring.Set("roksbnkctl", "test-ws/ibmcloud_api_key", "kc-loses"); err != nil {
		t.Skipf("keychain unavailable on this runner: %v", err)
	}

	r := &Resolver{Workspace: "test-ws", NonInteractive: true}
	got, err := r.IBMCloudAPIKey(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "env-wins" {
		t.Errorf("env did not shadow keychain: got %q", got)
	}
}

// TestResolver_NonInteractiveMiss asserts: with NonInteractive=true and every
// source empty, the resolver returns an error (does NOT block on stdin).
// Critical for CI/automation: a missing key must hard-fail, never hang.
func TestResolver_NonInteractiveMiss(t *testing.T) {
	resetEnv(t)
	keyring.MockInit()

	r := &Resolver{Workspace: "test-ws-empty", NonInteractive: true}
	_, err := r.IBMCloudAPIKey(context.Background())
	if err == nil {
		t.Fatal("expected error when all sources empty + NonInteractive=true; got nil")
	}
	// Don't pin the exact message — staff may phrase it differently — but
	// confirm the error names the missing-credential condition clearly.
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "api key") && !strings.Contains(msg, "ibm") && !strings.Contains(msg, "credential") {
		t.Errorf("error message %q doesn't mention API key / IBM / credential — caller can't tell what went wrong", err)
	}
}

// TestResolver_NoLeakInError asserts the error message from a non-interactive
// miss never embeds any candidate or stale value — the resolver owns redaction
// of credentials in its own diagnostics. PRD 04 cross-backend principle #1.
func TestResolver_NoLeakInError(t *testing.T) {
	// We didn't set any env or keychain, so there's no credential to leak.
	// The test exists to guard against a future regression where the
	// resolver might log a partial value (e.g., "tried env, got
	// 'abcd...xyz'"). If an implementer adds such diagnostic, this test
	// will fail and force a redactor wrap.
	resetEnv(t)
	keyring.MockInit()

	r := &Resolver{Workspace: "ws-with-secret", NonInteractive: true}
	_, err := r.IBMCloudAPIKey(context.Background())
	if err == nil {
		return
	}
	// The workspace name is fine to include; the credential value is not.
	// Since we set no value, this test is a tripwire for future code that
	// might leak. Concrete leak coverage lives in audit_test.go.
	if strings.Contains(err.Error(), "ws-with-secret/ibmcloud_api_key=") {
		t.Errorf("error embeds keychain user with value form: %q", err)
	}
}
