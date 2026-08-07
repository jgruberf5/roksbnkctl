package orchestration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

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

	if ca == "" {
		fmt.Fprintf(w, "→ no registry CA recorded for %s — assuming it is already trusted; checking reachability from every node\n", host)
	} else {
		fmt.Fprintf(w, "→ installing registry CA trust on all nodes (%s) and checking reachability\n", host)
	}
	if err := kc.EnsureRegistryCATrust(ctx, host, ca, "", true, targets...); err != nil {
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
