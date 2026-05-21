package cli

// Sprint 19 Issue 1 — `roksbnkctl init --var-file <path>`. Sibling
// helper to init.go that owns:
//
//   1. parsing the operator's tfvars file via
//      config.ReadTFVarsAssignments (the same tolerant parser the
//      applied-tfvars snapshot writer uses — do NOT re-implement);
//   2. mapping the parsed assignments onto the interview-targeted
//      config.yaml fields (region, resource group, cluster name,
//      OpenShift version, workers-per-zone, create_roks_cluster);
//   3. copying the file verbatim, mode 0600, to the workspace root
//      (sibling to `config.yaml`) as `terraform.tfvars.user` — the
//      canonical path the existing `tfws.HasUserTFVars()` codepath in
//      internal/tf auto-layers on every subsequent lifecycle op for
//      both phases without any further code change.
//
// The kubeconfig_dir / scratch_dir paths are workspace-local computed
// values; this helper deliberately does NOT seed them from the tfvars
// even if the file carries them (per the issue spec). Likewise
// `ibmcloud_api_key` lands verbatim on disk via the file-copy step but
// is NOT mapped into config.yaml — the cred resolver owns that surface.
//
// Touches only this sibling file + init.go. No edits to internal/tf,
// internal/orchestration, internal/cos, internal/ibm, or any pre-existing
// _test.go (parity discipline carries forward from Sprint 18).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// flagInitVarFile is the value bound to `roksbnkctl init --var-file
// <path>`. Distinct from the lifecycle-shared `flagVarFiles` array (which
// is the chokepoint-normalized repeatable flag on up/plan/apply/down):
// init takes a single file and persists its contents into both phase
// state dirs. Empty when the flag is not supplied — runInit falls
// through to the byte-identical interactive interview behaviour.
var flagInitVarFile string

func init() {
	initCmd.Flags().StringVar(&flagInitVarFile, "var-file", "",
		"path to a tfvars file (shaped like terraform.tfvars.example); seeds config.yaml and is copied verbatim to the workspace root as terraform.tfvars.user (sibling to config.yaml; serves both phases)")
}

// varFileSeeds carries the interview-targeted fields the operator's
// `--var-file` answered. Each Has* flag tracks presence so runInit can
// skip exactly the prompts the file covered and still prompt for the
// rest. Fields the var-file doesn't carry leave the corresponding Has*
// false and the value zero — the interview prompts (or defaults) for
// them exactly as today.
type varFileSeeds struct {
	Region            string
	HasRegion         bool
	ResourceGroup     string
	HasResourceGroup  bool
	ClusterName       string
	HasClusterName    bool
	OCPVersion        string
	HasOCPVersion     bool
	WorkersPerZone    int
	HasWorkersPerZone bool
	CreateCluster     bool
	HasCreateCluster  bool
}

// loadInitVarFile reads the operator-supplied tfvars file at path and
// returns the interview-targeted seeds. Missing file → actionable error
// naming the path the operator passed. Unparseable / no-recognised-keys
// → actionable error pointing at terraform.tfvars.example. The parser
// itself is config.ReadTFVarsAssignments — same tolerant shape the
// applied-tfvars snapshot writer consumes, same skip-unsupported-HCL
// behaviour, no re-implementation here.
func loadInitVarFile(path string) (varFileSeeds, error) {
	var seeds varFileSeeds

	// Pre-stat so the missing-file branch surfaces our own actionable
	// error naming the path the operator passed, rather than the
	// snapshot-writer's "skipping in applied snapshot" warning that the
	// underlying parser emits on fs.ErrNotExist. Acceptance criterion #4.
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return seeds, fmt.Errorf("--var-file %q does not exist; check the path or copy terraform.tfvars.example as a starting point", path)
		}
		return seeds, fmt.Errorf("--var-file %q: %w", path, err)
	}

	assigns, missing, err := config.ReadTFVarsAssignments(path)
	if err != nil {
		return seeds, fmt.Errorf("--var-file %q: %w", path, err)
	}
	if missing {
		// Stat said the file exists but ReadTFVarsAssignments saw it
		// disappear — race or symlink anomaly. Surface as the same
		// actionable shape as the stat-miss case so the operator sees a
		// single consistent message rather than the upstream "skipping
		// in applied snapshot" warning.
		return seeds, fmt.Errorf("--var-file %q became unreadable between stat and read", path)
	}
	if len(assigns) == 0 {
		return seeds, fmt.Errorf("--var-file %q has no recognised tfvars assignments; check the file shape against terraform.tfvars.example", path)
	}

	// Map the recognised keys onto the interview-targeted fields. Keys
	// the file doesn't carry leave the corresponding Has* false. Values
	// retain config.ReadTFVarsAssignments's raw shape — quoted strings
	// still have their surrounding quotes, bare bools/numbers stay
	// bare — so we unquote / coerce here at the seed-extraction seam.
	if v, ok := assigns["ibmcloud_cluster_region"]; ok {
		seeds.Region = unquoteTFVarString(v)
		seeds.HasRegion = true
	}
	if v, ok := assigns["ibmcloud_resource_group"]; ok {
		seeds.ResourceGroup = unquoteTFVarString(v)
		seeds.HasResourceGroup = true
	}
	if v, ok := assigns["openshift_cluster_name"]; ok {
		seeds.ClusterName = unquoteTFVarString(v)
		seeds.HasClusterName = true
	}
	if v, ok := assigns["openshift_cluster_version"]; ok {
		seeds.OCPVersion = unquoteTFVarString(v)
		seeds.HasOCPVersion = true
	}
	if v, ok := assigns["roks_workers_per_zone"]; ok {
		// Bare-int value in the tfvars file (no quotes). Tolerate a
		// quoted form too on the off-chance the operator typed one —
		// strconv.Atoi on the unquoted shape is the canonical coercion.
		n, perr := strconv.Atoi(strings.TrimSpace(unquoteTFVarString(v)))
		if perr != nil {
			return seeds, fmt.Errorf("--var-file %q: roks_workers_per_zone = %q is not an integer", path, v)
		}
		seeds.WorkersPerZone = n
		seeds.HasWorkersPerZone = true
	}
	if v, ok := assigns["create_roks_cluster"]; ok {
		b, perr := strconv.ParseBool(strings.TrimSpace(unquoteTFVarString(v)))
		if perr != nil {
			return seeds, fmt.Errorf("--var-file %q: create_roks_cluster = %q is not a bool", path, v)
		}
		seeds.CreateCluster = b
		seeds.HasCreateCluster = true
	}
	return seeds, nil
}

// unquoteTFVarString strips one layer of surrounding double quotes if
// present. config.ReadTFVarsAssignments keeps quoted strings verbatim;
// the seed-extraction seam wants the bare string so SaveWorkspace lands
// a clean YAML value (not a value with embedded escaped quotes).
func unquoteTFVarString(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if unq, err := strconv.Unquote(v); err == nil {
			return unq
		}
		// Fallback: strip outer quotes manually if the body had escapes
		// strconv.Unquote couldn't handle. roksbnkctl doesn't emit such
		// shapes, and terraform.tfvars.example doesn't carry them, but
		// the byte-strip keeps us from blowing up on an operator's
		// hand-typed value.
		return v[1 : len(v)-1]
	}
	return v
}

// writeUserTFVarsCopies copies srcPath verbatim to the workspace root
// as `terraform.tfvars.user`, mode 0600 (matches the existing
// applied-tfvars snapshot pattern — the file carries `ibmcloud_api_key`
// per the var-file shape). Returns the absolute destination path so
// runInit can print the `✓ Wrote <abs-path>` confirmation line in the
// same shape as the surrounding init output.
//
// Path. `tf.Workspace.UserTFVarsPath()` resolves to
// `filepath.Dir(stateDir) + "/terraform.tfvars.user"` — for BOTH the
// trial and cluster phases that resolves to the same workspace-root
// path (sibling to `config.yaml`), so a SINGLE copy at the workspace
// root is auto-layered by the lifecycle for either phase. Initially
// staff wrote two copies inside `state/` and `state-cluster/`, where
// `HasUserTFVars()` does NOT look; that's the bug the round-1 live
// verify caught — fixed to one copy at the workspace root.
//
// State-dir creation: not done here — `runInit`'s normal post-config
// writes (the applied-tfvars snapshot, the auto-rendered tfvars)
// MkdirAll on first use, and this helper no longer touches state dirs.
// WorkspaceDir itself is created by SaveWorkspace (which runs first).
func writeUserTFVarsCopies(workspace, srcPath string) (dest string, err error) {
	body, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("reading --var-file %q: %w", srcPath, err)
	}

	wsDir, err := config.WorkspaceDir(workspace)
	if err != nil {
		return "", err
	}
	// SaveWorkspace creates the dir, but be defensive — this helper runs
	// just after SaveWorkspace, and a 0700 MkdirAll is a no-op when the
	// dir already exists with that mode.
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		return "", fmt.Errorf("creating workspace dir %q: %w", wsDir, err)
	}

	dest = filepath.Join(wsDir, "terraform.tfvars.user")
	if err := writeUserTFVarsAtomic(dest, body); err != nil {
		return "", err
	}
	return dest, nil
}

// writeUserTFVarsAtomic writes body to dest at mode 0600 using the
// write-tempfile-then-rename pattern WriteAppliedTFVars uses. Avoids a
// half-written terraform.tfvars.user if the process is killed mid-write
// (the file carries the api key — a truncated copy would silently fail
// the next lifecycle op's IAM auth).
func writeUserTFVarsAtomic(dest string, body []byte) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".terraform.tfvars.user.*")
	if err != nil {
		return fmt.Errorf("creating temp file for %q: %w", dest, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing %q: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file for %q: %w", dest, err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod %q: %w", dest, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming %q into place: %w", dest, err)
	}
	return nil
}

// absVarFilePath resolves the operator's `--var-file <path>` to an
// absolute path against the current CWD. The chokepoint
// (orchestration.Resolve in root.PersistentPreRunE) normalizes the
// lifecycle commands' repeatable `--var-file` array but does NOT touch
// init's single-string `--var-file` — that flag is init-specific and
// the chokepoint guard test pins zero per-RunE re-derivations of
// `flagVarFiles`. We resolve here, once, at the entry of the var-file
// branch so the `✓ Wrote <abs-path>` confirmation lines print the
// absolute path the operator can grep on disk, and so the os.Stat /
// os.ReadFile in loadInitVarFile / writeUserTFVarsCopies sees the same
// path the operator sees in the confirmation output.
func absVarFilePath(p string) (string, error) {
	if filepath.IsAbs(p) {
		return p, nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolving --var-file %q to absolute path: %w", p, err)
	}
	return abs, nil
}
