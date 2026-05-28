// Package diameter implements demo use-case "diameter" — a curated walkthrough
// that proves BNK's TMM can carry arbitrary L4 (TCP) protocols by proxying the
// Diameter base protocol (RFC 6733) from a jumphost client through a BNK L4
// Gateway to a backend pod, and validating the full CER→CEA capabilities
// exchange returns Result-Code=2001 (DIAMETER_SUCCESS).
//
// This is L4 (transport) only — TMM proxies the Diameter TCP stream without
// inspecting Diameter messages. Diameter-aware L7 routing (F5DiameterEndpoint
// MRF) is a CNF/SPK-only feature, not available on BNK-on-EKS.
//
// Verify order (load-bearing, mirrors proxyprotocoll4 — NOT httproutee2e):
//  1. Wait diameter-responder Deployment Available.
//  2. Wait Gateway diameter-gateway Programmed=True.
//  3. Wait L4Route diameter-l4route Accepted=True.
//  4. Best-effort F5BnkGateway demo-diameter get.
//  5. Push diameter_client.py to the jumphost via CopyFileViaEICE.
//  6. Run the client via RunStagingCommands; assert Result-Code=2001 DIAMETER_SUCCESS.
//
// NOTE: this is L4/TCP — there is NO ResyncHTTPRoutes (that is an HTTP-only
// pool-member workaround). JUMPHOST_BNK_EXT_ENI_IP is also NOT required —
// Diameter is L4, no Host header concept; the jumphost's default route via the
// BNK_EXT ENI takes the connection out the right interface automatically.
// Only JUMPHOST_INSTANCE_ID is required (same as CopyFileViaEICE/RunStagingCommands).
package diameter

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
)

const (
	scnName      = "diameter"
	scnTitle     = "Diameter (RFC 6733) CER→CEA over L4 (TCP) BNK Gateway"
	scnNamespace = "demo-diameter"
	// scnVIP is the diameter demo's dedicated VIP (avoids colliding with
	// scenario-suite VIPs — .100 e2e, .101 split, .102 ext-pool, .103
	// proxy-protocol — and the http2 demo at .111).
	scnVIP = "10.0.10.110"
	// scnDiameterPort is the standard Diameter TCP port (RFC 6733).
	scnDiameterPort = "3868"
	// scnRemoteClientPath is the absolute path used on the jumphost.
	// The awsbnkctl- prefix avoids colliding with any operator-staged files.
	// Do NOT use ~/ — CopyFileViaEICE does no shell expansion.
	scnRemoteClientPath = "/home/ec2-user/awsbnkctl-diameter-client.py"
	// scnSuccessMarker1 and scnSuccessMarker2 are the two substrings that must
	// appear in the client's stdout for the CER→CEA exchange to be considered
	// successful. Both are required (mirrors diameter-l4.proof.txt line 25).
	scnSuccessMarker1 = "Result-Code=2001"
	scnSuccessMarker2 = "DIAMETER_SUCCESS"
)

// l4RouteGVR is the BNK L4Route CR. Verified against a live BNK cluster:
// group gateway.k8s.f5net.com, version v1, resource l4routes.
var l4RouteGVR = schema.GroupVersionResource{
	Group:    "gateway.k8s.f5net.com",
	Version:  "v1",
	Resource: "l4routes",
}

// f5BnkGatewayGVR is the F5BnkGateway CR.
var f5BnkGatewayGVR = schema.GroupVersionResource{
	Group:    "k8s.f5net.com",
	Version:  "v1",
	Resource: "f5-bnkgateways",
}

func init() { demo.Register(&scenario{}) }

// VerifyDeps holds the function pointers used by Verify. The zero value routes
// every call to the real package-level implementations. Tests swap individual
// fields to a recording stub to assert call order without touching the cluster.
type VerifyDeps struct {
	WaitDeploymentAvailableFn func(ctx context.Context, sctx *scenarios.Context, ns, name string, timeout time.Duration) error
	WaitConditionFn           func(ctx context.Context, sctx *scenarios.Context, gvr schema.GroupVersionResource, ns, name, condType string, timeout time.Duration) error
	WaitL4RouteConditionFn    func(ctx context.Context, sctx *scenarios.Context, ns, name, condType string, timeout time.Duration) error
	// RunDiameterProbeFn pushes the diameter client and runs the CER→CEA exchange.
	// Returns (ok bool, got string) where got is a human-readable summary.
	RunDiameterProbeFn func(ctx context.Context, sctx *scenarios.Context, vip string) (ok bool, got string)
}

func realVerifyDeps() VerifyDeps {
	return VerifyDeps{
		WaitDeploymentAvailableFn: scenarios.WaitDeploymentAvailable,
		WaitConditionFn:           scenarios.WaitCondition,
		WaitL4RouteConditionFn:    waitL4RouteCondition,
		RunDiameterProbeFn:        runDiameterProbe,
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
Diameter-over-L4 demo — proves BNK's TMM can carry arbitrary TCP protocols by
proxying the Diameter base protocol (RFC 6733) from a jumphost through a BNK
L4 Gateway to a python responder backend, and validating the full CER→CEA
capabilities exchange returns Result-Code=2001 (DIAMETER_SUCCESS).

L4 (TCP) only: TMM proxies the Diameter TCP stream transparently. The Diameter-
aware L7 routing (F5DiameterEndpoint MRF) is a CNF/SPK-only feature not present
on this BNK-on-EKS build. VIP 10.0.10.110:3868 (dedicated, no collision with
scenario-suite or http2 demo). The python diameter_client.py is pushed to the
jumphost via CopyFileViaEICE and run via RunStagingCommands.

Verify order (load-bearing, mirrors proxy-protocol-l4 — NOT httproutee2e):
  1. diameter-responder Deployment Available.
  2. Gateway diameter-gateway Programmed=True.
  3. L4Route diameter-l4route Accepted=True.
  4. Best-effort F5BnkGateway demo-diameter present.
  5. Push diameter_client.py to jumphost; run against VIP:3868.
  6. Assert stdout contains Result-Code=2001 AND DIAMETER_SUCCESS.

Requires: testing.jumphost.enabled=true in cluster.yaml + awsbnkctl up --demo first.
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

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	d := s.vDeps
	if d == nil {
		real := realVerifyDeps()
		d = &real
	}
	ns := namespace(ctx)
	res := scenarios.Result{}

	// --- Step 1: backend Deployment Available ---
	err := d.WaitDeploymentAvailableFn(ctx.Ctx, ctx, ns, "diameter-responder", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "diameter-responder Deployment Available",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// --- Step 2: Gateway Programmed=True ---
	err = d.WaitConditionFn(ctx.Ctx, ctx, scenarios.GatewayGVR, ns, "diameter-gateway", "Programmed", 5*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "Gateway diameter-gateway Programmed=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// --- Step 3: L4Route Accepted=True ---
	// Uses the inline waitL4RouteCondition helper (deliberate copy from
	// proxyprotocoll4 — see helper comment for the breadcrumb note).
	err = d.WaitL4RouteConditionFn(ctx.Ctx, ctx, ns, "diameter-l4route", "Accepted", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "L4Route diameter-l4route Accepted=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// --- Step 4: Best-effort F5BnkGateway present (skipped when Dynamic client is nil) ---
	if ctx.Dynamic != nil {
		_, ferr := ctx.Dynamic.Resource(f5BnkGatewayGVR).Namespace(ns).Get(ctx.Ctx, "demo-diameter", metav1.GetOptions{})
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "F5BnkGateway demo-diameter present",
			OK:          ferr == nil,
			Got:         scenarios.ErrString(ferr),
		})
	}

	// --- Step 5: Diameter probe via jumphost ---
	// Only JUMPHOST_INSTANCE_ID is required for Diameter — this is L4/TCP so
	// there is no Host header concept and no need to bind to a specific source
	// interface (JUMPHOST_BNK_EXT_ENI_IP). The jumphost's default route via the
	// BNK_EXT ENI takes the connection out the right interface automatically.
	// This differs from the HTTP scenarios which require JUMPHOST_BNK_EXT_ENI_IP
	// for --interface curl bindings.
	instanceID := ctx.State.Get("JUMPHOST_INSTANCE_ID")
	if instanceID == "" {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "jumphost state key present",
			OK:          false,
			Got:         "JUMPHOST_INSTANCE_ID missing from state.env — run `awsbnkctl up` with testing.jumphost.enabled=true",
		})
		return scenarios.FinalizeResult(res)
	}

	vip := resolveVIP(ctx)
	ok, got := d.RunDiameterProbeFn(ctx.Ctx, ctx, vip)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "Diameter CER→CEA exchange via L4 VIP returned Result-Code=2001 (DIAMETER_SUCCESS)",
		OK:          ok,
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

// resolveVIP returns the VIP for the diameter demo. It honours the
// Options["vip"] override (e.g. --vip flag) and falls back to scnVIP —
// the dedicated demo VIP that avoids colliding with scenario-suite VIPs
// (.100 e2e, .101 split, .102 ext-pool, .103 proxy-protocol) and the
// http2 demo (.111).
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

// runDiameterProbe reads diameter_client.py from the embedded ClientFS, pushes
// it to the jumphost via CopyFileViaEICE, then runs it via RunStagingCommands
// against the L4 VIP on the standard Diameter port (3868). It asserts the
// stdout contains both Result-Code=2001 AND DIAMETER_SUCCESS.
//
// Two separate EICE key-mint windows (one copy + one run) is the correct shape:
// CopyFileViaEICE's base64 mechanism is binary-safe for Python source, unlike a
// cat-heredoc approach which breaks on embedded sentinel strings.
func runDiameterProbe(ctx context.Context, sctx *scenarios.Context, vip string) (ok bool, got string) {
	clientBytes, err := fs.ReadFile(ClientFS(), "diameter_client.py")
	if err != nil {
		return false, fmt.Sprintf("reading diameter_client.py from embed: %v", err)
	}

	region := ""
	if sctx.Cluster != nil {
		region = sctx.Cluster.Metadata.Region
	}
	opts := jumphost.ProbeOptions{
		Region:     region,
		InstanceID: sctx.State.Get("JUMPHOST_INSTANCE_ID"),
	}

	if copyErr := jumphost.CopyFileViaEICE(ctx, opts, clientBytes, scnRemoteClientPath); copyErr != nil {
		return false, fmt.Sprintf("CopyFileViaEICE: %v", copyErr)
	}

	cmd := fmt.Sprintf("python3 %s %s %s", scnRemoteClientPath, vip, scnDiameterPort)
	out, runErr := jumphost.RunStagingCommands(ctx, opts, []string{cmd})
	stdout := strings.Join(out, "\n")

	if runErr != nil {
		return false, fmt.Sprintf("RunStagingCommands error: %v — stdout: %s", runErr, truncate(stdout, 500))
	}

	if !strings.Contains(stdout, scnSuccessMarker1) {
		return false, fmt.Sprintf("missing %q in output: %s", scnSuccessMarker1, truncate(stdout, 500))
	}
	if !strings.Contains(stdout, scnSuccessMarker2) {
		return false, fmt.Sprintf("missing %q in output: %s", scnSuccessMarker2, truncate(stdout, 500))
	}
	return true, stdout
}

// truncate returns s truncated to at most n bytes, appending "..." if trimmed.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// waitL4RouteCondition polls the L4Route for the named condition with
// status=True. It inspects BOTH the per-parent list (.status.parents[*].conditions)
// AND the flat top-level list (.status.conditions), so a True condition in either
// location satisfies the wait. This dual-path approach guards against uncertainty
// in the BNK L4Route status schema: some BNK versions surface conditions only at
// the top level, others use the Gateway-API per-parent shape.
//
// Deliberately copied inline from internal/scenarios/proxyprotocoll4 rather than
// imported — locality > DRY here; the helper is adapter #2 (diameter). Once a
// third L4 consumer appears, hoist into internal/scenarios/helpers.go as the
// canonical "two adapters = real seam" threshold will have been crossed.
func waitL4RouteCondition(ctx context.Context, sctx *scenarios.Context, ns, name, condType string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, err := sctx.Dynamic.Resource(l4RouteGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			// Path 1: per-parent conditions (.status.parents[*].conditions).
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
			// Path 2: flat top-level conditions (.status.conditions[*]).
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
	return fmt.Errorf("L4Route %s/%s condition %s not True after %s", ns, name, condType, timeout)
}
