package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/doctor"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
	"github.com/jgruberf5/roksbnkctl/internal/test"
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
	restCfg, restErr := k8s.BuildRESTConfig("")
	if restErr != nil {
		// Non-fatal; the env-runtime probe degrades to skip.
		restCfg = nil
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
		// Sprint 5: cred rotation freshness check. Annotated with
		// roksbnkctl.io/rotated-at on `ops install`; we surface a
		// warning (not error) when the value is older than 30 days
		// — best-practice rotation reminder, not a hard fail.
		if rotated := secret.Annotations["roksbnkctl.io/rotated-at"]; rotated != "" {
			if t, perr := time.Parse(time.RFC3339, rotated); perr == nil {
				age := time.Since(t)
				if age > 30*24*time.Hour {
					add("ops cred rotation", doctor.StatusWarning,
						fmt.Sprintf("API key has not been rotated for %d days; consider re-running `roksbnkctl ops install` with a fresh key", int(age.Hours()/24)))
				} else {
					add("ops cred rotation", doctor.StatusOK,
						fmt.Sprintf("rotated %d days ago", int(age.Hours()/24)))
				}
			}
		}
	}

	// Sprint 5: ops-pod env runtime check. Verify the pod's
	// environment actually carries IBMCLOUD_API_KEY at exec time.
	// Failure modes this catches: the Secret was deleted out-of-band
	// after install; the pod was created from a stale envFrom that
	// no longer references the Secret; the deployment somehow lost
	// the secretRef.
	//
	// We exec `printenv IBMCLOUD_API_KEY` against the pod and check
	// that the response is non-empty. The actual VALUE never lands
	// in the doctor output — we render "(present, redacted)" or
	// "(empty)" so the leak surface stays zero.
	if restCfg != nil {
		if probeOpsPodEnv(probeCtx, cs, restCfg) {
			add("ops pod env IBMCLOUD_API_KEY", doctor.StatusOK, "(present, redacted)")
		} else {
			add("ops pod env IBMCLOUD_API_KEY", doctor.StatusError,
				"empty at runtime — Secret missing or envFrom misconfigured; rerun `roksbnkctl ops install`")
		}
	} else {
		add("ops pod env IBMCLOUD_API_KEY", doctor.StatusWarning,
			"could not build REST config to probe pod env at runtime")
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

// runDNSProbeCheck runs the embedded miekg/dns probe against the
// workspace's configured default DNS target. Returns (Check, true)
// when a probe was attempted; (zero, false) when there's no
// default_target configured so the doctor output stays compact.
//
// Sprint 5 doctor extension. The probe library is built into the
// binary (no external `dig` install required), so this is mostly an
// informational latency measurement; an actual failure would surface
// a real DNS infrastructure problem worth flagging.
func runDNSProbeCheck(ctx context.Context, cctx *config.Context) (doctor.Check, bool) {
	if cctx == nil || cctx.Workspace == nil {
		return doctor.Check{}, false
	}
	target := cctx.Workspace.Test.DNS.DefaultTarget
	if target == "" {
		return doctor.Check{}, false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p := &test.Probe{
		Target:     target,
		Type:       1, // dns.TypeA — inlined to avoid pulling miekg/dns into the cli package directly
		Server:     "system",
		Iterations: 1,
		Timeout:    2 * time.Second,
		Backend:    "local",
	}
	res, err := p.Run(probeCtx)
	c := doctor.Check{Name: "dns probe (" + target + ")"}
	if err != nil {
		c.Status = doctor.StatusError
		c.Detail = err.Error()
		return c, true
	}
	if res.Err != "" {
		c.Status = doctor.StatusError
		c.Detail = fmt.Sprintf("%s: %s", res.Rcode, res.Err)
		return c, true
	}
	if len(res.Answers) == 0 {
		c.Status = doctor.StatusWarning
		c.Detail = fmt.Sprintf("no answers (rcode=%s, server=%s)", res.Rcode, res.Server)
		return c, true
	}
	c.Status = doctor.StatusOK
	c.Detail = fmt.Sprintf("%d answer(s) in %.1fms (server=%s)", len(res.Answers), res.RTTMs.P50, res.Server)
	return c, true
}

// probeOpsPodEnv exec's `printenv IBMCLOUD_API_KEY` against the ops
// pod and reports whether the value comes back non-empty. The actual
// value is read into a local buffer and discarded — only the
// "present" / "empty" verdict surfaces via the boolean return.
//
// Sprint 5 doctor extension: catches the failure mode where a stale
// envFrom or a missing Secret has the pod running but with no
// IBMCLOUD_API_KEY available — `roksbnkctl ibmcloud --backend k8s`
// would surface as "auth failed" with a confusing error; this probe
// surfaces it earlier.
func probeOpsPodEnv(ctx context.Context, cs kubernetes.Interface, cfg *rest.Config) bool {
	req := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(execbackend.K8sOpsPodName).
		Namespace(execbackend.K8sOpsNamespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"printenv", "IBMCLOUD_API_KEY"},
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return false
	}
	var stdout, stderr bytes.Buffer
	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return false
	}
	// printenv exits 0 + writes the value+newline when the var is
	// set; exits 1 + writes nothing when it isn't. Trim whitespace
	// and check for emptiness — never log the value.
	val := strings.TrimSpace(stdout.String())
	return val != ""
}
