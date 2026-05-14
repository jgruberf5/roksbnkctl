package cli

import (
	"context"
	"encoding/base64"
	encodingjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

// flagOpsConfirm — destructive-action gate for `ops uninstall`.
var flagOpsConfirm bool

// flagTrustedProfile controls the `--trusted-profile` flag on `ops
// install`. Three values, validated in validateTrustedProfileFlag:
//
//	auto (default) — try to provision a trusted profile; fall back to
//	                 the static-key Secret with a stderr warning when
//	                 the API key lacks `iam-identity` perms.
//	on             — try to provision; fail loudly if perms don't
//	                 allow. No fallback.
//	off            — skip the trusted-profile path entirely; provision
//	                 the v1.0.x static-key Secret directly.
//
// PRD 04 §"Resolved in Sprint 9" §"Trusted-profile auto-provisioning".
var flagTrustedProfile string

// trustedProfileSAAnnotation marks the ops pod's ServiceAccount when
// `ops install` provisioned a trusted profile. `ops show` reads this
// to display the profile ID; `ops uninstall` reads it to clean up the
// profile.
const trustedProfileSAAnnotation = "iam.cloud.ibm.com/trusted-profile"

// trustedProfileManagedAnnotation flags that roksbnkctl provisioned
// the profile (vs. a user pointing the SA at a pre-existing profile).
// Set in tandem with trustedProfileSAAnnotation when `--trusted-profile`
// took the create path.
const trustedProfileManagedAnnotation = "roksbnkctl.io/trusted-profile-managed"

// validateTrustedProfileFlag enforces the auto|on|off vocabulary at
// flag-parse time. Returns a clear error so users get actionable
// feedback before the install gets anywhere near IBM Cloud.
func validateTrustedProfileFlag(v string) error {
	switch v {
	case "auto", "on", "off":
		return nil
	default:
		return fmt.Errorf("--trusted-profile: %q is not one of auto|on|off", v)
	}
}

var opsCmd = &cobra.Command{
	Use:   "ops",
	Short: "Manage the in-cluster ops pod (k8s execution backend)",
	Long: `roksbnkctl ops manages the long-lived ops pod the k8s execution
backend exec's tools into. The pod runs in the roksbnkctl-ops
namespace with a least-privilege ServiceAccount + ClusterRole, and
gets its IBM Cloud API key from a Secret apply-time-templated from
the workspace credential.

Subcommands:
  install    apply the embedded manifests (idempotent)
  show       print pod + Secret + RBAC status
  uninstall  delete every roksbnkctl.io/managed object created by install`,
}

var opsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Apply (or update) the in-cluster ops fixtures",
	Long: `Applies the embedded namespaces, ServiceAccount, Secret, ClusterRole,
ClusterRoleBinding, and ops Pod. Idempotent: re-running with a new
API key updates the Secret and rolls the Pod.

Credential mode is selected via --trusted-profile (auto|on|off):

  auto (default) — provision an IBM Cloud IAM trusted profile linked
                   to the ops pod's ServiceAccount when the resolved
                   API key has 'iam-identity' perms; otherwise fall
                   back to the static-key Secret with a stderr warning.
  on             — require the trusted-profile path; fail loudly if
                   perms don't allow.
  off            — skip the trusted-profile path; install the v1.0.x
                   static-key Secret.`,
	PreRunE: func(_ *cobra.Command, _ []string) error {
		return validateTrustedProfileFlag(flagTrustedProfile)
	},
	RunE: runOpsInstall,
}

var opsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the ops pod's status, image, RBAC subject, and Secret rotation timestamp",
	RunE:  runOpsShow,
}

var opsUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Delete the ops fixtures (namespaces, RBAC, Pod, Secret)",
	RunE:  runOpsUninstall,
}

func init() {
	opsUninstallCmd.Flags().BoolVar(&flagOpsConfirm, "confirm", false, "actually perform the uninstall (otherwise prints what would be deleted)")
	opsInstallCmd.Flags().StringVar(&flagTrustedProfile, "trusted-profile", "auto",
		"IBM IAM trusted profile mode: auto (default; provision when perms allow, fall back to static-key Secret), on (require trusted profile), off (static-key Secret only)")
	opsCmd.AddCommand(opsInstallCmd, opsShowCmd, opsUninstallCmd)
	rootCmd.AddCommand(opsCmd)

	// Wire the k8s backend's lazy-init seam through internal/k8s so the
	// exec package doesn't need to import internal/k8s (cycle avoidance —
	// internal/k8s already imports the exec package's siblings; we keep
	// the dependency graph one-way by pushing this wiring up to cli).
	execbackend.SetK8sInit(func() (kubernetes.Interface, *rest.Config, error) {
		cfg, err := k8s.BuildRESTConfig("")
		if err != nil {
			return nil, nil, err
		}
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return nil, nil, err
		}
		return cs, cfg, nil
	})
}

// opsImage resolves the ops pod's container image. Pinned to the
// binary's version (e.g., :v0.10.0) on tagged builds; falls back to
// :dev for development builds where Version == "dev".
//
// PRD 03 §"K8s" + Sprint 4 polish carry-over 5b — same logic the
// docker backend's toolImages uses post-fix.
func opsImage() string {
	tag := Version
	if tag == "" || tag == "dev" {
		tag = "dev"
	}
	return "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:" + tag
}

func runOpsInstall(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}

	// Resolve API key. Used either to populate the static-key Secret
	// (off / fallback) or to provision the trusted profile (auto / on).
	resolver := &cred.Resolver{
		Workspace:      cctx.WorkspaceName,
		NonInteractive: true,
		Source:         cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return fmt.Errorf("resolving IBM Cloud API key: %w", err)
	}

	// Trusted-profile branch (Sprint 9 / PRD 04 §"Resolved in Sprint 9"
	// §"Trusted-profile auto-provisioning"). Resolves a TrustedProfile
	// per the flag mode; on `auto` with missing IAM perms, falls back
	// to static-key with a stderr warning. On `on` with missing perms,
	// errors out. On `off`, skips entirely.
	tp, useTrustedProfile, err := resolveTrustedProfileForInstall(ctx, cctx, apiKey)
	if err != nil {
		return err
	}

	cs, err := k8s.BuildClientset("")
	if err != nil {
		return fmt.Errorf("building kubernetes client: %w", err)
	}

	// Decode the embedded YAML (with placeholders substituted) into typed
	// objects, then apply each via a kind-typed Get-then-Create-or-Update.
	// When the trusted-profile path is in play, pass an empty API key to
	// the manifest renderer so the static-key Secret is rendered with
	// empty data fields (the trusted-profile annotation on the SA is what
	// authenticates the pod); the Secret remains in the manifest only as
	// a no-op placeholder the v1.0.x rollback path can repopulate.
	manifestKey := apiKey
	manifestProfileID := ""
	if useTrustedProfile && tp != nil {
		manifestKey = ""
		manifestProfileID = tp.ID
	}
	objs, err := decodeOpsManifests(manifestKey, manifestProfileID)
	if err != nil {
		return fmt.Errorf("decoding manifests: %w", err)
	}

	// Stamp the trusted-profile annotation on the ServiceAccount before
	// apply so the IAM webhook injects the projected token volume on
	// pod create. Annotation is the same one IBM Cloud's
	// trusted-profile sidecar-injector watches for.
	if useTrustedProfile && tp != nil {
		for _, obj := range objs {
			if sa, ok := obj.(*corev1.ServiceAccount); ok && sa.Name == "roksbnkctl-ops" {
				if sa.Annotations == nil {
					sa.Annotations = map[string]string{}
				}
				sa.Annotations[trustedProfileSAAnnotation] = tp.ID
				sa.Annotations[trustedProfileManagedAnnotation] = "true"
			}
		}
	}

	for _, obj := range objs {
		if err := applyOpsObject(ctx, cs, obj); err != nil {
			return err
		}
	}

	fmt.Fprintln(os.Stderr, "→ Waiting for ops pod to be Ready (60s timeout)")
	if err := waitForOpsPodReady(ctx, cs, 60*time.Second); err != nil {
		return fmt.Errorf("ops pod not ready: %w", err)
	}
	if useTrustedProfile && tp != nil {
		fmt.Fprintf(os.Stderr, "✓ Ops pod is Ready (trusted profile %s)\n", tp.Name)
	} else {
		fmt.Fprintln(os.Stderr, "✓ Ops pod is Ready (static-key Secret)")
	}
	return nil
}

// resolveTrustedProfileForInstall implements the --trusted-profile flag
// branching logic per PRD 04 §"Resolved in Sprint 9". Returns:
//
//   - (tp, true, nil) on the trusted-profile path (auto/on success);
//   - (nil, false, nil) on the static-key path (off, or auto-fallback);
//   - (nil, false, err) on hard failures (auto+non-perm error; on+any
//     error).
//
// Stderr is the warning surface for auto-fallback (one line; tells the
// user how to silence).
func resolveTrustedProfileForInstall(ctx context.Context, cctx *config.Context, apiKey string) (*ibm.TrustedProfile, bool, error) {
	mode := flagTrustedProfile
	if mode == "off" {
		return nil, false, nil
	}

	// Resolve cluster CRN. Required for the IAM trusted-profile link
	// (CrType=ROKS_SA needs the cluster CRN). The CRN lives in the
	// workspace's cluster-outputs.json — set by `cluster up` /
	// `cluster register`.
	outputs, err := config.ReadClusterOutputs(cctx.WorkspaceName)
	if err != nil {
		// auto: degrade with warning. on: hard fail.
		if mode == "auto" {
			fmt.Fprintf(os.Stderr, "warning: trusted-profile mode 'auto' needs a registered cluster (%v); falling back to static-key Secret. Pass `--trusted-profile=off` to silence.\n", err)
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("trusted-profile mode 'on' needs cluster registration first: %w", err)
	}

	region := cctx.Workspace.IBMCloud.Region
	if region == "" {
		region = outputs.Region
	}
	ibmClient, err := ibm.New(apiKey, region)
	if err != nil {
		return nil, false, fmt.Errorf("ibm client: %w", err)
	}

	// Look up the cluster's CRN. GetCluster also auto-verifies the
	// API key (the IAM token exchange happens internally), so if the
	// caller's perms are missing this is where it'll surface first.
	cluster, err := ibmClient.GetCluster(ctx, outputs.ClusterID)
	if err != nil {
		if mode == "auto" {
			fmt.Fprintf(os.Stderr, "warning: trusted-profile mode 'auto' couldn't look up cluster (%v); falling back to static-key Secret. Pass `--trusted-profile=off` to silence.\n", err)
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("trusted-profile mode 'on' couldn't look up cluster %s: %w", outputs.ClusterID, err)
	}

	profileName := "roksbnkctl-ops-" + cctx.WorkspaceName
	tp, err := ibmClient.TrustedProfiles().CreateForOpsPod(ctx,
		profileName, cluster.CRN, execbackend.K8sOpsNamespace, "roksbnkctl-ops")
	if err != nil {
		if errors.Is(err, ibm.ErrIAMPermDenied) {
			if mode == "auto" {
				fmt.Fprintf(os.Stderr, "warning: IAM perm 'iam-identity' missing; using static-key Secret. Pass `--trusted-profile=off` to silence.\n")
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("trusted-profile mode 'on' but API key lacks 'iam-identity' perms: %w", err)
		}
		return nil, false, fmt.Errorf("provisioning trusted profile: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ Provisioned IAM trusted profile %s (%s)\n", tp.Name, tp.ID)
	return tp, true, nil
}

func runOpsShow(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	cs, err := k8s.BuildClientset("")
	if err != nil {
		return fmt.Errorf("building kubernetes client: %w", err)
	}
	pod, err := cs.CoreV1().Pods(execbackend.K8sOpsNamespace).Get(ctx, execbackend.K8sOpsPodName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("ops pod %s/%s not found; run `roksbnkctl ops install`", execbackend.K8sOpsNamespace, execbackend.K8sOpsPodName)
		}
		return err
	}
	secret, serr := cs.CoreV1().Secrets(execbackend.K8sOpsNamespace).Get(ctx, execbackend.K8sOpsSecretName, metav1.GetOptions{})

	fmt.Printf("namespace:    %s\n", execbackend.K8sOpsNamespace)
	fmt.Printf("pod:          %s\n", pod.Name)
	fmt.Printf("phase:        %s\n", pod.Status.Phase)
	fmt.Printf("ready:        %v\n", podReady(pod))
	if len(pod.Spec.Containers) > 0 {
		fmt.Printf("image:        %s\n", pod.Spec.Containers[0].Image)
	}
	fmt.Printf("rbac subject: system:serviceaccount:%s:%s\n", execbackend.K8sOpsNamespace, execbackend.K8sOpsPodName)

	// Surface the trusted-profile annotation on the ops pod's SA if
	// present (Sprint 9 / PRD 04 §"Resolved in Sprint 9"). Absent
	// annotation → the install used the static-key Secret path.
	sa, saErr := cs.CoreV1().ServiceAccounts(execbackend.K8sOpsNamespace).Get(ctx, "roksbnkctl-ops", metav1.GetOptions{})
	if saErr == nil {
		if tpID, ok := sa.Annotations[trustedProfileSAAnnotation]; ok && tpID != "" {
			fmt.Printf("trusted-profile: %s\n", tpID)
		} else {
			fmt.Printf("trusted-profile: (none — static-key Secret path)\n")
		}
	}

	if serr == nil {
		rotated := secret.Annotations["roksbnkctl.io/rotated-at"]
		if rotated == "" {
			rotated = "(not stamped)"
		}
		fmt.Printf("secret:       %s (rotated %s)\n", secret.Name, rotated)
	} else {
		fmt.Printf("secret:       (missing: %v)\n", serr)
	}
	return nil
}

func runOpsUninstall(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if !flagOpsConfirm {
		fmt.Fprintln(os.Stderr, "Would delete (re-run with --confirm to proceed):")
		fmt.Fprintln(os.Stderr, "  - Pod        roksbnkctl-ops/roksbnkctl-ops")
		fmt.Fprintln(os.Stderr, "  - Secret     roksbnkctl-ops/roksbnkctl-ibm-creds")
		fmt.Fprintln(os.Stderr, "  - ServiceAccount roksbnkctl-ops/roksbnkctl-ops")
		fmt.Fprintln(os.Stderr, "  - ClusterRole/ClusterRoleBinding roksbnkctl-ops")
		fmt.Fprintln(os.Stderr, "  - Namespace  roksbnkctl-ops")
		fmt.Fprintln(os.Stderr, "  - Namespace  roksbnkctl-test")
		return nil
	}

	cs, err := k8s.BuildClientset("")
	if err != nil {
		return fmt.Errorf("building kubernetes client: %w", err)
	}

	// If `ops install` provisioned a trusted profile (the SA carries
	// the trustedProfileSAAnnotation + trustedProfileManagedAnnotation
	// pair), delete it via the IAM API. Best-effort: a failure here
	// shouldn't block cluster-side cleanup. PRD 04 §"Resolved in
	// Sprint 9" §"Trusted-profile auto-provisioning" — the
	// `-managed: "true"` flag is what distinguishes a roksbnkctl-
	// provisioned profile from one the user pre-created.
	deleteTrustedProfileIfManaged(ctx, cs)

	// Delete leaf resources first; namespaces last.
	delErrs := []string{}
	tryDel := func(label string, fn func() error) {
		if err := fn(); err != nil && !apierrors.IsNotFound(err) {
			delErrs = append(delErrs, fmt.Sprintf("%s: %v", label, err))
			fmt.Fprintf(os.Stderr, "warning: deleting %s: %v\n", label, err)
		} else {
			fmt.Fprintf(os.Stderr, "✓ deleted %s\n", label)
		}
	}

	tryDel("pod roksbnkctl-ops", func() error {
		return cs.CoreV1().Pods(execbackend.K8sOpsNamespace).Delete(ctx, execbackend.K8sOpsPodName, metav1.DeleteOptions{})
	})
	tryDel("secret roksbnkctl-ibm-creds", func() error {
		return cs.CoreV1().Secrets(execbackend.K8sOpsNamespace).Delete(ctx, execbackend.K8sOpsSecretName, metav1.DeleteOptions{})
	})
	tryDel("serviceaccount roksbnkctl-ops", func() error {
		return cs.CoreV1().ServiceAccounts(execbackend.K8sOpsNamespace).Delete(ctx, "roksbnkctl-ops", metav1.DeleteOptions{})
	})
	tryDel("clusterrolebinding roksbnkctl-ops", func() error {
		return cs.RbacV1().ClusterRoleBindings().Delete(ctx, "roksbnkctl-ops", metav1.DeleteOptions{})
	})
	tryDel("clusterrole roksbnkctl-ops", func() error {
		return cs.RbacV1().ClusterRoles().Delete(ctx, "roksbnkctl-ops", metav1.DeleteOptions{})
	})
	tryDel("namespace roksbnkctl-ops", func() error {
		return cs.CoreV1().Namespaces().Delete(ctx, execbackend.K8sOpsNamespace, metav1.DeleteOptions{})
	})
	tryDel("namespace roksbnkctl-test", func() error {
		return cs.CoreV1().Namespaces().Delete(ctx, execbackend.K8sTestNamespace, metav1.DeleteOptions{})
	})
	if len(delErrs) > 0 {
		return fmt.Errorf("%d delete errors (see warnings)", len(delErrs))
	}
	return nil
}

// decodeOpsManifests substitutes placeholders into the embedded YAML
// then decodes each document into a typed runtime.Object via the
// kubernetes scheme. Returns the objects in apply order.
//
// Sprint 10 / PRD 04 §"Resolved in Sprint 9" closure: when
// `iamProfileID` is non-empty (the trusted-profile auto/on success
// path), the renderer injects an `IAM_PROFILE_ID=<id>` env entry into
// the ops pod spec so the in-pod `ibmcloud login` wrap branches to the
// trusted-profile dance. Empty string (static-key path) substitutes
// to an empty line so the env list collapses cleanly.
func decodeOpsManifests(apiKey, iamProfileID string) ([]runtime.Object, error) {
	apiKeyB64 := base64.StdEncoding.EncodeToString([]byte(apiKey))
	rotated := time.Now().UTC().Format(time.RFC3339)
	// Render the IAM_PROFILE_ID env entry at the right indent so it
	// drops cleanly into the existing `env:` list. The placeholder
	// itself sits at column 0 in k8s_install.yaml so substitution
	// doesn't have to track indentation context.
	iamProfileEnvEntry := ""
	if iamProfileID != "" {
		iamProfileEnvEntry = "        - name: IAM_PROFILE_ID\n          value: " + iamProfileID
	}
	rendered := strings.NewReplacer(
		"${IBMCLOUD_API_KEY_B64}", apiKeyB64,
		"${ROTATED_AT}", rotated,
		"${OPS_IMAGE}", opsImage(),
		"${IAM_PROFILE_ID_ENV_ENTRY}", iamProfileEnvEntry,
	).Replace(execbackend.K8sInstallYAML())

	dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(rendered), 4096)
	codec := scheme.Codecs.UniversalDeserializer()
	var out []runtime.Object
	for {
		// Pull each YAML document as raw bytes, then decode to typed obj.
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("yaml: %w", err)
		}
		if len(raw) == 0 {
			continue
		}
		// Convert back to JSON bytes (the universal decoder accepts
		// JSON-encoded input) so we can decode into a typed object.
		jsonBytes, err := mapToJSONBytes(raw)
		if err != nil {
			return nil, err
		}
		obj, _, err := codec.Decode(jsonBytes, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("decoding manifest: %w", err)
		}
		out = append(out, obj)
	}
	return out, nil
}

// applyOpsObject does a kind-typed Get-then-Create-or-Update for each
// supported object type. Pods are immutable so we delete + recreate.
func applyOpsObject(ctx context.Context, cs kubernetes.Interface, obj runtime.Object) error {
	switch o := obj.(type) {
	case *corev1.Namespace:
		_, err := cs.CoreV1().Namespaces().Get(ctx, o.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, cerr := cs.CoreV1().Namespaces().Create(ctx, o, metav1.CreateOptions{})
			if cerr != nil {
				return fmt.Errorf("creating namespace %s: %w", o.Name, cerr)
			}
			fmt.Fprintf(os.Stderr, "✓ created namespace %s\n", o.Name)
			return nil
		}
		fmt.Fprintf(os.Stderr, "✓ namespace %s exists\n", o.Name)
		return err

	case *corev1.ServiceAccount:
		_, err := cs.CoreV1().ServiceAccounts(o.Namespace).Get(ctx, o.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, cerr := cs.CoreV1().ServiceAccounts(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
			if cerr != nil {
				return fmt.Errorf("creating sa %s/%s: %w", o.Namespace, o.Name, cerr)
			}
			fmt.Fprintf(os.Stderr, "✓ created sa %s/%s\n", o.Namespace, o.Name)
			return nil
		}
		fmt.Fprintf(os.Stderr, "✓ sa %s/%s exists\n", o.Namespace, o.Name)
		return err

	case *corev1.Secret:
		existing, err := cs.CoreV1().Secrets(o.Namespace).Get(ctx, o.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, cerr := cs.CoreV1().Secrets(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
			if cerr != nil {
				return fmt.Errorf("creating secret %s/%s: %w", o.Namespace, o.Name, cerr)
			}
			fmt.Fprintf(os.Stderr, "✓ created secret %s/%s\n", o.Namespace, o.Name)
			return nil
		}
		if err != nil {
			return err
		}
		existing.Data = o.Data
		if existing.Annotations == nil {
			existing.Annotations = map[string]string{}
		}
		for k, v := range o.Annotations {
			existing.Annotations[k] = v
		}
		_, uerr := cs.CoreV1().Secrets(o.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
		if uerr != nil {
			return fmt.Errorf("updating secret %s/%s: %w", o.Namespace, o.Name, uerr)
		}
		fmt.Fprintf(os.Stderr, "✓ updated secret %s/%s\n", o.Namespace, o.Name)
		return nil

	case *rbacv1.ClusterRole:
		existing, err := cs.RbacV1().ClusterRoles().Get(ctx, o.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, cerr := cs.RbacV1().ClusterRoles().Create(ctx, o, metav1.CreateOptions{})
			if cerr != nil {
				return fmt.Errorf("creating clusterrole %s: %w", o.Name, cerr)
			}
			fmt.Fprintf(os.Stderr, "✓ created clusterrole %s\n", o.Name)
			return nil
		}
		if err != nil {
			return err
		}
		existing.Rules = o.Rules
		_, uerr := cs.RbacV1().ClusterRoles().Update(ctx, existing, metav1.UpdateOptions{})
		if uerr != nil {
			return fmt.Errorf("updating clusterrole %s: %w", o.Name, uerr)
		}
		fmt.Fprintf(os.Stderr, "✓ updated clusterrole %s\n", o.Name)
		return nil

	case *rbacv1.ClusterRoleBinding:
		existing, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, o.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, cerr := cs.RbacV1().ClusterRoleBindings().Create(ctx, o, metav1.CreateOptions{})
			if cerr != nil {
				return fmt.Errorf("creating crb %s: %w", o.Name, cerr)
			}
			fmt.Fprintf(os.Stderr, "✓ created crb %s\n", o.Name)
			return nil
		}
		if err != nil {
			return err
		}
		existing.RoleRef = o.RoleRef
		existing.Subjects = o.Subjects
		_, uerr := cs.RbacV1().ClusterRoleBindings().Update(ctx, existing, metav1.UpdateOptions{})
		if uerr != nil {
			return fmt.Errorf("updating crb %s: %w", o.Name, uerr)
		}
		fmt.Fprintf(os.Stderr, "✓ updated crb %s\n", o.Name)
		return nil

	case *corev1.Pod:
		// Pods are immutable. Delete + recreate so a re-installed pod
		// picks up new Secret values via envFrom on next start.
		_ = cs.CoreV1().Pods(o.Namespace).Delete(ctx, o.Name, metav1.DeleteOptions{})
		// Wait briefly for the prior pod to disappear so the recreate
		// doesn't fail with AlreadyExists.
		for i := 0; i < 30; i++ {
			if _, err := cs.CoreV1().Pods(o.Namespace).Get(ctx, o.Name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
				break
			}
			time.Sleep(time.Second)
		}
		_, cerr := cs.CoreV1().Pods(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
		if cerr != nil {
			return fmt.Errorf("creating pod %s/%s: %w", o.Namespace, o.Name, cerr)
		}
		fmt.Fprintf(os.Stderr, "✓ created pod %s/%s\n", o.Namespace, o.Name)
		return nil

	default:
		return fmt.Errorf("unsupported object type %T in install manifest", o)
	}
}

func waitForOpsPodReady(ctx context.Context, cs kubernetes.Interface, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pod, err := cs.CoreV1().Pods(execbackend.K8sOpsNamespace).Get(ctx, execbackend.K8sOpsPodName, metav1.GetOptions{})
		if err == nil && pod.Status.Phase == corev1.PodRunning && podReady(pod) {
			return nil
		}
		if time.Now().After(deadline) {
			phase := "(not found)"
			if pod != nil {
				phase = string(pod.Status.Phase)
			}
			return fmt.Errorf("timeout waiting for pod (phase=%s)", phase)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// mapToJSONBytes is a tiny helper that turns a YAML-decoded
// `map[string]any` back into JSON bytes. The kubernetes universal
// decoder accepts JSON-encoded input, so this gets us from
// "yaml-decoded generic map" to "kubernetes-typed object" with a
// minimum of moving parts.
func mapToJSONBytes(m map[string]any) ([]byte, error) {
	return encodingjson.Marshal(m)
}

// deleteTrustedProfileIfManaged checks the ops SA for the
// roksbnkctl.io/trusted-profile-managed="true" annotation and, when
// present, deletes the named trusted profile via the IAM API.
// Best-effort: failures are logged to stderr but don't propagate.
//
// Surfaces ErrIAMPermDenied as a clear "perms missing for cleanup"
// warning so users know what's left behind. Other errors get a
// generic warning.
func deleteTrustedProfileIfManaged(ctx context.Context, cs kubernetes.Interface) {
	sa, err := cs.CoreV1().ServiceAccounts(execbackend.K8sOpsNamespace).Get(ctx, "roksbnkctl-ops", metav1.GetOptions{})
	if err != nil {
		return
	}
	managed := sa.Annotations[trustedProfileManagedAnnotation]
	profileID := sa.Annotations[trustedProfileSAAnnotation]
	if managed != "true" || profileID == "" {
		return
	}

	cctx, err := config.New(flagWorkspace)
	if err != nil || cctx.Workspace == nil {
		fmt.Fprintf(os.Stderr, "warning: trusted profile %s left behind (no workspace context to resolve cleanup API key)\n", profileID)
		return
	}
	resolver := &cred.Resolver{
		Workspace:      cctx.WorkspaceName,
		NonInteractive: true,
		Source:         cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: trusted profile %s left behind (couldn't resolve API key: %v)\n", profileID, err)
		return
	}
	ibmClient, err := ibm.New(apiKey, cctx.Workspace.IBMCloud.Region)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: trusted profile %s left behind (ibm client: %v)\n", profileID, err)
		return
	}
	if err := ibmClient.TrustedProfiles().Delete(ctx, profileID); err != nil {
		if errors.Is(err, ibm.ErrIAMPermDenied) {
			fmt.Fprintf(os.Stderr, "warning: trusted profile %s left behind (IAM perm 'iam-identity' missing for delete)\n", profileID)
		} else {
			fmt.Fprintf(os.Stderr, "warning: deleting trusted profile %s: %v\n", profileID, err)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "✓ deleted trusted profile %s\n", profileID)
}

func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
