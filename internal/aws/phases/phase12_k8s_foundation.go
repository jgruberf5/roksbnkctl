package phases

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

// bnkNamespaces is the ordered set of namespaces Phase12 creates (12.1).
// These match aws-gpu-setup's vars.env: OPERATOR_NS, INSTANCE_NS, UTILS_NS.
// cert-manager must come first (cert-manager YAML depends on it pre-existing or
// lets the static YAML create it — we create it explicitly for idempotency).
// f5-cne-system is the slice-7 INSTANCE_NS target for the CNEInstance CR and
// related resources (cloud-network-mapping CM, NADs, IRSA SA).
var bnkNamespaces = []string{
	"cert-manager",
	"f5-cne-core",
	"f5-cne-system",
	"f5-bnk-instance",
	"f5-utils",
}

// farSecretNamespaces is the set of namespaces that receive the FAR pull secret
// (12.2). The `default` ns is included because some pull controllers run there.
// f5-cne-system is included because the CNEInstance.spec.registry.imagePullSecrets
// references far-secret in its own namespace for image pulls.
var farSecretNamespaces = []string{
	"f5-cne-core",
	"f5-cne-system",
	"f5-bnk-instance",
	"f5-utils",
	"default",
}

const (
	farSecretName = "far-secret"
	// #nosec G101 -- this is a k8s Secret RESOURCE NAME, not a credential value
	licenseSecretName = "bnk-license-jwt"
	certManagerNS     = "cert-manager"
	operatorNS        = "f5-cne-core"

	certManagerYAMLPath = "cert-manager/cert-manager-v1.16.1.yaml"
	certChainYAMLPath   = "shared/bnk-cert-chain.yaml"
	multusYAMLPath      = "multus/multus-daemonset-v4.2.4.yaml"
	multusDaemonSet     = "kube-multus-ds"
	multusNamespace     = "kube-system"
	multusVersion       = "v4.2.4"

	phase12FieldManager = "awsbnkctl-phase12"

	// Timeouts
	certManagerDeployTimeout = 5 * time.Minute
	certManagerCRDTimeout    = 2 * time.Minute
	caCertReadyTimeout       = 3 * time.Minute
	multusDaemonSetTimeout   = 5 * time.Minute
	nsTerminateTimeout       = 5 * time.Minute
)

// Phase12K8sFoundation installs the BNK k8s foundation:
//  1. Creates the BNK namespaces (cert-manager, f5-cne-core, f5-cne-system, f5-bnk-instance, f5-utils)
//  2. Loads FAR archive + JWT as k8s Secrets
//  3. Applies cert-manager via embedded upstream YAML; waits for rollout
//  4. Applies the BNK cert chain (ClusterIssuer→Certificate→CAIssuer); waits for CA cert Ready
//
// D-005: CheckAuthOrDie is called at entry even though phase 12 doesn't touch AWS
// (the sentinel may have tripped during earlier EKS waits in the same run).
func Phase12K8sFoundation(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 12] k8s foundation: cluster=%s\n", name)

	// Validate bnk: block is present (required for phase 12+).
	if cl.Bnk == nil {
		return fmt.Errorf("phase12: cluster.yaml must include a 'bnk:' block (see slice-05 docs)")
	}

	farPath, jwtPath, err := resolveBnkFilePaths(cl)
	if err != nil {
		return err
	}

	// Read FAR archive + JWT files (surfaces ENOENT at dry-run time too).
	farData, err := os.ReadFile(farPath) // #nosec G304 -- path is operator-supplied via cluster.yaml bnk.farArchive
	if err != nil {
		return fmt.Errorf("phase12: reading FAR archive %s: %w", farPath, err)
	}
	if len(farData) == 0 {
		return fmt.Errorf("phase12: FAR archive %s is empty", farPath)
	}

	jwtData, err := os.ReadFile(jwtPath) // #nosec G304 -- path is operator-supplied via cluster.yaml bnk.jwt
	if err != nil {
		return fmt.Errorf("phase12: reading JWT %s: %w", jwtPath, err)
	}
	if len(jwtData) == 0 {
		return fmt.Errorf("phase12: JWT file %s is empty", jwtPath)
	}

	vars := render.CertChainVarsFromCluster(cl)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 12] dry-run: would create namespaces: %s\n", strings.Join(bnkNamespaces, ", "))
		for _, ns := range farSecretNamespaces {
			fmt.Fprintf(os.Stderr, "[phase 12] dry-run: would create secret %s/%s (dockerconfigjson)\n", ns, farSecretName)
		}
		fmt.Fprintf(os.Stderr, "[phase 12] dry-run: would create secret %s/%s (opaque/license.jwt)\n", operatorNS, licenseSecretName)
		fmt.Fprintf(os.Stderr, "[phase 12] dry-run: would apply cert-manager v%s\n", intent.EmbeddedCertManagerVersion)
		fmt.Fprintf(os.Stderr, "[phase 12] dry-run: would apply BNK cert chain (issuer=%s, ca=%s)\n",
			vars.SelfSignedIssuer, vars.CACertName)
		st.Set("BNK_NAMESPACES_CREATED", "dry-run-"+strings.Join(bnkNamespaces, ","))
		st.Set("BNK_FAR_SECRET_NAME", "dry-run-"+farSecretName)
		st.Set("BNK_LICENSE_JWT_SECRET", "dry-run-"+licenseSecretName)
		st.Set("CERT_MANAGER_VERSION", "dry-run-"+intent.EmbeddedCertManagerVersion)
		st.Set("BNK_SELFSIGNED_ISSUER", "dry-run-"+vars.SelfSignedIssuer)
		st.Set("BNK_CA_CERT_NAME", "dry-run-"+vars.CACertName)
		st.Set("BNK_CA_SECRET_NAME", "dry-run-"+vars.CASecretName)
		st.Set("BNK_CA_ISSUER", "dry-run-"+vars.CAIssuer)
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase12: Clients.K8s is nil — call clients.AttachK8s(kubeconfigPath) after phase 11")
	}

	// 12.1 — Namespaces.
	if err := ensureNamespaces(ctx, clients, bnkNamespaces); err != nil {
		return fmt.Errorf("phase12: namespaces: %w", err)
	}

	// 12.2 — Supply-chain Secrets.
	if err := applyFARSecrets(ctx, clients, farSecretNamespaces, farData); err != nil {
		return fmt.Errorf("phase12: FAR secrets: %w", err)
	}
	if err := applyLicenseJWTSecret(ctx, clients, operatorNS, jwtData); err != nil {
		return fmt.Errorf("phase12: license JWT secret: %w", err)
	}

	// 12.3 — cert-manager install.
	certManagerYAML, err := k8smanifests.FS.ReadFile(certManagerYAMLPath)
	if err != nil {
		return fmt.Errorf("phase12: reading embedded cert-manager YAML: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 12] applying cert-manager v%s (%d bytes)\n", intent.EmbeddedCertManagerVersion, len(certManagerYAML))
	if err := applyRawYAML(ctx, clients.Dynamic, certManagerYAML); err != nil {
		return fmt.Errorf("phase12: applying cert-manager YAML: %w", err)
	}

	// Wait for cert-manager Deployments.
	certManagerDeployments := []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"}
	for _, dep := range certManagerDeployments {
		fmt.Fprintf(os.Stderr, "[phase 12] waiting for cert-manager deployment %s\n", dep)
		if err := k8swait.WaitForDeploymentReady(ctx, clients.K8s, certManagerNS, dep, certManagerDeployTimeout); err != nil {
			return fmt.Errorf("phase12: cert-manager deployment %s not ready: %w", dep, err)
		}
	}
	fmt.Fprintln(os.Stderr, "[phase 12] cert-manager deployments ready")

	// Wait for cert-manager CRD (webhook readiness implied once deployment + CRD established).
	fmt.Fprintln(os.Stderr, "[phase 12] waiting for cert-manager CRD certificates.cert-manager.io")
	if err := k8swait.WaitForCRDExists(ctx, clients.Dynamic, "certificates.cert-manager.io", certManagerCRDTimeout); err != nil {
		return fmt.Errorf("phase12: cert-manager CRD not established: %w", err)
	}

	// 12.4 — BNK cert chain.
	certChainTmpl, err := k8smanifests.FS.ReadFile(certChainYAMLPath)
	if err != nil {
		return fmt.Errorf("phase12: reading embedded cert chain template: %w", err)
	}
	certChainYAML, err := render.RenderCertChain(certChainTmpl, cl)
	if err != nil {
		return fmt.Errorf("phase12: rendering cert chain template: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 12] applying BNK cert chain (issuer=%s ca=%s)\n",
		vars.SelfSignedIssuer, vars.CACertName)
	if err := applyRawYAML(ctx, clients.Dynamic, certChainYAML); err != nil {
		return fmt.Errorf("phase12: applying BNK cert chain: %w", err)
	}

	// Wait for CA Certificate Ready.
	fmt.Fprintf(os.Stderr, "[phase 12] waiting for CA certificate %s to be Ready\n", vars.CACertName)
	if err := k8swait.WaitForCertificateReady(ctx, clients.Dynamic, certManagerNS, vars.CACertName, caCertReadyTimeout); err != nil {
		return fmt.Errorf("phase12: CA certificate %s not ready: %w", vars.CACertName, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 12] CA certificate %s is Ready\n", vars.CACertName)

	// 12.5 — Multus CNI install (slice 7+). Required before Phase 20 NADs apply.
	// NetworkAttachmentDefinition CRD (k8s.cni.cncf.io/v1) is registered by Multus.
	multusYAML, err := k8smanifests.FS.ReadFile(multusYAMLPath)
	if err != nil {
		return fmt.Errorf("phase12: reading embedded multus YAML: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 12] applying Multus CNI %s (%d bytes)\n", multusVersion, len(multusYAML))
	if err := applyRawYAML(ctx, clients.Dynamic, multusYAML); err != nil {
		return fmt.Errorf("phase12: applying multus YAML: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 12] waiting for Multus DaemonSet %s/%s\n", multusNamespace, multusDaemonSet)
	if err := k8swait.WaitForDaemonSetReady(ctx, clients.K8s, multusNamespace, multusDaemonSet, multusDaemonSetTimeout); err != nil {
		return fmt.Errorf("phase12: multus DaemonSet not ready: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 12] Multus DaemonSet ready")
	fmt.Fprintln(os.Stderr, "[phase 12] waiting for NetworkAttachmentDefinition CRD")
	if err := k8swait.WaitForCRDExists(ctx, clients.Dynamic, "network-attachment-definitions.k8s.cni.cncf.io", certManagerCRDTimeout); err != nil {
		return fmt.Errorf("phase12: NetworkAttachmentDefinition CRD not established: %w", err)
	}

	// Persist state.
	st.Set("BNK_NAMESPACES_CREATED", strings.Join(bnkNamespaces, ","))
	st.Set("BNK_FAR_SECRET_NAME", farSecretName)
	st.Set("BNK_LICENSE_JWT_SECRET", licenseSecretName)
	st.Set("CERT_MANAGER_VERSION", intent.EmbeddedCertManagerVersion)
	st.Set("BNK_SELFSIGNED_ISSUER", vars.SelfSignedIssuer)
	st.Set("BNK_CA_CERT_NAME", vars.CACertName)
	st.Set("BNK_CA_SECRET_NAME", vars.CASecretName)
	st.Set("BNK_CA_ISSUER", vars.CAIssuer)
	st.Set("MULTUS_VERSION", multusVersion)
	return st.Save()
}

// Phase12K8sFoundationDown removes BNK k8s resources in reverse-create order.
// Tolerates NotFound at every step (idempotent destroy).
//
// Order: cert chain CRs → cert-manager YAML objects → FAR secrets from default ns →
// F5 namespaces (cert-manager ns deleted with cert-manager YAML).
func Phase12K8sFoundationDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 12 down] k8s foundation: cluster=%s\n", name)

	if clients.K8s == nil {
		// If k8s client isn't available (e.g. kubeconfig already gone), log and continue.
		fmt.Fprintln(os.Stderr, "[phase 12 down] warning: K8s client not available, skipping k8s teardown")
		clearPhase12State(st)
		return st.Save()
	}

	vars := render.CertChainVarsFromCluster(cl)

	// 0. Delete Multus CNI (DaemonSet + RBAC + CRD). Tolerate errors so we don't block.
	fmt.Fprintln(os.Stderr, "[phase 12 down] deleting Multus CNI resources")
	if mErr := deleteMultusResources(ctx, clients); mErr != nil {
		fmt.Fprintf(os.Stderr, "[phase 12 down] warning: multus delete had errors: %v\n", mErr)
	}

	// 1. Delete cert chain CRs via dynamic client (tolerate NotFound).
	fmt.Fprintln(os.Stderr, "[phase 12 down] deleting BNK cert chain CRs")
	deleteCertChainCRs(ctx, clients.Dynamic, vars)

	// 2. Delete cert-manager objects (parse embedded YAML, delete each object).
	fmt.Fprintln(os.Stderr, "[phase 12 down] deleting cert-manager resources")
	if err := deleteCertManagerResources(ctx, clients); err != nil {
		// Non-fatal: log and continue so we don't block NS/secret cleanup.
		fmt.Fprintf(os.Stderr, "[phase 12 down] warning: cert-manager delete had errors: %v\n", err)
	}

	// Wait for cert-manager namespace to terminate (best-effort, 5-min timeout).
	fmt.Fprintln(os.Stderr, "[phase 12 down] waiting for cert-manager namespace to terminate")
	if wErr := k8swait.WaitForNamespaceGone(ctx, clients.K8s, certManagerNS, nsTerminateTimeout); wErr != nil {
		fmt.Fprintf(os.Stderr, "[phase 12 down] warning: cert-manager namespace still present after timeout: %v\n", wErr)
	}

	// 3. Delete FAR secret from default ns (the 3 F5 ns will be deleted in step 4).
	fmt.Fprintf(os.Stderr, "[phase 12 down] deleting secret default/%s\n", farSecretName)
	err := clients.K8s.CoreV1().Secrets("default").Delete(ctx, farSecretName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "[phase 12 down] warning: delete secret default/%s: %v\n", farSecretName, err)
	}

	// 4. Delete the three F5 namespaces (cascades secrets within them).
	// Do NOT delete cert-manager (deleted with YAML in step 2) or default.
	f5Namespaces := []string{"f5-cne-core", "f5-cne-system", "f5-bnk-instance", "f5-utils"}
	for _, ns := range f5Namespaces {
		fmt.Fprintf(os.Stderr, "[phase 12 down] deleting namespace %s\n", ns)
		err := clients.K8s.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 12 down] warning: delete namespace %s: %v\n", ns, err)
		} else if k8serrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 12 down] namespace %s already gone\n", ns)
		}
	}

	clearPhase12State(st)
	return st.Save()
}

// --- helpers ---

// resolveBnkFilePaths returns the absolute-or-relative file paths for the FAR
// archive and JWT, resolving them against the cluster.yaml directory if needed.
// For now returns the values as-is (operators supply paths relative to their CWD).
func resolveBnkFilePaths(cl *intent.Cluster) (farPath, jwtPath string, err error) {
	if cl.Bnk == nil {
		return "", "", fmt.Errorf("bnk block is nil")
	}
	return cl.Bnk.FARArchive, cl.Bnk.JWT, nil
}

// ensureNamespaces creates each namespace if it does not already exist.
// Idempotent: Get first, Create only if NotFound.
func ensureNamespaces(ctx context.Context, clients *Clients, namespaces []string) error {
	for _, ns := range namespaces {
		_, err := clients.K8s.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if err == nil {
			fmt.Fprintf(os.Stderr, "[phase 12] namespace %s already exists, skipping\n", ns)
			continue
		}
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("get namespace %s: %w", ns, err)
		}
		_, err = clients.K8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		}, metav1.CreateOptions{})
		if err != nil && !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("create namespace %s: %w", ns, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 12] created namespace %s\n", ns)
	}
	return nil
}

// applyFARSecrets creates/patches the FAR pull secret (kubernetes.io/dockerconfigjson)
// in each target namespace. Uses Get-first + Create/Update (create-or-update pattern)
// via the typed client for reliability with both real and fake clientsets.
func applyFARSecrets(ctx context.Context, clients *Clients, namespaces []string, farData []byte) error {
	for _, ns := range namespaces {
		sec := buildFARSecret(ns, farData)
		if err := applySecretTyped(ctx, clients.K8s, sec); err != nil {
			return fmt.Errorf("apply FAR secret in %s: %w", ns, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 12] applied FAR secret in namespace %s\n", ns)
	}
	return nil
}

// applyLicenseJWTSecret creates/patches the license JWT secret (Opaque) in the
// operator namespace via the typed client.
func applyLicenseJWTSecret(ctx context.Context, clients *Clients, ns string, jwtData []byte) error {
	sec := buildLicenseJWTSecret(ns, jwtData)
	if err := applySecretTyped(ctx, clients.K8s, sec); err != nil {
		return fmt.Errorf("apply license JWT secret in %s: %w", ns, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 12] applied license JWT secret in namespace %s\n", ns)
	return nil
}

// buildFARSecret constructs the dockerconfigjson Secret object for the FAR archive.
//
// IMPORTANT: F5's FAR archive (`cne_pull_64.json`) is NOT a raw dockerconfigjson.
// It is a base64-encoded GCP service-account JSON used as the password for
// repo.f5.com (helm registry login -u _json_key_base64). We must wrap it as a
// dockerconfigjson before stuffing into Secret.Data.
//
// Wrapping per aws-gpu-setup/deploy-bnk.sh lines 61-63:
//
//	FAR_KEY_B64   = strings.TrimSpace(file_contents)              // already base64
//	AUTH_B64      = base64("_json_key_base64:" + FAR_KEY_B64)     // docker-auth value
//	DOCKER_CFG_JSON = {"auths":{"repo.f5.com":{"auth":"$AUTH_B64"}}}
//
// Secret.Data is map[string][]byte — client-go base64-encodes for the wire.
// We pass DOCKER_CFG_JSON as raw bytes; the API server sees decoded JSON and
// validates it.
func buildFARSecret(ns string, farData []byte) *corev1.Secret {
	farKeyB64 := strings.TrimSpace(string(farData))
	authValue := base64.StdEncoding.EncodeToString([]byte("_json_key_base64:" + farKeyB64))
	dockerCfg := map[string]any{
		"auths": map[string]any{
			"repo.f5.com": map[string]any{
				"auth": authValue,
			},
		},
	}
	// Marshal can't fail for this static shape, but check anyway.
	dockerCfgJSON, _ := json.Marshal(dockerCfg)

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      farSecretName,
			Namespace: ns,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: dockerCfgJSON,
		},
	}
}

// buildLicenseJWTSecret constructs the Opaque Secret for the subscription JWT.
func buildLicenseJWTSecret(ns string, jwtData []byte) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      licenseSecretName,
			Namespace: ns,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"license.jwt": jwtData,
		},
	}
}

// applySecretTyped creates or updates a Secret using the typed client.
// Get-first: if the secret doesn't exist, Create; otherwise Update.
// This is equivalent to SSA for our use case (we own the whole secret data)
// and works reliably with both real and fake clientsets.
func applySecretTyped(ctx context.Context, cs kubernetes.Interface, sec *corev1.Secret) error {
	existing, err := cs.CoreV1().Secrets(sec.Namespace).Get(ctx, sec.Name, metav1.GetOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get secret %s/%s: %w", sec.Namespace, sec.Name, err)
	}
	if k8serrors.IsNotFound(err) {
		_, err = cs.CoreV1().Secrets(sec.Namespace).Create(ctx, sec, metav1.CreateOptions{})
		if err != nil && !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("create secret %s/%s: %w", sec.Namespace, sec.Name, err)
		}
		return nil
	}
	// Update: preserve ResourceVersion for optimistic concurrency.
	sec.ResourceVersion = existing.ResourceVersion
	_, err = cs.CoreV1().Secrets(sec.Namespace).Update(ctx, sec, metav1.UpdateOptions{})
	return err
}

// applyRawYAML parses a multi-document YAML byte slice and server-side-applies
// each object via the dynamic client. Uses field-manager "awsbnkctl-phase12".
func applyRawYAML(ctx context.Context, dyn dynamic.Interface, rawYAML []byte) error {
	objs, err := parseYAMLDocs(rawYAML)
	if err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}
	for _, obj := range objs {
		if err := applyUnstructured(ctx, dyn, obj); err != nil {
			return err
		}
	}
	return nil
}

// parseYAMLDocs splits a multi-document YAML byte slice into individual
// unstructured maps. Empty documents are skipped.
func parseYAMLDocs(data []byte) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	// Split on document separator.
	docs := splitYAMLDocs(data)
	for _, doc := range docs {
		if len(doc) == 0 {
			continue
		}
		var obj map[string]interface{}
		if err := yaml.Unmarshal(doc, &obj); err != nil {
			return nil, fmt.Errorf("unmarshal YAML doc: %w", err)
		}
		if len(obj) == 0 {
			continue
		}
		out = append(out, obj)
	}
	return out, nil
}

// splitYAMLDocs splits on "\n---\n" and handles leading "---\n".
func splitYAMLDocs(data []byte) [][]byte {
	sep := []byte("\n---\n")
	parts := splitBytes(data, sep)
	if len(parts) > 0 && hasPrefix(parts[0], []byte("---\n")) {
		parts[0] = parts[0][4:]
	}
	return trimParts(parts)
}

func splitBytes(data, sep []byte) [][]byte {
	var parts [][]byte
	for {
		i := indexOf(data, sep)
		if i < 0 {
			parts = append(parts, data)
			break
		}
		parts = append(parts, data[:i])
		data = data[i+len(sep):]
	}
	return parts
}

func indexOf(data, sep []byte) int {
	for i := 0; i <= len(data)-len(sep); i++ {
		if string(data[i:i+len(sep)]) == string(sep) {
			return i
		}
	}
	return -1
}

func hasPrefix(data, prefix []byte) bool {
	if len(data) < len(prefix) {
		return false
	}
	return string(data[:len(prefix)]) == string(prefix)
}

func trimParts(parts [][]byte) [][]byte {
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		// trim leading/trailing whitespace
		trimmed := trimSpace(p)
		out = append(out, trimmed)
	}
	return out
}

func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}

// applyUnstructured server-side-applies one YAML document via the dynamic client.
// Resolves namespace from the object's metadata.namespace field; cluster-scoped
// objects (namespace == "") use the root resource interface.
func applyUnstructured(ctx context.Context, dyn dynamic.Interface, obj map[string]interface{}) error {
	apiVersion, _ := obj["apiVersion"].(string)
	kind, _ := obj["kind"].(string)
	if kind == "" {
		return nil // skip empty/comment-only docs
	}

	meta, _ := obj["metadata"].(map[string]interface{})
	objName, _ := meta["name"].(string)
	objNS, _ := meta["namespace"].(string)

	if objName == "" {
		return nil // skip docs without name
	}

	gvr, namespaced, err := resolveGVR(apiVersion, kind)
	if err != nil {
		// Unknown resource — skip gracefully (e.g. comment-only docs, unknown CRDs during teardown).
		fmt.Fprintf(os.Stderr, "[phase 12] warning: cannot resolve GVR for %s/%s %s: %v — skipping\n", apiVersion, kind, objName, err)
		return nil
	}

	body, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal %s %s: %w", kind, objName, err)
	}

	var ri dynamic.ResourceInterface
	if namespaced {
		ns := objNS
		if ns == "" {
			ns = "default"
		}
		ri = dyn.Resource(gvr).Namespace(ns)
	} else {
		ri = dyn.Resource(gvr)
	}

	_, err = ri.Patch(ctx, objName, types.ApplyPatchType, body, metav1.PatchOptions{
		FieldManager: phase12FieldManager,
		Force:        boolPtr(true),
	})
	if err != nil {
		return fmt.Errorf("SSA %s %s/%s: %w", kind, objNS, objName, err)
	}
	return nil
}

// resolveGVR maps an apiVersion+kind to a GroupVersionResource and namespace scope.
// This is a best-effort static map for the resources we know Phase 12 applies.
// Unknown resources are skipped (applyUnstructured logs and returns nil).
func resolveGVR(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	// Static map: apiVersion/kind → (GVR, namespaced)
	type entry struct {
		gvr        schema.GroupVersionResource
		namespaced bool
	}
	known := map[string]entry{
		// Core v1
		"v1/Namespace":      {schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}, false},
		"v1/ServiceAccount": {schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}, true},
		"v1/Secret":         {schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}, true},
		"v1/ConfigMap":      {schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}, true},
		"v1/Service":        {schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, true},
		"v1/Pod":            {schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, true},
		// RBAC
		"rbac.authorization.k8s.io/v1/ClusterRole":        {schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}, false},
		"rbac.authorization.k8s.io/v1/ClusterRoleBinding": {schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}, false},
		"rbac.authorization.k8s.io/v1/Role":               {schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, true},
		"rbac.authorization.k8s.io/v1/RoleBinding":        {schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}, true},
		// Apps
		"apps/v1/Deployment":  {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, true},
		"apps/v1/DaemonSet":   {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, true},
		"apps/v1/StatefulSet": {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, true},
		// Networking
		"networking.k8s.io/v1/NetworkPolicy": {schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, true},
		"networking.k8s.io/v1/IngressClass":  {schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses"}, false},
		// APIextensions (CRDs)
		"apiextensions.k8s.io/v1/CustomResourceDefinition": {schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}, false},
		// Admission webhooks
		"admissionregistration.k8s.io/v1/ValidatingWebhookConfiguration": {schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"}, false},
		"admissionregistration.k8s.io/v1/MutatingWebhookConfiguration":   {schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "mutatingwebhookconfigurations"}, false},
		// cert-manager CRs
		"cert-manager.io/v1/ClusterIssuer": {schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}, false},
		"cert-manager.io/v1/Issuer":        {schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "issuers"}, true},
		"cert-manager.io/v1/Certificate":   {schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}, true},
		// Multus NADs (slice 7b — host-device NADs)
		"k8s.cni.cncf.io/v1/NetworkAttachmentDefinition": {schema.GroupVersionResource{Group: "k8s.cni.cncf.io", Version: "v1", Resource: "network-attachment-definitions"}, true},
		// BNK CRs (slice 7c — CNEInstance + License)
		"k8s.f5.com/v1/CNEInstance": {schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: "cneinstances"}, true},
		"k8s.f5net.com/v1/License":  {schema.GroupVersionResource{Group: "k8s.f5net.com", Version: "v1", Resource: "licenses"}, true},
	}

	key := apiVersion + "/" + kind
	if e, ok := known[key]; ok {
		return e.gvr, e.namespaced, nil
	}
	return schema.GroupVersionResource{}, false, fmt.Errorf("unknown apiVersion/kind: %s", key)
}

// deleteCertChainCRs deletes the BNK cert chain custom resources via the dynamic
// client. Tolerates NotFound.
func deleteCertChainCRs(ctx context.Context, dyn dynamic.Interface, vars render.CertChainVars) {
	clusterIssuerGVR := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}
	certGVR := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}

	toDelete := []struct {
		gvr  schema.GroupVersionResource
		ns   string
		name string
	}{
		{clusterIssuerGVR, "", vars.CAIssuer},
		{certGVR, certManagerNS, vars.CACertName},
		{clusterIssuerGVR, "", vars.SelfSignedIssuer},
	}
	for _, d := range toDelete {
		var err error
		if d.ns == "" {
			err = dyn.Resource(d.gvr).Delete(ctx, d.name, metav1.DeleteOptions{})
		} else {
			err = dyn.Resource(d.gvr).Namespace(d.ns).Delete(ctx, d.name, metav1.DeleteOptions{})
		}
		if err != nil && !k8serrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 12 down] warning: delete %s/%s: %v\n", d.gvr.Resource, d.name, err)
		} else if k8serrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 12 down] %s/%s already gone\n", d.gvr.Resource, d.name)
		} else {
			fmt.Fprintf(os.Stderr, "[phase 12 down] deleted %s/%s\n", d.gvr.Resource, d.name)
		}
	}
}

// deleteCertManagerResources parses the embedded cert-manager YAML and deletes
// each object in reverse order. Tolerates NotFound.
func deleteCertManagerResources(ctx context.Context, clients *Clients) error {
	certManagerYAML, err := k8smanifests.FS.ReadFile(certManagerYAMLPath)
	if err != nil {
		return fmt.Errorf("reading embedded cert-manager YAML: %w", err)
	}
	objs, err := parseYAMLDocs(certManagerYAML)
	if err != nil {
		return fmt.Errorf("parsing cert-manager YAML: %w", err)
	}
	// Delete in reverse order.
	for i := len(objs) - 1; i >= 0; i-- {
		obj := objs[i]
		kind, _ := obj["kind"].(string)
		meta, _ := obj["metadata"].(map[string]interface{})
		objName, _ := meta["name"].(string)
		objNS, _ := meta["namespace"].(string)
		apiVersion, _ := obj["apiVersion"].(string)

		if kind == "" || objName == "" {
			continue
		}

		gvr, namespaced, err := resolveGVR(apiVersion, kind)
		if err != nil {
			continue // unknown resource, skip
		}

		var delErr error
		if namespaced {
			ns := objNS
			if ns == "" {
				ns = certManagerNS
			}
			delErr = clients.Dynamic.Resource(gvr).Namespace(ns).Delete(ctx, objName, metav1.DeleteOptions{})
		} else {
			delErr = clients.Dynamic.Resource(gvr).Delete(ctx, objName, metav1.DeleteOptions{})
		}
		if delErr != nil && !k8serrors.IsNotFound(delErr) {
			fmt.Fprintf(os.Stderr, "[phase 12 down] warning: delete %s %s: %v\n", kind, objName, delErr)
		}
	}
	return nil
}

// deleteMultusResources parses the embedded Multus YAML and deletes each object
// in reverse order. Tolerates NotFound and unknown-resource errors so partial
// state doesn't block the rest of Phase 12 down.
func deleteMultusResources(ctx context.Context, clients *Clients) error {
	multusYAML, err := k8smanifests.FS.ReadFile(multusYAMLPath)
	if err != nil {
		return fmt.Errorf("reading embedded multus YAML: %w", err)
	}
	objs, err := parseYAMLDocs(multusYAML)
	if err != nil {
		return fmt.Errorf("parsing multus YAML: %w", err)
	}
	for i := len(objs) - 1; i >= 0; i-- {
		obj := objs[i]
		kind, _ := obj["kind"].(string)
		meta, _ := obj["metadata"].(map[string]interface{})
		objName, _ := meta["name"].(string)
		objNS, _ := meta["namespace"].(string)
		apiVersion, _ := obj["apiVersion"].(string)

		if kind == "" || objName == "" {
			continue
		}

		gvr, namespaced, err := resolveGVR(apiVersion, kind)
		if err != nil {
			continue
		}

		var delErr error
		if namespaced {
			ns := objNS
			if ns == "" {
				ns = multusNamespace
			}
			delErr = clients.Dynamic.Resource(gvr).Namespace(ns).Delete(ctx, objName, metav1.DeleteOptions{})
		} else {
			delErr = clients.Dynamic.Resource(gvr).Delete(ctx, objName, metav1.DeleteOptions{})
		}
		if delErr != nil && !k8serrors.IsNotFound(delErr) {
			fmt.Fprintf(os.Stderr, "[phase 12 down] warning: delete multus %s %s: %v\n", kind, objName, delErr)
		}
	}
	return nil
}

// clearPhase12State zeroes all phase 12 state keys.
func clearPhase12State(st *state.State) {
	keys := []string{
		"BNK_NAMESPACES_CREATED",
		"BNK_FAR_SECRET_NAME",
		"BNK_LICENSE_JWT_SECRET",
		"CERT_MANAGER_VERSION",
		"BNK_SELFSIGNED_ISSUER",
		"BNK_CA_CERT_NAME",
		"BNK_CA_SECRET_NAME",
		"BNK_CA_ISSUER",
		"MULTUS_VERSION",
	}
	for _, k := range keys {
		st.Set(k, "")
	}
}
