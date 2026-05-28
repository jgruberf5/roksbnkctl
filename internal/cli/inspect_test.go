package cli

// Tests for `awsbnkctl status`'s state.env-driven deploy-state lines
// (PR4 — status now reports deploy state from the state.env IDs cache).
// The helpers under test are pure functions over a *state.State, so we
// build a real state.env on disk and load it through internal/aws/state
// (the same path runtime uses), then assert each line's shape.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// TestResolveKubeconfigForStatus verifies that resolveKubeconfigForStatus
// prefers the state.env KUBECONFIG_PATH over the host default, and falls back
// gracefully when the path is missing or state is nil.
func TestResolveKubeconfigForStatus(t *testing.T) {
	t.Parallel()

	t.Run("nil state falls back to host default", func(t *testing.T) {
		got := resolveKubeconfigForStatus(nil)
		// We can't assert the exact host default path, but we can assert
		// the function doesn't panic and returns something (or empty string)
		// without touching an absent state.
		_ = got // just ensure it doesn't panic
	})

	t.Run("state without KUBECONFIG_PATH falls back to host default", func(t *testing.T) {
		st, _ := stateFrom(t, map[string]string{"VPC_ID": "vpc-123"})
		got := resolveKubeconfigForStatus(st)
		// No KUBECONFIG_PATH → must not return the absent stored path.
		// The result is either the host default or "".
		_ = got
	})

	t.Run("state with existing KUBECONFIG_PATH returns that path", func(t *testing.T) {
		// Write a real file so os.Stat succeeds.
		dir := t.TempDir()
		kcFile := filepath.Join(dir, "kubeconfig")
		if err := os.WriteFile(kcFile, []byte("fake-kubeconfig"), 0o600); err != nil {
			t.Fatal(err)
		}

		st, _ := stateFrom(t, map[string]string{"KUBECONFIG_PATH": kcFile})
		got := resolveKubeconfigForStatus(st)
		if got != kcFile {
			t.Errorf("resolveKubeconfigForStatus = %q, want %q", got, kcFile)
		}
	})

	t.Run("state with relative KUBECONFIG_PATH resolves via state dir", func(t *testing.T) {
		// Simulate a relative path stored in state.env by using basename
		// and putting the file next to the state.env.
		dir := t.TempDir()
		kcFile := filepath.Join(dir, "kubeconfig")
		if err := os.WriteFile(kcFile, []byte("fake-kubeconfig"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.env"), []byte("KUBECONFIG_PATH=.awsbnkctl/mycluster/kubeconfig\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := state.Load(dir)
		if err != nil {
			t.Fatalf("state.Load: %v", err)
		}
		// The stored path ".awsbnkctl/mycluster/kubeconfig" won't exist
		// as-is in the temp dir, so resolveKubeconfigForStatus will try
		// filepath.Join(st.Dir(), "kubeconfig") = dir/kubeconfig, which
		// does exist (we wrote it above).
		got := resolveKubeconfigForStatus(st)
		if got != kcFile {
			t.Errorf("resolveKubeconfigForStatus = %q, want %q (state dir fallback)", got, kcFile)
		}
	})
}

// TestResolveKubeconfigFromConfig verifies the shared helper that derives
// a kubeconfig path from a cluster.yaml via its state.env KUBECONFIG_PATH.
func TestResolveKubeconfigFromConfig(t *testing.T) {
	t.Parallel()

	t.Run("empty configPath returns empty string", func(t *testing.T) {
		got, err := resolveKubeconfigFromConfig("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})

	t.Run("nonexistent config file returns error", func(t *testing.T) {
		_, err := resolveKubeconfigFromConfig("/nonexistent/path/cluster.yaml")
		if err == nil {
			t.Fatal("expected error for nonexistent config, got nil")
		}
	})
}

// stateFrom writes a state.env with the given KEY=VALUE pairs into a temp
// dir and loads it. Returns the loaded State and its dir.
func stateFrom(t *testing.T, kv map[string]string) (*state.State, string) {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	for k, v := range kv {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "state.env"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	return st, dir
}

func TestTMMNodeLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kv   map[string]string
		want string
	}{
		{"node name preferred", map[string]string{"TMM_NODE_NAME": "ip-10-0-1-5", "TMM_INSTANCE_ID": "i-abc"}, "ip-10-0-1-5"},
		{"instance id fallback", map[string]string{"TMM_INSTANCE_ID": "i-abc"}, "i-abc"},
		{"neither", map[string]string{}, "not provisioned"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := stateFrom(t, tc.kv)
			if got := tmmNodeLine(st); got != tc.want {
				t.Errorf("tmmNodeLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBNKActivationLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		kv         map[string]string
		wantPrefix string
	}{
		{"not activated", map[string]string{}, "not activated"},
		{"activating (license applied, not ready)", map[string]string{"LICENSE_APPLIED_AT": "2026-05-22T10:00:00Z", "LICENSE_NAME": "lic-1"}, "activating"},
		{"active (ready + license)", map[string]string{"CNEINSTANCE_READY_AT": "2026-05-22T11:00:00Z", "LICENSE_NAME": "lic-1"}, "Active"},
		{"active (ready only)", map[string]string{"CNEINSTANCE_READY_AT": "2026-05-22T11:00:00Z"}, "Active"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := stateFrom(t, tc.kv)
			if got := bnkActivationLine(st); !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("bnkActivationLine = %q, want prefix %q", got, tc.wantPrefix)
			}
		})
	}
}

func TestForgeLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kv   map[string]string
		want string
	}{
		{"not linked", map[string]string{}, "not linked"},
		{"status only", map[string]string{"FORGE_STATUS": "registered"}, "registered"},
		{"status + project", map[string]string{"FORGE_STATUS": "registered", "FORGE_PROJECT_ID": "proj-9"}, "registered (project proj-9)"},
		{"project only -> unknown status", map[string]string{"FORGE_PROJECT_ID": "proj-9"}, "unknown (project proj-9)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := stateFrom(t, tc.kv)
			if got := forgeLine(st); got != tc.want {
				t.Errorf("forgeLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLastPhaseAppliedLine(t *testing.T) {
	t.Parallel()

	// No timestamps → the "no phase timestamps" hint.
	stEmpty, _ := stateFrom(t, map[string]string{"VPC_ID": "vpc-1"})
	if got := lastPhaseAppliedLine(stEmpty); !strings.Contains(got, "no phase timestamps") {
		t.Errorf("empty: got %q, want a 'no phase timestamps' hint", got)
	}

	// Multiple timestamps + a non-RFC3339 sentinel ("dry-run") → reports
	// the most-recent parseable key, ignores the sentinel.
	stMany, _ := stateFrom(t, map[string]string{
		"NADS_APPLIED_AT":      "2026-05-22T09:00:00Z",
		"LICENSE_APPLIED_AT":   "dry-run", // must be skipped
		"CNEINSTANCE_READY_AT": "2026-05-22T12:30:00Z",
		"FLO_INSTALLED_AT":     "2026-05-22T10:15:00Z",
	})
	got := lastPhaseAppliedLine(stMany)
	if !strings.HasPrefix(got, "CNEINSTANCE_READY_AT at ") {
		t.Errorf("many: got %q, want most-recent key CNEINSTANCE_READY_AT", got)
	}
}

func TestLoadStatusState(t *testing.T) {
	t.Parallel()

	// No config + no fallback name → not found.
	if _, ok := loadStatusState("", ""); ok {
		t.Error("expected (nil,false) with no config and no fallback name")
	}

	// Fallback name resolves a state.env relative to the working dir.
	st, dir := stateFrom(t, map[string]string{"VPC_ID": "vpc-xyz"})
	// loadStatusState looks under .awsbnkctl/<name>/state.env relative to
	// CWD; stage that layout and chdir into the parent so the relative
	// path resolves.
	root := t.TempDir()
	clusterName := "fallbackcl"
	stateDir := filepath.Join(root, ".awsbnkctl", clusterName)
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(dir, "state.env"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.env"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	loaded, ok := loadStatusState("", clusterName)
	if !ok {
		t.Fatal("expected fallback-name lookup to find the staged state.env")
	}
	if loaded.Get("VPC_ID") != "vpc-xyz" {
		t.Errorf("loaded wrong state: VPC_ID=%q", loaded.Get("VPC_ID"))
	}

	// A fallback name pointing at a dir with no state.env → not found.
	if _, ok := loadStatusState("", "doesnotexist"); ok {
		t.Error("expected not-found for a cluster with no state.env")
	}

	_ = st // silence: st only used to seed the file via dir read above
}

func TestWriteStatusDemoBanner(t *testing.T) {
	t.Parallel()

	future := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)

	cases := []struct {
		name       string
		kv         map[string]string
		wantBanner bool
		wantSubstr string
		wantAbsent string
	}{
		{name: "demo + future expiry", kv: map[string]string{"DEMO_MODE": "true", "DEMO_EXPIRY": future}, wantBanner: true, wantSubstr: "in "},
		{name: "demo + past expiry", kv: map[string]string{"DEMO_MODE": "true", "DEMO_EXPIRY": past}, wantBanner: true, wantSubstr: "EXPIRED"},
		{name: "demo + missing expiry", kv: map[string]string{"DEMO_MODE": "true"}, wantBanner: true, wantAbsent: "expires"},
		{name: "non-demo cluster", kv: map[string]string{"VPC_ID": "vpc-1"}, wantBanner: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var st *state.State
			if tc.kv != nil {
				st, _ = stateFrom(t, tc.kv)
			}
			var buf bytes.Buffer
			writeStatusDemoBanner(&buf, st)
			got := buf.String()

			if tc.wantBanner && !strings.Contains(got, "⚠ DEMO") {
				t.Errorf("expected ⚠ DEMO banner, got: %q", got)
			}
			if !tc.wantBanner && got != "" {
				t.Errorf("expected no output for non-demo cluster, got: %q", got)
			}
			if tc.wantSubstr != "" && !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("expected %q in output, got: %q", tc.wantSubstr, got)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("expected %q to be absent from output, got: %q", tc.wantAbsent, got)
			}
		})
	}
}

func TestWriteStatusDemoBannerNilState(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	writeStatusDemoBanner(&buf, nil) // must not panic
	if buf.Len() != 0 {
		t.Errorf("expected no output for nil state, got: %q", buf.String())
	}
}
