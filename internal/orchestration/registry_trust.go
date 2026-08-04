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
	if host == "" || ca == "" {
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

	fmt.Fprintf(w, "→ installing registry CA trust on all nodes (%s) before pulling images\n", host)
	if err := kc.EnsureRegistryCATrust(ctx, host, ca, "", true); err != nil {
		return fmt.Errorf("registry CA trust: %w", err)
	}
	fmt.Fprintf(w, "✓ registry CA installed on all nodes; %s is trusted for image pulls\n", host)
	return nil
}
