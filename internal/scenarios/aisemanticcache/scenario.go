// Package aisemanticcache implements scenario "ai-semantic-cache" — the F5
// BNK AI semantic model caching feature (how-to #7).
//
// This feature is ANNOTATION-DRIVEN: there is no dedicated CR. Semantic
// caching is configured through two annotations —
//
//   - the Gateway's spec.infrastructure.annotations["k8s.f5.com/ai"] value
//     (semantic_cache=enabled,...),
//   - the HTTPRoute's metadata.annotations["k8s.f5.com/sse-enabled"] value
//     (server-sent-events streaming, required for streamed completions).
//
// Because the awsbnkctl EKS / host-device cluster has no ModelCache backend,
// this scenario is rated Amber: it asserts the control plane only —
//
//   - the nginx backend becomes Available (so the HTTPRoute resolves),
//   - the Gateway programs (Programmed=True),
//   - the HTTPRoute is Accepted=True,
//   - the k8s.f5.com/ai annotation persists on the Gateway,
//   - the k8s.f5.com/sse-enabled annotation persists on the HTTPRoute.
//
// It does NOT drive data-plane traffic and does NOT assert cache hits — that
// requires a real ModelCache backend the cluster cannot provide. The
// semantic_cache_ip_port value is a placeholder (127.0.0.1:80) for the same
// reason.
package aisemanticcache

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
)

//go:embed manifests/*.yaml
var manifestFS embed.FS

const (
	scnName      = "ai-semantic-cache"
	scnTitle     = "AI semantic model caching annotations (how-to #7, Amber)"
	scnNamespace = "awsbnkctl-scn-aisemcache"
	// gatewayAIKey is the verbatim Gateway annotation key from clouddocs 2.3.
	gatewayAIKey = "k8s.f5.com/ai"
	// httpRouteSSEKey is the verbatim HTTPRoute annotation key from clouddocs 2.3.
	httpRouteSSEKey = "k8s.f5.com/sse-enabled"
)

func init() { scenarios.Register(&scenario{}) }

// VerifyDeps holds the function pointers used by Verify. The zero value routes
// every call to the real package-level implementations. Tests swap individual
// fields to a recording stub to assert call order without touching the cluster.
type VerifyDeps struct {
	WaitDeploymentAvailableFn func(ctx context.Context, sctx *scenarios.Context, ns, name string, timeout time.Duration) error
	WaitConditionFn           func(ctx context.Context, sctx *scenarios.Context, gvr schema.GroupVersionResource, ns, name, condType string, timeout time.Duration) error
	WaitHTTPRouteConditionFn  func(ctx context.Context, sctx *scenarios.Context, ns, name, condType string, timeout time.Duration) error
}

func realVerifyDeps() VerifyDeps {
	return VerifyDeps{
		WaitDeploymentAvailableFn: scenarios.WaitDeploymentAvailable,
		WaitConditionFn:           scenarios.WaitCondition,
		WaitHTTPRouteConditionFn:  scenarios.WaitHTTPRouteCondition,
	}
}

type scenario struct {
	// vDeps is nil for the registered singleton; tests inject a non-nil value.
	vDeps *VerifyDeps
}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Amber }
func (s *scenario) Dependencies() []string   { return []string{} }
func (s *scenario) Description() string {
	return strings.TrimSpace(`
AI semantic model caching scenario (how-to #7).

The feature is annotation-driven — no dedicated CR. Semantic caching is
configured by two annotations: the Gateway
spec.infrastructure.annotations["k8s.f5.com/ai"] value
(semantic_cache=enabled,...) and the HTTPRoute
metadata.annotations["k8s.f5.com/sse-enabled"] value.

Applies 5 templated manifests into the scenario namespace:
  Namespace, F5BnkGateway IP pool (single-address, VIP only),
  one nginx Deployment+Service (so the HTTPRoute resolves),
  Gateway (spec.addresses=[VIP] + the k8s.f5.com/ai annotation under
  spec.infrastructure.annotations), HTTPRoute (host=
  awsbnkctl-aisemcache.local → nginx:80, with the k8s.f5.com/sse-enabled
  metadata annotation).

Verify (control-plane only):
  1. Wait nginx Deployment Available.
  2. Wait Gateway scn-aisemcache-gateway Programmed=True.
  3. Wait HTTPRoute scn-aisemcache-route Accepted=True.
  4. Assert the Gateway still carries the k8s.f5.com/ai annotation.
  5. Assert the HTTPRoute still carries the k8s.f5.com/sse-enabled
     annotation.

Rating Amber: no ModelCache backend on the awsbnkctl EKS / host-device
cluster, so cache hits are NOT data-plane tested. The
semantic_cache_ip_port value is a placeholder (127.0.0.1:80) — set the
real ModelCache address before any live verification. There is no curl
probe.

Cleanup: delete the scenario namespace (idempotent).
`)
}

// manifestVars holds the template variables for the 5 manifests.
type manifestVars struct {
	Namespace        string
	ClusterName      string
	GatewayClassName string
	VIP              string
	ExternalCIDR     string
}

func (s *scenario) Manifests(ctx *scenarios.Context) ([]string, error) {
	v, err := buildManifestVars(ctx)
	if err != nil {
		return nil, err
	}

	var paths []string
	err = fs.WalkDir(manifestFS, "manifests", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		tmplBytes, e := manifestFS.ReadFile(p)
		if e != nil {
			return e
		}
		rendered, e := scenarios.RenderTemplate(string(tmplBytes), v)
		if e != nil {
			return fmt.Errorf("rendering %s: %w", p, e)
		}
		base := p[len("manifests/"):]
		out, e := scenarios.WriteManifest(ctx.WorkspaceDir, scnName, base, rendered)
		if e != nil {
			return e
		}
		paths = append(paths, out)
		return nil
	})
	return paths, err
}

func (s *scenario) Apply(ctx *scenarios.Context) error {
	return scenarios.ApplyManifests(ctx, scnName)
}

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	d := s.vDeps
	if d == nil {
		real := realVerifyDeps()
		d = &real
	}
	ns := namespace(ctx)
	res := scenarios.Result{
		Details: "Amber: control-plane only — semantic cache hits are not data-plane tested (no ModelCache backend). semantic_cache_ip_port is a placeholder. No curl probe.",
	}

	// --- Control-plane assertions (no data-plane probe) ---

	// nginx Deployment Available (so the HTTPRoute can resolve its backend).
	err := d.WaitDeploymentAvailableFn(ctx.Ctx, ctx, ns, "nginx", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "nginx Deployment Available",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// Gateway Programmed=True.
	err = d.WaitConditionFn(ctx.Ctx, ctx, scenarios.GatewayGVR, ns, "scn-aisemcache-gateway", "Programmed", 5*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "Gateway scn-aisemcache-gateway Programmed=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// HTTPRoute Accepted=True.
	err = d.WaitHTTPRouteConditionFn(ctx.Ctx, ctx, ns, "scn-aisemcache-route", "Accepted", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute scn-aisemcache-route Accepted=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// Gateway k8s.f5.com/ai annotation persists (spec.infrastructure.annotations).
	gwOK, gwGot := annotationPresent(ctx, scenarios.GatewayGVR, ns, "scn-aisemcache-gateway", gatewayAIKey,
		"spec", "infrastructure", "annotations")
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "Gateway retains k8s.f5.com/ai annotation",
		OK:          gwOK,
		Got:         gwGot,
	})

	// HTTPRoute k8s.f5.com/sse-enabled annotation persists (metadata.annotations).
	rtOK, rtGot := annotationPresent(ctx, scenarios.HTTPRouteGVR, ns, "scn-aisemcache-route", httpRouteSSEKey,
		"metadata", "annotations")
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute retains k8s.f5.com/sse-enabled annotation",
		OK:          rtOK,
		Got:         rtGot,
	})

	return scenarios.FinalizeResult(res)
}

// annotationPresent reads the resource via the dynamic client and reports
// whether the annotation key under the given field path exists and is
// non-empty. fieldPath is the nested map path holding the annotations map
// (e.g. {"spec","infrastructure","annotations"} or {"metadata","annotations"}).
func annotationPresent(ctx *scenarios.Context, gvr schema.GroupVersionResource, ns, name, key string, fieldPath ...string) (bool, string) {
	obj, err := ctx.Dynamic.Resource(gvr).Namespace(ns).Get(ctx.Ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, scenarios.ErrString(err)
	}
	lookup := append(append([]string{}, fieldPath...), key)
	val, found, err := unstructured.NestedString(obj.Object, lookup...)
	if err != nil {
		return false, scenarios.ErrString(err)
	}
	if !found || strings.TrimSpace(val) == "" {
		return false, fmt.Sprintf("annotation %q missing or empty under %s", key, strings.Join(fieldPath, "."))
	}
	return true, "present"
}

func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	ns := namespace(ctx)
	err := ctx.Clientset.CoreV1().Namespaces().Delete(ctx.Ctx, ns, metav1.DeleteOptions{})
	if err != nil && !scenarios.IsNotFound(err) {
		return fmt.Errorf("deleting namespace %s: %w", ns, err)
	}
	return nil
}

func (s *scenario) Namespace(ctx *scenarios.Context) string { return namespace(ctx) }

// --- internal helpers ---

func namespace(ctx *scenarios.Context) string {
	if v := ctx.Options["namespace"]; v != "" {
		return v
	}
	return scnNamespace
}

// withLastOctet returns ip with its last dotted-quad octet replaced by octet.
// If ip is not a valid dotted-quad, it is returned unchanged.
func withLastOctet(ip, octet string) string {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return ip
	}
	parts[3] = octet
	return strings.Join(parts, ".")
}

func buildManifestVars(ctx *scenarios.Context) (manifestVars, error) {
	var v manifestVars
	v.Namespace = namespace(ctx)
	if ctx.Cluster != nil {
		v.ClusterName = ctx.Cluster.Metadata.Name
		v.GatewayClassName = ctx.Cluster.Metadata.Name + "-gatewayclass"
		if ctx.Cluster.Network.DataPath != nil {
			v.ExternalCIDR = ctx.Cluster.Network.DataPath.External.CIDR
		}
	}
	vip := ctx.Options["vip"]
	if vip == "" && ctx.Cluster != nil {
		var err error
		vip, err = ctx.Cluster.DefaultVIP()
		if err != nil {
			return v, fmt.Errorf("deriving VIP: %w", err)
		}
	}
	if vip == "" {
		return v, fmt.Errorf("VIP not derivable — set network.dataPath.external.cidr in cluster.yaml or pass --vip")
	}
	// Use .105 to avoid colliding with other scenarios' pools.
	v.VIP = withLastOctet(vip, strconv.Itoa(105))
	return v, nil
}
