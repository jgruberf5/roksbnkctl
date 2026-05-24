package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

const (
	otelSvrCertName      = "external-otelsvr"
	otelF5IngCertName    = "external-f5ingotelsvr"
	otelCertsYAMLPath    = "shared/otel-certs.yaml"
	otelCertReadyTimeout = 3 * time.Minute
	phase15FieldManager  = "awsbnkctl-phase15"
)

// Phase15OTELCerts applies two cert-manager Certificate CRs for OTEL:
//   - external-otelsvr
//   - external-f5ingotelsvr
//
// Both are signed by the slice-5 <cluster>-ca-cluster-issuer and created in
// the f5-cne-core namespace. Phase 15 waits until both are Ready=True.
//
// D-005: CheckAuthOrDie is called at entry.
func Phase15OTELCerts(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 15] OTEL certs: cluster=%s\n", name)

	// Check FLO enabled — OTEL certs are FLO prerequisites.
	var floSpec *intent.FloSpec
	if cl.Addons != nil {
		floSpec = cl.Addons.Flo
	}
	if !floSpec.FloEnabled() {
		fmt.Fprintln(os.Stderr, "[phase 15] FLO disabled (addons.flo.enabled: false), skipping OTEL certs")
		return nil
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 15] dry-run: would create Certificate %s/%s in %s\n",
			operatorNS, otelSvrCertName, operatorNS)
		fmt.Fprintf(os.Stderr, "[phase 15] dry-run: would create Certificate %s/%s in %s\n",
			operatorNS, otelF5IngCertName, operatorNS)
		st.Set("OTEL_SVR_CERT_NAME", "dry-run")
		st.Set("OTEL_F5ING_CERT_NAME", "dry-run")
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase15: Clients.K8s is nil — call clients.AttachK8s(kubeconfigPath) after phase 11")
	}

	// Load + render template.
	certsTmpl, err := k8smanifests.FS.ReadFile(otelCertsYAMLPath)
	if err != nil {
		return fmt.Errorf("phase15: reading embedded otel-certs template: %w", err)
	}
	rendered, err := render.RenderOTELCerts(certsTmpl, cl)
	if err != nil {
		return fmt.Errorf("phase15: rendering otel-certs template: %w", err)
	}

	// Apply via dynamic client (same applyRawYAML helper as phase 12).
	fmt.Fprintln(os.Stderr, "[phase 15] applying OTEL Certificate CRs")
	if err := applyRawYAML(ctx, clients, rendered); err != nil {
		return fmt.Errorf("phase15: applying OTEL certs: %w", err)
	}

	return waitForOTELCerts(ctx, cl, clients, st)
}

// waitForOTELCerts polls until both OTEL certs are Ready, then sets state keys.
// Extracted for testability — the apply step (SSA) works only on a real cluster;
// the waiter can be unit-tested against the dynamic fake with pre-seeded certs.
func waitForOTELCerts(ctx context.Context, _ *intent.Cluster, clients *Clients, st *state.State) error {
	for _, certName := range []string{otelSvrCertName, otelF5IngCertName} {
		fmt.Fprintf(os.Stderr, "[phase 15] waiting for Certificate %s/%s Ready\n", operatorNS, certName)
		if err := k8swait.WaitForCertificateReady(ctx, clients.Dynamic, operatorNS, certName, otelCertReadyTimeout); err != nil {
			return fmt.Errorf("phase15: Certificate %s/%s not ready: %w", operatorNS, certName, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 15] Certificate %s/%s is Ready\n", operatorNS, certName)
	}

	st.Set("OTEL_SVR_CERT_NAME", otelSvrCertName)
	st.Set("OTEL_F5ING_CERT_NAME", otelF5IngCertName)
	return st.Save()
}

// Phase15OTELCertsDown deletes both OTEL Certificate CRs. Tolerates NotFound.
func Phase15OTELCertsDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 15 down] OTEL certs: cluster=%s\n", name)

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 15 down] warning: dynamic client not available, skipping OTEL cert deletion")
		clearPhase15State(st)
		return st.Save()
	}

	certGVR := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}

	for _, certName := range []string{otelSvrCertName, otelF5IngCertName} {
		fmt.Fprintf(os.Stderr, "[phase 15 down] deleting Certificate %s/%s\n", operatorNS, certName)
		err := clients.Dynamic.Resource(certGVR).Namespace(operatorNS).Delete(ctx, certName, metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 15 down] warning: delete Certificate %s/%s: %v\n", operatorNS, certName, err)
		} else if k8serrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 15 down] Certificate %s/%s already gone\n", operatorNS, certName)
		} else {
			fmt.Fprintf(os.Stderr, "[phase 15 down] deleted Certificate %s/%s\n", operatorNS, certName)
		}
	}

	clearPhase15State(st)
	return st.Save()
}

// clearPhase15State zeroes all phase 15 state keys.
func clearPhase15State(st *state.State) {
	for _, k := range []string{"OTEL_SVR_CERT_NAME", "OTEL_F5ING_CERT_NAME"} {
		st.Set(k, "")
	}
}
