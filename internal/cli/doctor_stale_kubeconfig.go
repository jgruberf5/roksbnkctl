package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/doctor"
)

// staleKubeconfigCheck reports the expired credential files terraform leaves in
// the workspace (#277).
//
// WHAT THESE FILES ARE. The IBM provider's ibm_container_cluster_config data
// source writes a kubeconfig into config_dir as a SIDE EFFECT of being read. The
// providers themselves use the data source's host/token ATTRIBUTES, which
// terraform re-reads on every plan, so the files are a by-product that nothing
// consumes -- not the providers, not tfx (which takes --kube-host plus a token
// from the environment), not k8s.DefaultKubeconfigPath.
//
// WHY THAT IS WORTH A CHECK ANYWAY. They are complete, plausible kubeconfigs
// carrying an IAM token that lives about twenty minutes, and they are never
// cleaned up. Anyone who reaches for one -- a person, a script, an agent --
// gets nothing but:
//
//	error: You must be logged in to the server (Unauthorized)
//	memcache.go:265 couldn't get current server API group list:
//	  the server has asked for the client to provide credentials
//
// which names no file, no credential and no cause. Diagnosing that from scratch
// took about an hour: the cluster was healthy, the IBM login was fine, and
// ~/.kube/config worked, so every obvious explanation was wrong.
//
// This check does not fix anything and deliberately does not delete the files:
// they belong to terraform's config_dir, and removing state a provider believes
// it owns to tidy a report is a worse bug than the one being reported. It says
// where they are, that they are stale, and what to use instead.
func staleKubeconfigCheck(cctx *config.Context) (doctor.Check, bool) {
	if cctx == nil || cctx.Workspace == nil {
		return doctor.Check{}, false
	}
	dir := workspaceKubeconfigDir(cctx)
	if dir == "" {
		return doctor.Check{}, false
	}

	var stale, fresh int
	var first string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "config.yml" {
			return nil //nolint:nilerr // a missing dir is the normal case
		}
		exp, ok := kubeconfigTokenExpiry(p)
		if !ok {
			return nil
		}
		if time.Now().After(exp) {
			stale++
			if first == "" {
				first = p
			}
			return nil
		}
		fresh++
		return nil
	})

	if stale == 0 && fresh == 0 {
		return doctor.Check{}, false
	}

	c := doctor.Check{Name: "workspace phase kubeconfigs", Optional: true}
	if stale == 0 {
		c.Status = doctor.StatusOK
		c.Detail = fmt.Sprintf("%d cached, credentials still valid", fresh)
		return c, true
	}
	c.Status = doctor.StatusWarning
	c.Detail = fmt.Sprintf(
		"%d of %d carry an EXPIRED token (e.g. %s).\n"+
			"      Nothing reads these — terraform writes them as a side effect and takes its\n"+
			"      credential from the data source, which it re-reads every plan. roksbnkctl's\n"+
			"      own verbs skip an expired one when choosing a kubeconfig (#281), so this is\n"+
			"      informational: they are safe to ignore and safe to delete. Pointing kubectl\n"+
			"      at one YOURSELF still gives only \"Unauthorized\" with no clue why. Use\n"+
			"      ~/.kube/config, or refresh it with:\n"+
			"          roksbnkctl kubeconfig --download",
		stale, stale+fresh, shortenHome(first))
	return c, true
}

// workspaceKubeconfigDir is <workspace>/state/kubeconfig, the config_dir handed
// to every phase's data source.
func workspaceKubeconfigDir(cctx *config.Context) string {
	root, err := config.WorkspaceDir(cctx.WorkspaceName)
	if err != nil || strings.TrimSpace(root) == "" {
		return ""
	}
	dir := filepath.Join(root, "state", "kubeconfig")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return ""
	}
	return dir
}

var kubeconfigTokenRe = regexp.MustCompile(`(?m)^\s*token:\s*(\S+)\s*$`)

// kubeconfigTokenExpiry reads the `exp` claim from a kubeconfig's bearer token.
//
// Returns ok=false for anything it cannot read as a JWT — a cert-based
// kubeconfig, an opaque token, a malformed file. A check that guessed at those
// would report healthy credentials as expired, which is worse than staying quiet:
// the whole value here is that the report can be believed.
func kubeconfigTokenExpiry(path string) (time.Time, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	m := kubeconfigTokenRe.FindSubmatch(b)
	if m == nil {
		return time.Time{}, false
	}
	parts := strings.Split(string(m[1]), ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

// shortenHome renders a path with $HOME as ~ so the report stays readable.
func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
