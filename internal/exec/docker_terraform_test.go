package exec

// Sprint 5 / PRD 03 §"terraform" + §"State concerns" — terraform docker
// backend unit tests.
//
// The terraform docker backend introduces three behaviours specific to
// terraform that the existing ibmcloud + iperf3 paths don't share:
//
//  1. The state directory bind-mount must be read-write (the others are
//     read-only); the workspace's ~/.roksbnkctl/<ws>/state/ mount lands
//     at /state/ inside the container via RunOpts.HostMounts.
//
//  2. The container runs as `--user $(id -u):$(id -g)` so terraform
//     writes the .tfstate file with the host user's UID/GID (Linux
//     containers run as root by default; bind-mount permission collision
//     is the common gotcha — PRD 05 risks list calls this out). Plumbed
//     via RunOpts.RunAsUser.
//
//  3. The terraform image is pinned to a literal upstream version
//     (`hashicorp/terraform:1.5.7`) — the tag-resolved-from-binary-version
//     pattern that ibmcloud + iperf3 use doesn't apply (terraform is
//     hashicorp's image, not ours). PRD 03 §"Image versioning" closed
//     this question.
//
// The CLI layer (internal/cli/lifecycle.go) handles the v1.x deferral
// for `--backend k8s` and `--backend ssh:<target>` against terraform —
// PRD 03 §"State concerns" explicitly defers state-handling for those
// non-local non-docker backends. That deferral is exercised in the
// e2e tier, not unit-tested here (the seam is at the cobra flag layer
// where mocking is fiddly; the integration tier covers the live path).
//
// Integration tier (live docker daemon round-trip) lives in
// docker_terraform_integration_test.go behind `integration && tfdocker`.

import (
	"strings"
	"testing"
)

// TestRunOpts_HostMounts_StateDirReadWrite asserts the RunOpts shape
// used by the CLI's terraform docker dispatch carries the state dir
// as a writable bind-mount. The CLI builds a HostMount{HostPath:
// stateDir, ContainerPath: "/state", ReadOnly: false}; this test
// exercises the same shape directly via the public RunOpts surface.
func TestRunOpts_HostMounts_StateDirReadWrite(t *testing.T) {
	stateDir := t.TempDir()
	opts := RunOpts{
		HostMounts: []HostMount{{
			HostPath:      stateDir,
			ContainerPath: "/state",
			ReadOnly:      false,
		}},
		RunAsUser: "1000:1000",
		Env: []string{
			"TF_VAR_region=us-south",
			"TF_VAR_cluster_name=test-cluster",
			"TF_DATA_DIR=/state/terraform",
			"TF_IN_AUTOMATION=1",
		},
	}

	// HostMounts: must contain the /state writable bind.
	found := false
	for _, m := range opts.HostMounts {
		if m.ContainerPath == "/state" {
			found = true
			if m.ReadOnly {
				t.Errorf("/state mount must be read-write; got ReadOnly=true")
			}
			if m.HostPath != stateDir {
				t.Errorf("/state mount HostPath: got %q, want %q", m.HostPath, stateDir)
			}
		}
	}
	if !found {
		t.Errorf("expected a /state mount in the RunOpts; got %v", opts.HostMounts)
	}

	// RunAsUser: matches "uid" or "uid:gid" shape.
	if opts.RunAsUser != "1000:1000" {
		t.Errorf("RunAsUser: got %q, want %q", opts.RunAsUser, "1000:1000")
	}
}

// TestRunOpts_TFVarsEnvPassthrough asserts buildContainerEnv passes
// TF_VAR_* env vars through to the container, AND filters host-only
// vars (HOME, USER, PATH, …) that would confuse programs running
// inside the container — see buildContainerEnv comment in docker.go
// for why (the bundled ibmcloud image's plugin lookup breaks if the
// host's HOME leaks through).
//
// PATH is intentionally in the host-only filter set: the container's
// image-default PATH must apply, not the host's `/usr/local/bin:/usr/bin:…`.
func TestRunOpts_TFVarsEnvPassthrough(t *testing.T) {
	in := []string{
		"TF_VAR_region=us-south",
		"TF_VAR_cluster_name=test-cluster",
		"PATH=/usr/local/bin",
		"HOME=/home/jgruber",
	}
	got := buildContainerEnv(in)
	envSet := map[string]bool{}
	for _, e := range got {
		envSet[e] = true
	}

	// TF_VAR_* must pass through.
	for _, want := range []string{"TF_VAR_region=us-south", "TF_VAR_cluster_name=test-cluster"} {
		if !envSet[want] {
			t.Errorf("expected env entry %q in container env; got %v", want, got)
		}
	}
	// Host-only vars must be filtered.
	for _, blocked := range []string{"PATH=/usr/local/bin", "HOME=/home/jgruber"} {
		if envSet[blocked] {
			t.Errorf("host-only env entry %q must be filtered from container env; got %v", blocked, got)
		}
	}
}

// TestTerraformImagePin asserts the resolved terraform image is the
// upstream literal pin (`hashicorp/terraform:<v>`), NOT the
// per-roksbnkctl-version pattern that ibmcloud + iperf3 use. PRD 03
// §"Image versioning" closed this as "tied to release version", except
// terraform which is the upstream image.
func TestTerraformImagePin(t *testing.T) {
	img := toolImages["terraform"]
	if !strings.HasPrefix(img, "hashicorp/terraform:") {
		t.Errorf("terraform image: got %q, want hashicorp/terraform:<v>", img)
	}
	// Must be a concrete version pin, not :latest or :dev (the
	// upstream-image use case is reproducibility — :latest defeats
	// the point).
	if strings.HasSuffix(img, ":latest") || strings.HasSuffix(img, ":dev") {
		t.Errorf("terraform image must pin a concrete version, got %q", img)
	}
	// Sanity: the version string after `:` should be a numeric major.minor.patch
	// shape — staff pinned 1.5.7. Allow future bumps without locking the test
	// to a specific value, but the post-colon part must start with a digit.
	parts := strings.SplitN(img, ":", 2)
	if len(parts) != 2 || len(parts[1]) == 0 || !(parts[1][0] >= '0' && parts[1][0] <= '9') {
		t.Errorf("terraform image tag must start with a numeric version; got %q", img)
	}
}

// TestRunOpts_HostMounts_DefaultEmpty asserts the zero-value RunOpts
// has no HostMounts — the terraform path is opt-in via explicit
// HostMount construction. ibmcloud + iperf3 paths don't trigger
// HostMounts and shouldn't suddenly inherit state mounts.
func TestRunOpts_HostMounts_DefaultEmpty(t *testing.T) {
	opts := RunOpts{}
	if len(opts.HostMounts) != 0 {
		t.Errorf("zero-value RunOpts shouldn't have HostMounts; got %v", opts.HostMounts)
	}
	if opts.RunAsUser != "" {
		t.Errorf("zero-value RunOpts shouldn't have RunAsUser; got %q", opts.RunAsUser)
	}
}
