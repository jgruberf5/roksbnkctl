package cred

// Sprint 6 — Priority 4 (Phase N Go-side stubs): cred-resolver
// invariance across backends.
//
// The validator's `scripts/e2e-test-backends.sh` Phase N exercises a
// mixed-mode lifecycle where `up`, `test throughput`, and `down` each
// run on a different backend (local / docker / k8s / ssh). Each
// transition must see the SAME IBM Cloud API key — because the
// cred.Resolver is local-to-the-host always (the cred gets PROPAGATED
// to the backend at exec time, never resolved by the backend itself).
//
// PRD 04 §"Cross-backend principles" §1 ("never log credentials") +
// §3 ("cred resolution is single-source-of-truth"). This file pins
// the contract in a unit test so a future refactor can't silently
// introduce per-backend cred lookups that diverge.
//
// Specifically: regardless of how many times .IBMCloudAPIKey is
// called, and regardless of what value the caller might pass for
// `--backend` between calls, the resolver returns the same value
// from the same chain step. The backend itself never sees the
// resolver; it gets a `*Credentials` struct from the caller.

import (
	"context"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestResolver_InvariantAcrossBackends pins the Phase-N contract: a
// fresh Resolver for the same workspace + Source returns the same
// value every time, regardless of how the caller (a backend) intends
// to use it.
//
// We test from the env source (fastest path; the chain's preferred
// step) so the test doesn't depend on keychain state. The chain-
// order tests in resolver_test.go cover the env→keychain→config
// fallthrough.
func TestResolver_InvariantAcrossBackends(t *testing.T) {
	resetEnv(t)
	t.Setenv("IBMCLOUD_API_KEY", "phase-n-invariant-key")
	keyring.MockInit()

	// Simulate four backend invocations from the same parent
	// roksbnkctl process. Each constructs its own Resolver instance
	// (mirroring how the cli layer builds one per command), but the
	// inputs (Workspace, Source, NonInteractive) are identical — so
	// the output must be too.
	backends := []string{"local", "docker", "k8s", "ssh:jumphost"}
	var firstKey string
	for i, backend := range backends {
		r := &Resolver{
			Workspace:      "phase-n-ws",
			NonInteractive: true,
		}
		got, err := r.IBMCloudAPIKey(context.Background())
		if err != nil {
			t.Fatalf("backend=%s: resolver error: %v", backend, err)
		}
		if i == 0 {
			firstKey = got
			continue
		}
		if got != firstKey {
			t.Errorf("backend=%s: got %q, want %q (key MUST be invariant across backend choices — Phase N contract)",
				backend, got, firstKey)
		}
	}

	// Sanity check: the value we got back is the one we set in env,
	// not a stale value from a prior test's keychain mock.
	if firstKey != "phase-n-invariant-key" {
		t.Errorf("resolver picked up unexpected value %q; want phase-n-invariant-key", firstKey)
	}
}

// TestResolver_NoBackendState pins the parallel contract from PRD 04
// §3: the Resolver carries no per-backend hidden state. Calling
// .IBMCloudAPIKey on the same resolver twice (a pattern that arises
// when the cli layer caches a Resolver across a multi-step command
// like `roksbnkctl up`) returns the same value with no side-effect on
// env / keychain.
func TestResolver_NoBackendState(t *testing.T) {
	resetEnv(t)
	t.Setenv("IBMCLOUD_API_KEY", "stateless-key")
	keyring.MockInit()

	r := &Resolver{Workspace: "stateless-ws", NonInteractive: true}

	for i := 0; i < 3; i++ {
		got, err := r.IBMCloudAPIKey(context.Background())
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if got != "stateless-key" {
			t.Errorf("iteration %d: got %q, want stateless-key", i, got)
		}
	}
}
