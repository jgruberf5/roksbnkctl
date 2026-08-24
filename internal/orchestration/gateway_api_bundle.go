package orchestration

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/registry/mirror"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// Installing the Gateway API bundle (#185).
//
// BNK 2.4's FLO crd-installer no longer forces its own Gateway API CRDs; it logs
// a graceful skip and leaves the cluster on whatever bundle OpenShift ships.
// That is right for a base install and wrong for mTLS, which needs the upstream
// STANDARD channel at the version the CNE controller is told to expect. Nothing
// on the cluster installs that, so roksbnkctl does.
//
// WHERE IT RUNS is the whole design. The OpenShift ingress operator owns a
// ValidatingAdmissionPolicy that blocks third-party writes to the Gateway API
// CRDs and recreates it within about a minute of being deleted. The only window
// in which this apply survives is the one where the sweep goroutine is deleting
// that policy every few seconds — so the apply happens INSIDE
// applyBNKWithAdmissionSweep, after the sweep has started and before terraform
// is invoked. Applying it before the sweep starts would race the operator and
// lose; applying it after the apply finished would be too late for the
// crd-installer window it exists to serve.

const (
	// gatewayAPIBundleFieldManager is distinct from the shared k8s.FieldManager
	// so a later conflict names the thing that actually owns these fields. An
	// operator looking at an SSA conflict on a Gateway API CRD should be told
	// "the gateway-api bundle", not "roksbnkctl".
	gatewayAPIBundleFieldManager = "roksbnkctl-gateway-api-bundle"

	// The apply is retried because it is racing a controller. The sweep deletes
	// the blocking policy every 5s and the ingress operator recreates it within
	// ~1m, so a single attempt can land in a closed window through no fault of
	// its own. Twelve attempts at 5s spans a full recreate cycle — long enough
	// that a failure here is a real failure, short enough that it does not hide
	// one for minutes.
	gatewayAPIBundleAttempts = 12
	gatewayAPIBundleBackoff  = 5 * time.Second
)

// applyGatewayAPIBundle installs the Gateway API standard-install bundle when
// this workspace needs it, and does nothing otherwise.
//
// It returns an ERROR rather than warning and continuing. An mTLS install
// without its Gateway API bundle does not fail loudly later — the CNE controller
// is simply configured for a Gateway API the cluster does not carry, and the
// symptom is behavioural, arrives much later, and points nowhere near here. That
// is the same class of silent failure the admission sweep's own logging exists to
// prevent, so this fails before terraform is invoked and the cost is a plan, not
// a half-installed BNK.
func applyGatewayAPIBundle(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, w io.Writer) error {
	if cctx == nil || cctx.Workspace == nil || !cctx.Workspace.GatewayAPIBundleNeeded() {
		return nil
	}
	ws := cctx.Workspace

	art, err := bnkbom.GatewayAPIBundle(ws.GatewayAPIBundleVersion(), ws.BNK.GatewayAPIBundleURL)
	if err != nil {
		return err
	}

	raw, from, err := gatewayAPIBundleBytes(ctx, cctx, art)
	if err != nil {
		return fmt.Errorf("gateway API bundle: %w", err)
	}
	fmt.Fprintf(w, "→ gateway API bundle %s (%d bytes) from %s\n", art.Tag, len(raw), from)

	objs, err := k8s.ParseManifest(raw)
	if err != nil {
		return fmt.Errorf("gateway API bundle: parsing %s: %w", art.Tag, err)
	}
	if len(objs) == 0 {
		return fmt.Errorf("gateway API bundle %s parsed to no objects; nothing would be installed", art.Tag)
	}
	if err := checkBundleSurvivesTheSweep(objs); err != nil {
		return err
	}

	body, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		return fmt.Errorf("gateway API bundle: %w", err)
	}
	dc, mapper, err := k8s.DynamicAndMapperFromKubeconfigBytes(body)
	if err != nil {
		return fmt.Errorf("gateway API bundle: %w", err)
	}

	// Retry, because this is a race and not a request. The sweep deletes the
	// blocking policy every few seconds and the ingress operator puts it back;
	// an attempt that lands while the policy exists is refused for a reason that
	// has nothing to do with the manifest.
	var lastErr error
	for attempt := 1; attempt <= gatewayAPIBundleAttempts; attempt++ {
		lastErr = k8s.ApplyObjects(ctx, dc, mapper, objs, gatewayAPIBundleFieldManager, true, w)
		if lastErr == nil {
			fmt.Fprintf(w, "✓ gateway API bundle %s applied (%d objects)\n", art.Tag, len(objs))
			return nil
		}
		if ctx.Err() != nil {
			break
		}
		if attempt == 1 {
			fmt.Fprintf(w, "  ⚠ gateway API bundle apply refused (%v) — retrying while the admission-policy sweep clears the way\n", lastErr)
		}
		select {
		case <-ctx.Done():
		case <-time.After(gatewayAPIBundleBackoff):
		}
	}
	return fmt.Errorf("gateway API bundle %s could not be applied after %d attempts: %w\n"+
		"  the OpenShift ingress-operator's gateway-api admission policy blocks third-party writes to "+
		"these CRDs; the sweep deletes it, so a persistent refusal here means the sweep is not landing "+
		"(check the warnings above it)",
		art.Tag, gatewayAPIBundleAttempts, lastErr)
}

// checkBundleSurvivesTheSweep refuses to install an object the sweep running
// alongside this apply would delete again.
//
// The bundle ships its OWN ValidatingAdmissionPolicy and binding,
// "safe-upgrades.gateway.networking.k8s.io", while the sweep deletes OpenShift's
// "openshift-ingress-operator-gatewayapi-crd-admission". They are different
// objects and must stay that way. If they ever collided — the sweep widened to a
// prefix or a label, upstream renaming its policy — the sweep would delete what
// this apply had just installed, and the failure would be invisible: no error,
// no denied write, just an mTLS install whose admission policy is missing and a
// bundle that appears never to have been applied.
//
// So it is checked rather than assumed, at the moment both facts are in hand.
func checkBundleSurvivesTheSweep(objs []*unstructured.Unstructured) error {
	for _, obj := range objs {
		if admissionSweepWouldDelete(obj) {
			return fmt.Errorf(
				"gateway API bundle: it ships %s %q, which the gateway-api admission-policy sweep "+
					"running alongside this apply deletes — installing it would be undone within seconds, "+
					"and the install would look as though the bundle had never applied.\n"+
					"  the sweep targets OpenShift's own policy by exact name; if upstream has renamed "+
					"the bundle's policy onto that name, the sweep must be narrowed before this can proceed",
				obj.GetKind(), obj.GetName())
		}
	}
	return nil
}

// gatewayAPIBundleBytes resolves the bundle's content, preferring the mirror.
//
// The mirror is preferred and not merely permitted. A disconnected cluster can
// only reach the mirror, and in the CI path roksbnkctl runs as an Argo pod IN
// that cluster — its egress is the cluster's, so github.com is unreachable for
// exactly the estates that want mTLS. Reaching upstream "just this once" is how
// an air-gapped install acquires a dependency on the internet.
//
// Either way the bytes are checked against the pin before they are returned.
func gatewayAPIBundleBytes(ctx context.Context, cctx *config.Context, art bnkbom.Artifact) ([]byte, string, error) {
	rec, err := config.ReadRegistryMirror(cctx.WorkspaceName)
	switch {
	case errors.Is(err, config.ErrNoRegistryMirror):
		// No mirror: fetch upstream (or from the configured proxy).
		body, ferr := mirror.FetchAndVerifyFile(ctx, art.SourceURL, art.SHA256)
		return body, art.SourceURL, ferr
	case err != nil:
		return nil, "", err
	}
	// A record describing a registry the config has since been repointed away
	// from is refused, not quietly used — the same rule the tfvars render
	// applies, for the same reason. Pulling from a mirror nobody asked for is
	// how an install ends up reading from the previous customer's registry.
	if err := config.MirrorRecordMismatchError(cctx.WorkspaceName, cctx.Workspace, rec); err != nil {
		return nil, "", err
	}

	ref, ok := mirroredBundleRef(rec, art)
	if !ok {
		return nil, "", fmt.Errorf(
			"this workspace has a registry mirror, but it holds no %s — an mTLS install on a "+
				"disconnected cluster cannot fetch it from %s.\n"+
				"  run `roksbnkctl registry replicate` again: the bundle enters the BOM only when "+
				"bnk.gateway_api_mtls is on, so a mirror replicated before that was set does not carry it",
			art.Name, art.SourceHost)
	}
	auth, err := mirrorPullAuth(ctx, cctx)
	if err != nil {
		return nil, "", err
	}
	host, _, _ := strings.Cut(ref, "/")
	opts := mirror.PullFileOptions(ctx, host, auth, rec.CACert, false)
	body, err := mirror.PullFile(ctx, ref, bnkbom.GatewayAPIBundleFile, art.SHA256, opts...)
	return body, "the mirror (" + ref + ")", err
}

// mirroredBundleRef builds the pull reference for the bundle recorded in the
// mirror, by DIGEST when replicate recorded one.
//
// ChartHost, not ImageHost. The two differ only for a mirror whose push and pull
// endpoints split (an in-cluster registry route vs. its service); ChartHost is
// the endpoint reachable from wherever roksbnkctl itself runs, and roksbnkctl is
// what pulls this file — no kubelet is involved, because the artifact is a file
// and not an image anything schedules.
func mirroredBundleRef(rec *config.RegistryMirror, art bnkbom.Artifact) (string, bool) {
	if rec == nil {
		return "", false
	}
	base := strings.TrimSpace(rec.ChartHost)
	if base == "" {
		base = strings.TrimSpace(rec.ImageHost)
	}
	if base == "" {
		return "", false
	}
	for _, m := range rec.Artifacts {
		if m.Kind != string(bnkbom.KindFile) || m.Name != art.Name {
			continue
		}
		if m.Digest != "" {
			return base + "/" + m.Name + "@" + m.Digest, true
		}
		return base + "/" + m.Name + ":" + m.Tag, true
	}
	return "", false
}

// mirrorPullAuth resolves the credential for reading out of the mirror: the
// generic registry's basic auth, or an IBM Container Registry IAM key.
//
// The same credential `registry replicate` pushed with. It is resolved fresh
// rather than carried, because the install can run on a different host — or in a
// different pod — from the replicate.
func mirrorPullAuth(ctx context.Context, cctx *config.Context) (authn.Authenticator, error) {
	ws := cctx.Workspace
	reg := ws.Registry
	if reg != nil && strings.EqualFold(strings.TrimSpace(reg.Target), "generic") {
		if strings.TrimSpace(reg.GenericUsername) == "" {
			return authn.Anonymous, nil
		}
		pw, err := base64.StdEncoding.DecodeString(reg.GenericPasswordB64)
		if err != nil {
			return nil, fmt.Errorf("registry.generic_password_b64 is not valid base64: %w", err)
		}
		return &authn.Basic{Username: reg.GenericUsername, Password: string(pw)}, nil
	}
	// ICR (the default target) authenticates with the workspace's own IBM Cloud
	// API key under the fixed "iamapikey" username.
	apiKey, err := (&cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    ws.IBMCloud.APIKeySource,
	}).IBMCloudAPIKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving the IBM Cloud API key for the mirror pull: %w", err)
	}
	return &authn.Basic{Username: "iamapikey", Password: apiKey}, nil
}
