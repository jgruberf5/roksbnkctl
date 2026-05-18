package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Sprint 14 / get-well (issues/issue_sprint14_staff.md Issue 1,
// issues/issue_sprint13_architect.md Issue 2, option C part B).
//
// Part A hardens the cloud-init kubeconfig provisioning so NEW deploys
// are robust. But an already-running jumphost that booted before the
// fix (the 2026-05-18 14:54 live case) has no kubeconfig and stays that
// way until a human intervenes. Part B repairs it on the fly: before an
// `--on <target> kubectl|oc` dispatch, probe the target for a usable
// kubeconfig and, if absent, (re)authenticate the target's ibmcloud CLI
// and run `ibmcloud ks cluster config --cluster <id> --admin` ON THE
// TARGET so the wrapped command then finds /home/ubuntu/.kube/config.
//
// The heal RE-RUNS `ibmcloud login` every attempt rather than assuming
// the cloud-init `su - ubuntu -c "ibmcloud login … || true"` fork
// succeeded: on the user's 2026-05-18 already-broken jumphost that fork
// had failed silently, so `ks cluster config` returned `FAILED — Log in
// to the IBM Cloud CLI by running 'ibmcloud login'`. Healing the
// kubeconfig without first healing the login cannot unblock an
// already-broken box (the explicit Part B goal), so the login is part
// of the heal. Credentials come from the workspace (same resolver
// workspaceEnvCore uses) and are passed as POSITIONAL args to `sh -c`
// (injection-safe; key never interpolated into the script text). The
// key transits the encrypted SSH channel as a command argument — the
// same trust envelope the design already accepts (cloud-init bakes the
// key onto the box; IBMCLOUD_API_KEY is forwarded over --on by design).
//
// The hard requirement (issue acceptance criteria): distinguish
//
//	"no kubeconfig → heal"      (transient/never-provisioned: heal)
//
// from
//
//	"cluster genuinely down"    (heal cannot succeed: surface the real
//	                             `ibmcloud ks cluster config` error after
//	                             bounded retry — DON'T spin forever, DON'T
//	                             mask the outage as success)
//
// We never silently fall back to the broken state: a self-heal failure
// produces a clear, actionable error naming the underlying ibmcloud
// failure.

// selfHealMaxAttempts / selfHealBackoff bound the on-target
// `ibmcloud ks cluster config --admin` retry. Small + finite: the
// jumphost is either already provisioned (0 heal attempts), transiently
// not-ready (a couple of retries clears it), or genuinely down (we give
// up fast and surface the real error rather than spin). Total worst
// case ≈ selfHealMaxAttempts * selfHealBackoff.
var (
	selfHealMaxAttempts = 4
	selfHealBackoff     = 6 * time.Second
)

// kubectlOrOC reports whether argv invokes kubectl or oc — the only
// tools whose remote behaviour depends on a usable kubeconfig and thus
// the only ones the self-heal gates. (ibmcloud / shell / arbitrary
// `exec` commands don't read kubeconfig; healing them would be wasted
// round-trips and could mask unrelated failures.)
func kubectlOrOC(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch argv[0] {
	case "kubectl", "oc":
		return true
	default:
		return false
	}
}

// remoteRunner is the minimal seam dispatchRemote's self-heal needs: run
// one short command on the already-connected target and report its exit
// code (and any transport error). *remote.Client satisfies this in
// production; tests stub it to drive the heal-vs-outage matrix without a
// real SSH connection.
type remoteRunner interface {
	Run(ctx context.Context, argv []string, stdout, stderr io.Writer) (int, error)
}

// remoteKubeconfigUsable probes the target for a usable kubeconfig
// WITHOUT contacting the kube API (so a healthy-config-but-cluster-down
// case is NOT misread as "no config"). `kubectl config current-context`
// reads only the local kubeconfig file: exit 0 ⇒ a context exists ⇒ a
// kubeconfig is present and usable; non-zero (or kubectl's localhost:8080
// fallback path, which here means "no config file at all") ⇒ heal.
func remoteKubeconfigUsable(ctx context.Context, r remoteRunner) (bool, error) {
	var out, errb bytes.Buffer
	code, err := r.Run(ctx,
		[]string{"sh", "-c", "kubectl config current-context >/dev/null 2>&1; echo rc=$?"},
		&out, &errb)
	if err != nil {
		// Transport-level failure (not a remote exit code) — we can't
		// tell config state; surface it, don't guess.
		return false, fmt.Errorf("probing remote kubeconfig: %w", err)
	}
	if code != 0 {
		return false, fmt.Errorf("probing remote kubeconfig: remote probe exited %d: %s",
			code, strings.TrimSpace(errb.String()))
	}
	return strings.Contains(out.String(), "rc=0"), nil
}

// healRemoteKubeconfig runs `ibmcloud ks cluster config --cluster
// <clusterID> --admin` on the target with bounded retry. It is the
// heal-vs-outage discriminator:
//
//   - the command succeeding ⇒ kubeconfig provisioned, return nil.
//   - the command failing every bounded attempt ⇒ the cluster is
//     genuinely unreachable/down (or a region/RG/IAM mismatch). Return
//     a non-nil error carrying the LAST real ibmcloud stderr so the
//     outage is surfaced, never masked as success and never spun on
//     forever.
//
// Idempotent: re-running `ks cluster config --admin` on an
// already-configured host just rewrites the same kubeconfig.
// remoteHealCommand builds the on-target heal command. When an API key
// is available it (re)authenticates the target's ibmcloud CLI first and
// then provisions the admin kubeconfig, as a single `sh -c` so a
// login-then-config failure is surfaced as one bounded attempt. Values
// are POSITIONAL params ($1=clusterID, $2=apiKey, then optional
// region/resource-group) — never interpolated into the script text, so
// the key is injection-safe and absent from the literal. Empty apiKey
// falls back to the pre-Sprint-14 behaviour (assume pre-logged-in) so a
// keyless workspace still degrades sanely rather than mis-erroring.
func remoteHealCommand(clusterID, apiKey, region, resourceGroup string) []string {
	if apiKey == "" {
		return []string{"sh", "-c", `ibmcloud ks cluster config --cluster "$1" --admin`, "_", clusterID}
	}
	login := `ibmcloud login --apikey "$2"`
	args := []string{"_", clusterID, apiKey}
	n := 3
	if region != "" {
		login += fmt.Sprintf(` -r "$%d"`, n)
		args = append(args, region)
		n++
	}
	if resourceGroup != "" {
		login += fmt.Sprintf(` -g "$%d"`, n)
		args = append(args, resourceGroup)
		n++
	}
	script := login + ` && ibmcloud ks cluster config --cluster "$1" --admin`
	return append([]string{"sh", "-c", script}, args...)
}

func healRemoteKubeconfig(ctx context.Context, r remoteRunner, clusterID, apiKey, region, resourceGroup string, progress io.Writer) error {
	if clusterID == "" {
		return fmt.Errorf("self-heal: no cluster id/name resolvable from the workspace " +
			"(terraform outputs roks_cluster_id/roks_cluster_name absent and cluster.name unset) — " +
			"cannot run `ibmcloud ks cluster config --admin` on the target")
	}
	argv := remoteHealCommand(clusterID, apiKey, region, resourceGroup)
	var lastDetail string
	for attempt := 1; attempt <= selfHealMaxAttempts; attempt++ {
		if progress != nil {
			fmt.Fprintf(progress,
				"→ remote kubeconfig missing on target; self-healing (ibmcloud ks cluster config --admin, attempt %d/%d)\n",
				attempt, selfHealMaxAttempts)
		}
		var out, errb bytes.Buffer
		code, err := r.Run(ctx, argv, &out, &errb)
		if err != nil {
			lastDetail = err.Error()
		} else if code == 0 {
			// Re-probe: the command can exit 0 yet not have written a
			// usable config (rare, but we must not declare success on a
			// still-broken state).
			ok, perr := remoteKubeconfigUsable(ctx, r)
			if perr == nil && ok {
				if progress != nil {
					fmt.Fprintln(progress, "✓ remote kubeconfig provisioned via self-heal")
				}
				return nil
			}
			lastDetail = "ibmcloud reported success but no usable kubeconfig appeared on the target"
			if perr != nil {
				lastDetail = perr.Error()
			}
		} else {
			d := strings.TrimSpace(errb.String())
			if d == "" {
				d = strings.TrimSpace(out.String())
			}
			lastDetail = fmt.Sprintf("ibmcloud ks cluster config exited %d: %s", code, d)
		}
		if attempt < selfHealMaxAttempts {
			select {
			case <-time.After(selfHealBackoff):
			case <-ctx.Done():
				return fmt.Errorf("self-heal cancelled: %w", ctx.Err())
			}
		}
	}
	// Bounded retry exhausted and the config still isn't usable: this is
	// a genuine outage / misconfiguration, NOT "just not provisioned".
	// Surface the real underlying error — never mask it as success,
	// never fall back silently to the broken state.
	return fmt.Errorf("self-heal failed after %d attempts — `ibmcloud login` or `ibmcloud ks "+
		"cluster config --admin` on the target keeps failing: the cluster %q appears genuinely "+
		"unreachable/down, or there is a bad/expired API key or a region/resource-group/IAM "+
		"mismatch (this is NOT a missing-kubeconfig that healing can fix). Last error: %s",
		selfHealMaxAttempts, clusterID, lastDetail)
}

// maybeSelfHealRemoteKubeconfig is the dispatchRemote pre-flight for the
// kubectl/oc `--on` path. No-op for non-kubectl/oc argv. Probe → if a
// usable kubeconfig is already present, return nil immediately
// (idempotent, zero extra round-trips on the healthy path). If absent,
// heal with bounded retry; a heal failure is returned to the caller so
// the dispatch aborts with a clear actionable error instead of running
// kubectl into the `localhost:8080` fallback.
func maybeSelfHealRemoteKubeconfig(ctx context.Context, r remoteRunner, argv []string, clusterID, apiKey, region, resourceGroup string) error {
	if !kubectlOrOC(argv) {
		return nil
	}
	ok, err := remoteKubeconfigUsable(ctx, r)
	if err != nil {
		// Could not even probe (transport / remote shell failure). Don't
		// silently proceed into a likely-broken kubectl run — surface it.
		return err
	}
	if ok {
		return nil
	}
	return healRemoteKubeconfig(ctx, r, clusterID, apiKey, region, resourceGroup, os.Stderr)
}
