package phases

import (
	"context"
	"fmt"
	"os"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

// Phase13Postflight runs smoke checks against the BNK k8s foundation installed
// by Phase12. It is a pure read phase — it does not write state or call Save().
//
// Checks:
//  1. All four namespaces exist.
//  2. cert-manager Deployments are all ready (AvailableReplicas == Replicas).
//  3. CA Certificate has Ready condition True.
//  4. FAR secret exists in all four target namespaces.
//  5. (Optional) If forge enabled, trigger scan_cluster best-effort.
//  6. FLO Deployment ready (when FLO enabled).
//  7. cneinstances.k8s.f5.com CRD exists (when FLO enabled).
//  8. OTEL certs Ready (when FLO enabled).
//  9. CNEInstance exists with status.state ∈ {Ready, Running}, or F5Tmm+CNEController sub-conditions True (when Phase 25 ran).
//
// 10. License bnk-license exists with status.state=Active (when Phase 25 ran).
//
// D-005: CheckAuthOrDie is called at entry per convention.
func Phase13Postflight(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 13] postflight: cluster=%s\n", name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 13] dry-run: would verify: namespaces, cert-manager Deployments, CA cert, FAR secrets, FLO Deployment, CNEInstance CRD, OTEL certs, CNEInstance state, License state")
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase13: Clients.K8s is nil — call clients.AttachK8s(kubeconfigPath) after phase 11")
	}

	vars := render.CertChainVarsFromCluster(cl)

	// 1. Verify namespaces.
	for _, ns := range bnkNamespaces {
		if _, err := clients.K8s.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err != nil {
			return fmt.Errorf("phase13: namespace %s: %w", ns, err)
		}
	}
	fmt.Fprintln(os.Stderr, "[phase 13] namespaces OK")

	// 2. Verify cert-manager Deployments.
	certManagerDeployments := []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"}
	for _, dep := range certManagerDeployments {
		avail, desired, err := k8swait.DeploymentReplicaStatus(ctx, clients.K8s, certManagerNS, dep)
		if err != nil {
			return fmt.Errorf("phase13: cert-manager deployment %s: %w", dep, err)
		}
		if desired == 0 || avail != desired {
			return fmt.Errorf("phase13: cert-manager deployment %s not ready: available=%d desired=%d", dep, avail, desired)
		}
	}
	fmt.Fprintln(os.Stderr, "[phase 13] cert-manager Deployments OK")

	// 3. Verify CA Certificate Ready.
	obj, err := clients.Dynamic.Resource(k8swait.CertificateGVR).Namespace(certManagerNS).Get(ctx, vars.CACertName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("phase13: get CA certificate %s: %w", vars.CACertName, err)
	}
	if !k8swait.IsCertificateReady(obj.Object) {
		return fmt.Errorf("phase13: CA certificate %s Ready condition is not True", vars.CACertName)
	}
	fmt.Fprintf(os.Stderr, "[phase 13] CA certificate %s Ready\n", vars.CACertName)

	// 4. Verify FAR secret in all four namespaces.
	for _, ns := range farSecretNamespaces {
		if _, err := clients.K8s.CoreV1().Secrets(ns).Get(ctx, farSecretName, metav1.GetOptions{}); err != nil {
			return fmt.Errorf("phase13: FAR secret %s in namespace %s: %w", farSecretName, ns, err)
		}
	}
	fmt.Fprintln(os.Stderr, "[phase 13] FAR secrets OK")

	// 5. Optional forge scan_cluster (best-effort).
	if cl.Forge != nil && cl.Forge.Enabled && clients.ForgeClient != nil {
		if err := triggerForgeScanCluster(ctx, cl, st, clients); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 13] forge scan_cluster: warning (non-fatal): %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "[phase 13] forge scan_cluster triggered OK")
		}
	}

	// 6–8. FLO + CNEInstance CRD + OTEL cert checks (only when FLO is enabled).
	var floSpec *intent.FloSpec
	if cl.Addons != nil {
		floSpec = cl.Addons.Flo
	}
	if floSpec.FloEnabled() {
		// 6. Verify FLO Deployment is Available.
		avail, desired, err := k8swait.DeploymentReplicaStatus(ctx, clients.K8s, operatorNS, floDeployName)
		if err != nil {
			return fmt.Errorf("phase13: FLO deployment %s/%s: %w", operatorNS, floDeployName, err)
		}
		if desired == 0 || avail != desired {
			return fmt.Errorf("phase13: FLO deployment %s/%s not ready: available=%d desired=%d",
				operatorNS, floDeployName, avail, desired)
		}
		fmt.Fprintf(os.Stderr, "[phase 13] FLO deployment %s OK\n", floDeployName)

		// 7. Verify cneinstances.k8s.f5.com CRD exists.
		crdGVR := schema.GroupVersionResource{
			Group:    "apiextensions.k8s.io",
			Version:  "v1",
			Resource: "customresourcedefinitions",
		}
		if _, err := clients.Dynamic.Resource(crdGVR).Get(ctx, cneCRDName, metav1.GetOptions{}); err != nil {
			return fmt.Errorf("phase13: CRD %s: %w", cneCRDName, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 13] CRD %s OK\n", cneCRDName)

		// 8. Verify both OTEL certs Ready.
		for _, certName := range []string{otelSvrCertName, otelF5IngCertName} {
			obj, err := clients.Dynamic.Resource(k8swait.CertificateGVR).Namespace(operatorNS).Get(ctx, certName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("phase13: OTEL Certificate %s/%s: %w", operatorNS, certName, err)
			}
			if !k8swait.IsCertificateReady(obj.Object) {
				return fmt.Errorf("phase13: OTEL Certificate %s/%s Ready condition is not True", operatorNS, certName)
			}
			fmt.Fprintf(os.Stderr, "[phase 13] OTEL Certificate %s/%s Ready\n", operatorNS, certName)
		}
	}

	// 9–10. CNEInstance + License postflight (only when Phase 25 polling succeeded).
	// When --skip-activation-poll was used, CNEINSTANCE_READY_AT is absent — log a warning only.
	cneReadyAt := st.Get("CNEINSTANCE_READY_AT")
	if cneReadyAt != "" {
		if err := checkCNEInstanceActive(ctx, cl, clients); err != nil {
			return fmt.Errorf("phase13: %w", err)
		}
		if err := checkLicenseActive(ctx, clients); err != nil {
			return fmt.Errorf("phase13: %w", err)
		}
	} else {
		fmt.Fprintln(os.Stderr,
			"[phase 13] warning: CNEINSTANCE_READY_AT not set — activation poll skipped or did not complete; CNEInstance/License state not verified")
	}

	fmt.Fprintf(os.Stderr, "✓ postflight OK: cert-manager v%s ready, CA cert active, FAR secret in 4 ns\n",
		intent.EmbeddedCertManagerVersion)
	return nil
}

// checkCNEInstanceActive reads the CNEInstance CR and verifies status.state ∈ {Ready, Running},
// or, when state is empty, the F5TmmAvailable + CNEControllerAvailable sub-conditions (H1).
// Read-only; does not write state.
func checkCNEInstanceActive(ctx context.Context, cl *intent.Cluster, clients *Clients) error {
	crName := cl.Metadata.Name + "-bnk"
	obj, err := clients.Dynamic.Resource(cneinstanceGVR).Namespace(InstanceNamespace).Get(ctx, crName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("CNEInstance %s/%s: %w", InstanceNamespace, crName, err)
	}
	var state string
	if rawStatus, ok := obj.Object["status"].(map[string]interface{}); ok {
		state, _ = rawStatus["state"].(string)
	}
	// H1: mirror Phase 25's gate. status.state is empty on BNK 2.3.x / the
	// aws-syd-test gold reference even when traffic flows; the real readiness
	// signal is the F5TmmAvailable + CNEControllerAvailable sub-conditions.
	// See phase25_activation_poll.go cneFunctionallyReady() and
	// memory: project_sydney_reference_baseline.
	if !isCNEReady(state) && !cneFunctionallyReady(obj.Object) {
		return fmt.Errorf("CNEInstance %s/%s status.state=%q and F5TmmAvailable+CNEControllerAvailable not both True (want Ready/Running state or functional readiness)",
			InstanceNamespace, crName, state)
	}
	fmt.Fprintf(os.Stderr, "[phase 13] CNEInstance %s/%s state=%q functionallyReady=%v OK\n",
		InstanceNamespace, crName, state, cneFunctionallyReady(obj.Object))
	return nil
}

// checkLicenseActive reads the License CR and verifies status.state=Active.
// Read-only; does not write state.
func checkLicenseActive(ctx context.Context, clients *Clients) error {
	obj, err := clients.Dynamic.Resource(licenseGVR).Namespace(OperatorNamespace).Get(ctx, licenseCRName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("license %s/%s: %w", OperatorNamespace, licenseCRName, err)
	}
	state := ""
	if rawStatus, ok := obj.Object["status"].(map[string]interface{}); ok {
		state, _ = rawStatus["state"].(string)
	}
	if state != "Active" {
		return fmt.Errorf("license %s/%s status.state=%q, want Active", OperatorNamespace, licenseCRName, state)
	}
	fmt.Fprintf(os.Stderr, "[phase 13] License %s/%s state=Active OK\n", OperatorNamespace, licenseCRName)
	return nil
}

// Phase13PostflightDown is a no-op. Postflight has no resources to clean up.
func Phase13PostflightDown(_ context.Context, _ *intent.Cluster, _ *state.State, _ *Clients) error {
	return nil
}

// triggerForgeScanCluster calls forge scan_cluster for the registered cluster.
// Reads FORGE_CLUSTER_ID from state (written by Phase09). Best-effort: a missing
// or unparseable ID is logged and the function returns nil so postflight continues.
func triggerForgeScanCluster(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	if clients.ForgeClient == nil {
		return fmt.Errorf("forge client is nil")
	}
	rawID := st.Get("FORGE_CLUSTER_ID")
	if rawID == "" {
		fmt.Fprintf(os.Stderr, "[phase 13] forge scan_cluster: FORGE_CLUSTER_ID not in state — skipping (run `awsbnkctl forge register` first)\n")
		return nil
	}
	clusterID, err := strconv.Atoi(rawID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[phase 13] forge scan_cluster: FORGE_CLUSTER_ID=%q is not an integer — skipping\n", rawID)
		return nil
	}
	// ScanCluster triggers forge's post-registration scan; advisory only after up.
	if _, err := clients.ForgeClient.ScanCluster(ctx, clusterID); err != nil {
		fmt.Fprintf(os.Stderr, "[phase 13] forge scan_cluster: cluster=%s id=%d warning: %v\n",
			cl.Metadata.Name, clusterID, err)
	}
	return nil
}

// Note: IsCertificateReady and CertificateGVR are defined in internal/k8s/wait.go
// and accessed via the k8swait alias above.
