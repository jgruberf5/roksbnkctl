// Package ingressmigration implements demo use-case "ingress-migration" — a
// curated walkthrough that demonstrates migrating Kubernetes ingress to BNK's
// Gateway API by running ingress-nginx, HAProxy ingress, and a BNK Gateway all
// simultaneously in front of ONE shared backend (traefik/whoami).
//
// Verify order (load-bearing):
//  1. Wait for backend Deployment Available, ingress-nginx controller
//     Deployment Available, haproxy ingress controller Deployment Available.
//  2. Wait Gateway migration-gateway Programmed=True.
//  3. Wait HTTPRoute migration-route Accepted=True + ResolvedRefs=True.
//  4. Call pkg/bnk.ResyncHTTPRoutes AFTER control-plane conditions settle
//     (pool-member workaround — moving it earlier regresses the fix).
//  5. BNK leg: SSH+EICE curl VIP 10.0.10.113 with Host: web.bnk.migration.local
//     from the jumphost; assert HTTP 200 + "Hostname" in body (whoami marker).
//  6. nginx leg: in-cluster curl the ingress-nginx controller ClusterIP service
//     with Host: web.nginx.migration.local; assert HTTP 200 + whoami marker.
//  7. haproxy leg: in-cluster curl the haproxy controller ClusterIP service
//     with Host: web.haproxy.migration.local; assert HTTP 200 + whoami marker.
//
// Cleanup: helm uninstall both controllers (tolerates not-found); delete the
// demo-ingress-migration namespace.
package ingressmigration

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	"github.com/JLCode-tech/awsbnkctl/pkg/bnk"
)

const (
	scnName      = "ingress-migration"
	scnTitle     = "Ingress migration to BNK Gateway API — nginx + HAProxy + BNK side-by-side"
	scnNamespace = "demo-ingress-migration"

	// scnVIP is the ingress-migration demo's dedicated VIP.
	// Allocation: .100 e2e / .101 split / .102 extpool / .103 proxyproto /
	//             .110 diameter / .111 http2 / .112 grpc / .113 ingress-migration.
	scnVIP = "10.0.10.113"

	// scnBackendMarker is a stable substring present in every traefik/whoami
	// response body. whoami always echoes "Hostname: <pod-name>" as the first
	// line, making it trivially assertable.
	scnBackendMarker = "Hostname"

	// Hostnames used as HTTP Host headers / Ingress rules.
	scnHostBNK     = "web.bnk.migration.local"
	scnHostNginx   = "web.nginx.migration.local"
	scnHostHAProxy = "web.haproxy.migration.local"

	// Helm chart coordinates — pinned per task spec.
	nginxRepoURL = "https://kubernetes.github.io/ingress-nginx"
	nginxChart   = "ingress-nginx"
	nginxVersion = "4.15.1"
	nginxRelease = "ingress-nginx"
	nginxNS      = "ingress-nginx"

	haproxyRepoURL = "https://haproxytech.github.io/helm-charts"
	haproxyChart   = "kubernetes-ingress"
	haproxyVersion = "1.52.0"
	haproxyRelease = "haproxy-ingress"
	haproxyNS      = "haproxy-ingress"

	// Controller Deployment names produced by the pinned chart versions
	// (verified against chart metadata; controller Deployment = release name).
	nginxControllerDeploy   = "ingress-nginx-controller"
	haproxyControllerDeploy = "haproxy-ingress-kubernetes-ingress"

	// Service names for in-cluster curl probes.
	nginxSvcName   = "ingress-nginx-controller"
	haproxySvcName = "haproxy-ingress-kubernetes-ingress"

	// Timeout for in-cluster curl probes.
	inClusterCurlTimeout = 5 * time.Minute

	// curlPodName prefix for the one-shot verification pods.
	curlPodPrefix = "migration-verify"
)

var (
	nginxHelmRelease = helmRelease{
		ReleaseName: nginxRelease,
		Namespace:   nginxNS,
		RepoURL:     nginxRepoURL,
		Chart:       nginxChart,
		Version:     nginxVersion,
		Values: map[string]interface{}{
			"controller": map[string]interface{}{
				"service": map[string]interface{}{
					"type": "ClusterIP",
				},
				"ingressClassResource": map[string]interface{}{
					"name":    "nginx",
					"default": false,
				},
				"admissionWebhooks": map[string]interface{}{
					"enabled": false,
				},
			},
		},
	}

	haproxyHelmRelease = helmRelease{
		ReleaseName: haproxyRelease,
		Namespace:   haproxyNS,
		RepoURL:     haproxyRepoURL,
		Chart:       haproxyChart,
		Version:     haproxyVersion,
		Values: map[string]interface{}{
			"controller": map[string]interface{}{
				"service": map[string]interface{}{
					"type": "ClusterIP",
				},
				"ingressClass": "haproxy",
			},
		},
	}
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
	RunBNKProbeFn             func(ctx context.Context, sctx *scenarios.Context, vip string, timeout time.Duration) (ok bool, body string)
	RunInClusterCurlFn        func(ctx context.Context, sctx *scenarios.Context, controllerNS, svcName, host string, timeout time.Duration) (ok bool, body string, err error)
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
		RunBNKProbeFn:      runBNKProbe,
		RunInClusterCurlFn: runInClusterCurl,
	}
}

type scenario struct {
	// vDeps is nil for the registered singleton; tests inject a non-nil value.
	vDeps *VerifyDeps
	// helm is nil for the registered singleton (uses newRealHelmRunner); tests
	// inject a no-op to skip actual Helm network calls.
	helm helmRunner
}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Green }
func (s *scenario) Dependencies() []string   { return []string{} }
func (s *scenario) Description() string {
	return strings.TrimSpace(`
Ingress migration demo — ingress-nginx, HAProxy, and BNK Gateway API all front
one shared backend (traefik/whoami) simultaneously so you can compare traffic
paths live before cutting over.

Installs via Helm:
  ingress-nginx  4.15.1  (controller.service.type=ClusterIP, webhooks disabled)
  haproxy        1.52.0  (controller.service.type=ClusterIP)

Applies 6 SSA manifests into demo-ingress-migration:
  Namespace, F5BnkGateway IP pool, whoami Deployment+Service,
  nginx+haproxy Ingress objects, Gateway (VIP 10.0.10.113), HTTPRoute.

Verify order (load-bearing):
  1. Wait backend + both controller Deployments Available.
  2. Wait Gateway Programmed=True, HTTPRoute Accepted=True + ResolvedRefs=True.
  3. Call pkg/bnk.ResyncHTTPRoutes (pool-member workaround).
  4. BNK leg:     SSH+EICE curl VIP with Host: web.bnk.migration.local → assert HTTP 200.
  5. nginx leg:   in-cluster curl nginx ClusterIP with Host: web.nginx.migration.local → assert HTTP 200.
  6. haproxy leg: in-cluster curl haproxy ClusterIP with Host: web.haproxy.migration.local → assert HTTP 200.

Cleanup: helm uninstall both controllers (tolerates not-found); delete demo namespace.
Requires: --demo cluster (awsbnkctl up --demo).
`)
}

// manifestVars holds the template variables for the 6 manifests.
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
	// Step A: Install both ingress controllers via Helm.
	hr := s.helm
	if hr == nil {
		hr = newRealHelmRunner(ctx.KubeconfigPath)
	}
	if err := hr.EnsureRelease(nginxHelmRelease); err != nil {
		return fmt.Errorf("ingress-nginx helm install: %w", err)
	}
	if err := hr.EnsureRelease(haproxyHelmRelease); err != nil {
		return fmt.Errorf("haproxy-ingress helm install: %w", err)
	}

	// Step B: SSA-apply the namespace, backend, gateway, httproute manifests.
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

	// migration-backend Deployment Available.
	err := d.WaitDeploymentAvailableFn(ctx.Ctx, ctx, ns, "migration-backend", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "migration-backend Deployment Available",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// ingress-nginx controller Deployment Available.
	err = d.WaitDeploymentAvailableFn(ctx.Ctx, ctx, nginxNS, nginxControllerDeploy, 5*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "ingress-nginx controller Deployment Available",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// haproxy controller Deployment Available.
	err = d.WaitDeploymentAvailableFn(ctx.Ctx, ctx, haproxyNS, haproxyControllerDeploy, 5*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "haproxy ingress controller Deployment Available",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// Gateway Programmed=True.
	err = d.WaitConditionFn(ctx.Ctx, ctx, scenarios.GatewayGVR, ns, "migration-gateway", "Programmed", 5*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "Gateway migration-gateway Programmed=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// HTTPRoute Accepted=True.
	err = d.WaitHTTPRouteConditionFn(ctx.Ctx, ctx, ns, "migration-route", "Accepted", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute migration-route Accepted=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// HTTPRoute ResolvedRefs=True.
	err = d.WaitHTTPRouteConditionFn(ctx.Ctx, ctx, ns, "migration-route", "ResolvedRefs", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute migration-route ResolvedRefs=True",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// Best-effort: F5BnkGateway present (skipped when Dynamic client is nil).
	// The resource name tracks the namespace (set by the {{.Namespace}} template
	// in manifests/02-f5bnkgateway.yaml), so use ns here — not the hardcoded
	// default — so overridden namespaces are checked correctly.
	if ctx.Dynamic != nil {
		_, ferr := ctx.Dynamic.Resource(f5BnkGatewayGVR).Namespace(ns).Get(ctx.Ctx, ns, metav1.GetOptions{})
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "F5BnkGateway " + ns + " present",
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

	// --- Step 3: BNK leg (SSH+EICE curl from jumphost) ---
	_, _, probeTimeout, probeErr := scenarios.BuildProbeParams(ctx)
	if probeErr != nil {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "BNK leg: jumphost probe setup",
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
			Description: "BNK leg: jumphost state keys present",
			OK:          false,
			Got:         "JUMPHOST_INSTANCE_ID / JUMPHOST_BNK_EXT_ENI_IP missing from state.env — run `awsbnkctl up` with testing.jumphost.enabled=true",
		})
		return scenarios.FinalizeResult(res)
	}

	bnkOK, bnkBody := d.RunBNKProbeFn(ctx.Ctx, ctx, vip, probeTimeout)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("BNK leg: HTTP 200 from VIP %s (Host: %s)", vip, scnHostBNK),
		OK:          bnkOK,
		Got:         bnkBody,
	})

	// --- Step 4: nginx + haproxy in-cluster legs ---
	nginxOK, nginxBody, nginxErr := d.RunInClusterCurlFn(ctx.Ctx, ctx, nginxNS, nginxSvcName, scnHostNginx, inClusterCurlTimeout)
	nginxGot := nginxBody
	if nginxErr != nil {
		nginxGot = nginxErr.Error()
	}
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("nginx leg: HTTP 200 via ClusterIP (Host: %s)", scnHostNginx),
		OK:          nginxOK,
		Got:         nginxGot,
	})

	haproxyOK, haproxyBody, haproxyErr := d.RunInClusterCurlFn(ctx.Ctx, ctx, haproxyNS, haproxySvcName, scnHostHAProxy, inClusterCurlTimeout)
	haproxyGot := haproxyBody
	if haproxyErr != nil {
		haproxyGot = haproxyErr.Error()
	}
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("haproxy leg: HTTP 200 via ClusterIP (Host: %s)", scnHostHAProxy),
		OK:          haproxyOK,
		Got:         haproxyGot,
	})

	// Best-effort: forge scan_cluster refresh so forge re-inventories the new workloads.
	// The demo's whole point is showing the migration in forge. Warn-and-continue — never
	// fail the demo on forge.
	tryForgeRefresh(ctx)

	return scenarios.FinalizeResult(res)
}

func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	// Uninstall Helm releases (tolerate not-found via IgnoreNotFound).
	hr := s.helm
	if hr == nil {
		hr = newRealHelmRunner(ctx.KubeconfigPath)
	}
	if err := hr.UninstallRelease(nginxRelease, nginxNS); err != nil {
		fmt.Fprintf(ctx.Out, "[demo/ingress-migration] warn: helm uninstall %s: %v\n", nginxRelease, err)
	}
	if err := hr.UninstallRelease(haproxyRelease, haproxyNS); err != nil {
		fmt.Fprintf(ctx.Out, "[demo/ingress-migration] warn: helm uninstall %s: %v\n", haproxyRelease, err)
	}

	// Delete the demo namespace (idempotent).
	if ctx.Clientset != nil {
		ns := namespace(ctx)
		err := ctx.Clientset.CoreV1().Namespaces().Delete(ctx.Ctx, ns, metav1.DeleteOptions{})
		if err != nil && !scenarios.IsNotFound(err) {
			return fmt.Errorf("deleting namespace %s: %w", ns, err)
		}
	}
	return nil
}

func (s *scenario) Namespace(ctx *scenarios.Context) string { return namespace(ctx) }

// --- probes ---

// runBNKProbe issues a single curl probe from the jumphost via SSH+EICE.
func runBNKProbe(ctx context.Context, sctx *scenarios.Context, vip string, timeout time.Duration) (ok bool, body string) {
	sourceIP := sctx.State.Get("JUMPHOST_BNK_EXT_ENI_IP")
	timeoutSecs := int(timeout.Seconds())
	if timeoutSecs < 5 {
		timeoutSecs = 5
	}

	cmd := fmt.Sprintf(
		`curl -s -o /dev/null -w '%%{http_code}' -H 'Host: %s' --interface %s --max-time %d http://%s/`,
		scnHostBNK, sourceIP, timeoutSecs, vip,
	)
	bodyCmd := fmt.Sprintf(
		`curl -s -H 'Host: %s' --interface %s --max-time %d http://%s/`,
		scnHostBNK, sourceIP, timeoutSecs, vip,
	)

	probeOpts := jumphost.ProbeOptions{
		Region:     sctx.Cluster.Metadata.Region,
		InstanceID: sctx.State.Get("JUMPHOST_INSTANCE_ID"),
		SourceIP:   sourceIP,
		VIP:        vip,
		Timeout:    timeout,
		Hostname:   scnHostBNK,
	}

	out, err := jumphost.RunStagingCommands(ctx, probeOpts, []string{cmd, bodyCmd})
	if err != nil {
		partial := ""
		if len(out) > 0 {
			partial = " (partial: " + strings.Join(out, "; ") + ")"
		}
		return false, "probe error: " + err.Error() + partial
	}

	code := ""
	if len(out) > 0 {
		code = strings.TrimSpace(out[0])
	}
	responseBody := ""
	if len(out) > 1 {
		responseBody = out[1]
	}

	if code != "200" {
		return false, fmt.Sprintf("HTTP %s (want 200); body: %s", code, strings.TrimSpace(responseBody))
	}
	if !strings.Contains(responseBody, scnBackendMarker) {
		return false, fmt.Sprintf("HTTP 200 but body missing %q: %s", scnBackendMarker, strings.TrimSpace(responseBody))
	}
	return true, fmt.Sprintf("HTTP 200, body contains %q", scnBackendMarker)
}

// runInClusterCurl verifies that the named ClusterIP service (in controllerNS)
// routes requests with the given Host header to the shared backend. It does
// this by creating a short-lived Pod in the demo namespace that runs a single
// curl, waiting for it to complete, then reading its logs.
//
// The Pod is deleted on return (best-effort; a leftover pod does not fail the
// scenario).
func runInClusterCurl(ctx context.Context, sctx *scenarios.Context, controllerNS, svcName, host string, timeout time.Duration) (ok bool, body string, err error) {
	if sctx.Clientset == nil {
		return false, "", fmt.Errorf("clientset is nil — cannot run in-cluster curl")
	}

	// Resolve the ClusterIP of the controller service.
	svc, svcErr := sctx.Clientset.CoreV1().Services(controllerNS).Get(ctx, svcName, metav1.GetOptions{})
	if svcErr != nil {
		return false, "", fmt.Errorf("get service %s/%s: %w", controllerNS, svcName, svcErr)
	}
	clusterIP := svc.Spec.ClusterIP
	if clusterIP == "" || clusterIP == "None" {
		return false, "", fmt.Errorf("service %s/%s has no ClusterIP", controllerNS, svcName)
	}

	// Build a unique pod name derived from the service name.
	podName := fmt.Sprintf("%s-%s", curlPodPrefix, safeLabel(svcName))
	ns := namespace(sctx)

	// Unconditionally delete any pre-existing pod with this name so the probe
	// is idempotent on re-run.
	_ = sctx.Clientset.CoreV1().Pods(ns).Delete(ctx, podName, metav1.DeleteOptions{})

	// Wait for the old pod to be fully gone before creating the replacement.
	// Without this, a Terminating pod from a prior run causes Create to fail
	// with "already exists". Cap at 30s; if still present we proceed and let
	// Create surface the error naturally.
	{
		deleteDeadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deleteDeadline) {
			_, getErr := sctx.Clientset.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
			if scenarios.IsNotFound(getErr) {
				break
			}
			select {
			case <-ctx.Done():
				return false, "", ctx.Err()
			case <-time.After(1 * time.Second):
			}
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			Labels:    map[string]string{"app": "migration-verify", "demo": "ingress-migration"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "curl",
					Image: "curlimages/curl:8.11.1",
					Command: []string{
						"curl",
						"-s",
						"-w", "\n---HTTP_CODE:%{http_code}---\n",
						"-H", fmt.Sprintf("Host: %s", host),
						"--max-time", "10",
						fmt.Sprintf("http://%s/", clusterIP),
					},
				},
			},
		},
	}

	if _, createErr := sctx.Clientset.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); createErr != nil {
		return false, "", fmt.Errorf("create curl pod %s: %w", podName, createErr)
	}
	// Best-effort cleanup — pod logs are already captured before this runs.
	defer func() {
		bg := context.Background()
		_ = sctx.Clientset.CoreV1().Pods(ns).Delete(bg, podName, metav1.DeleteOptions{})
	}()

	// Poll until the pod succeeds, fails, or the timeout elapses.
	deadline := time.Now().Add(timeout)
	var finalPhase corev1.PodPhase
	for time.Now().Before(deadline) {
		p, pollErr := sctx.Clientset.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
		if pollErr == nil {
			finalPhase = p.Status.Phase
			if finalPhase == corev1.PodSucceeded || finalPhase == corev1.PodFailed {
				break
			}
		}
		select {
		case <-ctx.Done():
			return false, "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}

	// Read pod logs regardless of exit phase (they contain the curl output).
	logReq := sctx.Clientset.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{})
	logResult := logReq.Do(ctx)
	rawBody, logErr := logResult.Raw()
	if logErr != nil {
		return false, "", fmt.Errorf("read logs for curl pod %s: %w", podName, logErr)
	}
	logBody := string(rawBody)

	if finalPhase == corev1.PodFailed {
		return false, logBody, fmt.Errorf("curl pod %s exited with failure; logs: %s", podName, strings.TrimSpace(logBody))
	}
	if finalPhase != corev1.PodSucceeded {
		return false, logBody, fmt.Errorf("curl pod %s did not complete within %s (phase=%s)", podName, timeout, finalPhase)
	}

	// Extract HTTP code from the sentinel line.
	if !strings.Contains(logBody, "---HTTP_CODE:200---") {
		return false, logBody, nil
	}
	if !strings.Contains(logBody, scnBackendMarker) {
		return false, logBody, nil
	}
	return true, fmt.Sprintf("HTTP 200, body contains %q", scnBackendMarker), nil
}

// safeLabel truncates s to a kube-label-safe suffix for pod names.
func safeLabel(s string) string {
	if len(s) > 20 {
		s = s[len(s)-20:]
	}
	return strings.ReplaceAll(s, "_", "-")
}

// --- forge refresh ---

// tryForgeRefresh triggers forge scan_cluster best-effort after Apply.
// Reads FORGE_CLUSTER_ID from state; silently skips if not registered.
// Never returns an error — it logs warnings via ctx.Out and returns.
func tryForgeRefresh(ctx *scenarios.Context) {
	if ctx.State == nil {
		return
	}
	rawID := ctx.State.Get("FORGE_CLUSTER_ID")
	if rawID == "" {
		return
	}

	clusterID := 0
	if _, err := fmt.Sscanf(rawID, "%d", &clusterID); err != nil || clusterID == 0 {
		fmt.Fprintf(ctx.Out, "[demo/ingress-migration] forge scan_cluster: FORGE_CLUSTER_ID=%q is not an integer — skipping\n", rawID)
		return
	}

	// Import forge client inline to avoid a circular import: the forge package
	// imports nothing from demo, so this direction is safe.
	forgeScanFn(ctx.Ctx, clusterID, ctx.Out)
}

// forgeScanFn is the injectable seam for forge scan_cluster. Tests replace this
// with a no-op. Production implementation is set in forge_scan.go.
var forgeScanFn = defaultForgeScan

// --- internal helpers ---

func namespace(ctx *scenarios.Context) string {
	if v := ctx.Options["namespace"]; v != "" {
		return v
	}
	return scnNamespace
}

// resolveVIP returns the VIP for the ingress-migration demo. Honors
// Options["vip"] and falls back to scnVIP (10.0.10.113).
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
