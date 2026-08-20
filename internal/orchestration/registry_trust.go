package orchestration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// ensureRegistryCATrust is the air-gap precondition that runs at the start of a
// BNK apply, before any chart pulls images. When the workspace's mirror record
// carries a private registry host + CA (a co-located registry the no-egress
// nodes pull from directly), it installs that CA into every node's container
// runtime trust store via a DaemonSet and blocks until it is present on all
// nodes — otherwise the first pull fails with x509 "unknown authority" and
// cert-manager (and the rest of BNK) stalls in ImagePullBackOff.
//
// Off the air-gap path — no mirror record, or a mirror with no CA (public /
// anonymous / already node-trusted) — it is a no-op.
func ensureRegistryCATrust(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, w io.Writer) error {
	rec, err := config.ReadRegistryMirror(cctx.WorkspaceName)
	if err != nil {
		if errors.Is(err, config.ErrNoRegistryMirror) {
			return nil
		}
		return err
	}
	// The record names the host whose CA is about to be installed into every
	// node's container-runtime trust store. If it describes a mirror this
	// workspace is not configured for, that is the wrong CA on every node and a
	// reachability probe against the wrong registry — refuse rather than trust it.
	if err := config.MirrorRecordMismatchError(cctx.WorkspaceName, cctx.Workspace, rec); err != nil {
		return fmt.Errorf("registry CA trust: %w", err)
	}
	host := strings.TrimSpace(rec.RegistryHost)
	ca := strings.TrimSpace(rec.CACert)
	// A missing CA is NOT a reason to skip: the registry may already be trusted (its CA
	// in the node bundle, or a publicly-signed certificate) and still be unroutable
	// from the workers, which is the failure that costs an hour. Only a missing HOST
	// leaves nothing to check.
	if host == "" {
		return nil
	}

	body, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		return fmt.Errorf("registry CA trust: %w", err)
	}
	kc, err := k8s.NewFromKubeconfigBytes(body)
	if err != nil {
		return fmt.Errorf("registry CA trust: building k8s client: %w", err)
	}

	// Probe from every node, in every zone, BEFORE anything tries to pull. The mirror is
	// Required: a disconnected cluster cannot install from a registry it cannot reach,
	// and letting the apply proceed only defers the failure to ImagePullBackOff and a
	// helm deadline that name neither the registry nor the node.
	regHost, regPort := k8s.SplitHostPort(host, "443")
	targets := []k8s.ProbeTarget{{Label: "registry", Host: regHost, Port: regPort, Required: true}}

	// The licence proxy, when one is configured. Not Required: licensing failing later
	// is bad, but it is recoverable and visible, whereas a wrong FLP endpoint is often
	// deliberate during staged bring-up. Report it loudly instead of blocking.
	if ws := cctx.Workspace; ws != nil && ws.BNK.FLP.External != nil {
		if u := strings.TrimSpace(ws.BNK.FLP.External.URL); u != "" {
			h, p := k8s.SplitHostPort(u, "8443")
			targets = append(targets, k8s.ProbeTarget{Label: "F5 License Proxy", Host: h, Port: p})
		}
	}

	// Both tunables come from the workspace (bnk.preflight), because the right values
	// are a property of the environment rather than of the tool: a gateway that has
	// been up for days needs no retry budget at all, and a fabric that programs routes
	// slowly needs more than any default could anticipate. See config/preflight.go.
	retry := cctx.Workspace.ReachabilityRetrySeconds()
	timeout := cctx.Workspace.ReachabilityTimeout()

	if ca == "" {
		fmt.Fprintf(w, "→ no registry CA recorded for %s — assuming it is already trusted; checking reachability from every node\n", host)
	} else {
		fmt.Fprintf(w, "→ installing registry CA trust on all nodes (%s) and checking reachability\n", host)
	}
	if retry > 0 {
		fmt.Fprintf(w, "  each target is retried for up to %ds before it is called unreachable (bnk.preflight.reachability_retry_seconds)\n", retry)
	}
	if err := kc.EnsureRegistryCATrust(ctx, k8s.RegistryTrustOptions{
		Host:              host,
		CAPEM:             ca,
		Wait:              true,
		Targets:           targets,
		ProbeRetrySeconds: retry,
		ReadyTimeout:      timeout,
		// A fresh run id every time, so the DaemonSet rolls and the verdict read back
		// belongs to THIS run. Without it a re-run after fixing the routing re-reads the
		// original pod's log and shows the same failure (issue #57).
		RunID: probeRunID(),
	}); err != nil {
		return fmt.Errorf("registry CA trust: %w", err)
	}

	results, err := kc.CollectNodeProbeResults(ctx)
	if err != nil {
		// Best-effort: an unreadable log must not block an install that would work.
		fmt.Fprintf(w, "  ⚠ could not read node probe results (%v) — continuing without the reachability check\n", err)
		return nil
	}
	summary, probeErr := k8s.SummariseProbeResults(results, targets)
	fmt.Fprint(w, summary)
	if probeErr != nil {
		return probeErr
	}
	if ca != "" {
		fmt.Fprintf(w, "✓ registry CA installed on all nodes; %s is trusted and reachable\n", host)
	} else {
		fmt.Fprintf(w, "✓ %s reachable from every node\n", host)
	}
	return nil
}

// probeRunID is a value that differs on every invocation, so the installer
// DaemonSet's pod template changes and the probe actually re-runs. Only uniqueness
// matters; the nanosecond clock supplies it, and the value never leaves the cluster.
func probeRunID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
