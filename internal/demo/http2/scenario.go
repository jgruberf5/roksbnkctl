// Package http2 implements demo use-case "http2" — a curated walkthrough that
// proves BNK's TMM proxies HTTP/2 (h2c) end-to-end: client→TMM leg uses
// HTTP/2 prior-knowledge; TMM→backend leg is also HTTP/2 because the backend
// Service carries appProtocol: kubernetes.io/h2c.
//
// Verify order (load-bearing):
//  1. Wait for control-plane conditions: h2c-backend Deployment Available,
//     Gateway http2-gateway Programmed=True, HTTPRoute http2-route Accepted=True,
//     HTTPRoute http2-route ResolvedRefs=True.
//  2. Best-effort F5BnkGateway get (demo-http2 resource).
//  3. Call pkg/bnk.ResyncHTTPRoutes — idempotent pool-member workaround for
//     cne-controller stale-pool bug (project_pool_member_sync_root_cause).
//     MUST run after control-plane is settled; moving it before the condition
//     waits regresses the pool-member fix.
//  4. SSH+EICE curl probes via internal/jumphost.RunStagingCommands.
//  5. Assert: 5/5 HTTP/2 200 (client→TMM wire HTTP/2) + body contains "HTTP/2.0"
//     (TMM→backend leg is HTTP/2).
package http2

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	"github.com/JLCode-tech/awsbnkctl/pkg/bnk"
)

const (
	scnName      = "http2"
	scnTitle     = "HTTP/2 (h2c) end-to-end through BNK TMM — both legs"
	scnNamespace = "demo-http2"
	// scnHostname must match the hostnames: value in manifests/05-httproute.yaml.
	scnHostname = "http2.awsbnkctl.local"
	// scnBackendMarker is the literal substring the nginx backend echoes for $server_protocol
	// when TMM→backend is HTTP/2. D3: the ".0" is load-bearing (nginx $server_protocol = "HTTP/2.0").
	scnBackendMarker = "HTTP/2.0"
	// scnVIP is the http2 demo's dedicated VIP (avoids colliding with scenario-suite VIPs — see docs/demo/http2/README.md).
	scnVIP = "10.0.10.111"
)

func init() { demo.Register(&scenario{}) }

// VerifyDeps holds the function pointers used by Verify. The zero value routes
// every call to the real package-level implementations. Tests swap individual
// fields to a recording stub to assert call order without touching the cluster.
type VerifyDeps struct {
	WaitDeploymentAvailableFn func(ctx context.Context, sctx *scenarios.Context, ns, name string, timeout time.Duration) error
	WaitConditionFn           func(ctx context.Context, sctx *scenarios.Context, gvr schema.GroupVersionResource, ns, name, condType string, timeout time.Duration) error
	WaitHTTPRouteConditionFn  func(ctx context.Context, sctx *scenarios.Context, ns, name, condType string, timeout time.Duration) error
	ResyncHTTPRoutesFn        func(ctx context.Context, sctx *scenarios.Context, ns string) error
	RunHTTP2ProbesFn          func(ctx context.Context, sctx *scenarios.Context, vip string, iterations int, timeout time.Duration) (clientOK bool, backendOK bool, got string)
}

func realVerifyDeps() VerifyDeps {
	return VerifyDeps{
		WaitDeploymentAvailableFn: scenarios.WaitDeploymentAvailable,
		WaitConditionFn:           scenarios.WaitCondition,
		WaitHTTPRouteConditionFn:  scenarios.WaitHTTPRouteCondition,
		ResyncHTTPRoutesFn: func(ctx context.Context, sctx *scenarios.Context, ns string) error {
			_, err := bnk.ResyncHTTPRoutes(ctx, sctx.Dynamic, bnk.ResyncOptions{
				Namespace:      ns,
				AllInNamespace: true,
			})
			return err
		},
		RunHTTP2ProbesFn: runHTTP2Probes,
	}
}

// runHTTP2Probes issues both HTTP/2 probes in a single RunStagingCommands call
// (one EICE key-mint+push window). Probe A checks client→TMM wire HTTP/2 (N
// iterations); Probe B checks TMM→backend leg (single body fetch).
func runHTTP2Probes(ctx context.Context, sctx *scenarios.Context, vip string, iterations int, timeout time.Duration) (clientOK bool, backendOK bool, got string) {
	sourceIP := sctx.State.Get("JUMPHOST_BNK_EXT_ENI_IP")
	timeoutSecs := int(timeout.Seconds())

	// Probe A: N iterations of --http2-prior-knowledge with %{http_code} %{http_version}.
	// Build the command as a loop so the single RunStagingCommands entry returns N lines.
	probeACmd := buildHTTP2StatusCmd(sourceIP, vip, scnHostname, timeoutSecs, iterations)
	// Probe B: single body fetch asserting the backend reports HTTP/2.0.
	probeBCmd := buildHTTP2BodyCmd(sourceIP, vip, scnHostname, timeoutSecs)

	probeOpts := jumphost.ProbeOptions{
		Region:     sctx.Cluster.Metadata.Region,
		InstanceID: sctx.State.Get("JUMPHOST_INSTANCE_ID"),
		SourceIP:   sourceIP,
		VIP:        vip,
		Iterations: iterations,
		Timeout:    timeout,
		Hostname:   scnHostname,
	}

	out, err := jumphost.RunStagingCommands(ctx, probeOpts, []string{probeACmd, probeBCmd})
	if err != nil {
		partial := ""
		if len(out) > 0 {
			partial = " (partial: " + strings.Join(out, "; ") + ")"
		}
		return false, false, "probe error: " + err.Error() + partial
	}

	// Parse Probe A: out[0] contains N lines of "<code> <version>".
	probeAOut := ""
	if len(out) > 0 {
		probeAOut = out[0]
	}
	successCount := 0
	lines := strings.Split(strings.TrimSpace(probeAOut), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[0] == "200" && parts[1] == "2" {
			successCount++
		}
	}
	clientOK = successCount == iterations
	got = fmt.Sprintf("%d/%d HTTP/2 probes returned HTTP 200 on the wire (client→TMM HTTP/2)", successCount, iterations)

	// Parse Probe B: out[1] is the response body.
	probeBOut := ""
	if len(out) > 1 {
		probeBOut = out[1]
	}
	backendOK = strings.Contains(probeBOut, scnBackendMarker)
	if !backendOK {
		got += fmt.Sprintf("; backend body did not contain %q (got: %q)", scnBackendMarker, strings.TrimSpace(probeBOut))
	}
	return clientOK, backendOK, got
}

// buildHTTP2StatusCmd returns a remote shell command that runs N iterations of
// curl --http2-prior-knowledge, writing one "<http_code> <http_version>" line
// per iteration to stdout. Uses a for-loop so a single RunStagingCommands entry
// returns N newline-separated lines.
func buildHTTP2StatusCmd(sourceIP, vip, host string, timeoutSecs, iterations int) string {
	hostHdr := ""
	if host != "" {
		hostHdr = fmt.Sprintf(`-H 'Host: %s' `, host)
	}
	// seq 1 N drives N iterations; %{http_version} returns "2" for HTTP/2.
	return fmt.Sprintf(
		`for i in $(seq 1 %d); do curl --http2-prior-knowledge -s -o /dev/null -w '%%{http_code} %%{http_version}\n' %s--interface %s --max-time %d http://%s/; done`,
		iterations, hostHdr, sourceIP, timeoutSecs, vip,
	)
}

// buildHTTP2BodyCmd returns a remote shell command that captures the full
// response body with --http2-prior-knowledge. The nginx backend echoes
// $server_protocol; Probe B asserts the body contains "HTTP/2.0".
func buildHTTP2BodyCmd(sourceIP, vip, host string, timeoutSecs int) string {
	hostHdr := ""
	if host != "" {
		hostHdr = fmt.Sprintf(`-H 'Host: %s' `, host)
	}
	return fmt.Sprintf(
		`curl --http2-prior-knowledge -s %s--interface %s --max-time %d http://%s/`,
		hostHdr, sourceIP, timeoutSecs, vip,
	)
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
HTTP/2 (h2c) end-to-end through BNK — both legs are HTTP/2.

Applies 5 templated manifests into the demo namespace:
  Namespace, F5BnkGateway IP pool, nginx Deployment+Service (appProtocol h2c),
  Gateway (spec.addresses=[VIP]), HTTPRoute (host=http2.awsbnkctl.local → nginx).

Verify order (load-bearing):
  1. Wait for control-plane conditions: h2c-backend Available, Gateway Programmed=True,
     HTTPRoute Accepted=True + ResolvedRefs=True.
  2. Best-effort F5BnkGateway demo-http2 check.
  3. Call pkg/bnk.ResyncHTTPRoutes — idempotent pool-member workaround for
     cne-controller stale pool-member bug (project_pool_member_sync_root_cause).
  4. SSH+EICE curl --http2-prior-knowledge from the jumphost's BNK_EXT ENI.
  5. Assert: 5/5 HTTP/2 200 (client→TMM wire HTTP/2) + body contains "HTTP/2.0"
     (TMM→backend leg is also HTTP/2).

Cleanup: delete the demo namespace (idempotent).
Requires: --demo cluster (awsbnkctl up --demo).
`)
}

// manifestVars holds the template variables for the 5 manifests.
type manifestVars struct {
	Namespace        string
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

var f5BnkGatewayGVR = schema.GroupVersionResource{
	Group:    "k8s.f5net.com",
	Version:  "v1",
	Resource: "f5-bnkgateways",
}

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	d := s.vDeps
	if d == nil {
		real := realVerifyDeps()
		d = &real
	}
	ns := namespace(ctx)
	res := scenarios.Result{}

	// --- Step 1: Control-plane assertions ---
	// Order is load-bearing: control-plane must settle before ResyncHTTPRoutes.

	// h2c-backend Deployment Available.
	err := d.WaitDeploymentAvailableFn(ctx.Ctx, ctx, ns, "h2c-backend", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "h2c-backend Deployment Available",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// Gateway Programmed=True.
	err = d.WaitConditionFn(ctx.Ctx, ctx, scenarios.GatewayGVR, ns, "http2-gateway", "Programmed", 5*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "Gateway http2-gateway Programmed=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// HTTPRoute Accepted=True.
	err = d.WaitHTTPRouteConditionFn(ctx.Ctx, ctx, ns, "http2-route", "Accepted", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute http2-route Accepted=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// HTTPRoute ResolvedRefs=True.
	err = d.WaitHTTPRouteConditionFn(ctx.Ctx, ctx, ns, "http2-route", "ResolvedRefs", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute http2-route ResolvedRefs=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// Best-effort: F5BnkGateway present (skipped when Dynamic client is nil).
	if ctx.Dynamic != nil {
		_, ferr := ctx.Dynamic.Resource(f5BnkGatewayGVR).Namespace(ns).Get(ctx.Ctx, "demo-http2", metav1.GetOptions{})
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "F5BnkGateway demo-http2 present",
			OK:          ferr == nil,
			Got:         scenarios.ErrString(ferr),
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
		Got:         scenarios.ErrString(resyncErr),
	})

	// --- Step 3: SSH+EICE HTTP/2 probes ---
	// Call BuildProbeParams for iterations + timeout only; its vip return is
	// intentionally discarded — each demo owns a dedicated VIP and resolveVIP
	// is the canonical source (avoids colliding with scenario-suite VIPs at .100-.103).
	_, iterations, timeout, probeErr := scenarios.BuildProbeParams(ctx)
	if probeErr != nil {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "jumphost probe setup",
			OK:          false,
			Got:         probeErr.Error(),
		})
		return scenarios.FinalizeResult(res)
	}
	vip := resolveVIP(ctx)

	instanceID := ctx.State.Get("JUMPHOST_INSTANCE_ID")
	sourceIP := ctx.State.Get("JUMPHOST_BNK_EXT_ENI_IP")
	if instanceID == "" || sourceIP == "" {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "jumphost state keys present",
			OK:          false,
			Got:         "JUMPHOST_INSTANCE_ID / JUMPHOST_BNK_EXT_ENI_IP missing from state.env — run `awsbnkctl up` with testing.jumphost.enabled=true",
		})
		return scenarios.FinalizeResult(res)
	}

	clientOK, backendOK, got := d.RunHTTP2ProbesFn(ctx.Ctx, ctx, vip, iterations, timeout)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("%d/%d HTTP/2 probes returned HTTP 200 on the wire (client→TMM HTTP/2)", iterations, iterations),
		OK:          clientOK,
		Got:         got,
	})
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "backend reported HTTP/2.0 in response body (TMM→backend HTTP/2)",
		OK:          backendOK,
		Got:         got,
	})

	return scenarios.FinalizeResult(res)
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

// resolveVIP returns the VIP for the http2 demo. It honours the Options["vip"]
// override (e.g. --vip flag) and falls back to scnVIP — the dedicated demo VIP
// that avoids colliding with scenario-suite VIPs (.100 e2e, .101 split, .102
// ext-pool, .103 proxy-protocol) and the Diameter demo (.110).
func resolveVIP(ctx *scenarios.Context) string {
	if v := ctx.Options["vip"]; v != "" {
		return v
	}
	return scnVIP
}

func buildManifestVars(ctx *scenarios.Context) (manifestVars, error) {
	var v manifestVars
	v.Namespace = namespace(ctx)
	if ctx.Cluster != nil {
		v.GatewayClassName = ctx.Cluster.Metadata.Name + "-gatewayclass"
		if ctx.Cluster.Network.DataPath != nil {
			v.ExternalCIDR = ctx.Cluster.Network.DataPath.External.CIDR
		}
	}
	v.VIP = resolveVIP(ctx)
	return v, nil
}
