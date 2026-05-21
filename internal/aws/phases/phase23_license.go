package phases

import (
	"context"
	"fmt"
	"os"
	"strings"
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
	licenseCRYAMLPath = "shared/license-cr.yaml.tmpl"
	licenseCRDName    = "licenses.k8s.f5net.com"
	licenseCRName     = "bnk-license"
	phase23CRDWait    = 10 * time.Minute
)

// licenseGVR is the GVR for the License CR.
var licenseGVR = schema.GroupVersionResource{
	Group:    "k8s.f5net.com",
	Version:  "v1",
	Resource: "licenses",
}

// Phase23License waits for the licenses.k8s.f5net.com CRD (installed by the
// FLO operator in Phase 14), then renders and applies the License CR into
// f5-cne-core (OperatorNamespace).
//
// The JWT file is read from cl.Bnk.JWT and inlined as the raw token string.
//
// State keys written:
//   - LICENSE_CRD_READY_AT
//   - LICENSE_APPLIED_AT
//   - LICENSE_NAME
//
// D-005: CheckAuthOrDie called at entry.
func Phase23License(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 23] License: cluster=%s\n", name)

	if dryRun {
		fmt.Fprintf(os.Stderr,
			"[phase 23] dry-run: would wait for %s CRD then apply License %s in %s\n",
			licenseCRDName, licenseCRName, OperatorNamespace)
		st.Set("LICENSE_CRD_READY_AT", "dry-run")
		st.Set("LICENSE_APPLIED_AT", "dry-run")
		st.Set("LICENSE_NAME", licenseCRName)
		return nil
	}

	if clients.Dynamic == nil {
		return fmt.Errorf("phase23: Clients.Dynamic is nil — call clients.AttachK8s(kubeconfigPath) first")
	}

	// Wait for the License CRD to exist (installed by FLO).
	fmt.Fprintf(os.Stderr, "[phase 23] waiting for CRD %s (up to %s)\n", licenseCRDName, phase23CRDWait)
	if err := k8swait.WaitForCRDExists(ctx, clients.Dynamic, licenseCRDName, phase23CRDWait); err != nil {
		return fmt.Errorf("phase23: waiting for License CRD: %w", err)
	}
	st.Set("LICENSE_CRD_READY_AT", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(os.Stderr, "[phase 23] CRD %s ready\n", licenseCRDName)

	// Read and trim the JWT file.
	jwtRaw, err := os.ReadFile(cl.Bnk.JWT)
	if err != nil {
		return fmt.Errorf("phase23: reading JWT file %s: %w", cl.Bnk.JWT, err)
	}
	jwt := strings.TrimSpace(string(jwtRaw))

	// Load + render template.
	tmplBytes, err := k8smanifests.FS.ReadFile(licenseCRYAMLPath)
	if err != nil {
		return fmt.Errorf("phase23: reading embedded license-cr template: %w", err)
	}
	rendered, err := render.RenderLicenseCR(tmplBytes, cl, jwt)
	if err != nil {
		return fmt.Errorf("phase23: rendering license-cr: %w", err)
	}

	// Apply via dynamic client (SSA).
	fmt.Fprintf(os.Stderr, "[phase 23] applying License %s in %s\n", licenseCRName, OperatorNamespace)
	if err := applyRawYAML(ctx, clients.Dynamic, rendered); err != nil {
		return fmt.Errorf("phase23: applying License CR: %w", err)
	}

	st.Set("LICENSE_APPLIED_AT", time.Now().UTC().Format(time.RFC3339))
	st.Set("LICENSE_NAME", licenseCRName)
	return st.Save()
}

// Phase23LicenseDown deletes the License CR from f5-cne-core.
// Tolerates NotFound. CRD cleanup happens at FLO/CNEInstance down time.
func Phase23LicenseDown(ctx context.Context, _ *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintf(os.Stderr, "[phase 23 down] License: deleting %s from %s\n", licenseCRName, OperatorNamespace)

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 23 down] warning: dynamic client not available, skipping License deletion")
		clearPhase23State(st)
		return st.Save()
	}

	err := clients.Dynamic.Resource(licenseGVR).Namespace(OperatorNamespace).Delete(ctx, licenseCRName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 23 down] warning: delete License %s/%s: %v\n",
			OperatorNamespace, licenseCRName, err)
	} else if k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 23 down] License %s/%s already gone\n",
			OperatorNamespace, licenseCRName)
	} else {
		fmt.Fprintf(os.Stderr, "[phase 23 down] deleted License %s/%s\n",
			OperatorNamespace, licenseCRName)
	}

	clearPhase23State(st)
	return st.Save()
}

// clearPhase23State zeroes all phase 23 state keys.
func clearPhase23State(st *state.State) {
	for _, k := range []string{"LICENSE_CRD_READY_AT", "LICENSE_APPLIED_AT", "LICENSE_NAME"} {
		st.Set(k, "")
	}
}
