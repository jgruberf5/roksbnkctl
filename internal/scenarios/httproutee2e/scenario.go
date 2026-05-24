// Package httproutee2e implements scenario "http-routing-e2e" — the
// full data-plane version of how-to #8 (HTTP traffic steering with
// Gateway API HTTPRoute).
//
// AWS-specific shape:
//   - GatewayClass is provisioned by Phase 23b (<cluster>-gatewayclass).
//   - F5BnkGateway IP pool is owned by the scenario (02-f5bnkgateway.yaml)
//     because no lifecycle phase provisions it cluster-wide; the scenario
//     provisions a namespaced pool so it can be self-contained and cleaned up.
//   - Verification curls through SSH+EICE from the slice-12 jumphost's
//     BNK_EXT ENI (JUMPHOST_BNK_EXT_ENI_IP in state.env), not from an
//     in-cluster pod, so traffic exercises the real data path.
//
// Verify order (load-bearing):
//  1. Wait for control-plane conditions (Deployment Available, Gateway
//     Programmed=True, HTTPRoute Accepted+ResolvedRefs=True).
//  2. Call pkg/bnk.ResyncHTTPRoutes — idempotent pool-member workaround.
//  3. Run SSH+EICE curl probes via internal/jumphost.
//  4. Assert 5/5 HTTP 200 + body contains marker.
package httproutee2e

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"text/template"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
	k8sapply "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	"github.com/JLCode-tech/awsbnkctl/pkg/bnk"
)

//go:embed manifests/*.yaml
var manifestFS embed.FS

const (
	scnName      = "http-routing-e2e"
	scnTitle     = "HTTP traffic steering with Gateway API HTTPRoute (how-to #8)"
	scnNamespace = "awsbnkctl-scn-httproute-e2e"
)

func init() { scenarios.Register(&scenario{}) }

// VerifyDeps holds the function pointers used by Verify. The zero value routes
// every call to the real package-level implementations. Tests swap individual
// fields to a recording stub to assert call order without touching the cluster.
type VerifyDeps struct {
	WaitDeploymentAvailableFn func(ctx context.Context, sctx *scenarios.Context, ns, name string, timeout time.Duration) error
	WaitConditionFn           func(ctx context.Context, sctx *scenarios.Context, gvr schema.GroupVersionResource, ns, name, condType string, timeout time.Duration) error
	WaitHTTPRouteConditionFn  func(ctx context.Context, sctx *scenarios.Context, ns, name, condType string, timeout time.Duration) error
	ResyncHTTPRoutesFn        func(ctx context.Context, sctx *scenarios.Context, ns string) error
	RunCurlProbesFn           func(ctx context.Context, sctx *scenarios.Context, vip string, iterations int, timeout time.Duration) (bool, string)
}

func realVerifyDeps() VerifyDeps {
	return VerifyDeps{
		WaitDeploymentAvailableFn: waitDeploymentAvailable,
		WaitConditionFn:           waitCondition,
		WaitHTTPRouteConditionFn:  waitHTTPRouteCondition,
		ResyncHTTPRoutesFn: func(ctx context.Context, sctx *scenarios.Context, ns string) error {
			_, err := bnk.ResyncHTTPRoutes(ctx, sctx.Dynamic, bnk.ResyncOptions{
				Namespace:      ns,
				AllInNamespace: true,
			})
			return err
		},
		RunCurlProbesFn: func(_ context.Context, sctx *scenarios.Context, vip string, iterations int, timeout time.Duration) (bool, string) {
			instanceID := sctx.State.Get("JUMPHOST_INSTANCE_ID")
			sourceIP := sctx.State.Get("JUMPHOST_BNK_EXT_ENI_IP")
			probeOpts := jumphost.ProbeOptions{
				Region:     sctx.Cluster.Metadata.Region,
				InstanceID: instanceID,
				SourceIP:   sourceIP,
				VIP:        vip,
				Iterations: iterations,
				Timeout:    timeout,
			}
			probes, probeRunErr := jumphost.RunCurlProbes(sctx.Ctx, probeOpts)
			successCount := 0
			var lastErrStr string
			for _, p := range probes {
				if p.HTTPCode == 200 && p.Err == "" {
					successCount++
				} else if p.Err != "" {
					lastErrStr = p.Err
				}
			}
			curlOK := probeRunErr == nil && successCount == iterations
			got := fmt.Sprintf("%d/%d curls returned HTTP 200", successCount, iterations)
			if probeRunErr != nil {
				got += " — probe error: " + probeRunErr.Error()
			} else if !curlOK && lastErrStr != "" {
				got += " — last error: " + lastErrStr
			}
			return curlOK, got
		},
	}
}

type scenario struct {
	// vDeps is nil for the registered singleton; tests inject a non-nil value.
	vDeps *VerifyDeps
}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Green }
func (s *scenario) Dependencies() []string   { return []string{} }
func (s *scenario) Description() string {
	return strings.TrimSpace(`
End-to-end HTTPRoute scenario with real data-plane traffic via AWS EICE jumphost.

Applies 5 templated manifests into the scenario namespace:
  Namespace, F5BnkGateway IP pool, nginx Deployment+Service,
  Gateway (spec.addresses=[VIP]), HTTPRoute (host=awsbnkctl.local → nginx).

Verify order (load-bearing):
  1. Wait for control-plane conditions: nginx Available, Gateway Programmed=True,
     HTTPRoute Accepted=True + ResolvedRefs=True.
  2. Call pkg/bnk.ResyncHTTPRoutes — idempotent pool-member workaround for
     cne-controller stale pool-member bug (project_pool_member_sync_root_cause).
  3. Open SSH via EICE to the jumphost and curl --interface <BNK_EXT_ENI_IP>
     http://<VIP>/ N times (default 5).
  4. Assert: N/N HTTP 200.

Cleanup: delete the scenario namespace (idempotent).
Requires: testing.jumphost.enabled=true in cluster.yaml + awsbnkctl up first.
`)
}

// manifestVars holds the template variables for the 5 manifests.
type manifestVars struct {
	Namespace        string
	ClusterName      string
	GatewayClassName string
	VIP              string
	VIPRangeEnd      string
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
		rendered, e := renderTemplate(string(tmplBytes), v)
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
	scenarioDir, err := scenarios.EnsureScenarioDir(ctx.WorkspaceDir, scnName)
	if err != nil {
		return fmt.Errorf("ensuring scenario dir: %w", err)
	}
	// Use SSA + live RESTMapper — NOT applyRawYAML or the static GVR map
	// which silently skips Gateway/HTTPRoute/F5BnkGateway (phase23b_gvr_bug).
	ao := &k8sapply.ApplyOptions{
		Filename:       scenarioDir,
		KubeconfigPath: ctx.KubeconfigPath,
	}
	return ao.Run(ctx.Ctx)
}

var (
	gatewayGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}
	httpRouteGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}
	f5BnkGatewayGVR = schema.GroupVersionResource{
		Group:    "k8s.f5net.com",
		Version:  "v1",
		Resource: "f5bnkgateways",
	}
)

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	d := s.vDeps
	if d == nil {
		real := realVerifyDeps()
		d = &real
	}
	ns := namespace(ctx)
	res := scenarios.Result{}

	// --- Step 1: Control-plane assertions ---
	// Order is load-bearing: control-plane must be settled before ResyncHTTPRoutes.

	// nginx Deployment Available.
	err := d.WaitDeploymentAvailableFn(ctx.Ctx, ctx, ns, "nginx", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "nginx Deployment Available",
		OK:          err == nil,
		Got:         errString(err),
	})

	// Gateway Programmed=True.
	err = d.WaitConditionFn(ctx.Ctx, ctx, gatewayGVR, ns, "scn-gateway", "Programmed", 5*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "Gateway scn-gateway Programmed=True",
		OK:          err == nil,
		Got:         errString(err),
	})

	// HTTPRoute Accepted=True.
	err = d.WaitHTTPRouteConditionFn(ctx.Ctx, ctx, ns, "scn-route", "Accepted", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute scn-route Accepted=True",
		OK:          err == nil,
		Got:         errString(err),
	})

	// HTTPRoute ResolvedRefs=True.
	err = d.WaitHTTPRouteConditionFn(ctx.Ctx, ctx, ns, "scn-route", "ResolvedRefs", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute scn-route ResolvedRefs=True",
		OK:          err == nil,
		Got:         errString(err),
	})

	// Best-effort: F5BnkGateway present (skipped when Dynamic client is nil).
	if ctx.Dynamic != nil {
		_, ferr := ctx.Dynamic.Resource(f5BnkGatewayGVR).Namespace(ns).Get(ctx.Ctx, "awsbnkctl-default", metav1.GetOptions{})
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "F5BnkGateway awsbnkctl-default present",
			OK:          ferr == nil,
			Got:         errString(ferr),
		})
	}

	// --- Step 2: ResyncHTTPRoutes (after control-plane is ready) ---
	// Idempotent workaround for cne-controller pool-member stale bug.
	// ResyncHTTPRoutes MUST run after HTTPRoute conditions are settled; moving
	// it before the condition waits regresses the pool-member fix.
	resyncErr := d.ResyncHTTPRoutesFn(ctx.Ctx, ctx, ns)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "ResyncHTTPRoutes (pool-member refresh)",
		OK:          resyncErr == nil,
		Got:         errString(resyncErr),
	})

	// --- Step 3: SSH+EICE curl probes ---
	vip, iterations, timeout, probeErr := buildProbeParams(ctx)
	if probeErr != nil {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "jumphost probe setup",
			OK:          false,
			Got:         probeErr.Error(),
		})
		return finalizeResult(res)
	}

	instanceID := ctx.State.Get("JUMPHOST_INSTANCE_ID")
	sourceIP := ctx.State.Get("JUMPHOST_BNK_EXT_ENI_IP")
	if instanceID == "" || sourceIP == "" {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "jumphost state keys present",
			OK:          false,
			Got:         "JUMPHOST_INSTANCE_ID / JUMPHOST_BNK_EXT_ENI_IP missing from state.env — run `awsbnkctl up` with testing.jumphost.enabled=true",
		})
		return finalizeResult(res)
	}

	curlOK, got := d.RunCurlProbesFn(ctx.Ctx, ctx, vip, iterations, timeout)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("%d/%d end-to-end curls via Gateway return HTTP 200", iterations, iterations),
		OK:          curlOK,
		Got:         got,
	})

	return finalizeResult(res)
}

func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	ns := namespace(ctx)
	err := ctx.Clientset.CoreV1().Namespaces().Delete(ctx.Ctx, ns, metav1.DeleteOptions{})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("deleting namespace %s: %w", ns, err)
	}
	return nil
}

// --- internal helpers ---

func namespace(ctx *scenarios.Context) string {
	if v := ctx.Options["namespace"]; v != "" {
		return v
	}
	return scnNamespace
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
	v.VIP = vip
	v.VIPRangeEnd = vipPlus100(vip)
	return v, nil
}

// vipPlus100 increments the last octet by 100 (capped at 254).
func vipPlus100(vip string) string {
	parts := strings.Split(vip, ".")
	if len(parts) != 4 {
		return vip
	}
	last, err := strconv.Atoi(parts[3])
	if err != nil {
		return vip
	}
	end := last + 100
	if end > 254 {
		end = 254
	}
	parts[3] = strconv.Itoa(end)
	return strings.Join(parts, ".")
}

func renderTemplate(tmpl string, data interface{}) (string, error) {
	t, err := template.New("manifest").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// waitDeploymentAvailable polls until the named Deployment has the Available
// condition set to True, or timeout elapses.
func waitDeploymentAvailable(ctx context.Context, sctx *scenarios.Context, ns, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dep, err := sctx.Clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			for _, c := range dep.Status.Conditions {
				if string(c.Type) == "Available" && string(c.Status) == "True" {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("deployment %s/%s not Available after %s", ns, name, timeout)
}

// waitCondition polls a Gateway (or any GVR-based resource) for a named
// condition status=True in .status.conditions.
func waitCondition(ctx context.Context, sctx *scenarios.Context, gvr schema.GroupVersionResource, ns, name, condType string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, err := sctx.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			conditions, _, _ := scenarios.NestedSlice(obj.Object, "status", "conditions")
			for _, cRaw := range conditions {
				c, ok := cRaw.(map[string]interface{})
				if !ok {
					continue
				}
				if c["type"] == condType && c["status"] == "True" {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("%s %s/%s condition %s not True after %s", gvr.Resource, ns, name, condType, timeout)
}

// waitHTTPRouteCondition polls the HTTPRoute's .status.parents[*].conditions
// for the named condition with status=True.
func waitHTTPRouteCondition(ctx context.Context, sctx *scenarios.Context, ns, name, condType string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, err := sctx.Dynamic.Resource(httpRouteGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			parents, _, _ := scenarios.NestedSlice(obj.Object, "status", "parents")
			for _, pRaw := range parents {
				p, ok := pRaw.(map[string]interface{})
				if !ok {
					continue
				}
				conditions, _, _ := scenarios.NestedSlice(p, "conditions")
				for _, cRaw := range conditions {
					c, ok2 := cRaw.(map[string]interface{})
					if !ok2 {
						continue
					}
					if c["type"] == condType && c["status"] == "True" {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("HTTPRoute %s/%s condition %s not True after %s", ns, name, condType, timeout)
}

func buildProbeParams(ctx *scenarios.Context) (vip string, iterations int, timeout time.Duration, err error) {
	vip = ctx.Options["vip"]
	if vip == "" && ctx.Cluster != nil {
		vip, err = ctx.Cluster.DefaultVIP()
		if err != nil {
			return "", 0, 0, fmt.Errorf("deriving VIP: %w", err)
		}
	}
	if vip == "" {
		return "", 0, 0, fmt.Errorf("VIP not set — pass --vip or set network.dataPath.external.cidr")
	}
	iterations = 5
	if v := ctx.Options["iterations"]; v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			iterations = n
		}
	}
	timeout = 10 * time.Second
	if v := ctx.Options["timeout"]; v != "" {
		if d, e := time.ParseDuration(v); e == nil && d > 0 {
			timeout = d
		}
	}
	return vip, iterations, timeout, nil
}

func finalizeResult(res scenarios.Result) scenarios.Result {
	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = "control-plane reconciled + end-to-end curls via Gateway returned HTTP 200"
	} else {
		res.Status = "failed"
		var failed []string
		for _, a := range res.Assertions {
			if !a.OK {
				failed = append(failed, a.Description)
			}
		}
		res.Summary = "failed: " + strings.Join(failed, "; ")
	}
	return res
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return oneLine(err.Error(), 200)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found")
}
