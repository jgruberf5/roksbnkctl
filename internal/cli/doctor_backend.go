package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/doctor"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

// runBackendChecks dispatches to the per-backend doctor probes per PRD 03
// §"doctor extensions". `spec` is one of:
//
//	k8s              → cluster reachable, ops pod Ready, RBAC subjects exist
//	ssh:<target>     → target resolves, ssh connects, sudo / PATH readiness
//
// Each probe returns one or more doctor.Check entries with BackendName
// set (so PrintResults could later split them out per backend); the
// rendering is unchanged today.
func runBackendChecks(ctx context.Context, cctx *config.Context, spec string) []doctor.Check {
	switch {
	case spec == "k8s":
		return runK8sBackendChecks(ctx)
	case strings.HasPrefix(spec, "ssh:"):
		target := strings.TrimPrefix(spec, "ssh:")
		return runSSHBackendChecks(ctx, cctx, target)
	default:
		return []doctor.Check{{
			Name:     "doctor backend " + spec,
			Status:   doctor.StatusError,
			Detail:   fmt.Sprintf("unsupported --backend value %q (want k8s | ssh:<target>)", spec),
			Optional: false,
		}}
	}
}

// runK8sBackendChecks probes the k8s execution backend's prerequisites.
//
//   - apiserver reachable (clientset construction succeeds)
//   - ops pod Ready
//   - ServiceAccount + ClusterRole + ClusterRoleBinding present
//   - cred Secret has IBMCLOUD_API_KEY populated
//   - RBAC negative check: ops SA can NOT delete pods cluster-wide
//
// PRD 03 §"K8s" §"doctor extensions".
func runK8sBackendChecks(ctx context.Context) []doctor.Check {
	out := []doctor.Check{}
	add := func(name string, status doctor.CheckStatus, detail string) {
		out = append(out, doctor.Check{
			Name:        name,
			Status:      status,
			Detail:      detail,
			BackendName: "k8s",
		})
	}

	cs, err := k8s.BuildClientset("")
	if err != nil {
		add("k8s cluster reachable", doctor.StatusError, err.Error())
		return out
	}
	add("k8s cluster reachable", doctor.StatusOK, "kubeconfig loaded")

	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if _, err := cs.CoreV1().Namespaces().Get(probeCtx, execbackend.K8sOpsNamespace, metav1.GetOptions{}); err != nil {
		add("ops namespace", doctor.StatusError, "missing — run `roksbnkctl ops install`")
		return out
	}
	add("ops namespace", doctor.StatusOK, execbackend.K8sOpsNamespace)

	pod, err := cs.CoreV1().Pods(execbackend.K8sOpsNamespace).Get(probeCtx, execbackend.K8sOpsPodName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			add("ops pod", doctor.StatusError, "not found — run `roksbnkctl ops install`")
		} else {
			add("ops pod", doctor.StatusError, err.Error())
		}
		return out
	}
	if !podReady(pod) {
		add("ops pod", doctor.StatusError, fmt.Sprintf("not Ready (phase=%s)", pod.Status.Phase))
	} else {
		add("ops pod", doctor.StatusOK, fmt.Sprintf("%s (image=%s)", pod.Status.Phase, pod.Spec.Containers[0].Image))
	}

	if _, err := cs.CoreV1().ServiceAccounts(execbackend.K8sOpsNamespace).Get(probeCtx, "roksbnkctl-ops", metav1.GetOptions{}); err != nil {
		add("ops serviceaccount", doctor.StatusError, err.Error())
	} else {
		add("ops serviceaccount", doctor.StatusOK, "roksbnkctl-ops")
	}

	if _, err := cs.RbacV1().ClusterRoles().Get(probeCtx, "roksbnkctl-ops", metav1.GetOptions{}); err != nil {
		add("ops clusterrole", doctor.StatusError, err.Error())
	} else {
		add("ops clusterrole", doctor.StatusOK, "roksbnkctl-ops")
	}

	if _, err := cs.RbacV1().ClusterRoleBindings().Get(probeCtx, "roksbnkctl-ops", metav1.GetOptions{}); err != nil {
		add("ops clusterrolebinding", doctor.StatusError, err.Error())
	} else {
		add("ops clusterrolebinding", doctor.StatusOK, "roksbnkctl-ops")
	}

	secret, err := cs.CoreV1().Secrets(execbackend.K8sOpsNamespace).Get(probeCtx, execbackend.K8sOpsSecretName, metav1.GetOptions{})
	if err != nil {
		add("ops cred secret", doctor.StatusError, err.Error())
	} else {
		key := secret.Data["IBMCLOUD_API_KEY"]
		if len(key) == 0 {
			add("ops cred secret", doctor.StatusError, "IBMCLOUD_API_KEY data field empty")
		} else {
			add("ops cred secret", doctor.StatusOK, fmt.Sprintf("%s (rotated %s)", secret.Name, secret.Annotations["roksbnkctl.io/rotated-at"]))
		}
	}

	// RBAC negative check: ops SA must NOT have cluster-wide pods/delete.
	// Uses SubjectAccessReview impersonating the ops SA.
	sar := &authv1.SubjectAccessReview{
		Spec: authv1.SubjectAccessReviewSpec{
			User: "system:serviceaccount:" + execbackend.K8sOpsNamespace + ":roksbnkctl-ops",
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: "default",
				Verb:      "delete",
				Resource:  "pods",
			},
		},
	}
	resp, err := cs.AuthorizationV1().SubjectAccessReviews().Create(probeCtx, sar, metav1.CreateOptions{})
	if err != nil {
		add("ops rbac (least-privilege)", doctor.StatusWarning, "could not run SubjectAccessReview: "+err.Error())
	} else if resp.Status.Allowed {
		add("ops rbac (least-privilege)", doctor.StatusError, "ops SA can delete pods in 'default' namespace — too permissive! Reapply `roksbnkctl ops install` to reset RBAC")
	} else {
		add("ops rbac (least-privilege)", doctor.StatusOK, "ops SA cannot delete pods cluster-wide (good)")
	}

	return out
}

// runSSHBackendChecks probes the SSH backend's prerequisites for the
// named target.
//
//   - target resolves in the workspace config
//   - ssh connect succeeds
//   - sudo -n true succeeds (for the apt bootstrap path)
//   - if a tool name is implied, command -v finds it on PATH
//
// PRD 03 §"SSH" §"doctor extensions".
func runSSHBackendChecks(ctx context.Context, cctx *config.Context, name string) []doctor.Check {
	out := []doctor.Check{}
	add := func(rowName string, status doctor.CheckStatus, detail string) {
		out = append(out, doctor.Check{
			Name:        rowName,
			Status:      status,
			Detail:      detail,
			BackendName: "ssh",
		})
	}

	if cctx == nil || cctx.Workspace == nil {
		add("ssh:"+name+" target", doctor.StatusError, "no workspace context")
		return out
	}

	t, err := remote.LoadTarget(cctx.WorkspaceName, name)
	if err != nil {
		add("ssh:"+name+" target", doctor.StatusError, err.Error())
		return out
	}
	tfOutputs, err := loadTFOutputsForTarget(ctx, cctx, t)
	if err != nil {
		add("ssh:"+name+" target", doctor.StatusError, "tf outputs: "+err.Error())
		return out
	}
	signer, err := remote.ResolveSigner(t, tfOutputs)
	if err != nil {
		add("ssh:"+name+" target", doctor.StatusError, "key: "+err.Error())
		return out
	}
	t.Signer = signer
	t.HostKeyCallback = remote.HostKeyCallback(remote.HostKeyOptions{Insecure: flagInsecureHostKey})
	add("ssh:"+name+" target", doctor.StatusOK, fmt.Sprintf("%s@%s:%d", t.User, t.Host, t.Port))

	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	client, err := remote.Connect(probeCtx, t)
	if err != nil {
		add("ssh:"+name+" connect", doctor.StatusError, err.Error())
		return out
	}
	defer client.Close()
	add("ssh:"+name+" connect", doctor.StatusOK, "tcp + handshake OK")

	// sudo -n true → exit 0 ⇒ passwordless sudo configured.
	rc, _ := client.Run(probeCtx, []string{"sudo", "-n", "true"}, remote.RunOpts{})
	if rc == 0 {
		add("ssh:"+name+" sudo", doctor.StatusOK, "passwordless (apt bootstrap feasible)")
	} else {
		add("ssh:"+name+" sudo", doctor.StatusWarning, fmt.Sprintf("sudo -n true rc=%d — bootstrap will fail; pre-install tools or configure NOPASSWD", rc))
	}

	return out
}
