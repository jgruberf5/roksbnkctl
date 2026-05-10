package cli

import (
	"context"
	"encoding/base64"
	encodingjson "encoding/json"
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
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

// flagOpsConfirm — destructive-action gate for `ops uninstall`.
var flagOpsConfirm bool

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
API key updates the Secret and rolls the Pod.`,
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

	// Resolve API key for the Secret.
	resolver := &cred.Resolver{
		Workspace:      cctx.WorkspaceName,
		NonInteractive: true,
		Source:         cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return fmt.Errorf("resolving IBM Cloud API key: %w", err)
	}

	cs, err := k8s.BuildClientset("")
	if err != nil {
		return fmt.Errorf("building kubernetes client: %w", err)
	}

	// Decode the embedded YAML (with placeholders substituted) into typed
	// objects, then apply each via a kind-typed Get-then-Create-or-Update.
	objs, err := decodeOpsManifests(apiKey)
	if err != nil {
		return fmt.Errorf("decoding manifests: %w", err)
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
	fmt.Fprintln(os.Stderr, "✓ Ops pod is Ready")
	return nil
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
func decodeOpsManifests(apiKey string) ([]runtime.Object, error) {
	apiKeyB64 := base64.StdEncoding.EncodeToString([]byte(apiKey))
	rotated := time.Now().UTC().Format(time.RFC3339)
	rendered := strings.NewReplacer(
		"${IBMCLOUD_API_KEY_B64}", apiKeyB64,
		"${ROTATED_AT}", rotated,
		"${OPS_IMAGE}", opsImage(),
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

func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
