package cli

// Sprint 30 (PRD 13) — `roksbnkctl init` config seeding. Two related surfaces:
//
//   - `--config-file <path|url>` seeds the workspace config.yaml directly
//     (sibling of `--var-file`, which seeds terraform.tfvars). Non-interactive
//     when the supplied config is complete.
//   - both `--var-file` and `--config-file` accept a local path OR an http(s)
//     URL, fetched by resolveSeedInput before the existing file-based logic.

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

const (
	seedFetchTimeout = 30 * time.Second
	seedMaxBytes     = 10 << 20 // 10 MB cap on a fetched seed body
)

var (
	// flagInitConfigFile — `init --config-file <path|url>`: seed the workspace
	// config.yaml directly (PRD 13 Issue 2).
	flagInitConfigFile string
	// flagInitOverrideFromEnv — `init --override-from-env`: overlay config.yaml
	// fields from environment variables after seeding (PRD 13 Issue 4).
	flagInitOverrideFromEnv bool

	// flagInitNonInteractive — `init --non-interactive`: build config.yaml from
	// the ROKSBNKCTL_* / IBMCLOUD_API_KEY environment variables ALONE — no
	// prompts, no --config-file. The path for an argv+env container runner
	// (CI / BNK Forge container step), where there is no TTY and no way to stage
	// a seed file. tf_source.type defaults to "embedded".
	flagInitNonInteractive bool

	seedURLRe = regexp.MustCompile(`^https?://`)
)

func init() {
	initCmd.Flags().StringVar(&flagInitConfigFile, "config-file", "",
		"path or http(s) URL to a workspace config.yaml to seed (sibling of --var-file; non-interactive when complete)")
	initCmd.Flags().BoolVar(&flagInitOverrideFromEnv, "override-from-env", false,
		"after seeding, overlay config.yaml fields from environment variables (e.g. IBMCLOUD_API_KEY → ibmcloud.api_key_b64)")
	initCmd.Flags().BoolVar(&flagInitNonInteractive, "non-interactive", false,
		"build config.yaml from environment variables ALONE — no prompts, no --config-file (for argv+env runners; pair with the ROKSBNKCTL_* env vars)")
}

// runInitFromEnv is the `--non-interactive` path: assemble a Workspace purely
// from the supported environment variables (config.OverrideFromEnv), default
// tf_source.type to embedded, validate completeness, and write it. No file is
// read and no prompt is shown — the path for an argv+env container runner that
// cannot stage a --config-file. A `--var-file` supplied alongside is still
// copied verbatim to terraform.tfvars.user.
func runInitFromEnv(cctx *config.Context) error {
	var ws config.Workspace
	// Seed the standard resource toggles BEFORE the env overlay so an override
	// that touches one toggle (e.g. adopting an existing transit gateway) leaves
	// the rest at their create:true defaults instead of the bool zero value.
	ws.Resources = config.DefaultResources()
	applied := config.OverrideFromEnv(&ws)
	if len(applied) > 0 {
		fmt.Fprintf(os.Stderr, "✓ Applied %d field(s) from environment: %s\n", len(applied), strings.Join(applied, ", "))
	}

	// tf_source.type is the one required field with no env override; an empty
	// type already means "embedded" at render time, so default it explicitly so
	// the completeness check passes (PRD 13 / runner contract).
	if ws.TFSource.Type == "" {
		ws.TFSource.Type = "embedded"
	}

	if missing := missingRequiredConfigFields(&ws); len(missing) > 0 {
		return fmt.Errorf("--non-interactive init: missing required field(s): %s\n  set them via env — ROKSBNKCTL_REGION, ROKSBNKCTL_RESOURCE_GROUP, ROKSBNKCTL_PREFIX (and IBMCLOUD_API_KEY); cluster identity via ROKSBNKCTL_CLUSTER_NAME / ROKSBNKCTL_CLUSTER_CREATE",
			strings.Join(missing, ", "))
	}

	if err := config.SaveWorkspace(cctx.WorkspaceName, &ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	cfgPath, _ := config.WorkspaceConfigPath(cctx.WorkspaceName)
	fmt.Fprintf(os.Stderr, "✓ Wrote %s (non-interactive, from environment)\n", cfgPath)

	// A --var-file supplied alongside still seeds terraform.tfvars.user.
	if flagInitVarFile != "" {
		vpath, vcleanup, verr := resolveSeedInput(flagInitVarFile)
		if verr != nil {
			return verr
		}
		defer vcleanup()
		dest, cerr := writeUserTFVarsCopies(cctx.WorkspaceName, vpath)
		if cerr != nil {
			return cerr
		}
		fmt.Fprintf(os.Stderr, "✓ Wrote %s\n", dest)
	}

	if err := config.SetCurrent(cctx.WorkspaceName); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not set current workspace: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "✓ Current workspace: %s\n", cctx.WorkspaceName)
	}
	return nil
}

// isSeedURL reports whether s is an http(s) URL — the only fetchable seed form.
func isSeedURL(s string) bool { return seedURLRe.MatchString(strings.TrimSpace(s)) }

// resolveSeedInput turns a --var-file / --config-file value into a LOCAL file
// path the rest of init can os.ReadFile. A local path is returned absolute and
// cleanup is a no-op. An http(s) URL is fetched (30s timeout, 10 MB cap) into a
// temp file whose path is returned; the caller MUST defer cleanup() to remove
// it. Non-http(s) values are treated as local paths.
func resolveSeedInput(s string) (localPath string, cleanup func(), err error) {
	noop := func() {}
	s = strings.TrimSpace(s)
	if !isSeedURL(s) {
		abs, aerr := absVarFilePath(s)
		return abs, noop, aerr
	}
	client := &http.Client{Timeout: seedFetchTimeout}
	resp, err := client.Get(s)
	if err != nil {
		return "", noop, fmt.Errorf("fetching %q: %w", s, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", noop, fmt.Errorf("fetching %q: HTTP %d", s, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, seedMaxBytes+1))
	if err != nil {
		return "", noop, fmt.Errorf("reading %q: %w", s, err)
	}
	if len(body) > seedMaxBytes {
		return "", noop, fmt.Errorf("fetching %q: body exceeds the %d-byte limit", s, seedMaxBytes)
	}
	tmp, err := os.CreateTemp("", "roksbnkctl-seed-*.tmp")
	if err != nil {
		return "", noop, fmt.Errorf("creating temp file for %q: %w", s, err)
	}
	tmpPath := tmp.Name()
	cleanup = func() { _ = os.Remove(tmpPath) }
	if _, werr := tmp.Write(body); werr != nil {
		tmp.Close()
		cleanup()
		return "", noop, fmt.Errorf("writing fetched %q: %w", s, werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		cleanup()
		return "", noop, fmt.Errorf("closing temp file for %q: %w", s, cerr)
	}
	return tmpPath, cleanup, nil
}

// runInitFromConfigFile is the non-interactive `--config-file` path: resolve
// the input, strict-parse it into a Workspace (unknown fields rejected — never
// silently dropped), apply `--override-from-env`, validate completeness, then
// write it. The interview is skipped. A `--var-file` supplied alongside is
// still copied verbatim to terraform.tfvars.user.
func runInitFromConfigFile(cctx *config.Context) error {
	path, cleanup, err := resolveSeedInput(flagInitConfigFile)
	if err != nil {
		return err
	}
	defer cleanup()

	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading --config-file: %w", err)
	}
	var ws config.Workspace
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true) // reject unknown fields rather than silently dropping
	if derr := dec.Decode(&ws); derr != nil {
		return fmt.Errorf("parsing --config-file %q: %w", flagInitConfigFile, derr)
	}

	if flagInitOverrideFromEnv {
		if applied := config.OverrideFromEnv(&ws); len(applied) > 0 {
			fmt.Fprintf(os.Stderr, "✓ Applied %d override(s) from environment: %s\n", len(applied), strings.Join(applied, ", "))
		}
	}

	if missing := missingRequiredConfigFields(&ws); len(missing) > 0 {
		return fmt.Errorf("--config-file %q is missing required field(s): %s\n  supply them in the file, set them via --override-from-env, or run `roksbnkctl init` interactively",
			flagInitConfigFile, strings.Join(missing, ", "))
	}

	if err := config.SaveWorkspace(cctx.WorkspaceName, &ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	cfgPath, _ := config.WorkspaceConfigPath(cctx.WorkspaceName)
	fmt.Fprintf(os.Stderr, "✓ Wrote %s\n", cfgPath)

	// A --var-file alongside --config-file still seeds terraform.tfvars.user.
	if flagInitVarFile != "" {
		vpath, vcleanup, verr := resolveSeedInput(flagInitVarFile)
		if verr != nil {
			return verr
		}
		defer vcleanup()
		dest, cerr := writeUserTFVarsCopies(cctx.WorkspaceName, vpath)
		if cerr != nil {
			return cerr
		}
		fmt.Fprintf(os.Stderr, "✓ Wrote %s\n", dest)
	}

	if err := config.SetCurrent(cctx.WorkspaceName); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not set current workspace: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "✓ Current workspace: %s\n", cctx.WorkspaceName)
	}
	fmt.Fprintln(os.Stderr, "\nNext: roksbnkctl up")
	return nil
}

// missingRequiredConfigFields returns the required config.yaml fields absent
// from a --config-file seed (after any env override). A usable workspace needs
// an IBM Cloud region + resource group, a name prefix, and a terraform source.
// The API key is intentionally NOT required here — it resolves from the env /
// keychain / config at run time.
func missingRequiredConfigFields(ws *config.Workspace) []string {
	var missing []string
	if ws.IBMCloud.Region == "" {
		missing = append(missing, "ibmcloud.region")
	}
	if ws.IBMCloud.ResourceGroup == "" {
		missing = append(missing, "ibmcloud.resource_group")
	}
	if strings.TrimSpace(ws.Prefix) == "" {
		missing = append(missing, "prefix")
	}
	if ws.TFSource.Type == "" {
		missing = append(missing, "tf_source.type")
	}
	return missing
}
