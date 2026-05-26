// Package aitokencounting implements scenario "ai-token-counting" — the F5
// BNK AI token counting + enforcement feature (how-to #6).
//
// This feature is ANNOTATION-DRIVEN: there is no dedicated CR. Token
// counting + per-user enforcement is configured entirely via the
// `k8s.f5.com/ai-token-counting` annotation on the Gateway's
// spec.infrastructure.annotations. Because the awsbnkctl EKS / host-device
// cluster has no LLM backend to meter, this scenario is rated Amber: it
// asserts the control plane only —
//
//   - the nginx backend becomes Available (so the HTTPRoute resolves),
//   - the Gateway programs (Programmed=True),
//   - the HTTPRoute is Accepted=True,
//   - the k8s.f5.com/ai-token-counting annotation persists on the Gateway.
//
// It does NOT drive data-plane traffic and does NOT assert token metering /
// rate-limit enforcement — that requires a real LLM backend the cluster
// cannot provide.
package aitokencounting

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
	scnName      = "ai-token-counting"
	scnTitle     = "AI token counting + enforcement annotation (how-to #6, Amber)"
	scnNamespace = "awsbnkctl-scn-aitokens"
	// aiAnnotationKey is the verbatim Gateway annotation key from clouddocs 2.3.
	aiAnnotationKey = "k8s.f5.com/ai-token-counting"
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
AI token counting + enforcement scenario (how-to #6).

The feature is annotation-driven — no dedicated CR. Token counting and
per-user rate-limit enforcement are configured through the Gateway
spec.infrastructure.annotations["k8s.f5.com/ai-token-counting"] value.

Applies 5 templated manifests into the scenario namespace:
  Namespace, F5BnkGateway IP pool (single-address, VIP only),
  one nginx Deployment+Service (so the HTTPRoute resolves),
  Gateway (spec.addresses=[VIP] + the k8s.f5.com/ai-token-counting
  annotation under spec.infrastructure.annotations), HTTPRoute
  (host=awsbnkctl-aitokens.local → nginx:80).

Verify (control-plane only):
  1. Wait nginx Deployment Available.
  2. Wait Gateway scn-aitokens-gateway Programmed=True.
  3. Wait HTTPRoute scn-aitokens-route Accepted=True.
  4. Assert the Gateway still carries the k8s.f5.com/ai-token-counting
     annotation (read-back).

Rating Amber: enforcement/metering is NOT data-plane tested — the
awsbnkctl EKS / host-device cluster has no LLM backend to meter. There
is no curl probe.

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
		Details: "Amber: control-plane only — token metering / rate-limit enforcement is not data-plane tested (no LLM backend). No curl probe.",
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
	err = d.WaitConditionFn(ctx.Ctx, ctx, scenarios.GatewayGVR, ns, "scn-aitokens-gateway", "Programmed", 5*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "Gateway scn-aitokens-gateway Programmed=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// HTTPRoute Accepted=True.
	err = d.WaitHTTPRouteConditionFn(ctx.Ctx, ctx, ns, "scn-aitokens-route", "Accepted", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute scn-aitokens-route Accepted=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// Annotation persists: read the Gateway back and confirm the
	// k8s.f5.com/ai-token-counting annotation is present + non-empty.
	annOK, annGot := s.gatewayAnnotationPresent(ctx, ns, "scn-aitokens-gateway", aiAnnotationKey)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "Gateway retains k8s.f5.com/ai-token-counting annotation",
		OK:          annOK,
		Got:         annGot,
	})

	return scenarios.FinalizeResult(res)
}

// gatewayAnnotationPresent reads the Gateway via the dynamic client and reports
// whether spec.infrastructure.annotations[key] exists and is non-empty.
func (s *scenario) gatewayAnnotationPresent(ctx *scenarios.Context, ns, name, key string) (bool, string) {
	obj, err := ctx.Dynamic.Resource(scenarios.GatewayGVR).Namespace(ns).Get(ctx.Ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, scenarios.ErrString(err)
	}
	val, found, err := unstructured.NestedString(obj.Object, "spec", "infrastructure", "annotations", key)
	if err != nil {
		return false, scenarios.ErrString(err)
	}
	if !found || strings.TrimSpace(val) == "" {
		return false, fmt.Sprintf("annotation %q missing or empty on spec.infrastructure.annotations", key)
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
	// Use .104 to avoid colliding with other scenarios' pools.
	v.VIP = withLastOctet(vip, strconv.Itoa(104))
	return v, nil
}
