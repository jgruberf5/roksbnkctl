// Package bigipcis implements demo use-case "bigip-cis" — the TRADITIONAL
// ingress model that BNK's in-cluster Gateway API replaces: an EXTERNAL F5
// BIG-IP VE appliance fronted by an in-cluster CIS controller
// (k8s-bigip-ctlr), programmed via a VirtualServer custom resource.
//
// It is the contrast use-case to ingress-migration: where BNK runs the L7
// dataplane (TMM) INSIDE the cluster and is programmed by Gateway API
// (HTTPRoute → cne-controller), this use-case keeps the dataplane on a
// SEPARATE BIG-IP appliance and drives it from inside the cluster with CIS
// watching a VirtualServer CRD.
//
//	BNK (in-cluster)              Traditional CIS + external BIG-IP (this demo)
//	---------------------------   -------------------------------------------
//	cne-controller (programs TMM) k8s-bigip-ctlr / CIS (programs the BIG-IP)
//	TMM (in-cluster dataplane)    BIG-IP VE (external appliance dataplane)
//	HTTPRoute / Gateway API       VirtualServer CRD (cis.f5.com)
//	in-cluster VIP on TMM         VIP on the external BIG-IP (10.0.10.120)
//
// Prerequisite: the BIG-IP VE must already be provisioned + onboarded by
// `awsbnkctl up` with bigipVE.enabled=true (state key BIGIP_ONBOARDED=true,
// plus BIGIP_MGMT_IP / BIGIP_VIP). Verify surfaces a clear error otherwise.
//
// Apply:
//  1. Namespace demo-bigip-cis (SSA).
//  2. Shared backend: traefik/whoami Deployment (app=cis-backend) + Service.
//  3. bigip-login Secret (created via the typed client from
//     $AWSBNKCTL_BIGIP_PASSWORD — NEVER written to a manifest on disk).
//  4. CIS (f5-bigip-ctlr 2.20.3) installed via Helm into kube-system, pointed
//     at BIGIP_MGMT_IP, partition cis, customResourceMode, pool_member_type
//     cluster, insecure (self-signed BIG-IP cert).
//  5. Static routes ON THE BIG-IP for the private/pod subnets via the internal
//     subnet gateway (.1), driven through the jumphost (idempotent).
//  6. VirtualServer CR (cis.f5.com/v1) → cis-backend on the BIG-IP VIP.
//
// Verify (proves traffic flows through the EXTERNAL BIG-IP):
//  1. cis-backend Deployment Available + CIS controller Deployment Available.
//  2. Best-effort: confirm CIS programmed the BIG-IP VS (non-fatal).
//  3. SSH+EICE curl the BIG-IP VIP from the jumphost with the demo Host header;
//     assert HTTP 200 + the whoami backend marker.
//
// Cleanup: helm uninstall CIS, delete the VirtualServer + bigip-login secret +
// demo namespace, best-effort remove the BIG-IP routes (all idempotent).
package bigipcis

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
)

const (
	scnName      = "bigip-cis"
	scnTitle     = "Traditional ingress — external BIG-IP VE + in-cluster CIS (VirtualServer CRD)"
	scnNamespace = "demo-bigip-cis"

	// scnDefaultVIP is the fallback VIP for the BIG-IP virtual server. The real
	// VIP is read from state (BIGIP_VIP, set by F2-B / phase17e = 10.0.10.120);
	// this default only applies when state is absent (e.g. dry-run / unit tests).
	scnDefaultVIP = "10.0.10.120"

	// scnHost is the HTTP Host header / VirtualServer host the demo routes on.
	scnHost = "web.cis.migration.local"

	// scnBackendMarker is a stable substring in every traefik/whoami response.
	scnBackendMarker = "Hostname"

	// bigipPasswordEnv is the env var carrying the BIG-IP admin password. It is
	// the same variable phase17f reads; it is NEVER written to a manifest on
	// disk, to state.env, or onto argv. (env var NAME, not a credential.)
	bigipPasswordEnv = "AWSBNKCTL_BIGIP_PASSWORD" // #nosec G101

	// bigipLoginSecret is the basic-auth-style Secret CIS reads BIG-IP creds from.
	bigipLoginSecret = "bigip-login"
	// bigipUser is the BIG-IP admin username (matches phase17f onboarding).
	bigipUser = "admin"

	// CIS Helm chart coordinates — pinned per task spec.
	cisRepoURL  = "https://f5networks.github.io/charts/stable"
	cisChart    = "f5-bigip-ctlr"
	cisVersion  = "0.0.37" // newest f5-bigip-ctlr chart; controller image tag (2.20.3) is pinned via the chart's top-level `version` key (NOT image.tag, which the chart ignores)
	cisRelease  = "bigip-cis"
	cisNS       = "kube-system"
	cisImageTag = "2.20.3"

	// cisControllerDeploy is the controller Deployment name produced by the chart
	// (the chart names the Deployment after the release).
	cisControllerDeploy = "bigip-cis-f5-bigip-ctlr"

	// bigipCISPartition is the LTM partition F2-B created and CIS writes into.
	bigipCISPartition = "cis"

	// bigipRouteNamePrefix prefixes the per-CIDR BIG-IP static route names this
	// demo creates (so Cleanup can remove exactly the routes it added).
	bigipRouteNamePrefix = "bnkdemo-cis-pods-"
)

// cisVerifyTimeout caps the controller/backend readiness waits.
const cisVerifyTimeout = 5 * time.Minute

func init() { demo.Register(&scenario{}) }

// VerifyDeps holds the function pointers used by Verify. The zero value routes
// every call to the real package-level implementations. Tests swap individual
// fields to a recording stub to assert call order without touching the cluster.
type VerifyDeps struct {
	WaitDeploymentAvailableFn func(ctx context.Context, sctx *scenarios.Context, ns, name string, timeout time.Duration) error
	CheckVSProgrammedFn       func(ctx context.Context, sctx *scenarios.Context, ns, name string) (ok bool, detail string)
	RunBNKProbeFn             func(ctx context.Context, sctx *scenarios.Context, vip string, timeout time.Duration) (ok bool, body string)
}

func realVerifyDeps() VerifyDeps {
	return VerifyDeps{
		WaitDeploymentAvailableFn: scenarios.WaitDeploymentAvailable,
		CheckVSProgrammedFn:       checkVSProgrammed,
		RunBNKProbeFn:             runBNKProbe,
	}
}

type scenario struct {
	// vDeps is nil for the registered singleton; tests inject a non-nil value.
	vDeps *VerifyDeps
	// helm is nil for the registered singleton (uses newRealHelmRunner); tests
	// inject a no-op to skip actual Helm network calls.
	helm helmRunner
	// applyRouteFn / removeRouteFn are the BIG-IP route seams (jumphost SSH).
	// nil for the registered singleton (uses the real jumphost helpers); tests
	// inject recorders to assert sequencing without SSH.
	applyRoutesFn  func(ctx context.Context, sctx *scenarios.Context, cidrs []string) error
	removeRoutesFn func(ctx context.Context, sctx *scenarios.Context, cidrs []string) error
	// createSecretFn is the bigip-login Secret seam. nil → real typed-client
	// create. Tests inject a recorder to assert the password never lands in a
	// manifest while still capturing the value passed in.
	createSecretFn func(ctx context.Context, sctx *scenarios.Context, password string) error
	// applyManifestsFn is the SSA-apply seam. nil → scenarios.ApplyManifests
	// (real cluster). Tests inject a no-op so Apply can be exercised end-to-end
	// without a live kubeconfig.
	applyManifestsFn func(ctx *scenarios.Context, scnName string) error
}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Green }
func (s *scenario) Dependencies() []string   { return []string{} }
func (s *scenario) Description() string {
	return strings.TrimSpace(`
BIG-IP CIS demo — the TRADITIONAL ingress model BNK replaces: an EXTERNAL F5
BIG-IP VE appliance fronted by an in-cluster CIS controller (k8s-bigip-ctlr),
programmed via a VirtualServer custom resource.

Migration comparison (this demo  →  BNK):
  CIS / k8s-bigip-ctlr   →  cne-controller (programs the dataplane)
  BIG-IP VE appliance    →  TMM (in-cluster dataplane)
  VirtualServer CRD      →  Gateway API HTTPRoute
  external appliance VIP →  in-cluster VIP on TMM

Installs via Helm:
  f5-bigip-ctlr  (k8s-bigip-ctlr image 2.20.3) into kube-system, pointed at the
  onboarded BIG-IP (partition cis, customResourceMode, pool_member_type=cluster
  so it programs pod IPs directly over VPC CNI, insecure for the self-signed cert).

Applies into demo-bigip-cis:
  Namespace, whoami Deployment+Service (app=cis-backend), VirtualServer CR
  (cis.f5.com/v1) on the BIG-IP VIP (default 10.0.10.120). The bigip-login Secret
  is created from $AWSBNKCTL_BIGIP_PASSWORD via the typed client — never a manifest.
Adds static routes ON THE BIG-IP for the private/pod subnets via the internal
subnet gateway, through the jumphost (idempotent).

Verify:
  1. cis-backend + CIS controller Deployments Available.
  2. Best-effort: confirm CIS programmed the BIG-IP VirtualServer (non-fatal).
  3. SSH+EICE curl the BIG-IP VIP with Host: web.cis.migration.local → assert
     HTTP 200 + whoami marker (client → external BIG-IP → in-cluster pod).

Cleanup: helm uninstall CIS; delete the VirtualServer + bigip-login Secret +
demo namespace; best-effort remove the BIG-IP routes. All idempotent.
Requires: awsbnkctl up --demo with bigipVE.enabled=true (BIGIP_ONBOARDED=true).

Known limitation: with VPC-CNI default, pods land in the EKS node subnet
(e.g. 10.0.1.0/24), not the BNK data CIDRs. The BIG-IP management self-IP
shares that subnet, so the automatic /24 pod-subnet route is rejected by the
BIG-IP ("matches management network"). The traffic assertion in Verify requires
either a per-pod /32 host route via the internal gateway (proven workaround) or
the preferred durable fix: move the BIG-IP management ENI to a non-overlapping
subnet. See the comment above applyRoutes in Apply() for full details.
`)
}

// manifestVars holds the template variables for the manifests.
type manifestVars struct {
	Namespace string
	VIP       string
	Host      string
}

func (s *scenario) Manifests(ctx *scenarios.Context) ([]string, error) {
	v := buildManifestVars(ctx)

	var paths []string
	err := fs.WalkDir(manifestFS, "manifests", func(p string, d fs.DirEntry, werr error) error {
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
	// Step A: validate the onboarded BIG-IP mgmt IP is in state.
	mgmtIP := ctx.State.Get("BIGIP_MGMT_IP")
	if mgmtIP == "" {
		return fmt.Errorf("bigip-cis: BIGIP_MGMT_IP not in state — run `awsbnkctl up` with bigipVE.enabled=true first")
	}

	// Step B: read + validate the env password (fail fast, before any cluster
	// mutation). The password is read from env only — never state/argv/manifest.
	pw := getBigIPPassword()
	if pw == "" {
		return fmt.Errorf("bigip-cis: %s is not set — the BIG-IP admin password is required "+
			"(export it; never put it in a manifest or cluster.yaml)", bigipPasswordEnv)
	}

	// Step C: create the bigip-login Secret from the env password via the typed
	// client, BEFORE the Helm install. The CIS pod mounts this secret and stays
	// ContainerCreating until it exists, so it must precede the Helm --wait or
	// the install blocks the full timeout. The Secret lands in the CIS namespace
	// (kube-system, already present) so it does not depend on the demo manifests.
	// The password is NEVER rendered into a manifest on disk.
	createSecret := s.createSecretFn
	if createSecret == nil {
		createSecret = createBigIPLoginSecret
	}
	if err := createSecret(ctx.Ctx, ctx, pw); err != nil {
		return fmt.Errorf("bigip-cis: creating %s secret: %w", bigipLoginSecret, err)
	}

	// Step D: install CIS via Helm (pointed at the onboarded BIG-IP). The pod
	// now finds the bigip-login secret and becomes Ready within the --wait.
	hr := s.helm
	if hr == nil {
		hr = newRealHelmRunner(ctx.KubeconfigPath)
	}
	if err := hr.EnsureRelease(cisHelmRelease(mgmtIP)); err != nil {
		return fmt.Errorf("f5-bigip-ctlr helm install: %w", err)
	}

	// Step E: SSA-apply the namespace + backend manifests + the VirtualServer
	// (NOT the secret).
	applyManifests := s.applyManifestsFn
	if applyManifests == nil {
		applyManifests = scenarios.ApplyManifests
	}
	if err := applyManifests(ctx, scnName); err != nil {
		return err
	}

	// Step F: add static routes ON THE BIG-IP for the private/pod subnets so the
	// appliance can reach the pod IPs CIS programs (VPC CNI gives pods routable
	// addresses, but the BIG-IP only has a self-IP on the internal subnet).
	//
	// KNOWN LIMITATION — BIG-IP route rejection under VPC-CNI default:
	//
	// podCIDRs() returns the cluster's private subnet CIDRs (e.g. 10.0.11.0/24,
	// 10.0.12.0/24) because that is where BNK data-plane subnets are declared in
	// the cluster intent. However, with VPC-CNI in default (non-prefix-delegation)
	// mode, pods receive IP addresses from the EKS node subnet — typically the
	// same /24 as the BIG-IP management ENI (e.g. 10.0.1.0/24). This creates two
	// problems:
	//
	//   1. The /24 routes added here do NOT match actual pod placement, so the
	//      BIG-IP pool members programmed by CIS are unreachable via these routes.
	//
	//   2. The BIG-IP management self-IP lives in the node subnet (e.g.
	//      10.0.1.50/24). When tmsh is asked to add a /24 data-plane route
	//      overlapping the management network, it rejects the command with:
	//      "matches management network ... not adding it". The route silently
	//      fails to install.
	//
	// Proven workaround (used to validate the demo live):
	//   Install a per-pod /32 host route via the internal gateway instead:
	//     tmsh create net route <name> network <podIP>/32 gw 10.0.20.1
	//   A /32 is more specific than the mgmt /24, so the BIG-IP accepts it and
	//   traffic reaches the pod — HTTP 200 proven end-to-end with this technique.
	//   A working /32 route is currently left on the live BIG-IP.
	//
	// Durable fix options (deferred — not implemented here):
	//
	//   A (preferred): place the BIG-IP management ENI in a dedicated subnet that
	//      does NOT overlap the EKS node/pod subnet (e.g. a separate mgmt /28 or
	//      /24). Then add /24 routes to each node subnet via the internal gateway
	//      (10.0.20.1). This is clean and survives pod reschedule without any
	//      per-pod route management.
	//
	//   B (fallback): program per-pod /32 routes for each CIS pool-member IP via
	//      the internal gateway. Works, but is brittle: pods reschedule and the
	//      routes go stale, analogous to the HTTPRoute pool-member-sync issue.
	//      Requires a watch loop or re-run of Apply to refresh routes after
	//      pod churn.
	//
	// Note: the CIS pool member also has no health monitor configured. A future
	// improvement is to add a tcp or http monitor and a post-Apply wait on
	// pool-member availability before the Verify traffic probe.
	applyRoutes := s.applyRoutesFn
	if applyRoutes == nil {
		applyRoutes = applyBigIPRoutes
	}
	if err := applyRoutes(ctx.Ctx, ctx, podCIDRs(ctx)); err != nil {
		return fmt.Errorf("bigip-cis: adding BIG-IP pod routes: %w", err)
	}
	return nil
}

var virtualServerGVR = schema.GroupVersionResource{
	Group:    "cis.f5.com",
	Version:  "v1",
	Resource: "virtualservers",
}

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	d := s.vDeps
	if d == nil {
		real := realVerifyDeps()
		d = &real
	}
	ns := namespace(ctx)
	res := scenarios.Result{}

	// --- Gating: the BIG-IP must be onboarded. ---
	// Mirror how ingressmigration surfaces missing jumphost state: a single
	// clear assertion + early return, not a panic.
	if ctx.Cluster != nil && !ctx.Cluster.BigIPVEEnabled() {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "bigipVE enabled in cluster.yaml",
			OK:          false,
			Got:         "bigipVE is not enabled — run `awsbnkctl up` with bigipVE.enabled=true first",
		})
		return scenarios.FinalizeResult(res)
	}
	if ctx.State.Get("BIGIP_ONBOARDED") != "true" {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "BIG-IP onboarded (BIGIP_ONBOARDED=true)",
			OK:          false,
			Got:         "BIG-IP not onboarded — run `awsbnkctl up` with bigipVE.enabled=true first",
		})
		return scenarios.FinalizeResult(res)
	}

	// --- Step 1: control-plane readiness ---
	err := d.WaitDeploymentAvailableFn(ctx.Ctx, ctx, ns, "cis-backend", 3*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "cis-backend Deployment Available",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	err = d.WaitDeploymentAvailableFn(ctx.Ctx, ctx, cisNS, cisControllerDeploy, cisVerifyTimeout)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "CIS controller Deployment Available",
		OK:          err == nil,
		Got:         scenarios.ErrString(err),
	})

	// --- Step 2: best-effort VS-programmed check (non-fatal) ---
	vsOK, vsDetail := d.CheckVSProgrammedFn(ctx.Ctx, ctx, ns, "cis-migration-vs")
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "CIS programmed VirtualServer (best-effort, non-fatal)",
		OK:          true, // never fails the demo — informational only
		Got:         fmt.Sprintf("programmed=%v: %s", vsOK, vsDetail),
	})

	// --- Step 3: the real assertion — curl the EXTERNAL BIG-IP VIP ---
	instanceID := ctx.State.Get("JUMPHOST_INSTANCE_ID")
	sourceIP := ctx.State.Get("JUMPHOST_BNK_EXT_ENI_IP")
	if instanceID == "" || sourceIP == "" {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "BIG-IP leg: jumphost state keys present",
			OK:          false,
			Got:         "JUMPHOST_INSTANCE_ID / JUMPHOST_BNK_EXT_ENI_IP missing from state.env — run `awsbnkctl up` with testing.jumphost.enabled=true",
		})
		return scenarios.FinalizeResult(res)
	}

	vip := resolveVIP(ctx)
	bnkOK, bnkBody := d.RunBNKProbeFn(ctx.Ctx, ctx, vip, 10*time.Second)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("BIG-IP leg: HTTP 200 from VIP %s (Host: %s)", vip, scnHost),
		OK:          bnkOK,
		Got:         bnkBody,
	})

	return scenarios.FinalizeResult(res)
}

func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	// helm uninstall CIS (tolerates not-found via IgnoreNotFound).
	hr := s.helm
	if hr == nil {
		hr = newRealHelmRunner(ctx.KubeconfigPath)
	}
	if err := hr.UninstallRelease(cisRelease, cisNS); err != nil {
		fmt.Fprintf(ctx.Out, "[demo/bigip-cis] warn: helm uninstall %s: %v\n", cisRelease, err)
	}

	ns := namespace(ctx)

	// Delete the VirtualServer CR (CIS removes its own BIG-IP VS objects in the
	// cis partition when the CR is deleted). Tolerate missing.
	if ctx.Dynamic != nil {
		delErr := ctx.Dynamic.Resource(virtualServerGVR).Namespace(ns).Delete(ctx.Ctx, "cis-migration-vs", metav1.DeleteOptions{})
		if delErr != nil && !scenarios.IsNotFound(delErr) {
			fmt.Fprintf(ctx.Out, "[demo/bigip-cis] warn: delete VirtualServer: %v\n", delErr)
		}
	}

	// Delete the bigip-login Secret. Tolerate missing.
	if ctx.Clientset != nil {
		secErr := ctx.Clientset.CoreV1().Secrets(ns).Delete(ctx.Ctx, bigipLoginSecret, metav1.DeleteOptions{})
		if secErr != nil && !scenarios.IsNotFound(secErr) {
			fmt.Fprintf(ctx.Out, "[demo/bigip-cis] warn: delete %s secret: %v\n", bigipLoginSecret, secErr)
		}
	}

	// Best-effort remove the BIG-IP routes added in Apply (via jumphost).
	removeRoutes := s.removeRoutesFn
	if removeRoutes == nil {
		removeRoutes = removeBigIPRoutes
	}
	if err := removeRoutes(ctx.Ctx, ctx, podCIDRs(ctx)); err != nil {
		fmt.Fprintf(ctx.Out, "[demo/bigip-cis] warn: removing BIG-IP routes: %v\n", err)
	}

	// Delete the demo namespace (idempotent).
	if ctx.Clientset != nil {
		err := ctx.Clientset.CoreV1().Namespaces().Delete(ctx.Ctx, ns, metav1.DeleteOptions{})
		if err != nil && !scenarios.IsNotFound(err) {
			return fmt.Errorf("deleting namespace %s: %w", ns, err)
		}
	}
	return nil
}

func (s *scenario) Namespace(ctx *scenarios.Context) string { return namespace(ctx) }

// cisHelmRelease builds the f5-bigip-ctlr release pointed at the onboarded
// BIG-IP. mgmtIP is the BIG-IP management IP read from state (BIGIP_MGMT_IP).
func cisHelmRelease(mgmtIP string) helmRelease {
	return helmRelease{
		ReleaseName: cisRelease,
		Namespace:   cisNS,
		RepoURL:     cisRepoURL,
		Chart:       cisChart,
		Version:     cisVersion,
		Values: map[string]interface{}{
			"bigip_login_secret": bigipLoginSecret,
			// version is the chart's top-level controller image tag key. The chart
			// does NOT read image.tag, so the tag must be pinned here or the pod
			// pulls k8s-bigip-ctlr:latest.
			"version": cisImageTag,
			"rbac": map[string]interface{}{
				"create": true,
			},
			"image": map[string]interface{}{
				"user":       "f5networks",
				"repo":       "k8s-bigip-ctlr",
				"pullPolicy": "Always",
			},
			"args": map[string]interface{}{
				"bigip_url":            mgmtIP,
				"bigip_partition":      bigipCISPartition,
				"pool_member_type":     "cluster",
				"custom_resource_mode": true,
				"insecure":             true,
				"log_level":            "INFO",
			},
		},
	}
}

// --- bigip-login secret ---

// getBigIPPassword reads the BIG-IP admin password from the environment. It is
// a seam-free read (env only) so the password never travels via state/argv.
var getBigIPPassword = func() string { return os.Getenv(bigipPasswordEnv) }

// createBigIPLoginSecret creates the basic-auth-style bigip-login Opaque Secret
// in the CIS namespace via the typed client. The password is supplied as an
// argument (read from env by the caller) and is NEVER written to a manifest on
// disk. Idempotent: if the Secret already exists it is updated in place.
func createBigIPLoginSecret(ctx context.Context, sctx *scenarios.Context, password string) error {
	if sctx.Clientset == nil {
		return fmt.Errorf("clientset is nil — cannot create %s secret", bigipLoginSecret)
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bigipLoginSecret,
			Namespace: cisNS,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"username": []byte(bigipUser),
			"password": []byte(password),
		},
	}
	_, err := sctx.Clientset.CoreV1().Secrets(cisNS).Create(ctx, sec, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !isAlreadyExists(err) {
		return err
	}
	// Already exists — update in place so a re-run picks up a rotated password.
	_, upErr := sctx.Clientset.CoreV1().Secrets(cisNS).Update(ctx, sec, metav1.UpdateOptions{})
	return upErr
}

func isAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

// --- BIG-IP static routes (jumphost → BIG-IP SSH, mirrors phase17f) ---

// bigipPEMRemotePath mirrors phase17f: the BIG-IP SSH key staged on the jumphost
// by onboarding. This demo reuses it (onboarding runs in `up`, before any demo).
const bigipPEMRemotePath = "/home/ec2-user/bigip.pem"

// applyBigIPRoutes creates one BIG-IP `net route` per pod/private CIDR via the
// internal subnet gateway, driven through the jumphost. Idempotent: a route that
// already exists is treated as success (same tolerant-create shape as phase17f).
func applyBigIPRoutes(ctx context.Context, sctx *scenarios.Context, cidrs []string) error {
	mgmtIP := sctx.State.Get("BIGIP_MGMT_IP")
	if mgmtIP == "" {
		return fmt.Errorf("BIGIP_MGMT_IP not in state")
	}
	gw := internalGateway(sctx)
	if gw == "" {
		return fmt.Errorf("could not derive internal subnet gateway from network.dataPath.internal.cidr")
	}
	opts := jumphost.ProbeOptions{
		Region:     sctx.Cluster.Metadata.Region,
		InstanceID: sctx.State.Get("JUMPHOST_INSTANCE_ID"),
	}
	var cmds []string
	for i, cidr := range cidrs {
		name := fmt.Sprintf("%s%d", bigipRouteNamePrefix, i)
		create := fmt.Sprintf("tmsh create net route %s network %s gw %s", name, cidr, gw)
		cmds = append(cmds, bigipSSH(mgmtIP, tolerantCreate(create)))
	}
	cmds = append(cmds, bigipSSH(mgmtIP, "tmsh save sys config"))
	_, err := jumphost.RunStagingCommands(ctx, opts, cmds)
	return err
}

// removeBigIPRoutes best-effort deletes the per-CIDR routes added by Apply. A
// missing route is tolerated. Errors are returned so Cleanup can warn-and-continue.
func removeBigIPRoutes(ctx context.Context, sctx *scenarios.Context, cidrs []string) error {
	mgmtIP := sctx.State.Get("BIGIP_MGMT_IP")
	if mgmtIP == "" {
		return nil // nothing could have been created without an onboarded BIG-IP
	}
	opts := jumphost.ProbeOptions{
		Region:     sctx.Cluster.Metadata.Region,
		InstanceID: sctx.State.Get("JUMPHOST_INSTANCE_ID"),
	}
	var cmds []string
	for i := range cidrs {
		name := fmt.Sprintf("%s%d", bigipRouteNamePrefix, i)
		// Tolerate missing route: `|| true` so a not-found delete is not fatal.
		del := fmt.Sprintf("tmsh delete net route %s 2>/dev/null || true", name)
		cmds = append(cmds, bigipSSH(mgmtIP, del))
	}
	cmds = append(cmds, bigipSSH(mgmtIP, "tmsh save sys config"))
	_, err := jumphost.RunStagingCommands(ctx, opts, cmds)
	return err
}

// bigipSSH wraps a BIG-IP-side command in the jumphost-side ssh invocation —
// the operator host cannot reach the BIG-IP mgmt IP, so all BIG-IP access is
// `ssh -i <pem> admin@<mgmt>` run ON the jumphost (mirrors phase17f.bigipSSH).
func bigipSSH(mgmtIP, remoteCmd string) string {
	return fmt.Sprintf(
		"ssh -i %s -o StrictHostKeyChecking=no -o ConnectTimeout=15 admin@%s %s",
		bigipPEMRemotePath, mgmtIP, jumphost.ShellSingleQuote(remoteCmd),
	)
}

// tolerantCreate wraps a tmsh create so a pre-existing object is treated as
// success while genuine failures propagate (mirrors phase17f.bigipTolerantCreate).
func tolerantCreate(createCmd string) string {
	return fmt.Sprintf(
		`__out=$({ %s ; } 2>&1); __rc=$?; `+
			`if [ $__rc -eq 0 ]; then :; `+
			`elif printf '%%s' "$__out" | grep -qE 'already exists|01020066'; then :; `+
			`else printf '%%s\n' "$__out" >&2; exit $__rc; fi`,
		createCmd,
	)
}

// --- probes ---

// runBNKProbe issues a single curl probe at the BIG-IP VIP from the jumphost via
// SSH+EICE, asserting HTTP 200 + the whoami marker (mirrors ingressmigration).
func runBNKProbe(ctx context.Context, sctx *scenarios.Context, vip string, timeout time.Duration) (ok bool, body string) {
	sourceIP := sctx.State.Get("JUMPHOST_BNK_EXT_ENI_IP")
	timeoutSecs := int(timeout.Seconds())
	if timeoutSecs < 5 {
		timeoutSecs = 5
	}

	codeCmd := fmt.Sprintf(
		`curl -s -o /dev/null -w '%%{http_code}' -H 'Host: %s' --interface %s --max-time %d http://%s/`,
		scnHost, sourceIP, timeoutSecs, vip,
	)
	bodyCmd := fmt.Sprintf(
		`curl -s -H 'Host: %s' --interface %s --max-time %d http://%s/`,
		scnHost, sourceIP, timeoutSecs, vip,
	)

	probeOpts := jumphost.ProbeOptions{
		Region:     sctx.Cluster.Metadata.Region,
		InstanceID: sctx.State.Get("JUMPHOST_INSTANCE_ID"),
		SourceIP:   sourceIP,
		VIP:        vip,
		Timeout:    timeout,
		Hostname:   scnHost,
	}

	out, err := jumphost.RunStagingCommands(ctx, probeOpts, []string{codeCmd, bodyCmd})
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

// checkVSProgrammed best-effort reads the VirtualServer CR status to see whether
// CIS reported it Ok. Non-fatal: any error / missing status just yields ok=false
// with a detail string. Returns ok=false (not an error) when Dynamic is nil.
func checkVSProgrammed(ctx context.Context, sctx *scenarios.Context, ns, name string) (ok bool, detail string) {
	if sctx.Dynamic == nil {
		return false, "dynamic client nil — skipped"
	}
	obj, err := sctx.Dynamic.Resource(virtualServerGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, "VirtualServer get: " + scenarios.ErrString(err)
	}
	status, found := obj.Object["status"].(map[string]interface{})
	if !found {
		return false, "VirtualServer has no .status yet"
	}
	vsStatus, _ := status["status"].(string)
	if strings.EqualFold(vsStatus, "Ok") {
		return true, "status.status=Ok"
	}
	return false, fmt.Sprintf("status.status=%q", vsStatus)
}

// --- internal helpers ---

func namespace(ctx *scenarios.Context) string {
	if v := ctx.Options["namespace"]; v != "" {
		return v
	}
	return scnNamespace
}

// resolveVIP returns the BIG-IP virtual-server VIP. Honors Options["vip"], then
// state BIGIP_VIP (set by F2-B / phase17e), then the package default.
func resolveVIP(ctx *scenarios.Context) string {
	if v := ctx.Options["vip"]; v != "" {
		return v
	}
	if ctx.State != nil {
		if v := ctx.State.Get("BIGIP_VIP"); v != "" {
			return v
		}
	}
	return scnDefaultVIP
}

func buildManifestVars(ctx *scenarios.Context) manifestVars {
	return manifestVars{
		Namespace: namespace(ctx),
		VIP:       resolveVIP(ctx),
		Host:      scnHost,
	}
}

// podCIDRs returns the private subnet CIDRs (where pods + nodes live) from the
// cluster intent. CIS programs pod IPs in these ranges; the BIG-IP needs routes
// to reach them.
func podCIDRs(ctx *scenarios.Context) []string {
	var cidrs []string
	if ctx.Cluster == nil {
		return cidrs
	}
	for _, s := range ctx.Cluster.Network.Subnets.Private {
		if s.CIDR != "" {
			cidrs = append(cidrs, s.CIDR)
		}
	}
	return cidrs
}

// internalGateway returns the gateway IP for the internal data-path subnet —
// AWS subnet gateways are always the network base + 1 (.1). The BIG-IP's
// internal self-IP is on this subnet, so pod-subnet routes egress via this gw.
func internalGateway(ctx *scenarios.Context) string {
	if ctx.Cluster == nil || ctx.Cluster.Network.DataPath == nil {
		return ""
	}
	cidr := ctx.Cluster.Network.DataPath.Internal.CIDR
	return gatewayFromCIDR(cidr)
}

// gatewayFromCIDR derives the AWS subnet gateway (.1) from a CIDR like
// "10.0.20.0/24" → "10.0.20.1". Returns "" on a malformed CIDR.
func gatewayFromCIDR(cidr string) string {
	slash := strings.IndexByte(cidr, '/')
	if slash < 0 {
		return ""
	}
	ip := cidr[:slash]
	lastDot := strings.LastIndexByte(ip, '.')
	if lastDot < 0 {
		return ""
	}
	return ip[:lastDot] + ".1"
}
