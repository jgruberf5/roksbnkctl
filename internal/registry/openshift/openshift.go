// Package openshift implements the Sprint 29 RegistryTarget for the cluster's
// own OpenShift internal image registry (PRD 11 §2) — the first-class air-gap
// target. The registry has two faces: an external route (host-reachable, where
// roksbnkctl pushes and the in-process helm provider pulls charts) and an
// in-cluster service (where pods pull images via system:image-puller RBAC, no
// pull secret). The endpoint methods are pure string logic; Prepare performs the
// live cluster bootstrap (enable the route, mint a push token, bind RBAC) and is
// validated by the Stage 6 gated-live air-gap verify.
package openshift

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// InternalServiceHost is the in-cluster address the kubelet pulls images from.
const InternalServiceHost = "image-registry.openshift-image-registry.svc:5000"

// pushSAName is the ServiceAccount roksbnkctl mints to push into the mirror.
const pushSAName = "bnk-mirror-pusher"

// bnkNamespaces are the BNK install namespaces whose ServiceAccounts must be
// able to pull the mirrored images.
var bnkNamespaces = []string{"f5-bnk", "f5-utils", "f5-app"}

// Target is the OpenShift internal-registry RegistryTarget. RouteHost + PushToken
// are populated by Prepare; the endpoint methods are usable once they are set
// (or hydrated from a registry-mirror.json record).
type Target struct {
	Namespace string // the mirror project, e.g. "bnk-mirror"
	RouteHost string // the external registry route, set by Prepare
	PushToken string // the push SA token, set by Prepare
}

// ── mirror.Target (push side) ───────────────────────────────────────────────

// PushHost is the external route host roksbnkctl pushes to.
func (t *Target) PushHost() string { return t.RouteHost }

// PushRef is the destination reference for an artifact on the route, preserving
// its charts/ | images/ | utils/ path: "<route>/<ns>/<name>:<tag>".
func (t *Target) PushRef(a bnkbom.Artifact) string {
	return fmt.Sprintf("%s/%s/%s:%s", t.RouteHost, t.Namespace, a.Name, a.Tag)
}

// PushAuth authenticates pushes. The OpenShift registry accepts any username with
// a valid bearer token as the password.
func (t *Target) PushAuth() authn.Authenticator {
	return &authn.Basic{Username: pushSAName, Password: t.PushToken}
}

// ── pull-side endpoints (the install redirect consumes these) ───────────────

// ImagePullRef is where pods pull an image — the in-cluster service, by digest
// when known (immutable), else by tag.
func (t *Target) ImagePullRef(a bnkbom.Artifact) string {
	ref := fmt.Sprintf("%s/%s/%s", InternalServiceHost, t.Namespace, a.Name)
	if a.Digest != "" {
		return ref + "@" + a.Digest
	}
	return ref + ":" + a.Tag
}

// ChartPullRef is where the host's helm provider pulls a chart — the route, as an
// OCI reference.
func (t *Target) ChartPullRef(a bnkbom.Artifact) string {
	return fmt.Sprintf("oci://%s/%s/%s", t.RouteHost, t.Namespace, a.Name)
}

// ImageHostPath is the image-host root the BNK install redirect points
// far_repo_url's image references at: "<svc>/<ns>" (pods → kubelet).
func (t *Target) ImageHostPath() string {
	return InternalServiceHost + "/" + t.Namespace
}

// ChartHostPath is the chart-host root the helm_release repository points at:
// "<route>/<ns>" (host → helm provider).
func (t *Target) ChartHostPath() string {
	return t.RouteHost + "/" + t.Namespace
}

// ── Prepare (live cluster bootstrap) ────────────────────────────────────────

var (
	imageRegistryConfigGVR = schema.GroupVersionResource{Group: "imageregistry.operator.openshift.io", Version: "v1", Resource: "configs"}
	routeGVR               = schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
)

// Prepare bootstraps the internal registry for mirroring: enable the external
// route, read its host, ensure the mirror namespace, mint a push token, and bind
// the pull RBAC. It is idempotent. NOTE: validated by the Stage 6 gated-live
// air-gap verify — the OpenShift GVRs, the route name, and the token-request flow
// are exercised against a real cluster there.
func (t *Target) Prepare(ctx context.Context, cfg *rest.Config) error {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("dynamic client: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("clientset: %w", err)
	}

	// 1. Enable the default registry route.
	patch := []byte(`{"spec":{"defaultRoute":true}}`)
	if _, err := dyn.Resource(imageRegistryConfigGVR).Patch(ctx, "cluster", types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("enable registry defaultRoute: %w", err)
	}

	// 2. Read the route host (the operator reconciles the route shortly after
	//    defaultRoute flips; poll briefly).
	host, err := t.waitForRouteHost(ctx, dyn)
	if err != nil {
		return err
	}
	t.RouteHost = host

	// 3. Ensure the mirror namespace.
	if err := ensureNamespace(ctx, cs, t.Namespace); err != nil {
		return err
	}

	// 4. Push identity: a ServiceAccount + registry-editor binding + a token.
	if err := ensureServiceAccount(ctx, cs, t.Namespace, pushSAName); err != nil {
		return err
	}
	if err := ensureRoleBinding(ctx, cs, t.Namespace, "bnk-mirror-pusher-editor", "registry-editor",
		[]rbacv1.Subject{{Kind: "ServiceAccount", Name: pushSAName, Namespace: t.Namespace}}); err != nil {
		return err
	}
	tok, err := cs.CoreV1().ServiceAccounts(t.Namespace).CreateToken(ctx, pushSAName,
		&authnv1.TokenRequest{Spec: authnv1.TokenRequestSpec{ExpirationSeconds: ptr(int64(3600))}}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("mint push token: %w", err)
	}
	t.PushToken = tok.Status.Token

	// 5. Pull RBAC: let the BNK namespaces' ServiceAccounts pull from the mirror.
	var pullers []rbacv1.Subject
	for _, ns := range bnkNamespaces {
		pullers = append(pullers, rbacv1.Subject{Kind: "Group", Name: "system:serviceaccounts:" + ns, APIGroup: "rbac.authorization.k8s.io"})
	}
	if err := ensureRoleBinding(ctx, cs, t.Namespace, "bnk-mirror-pullers", "system:image-puller", pullers); err != nil {
		return err
	}
	return nil
}

func (t *Target) waitForRouteHost(ctx context.Context, dyn dynamic.Interface) (string, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		u, err := dyn.Resource(routeGVR).Namespace("openshift-image-registry").
			Get(ctx, "default-route-openshift-image-registry", metav1.GetOptions{})
		if err == nil {
			if host, found, _ := unstructured.NestedString(u.Object, "spec", "host"); found && host != "" {
				return host, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("registry route not ready within 2m (last err: %v)", err)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func ensureNamespace(ctx context.Context, cs kubernetes.Interface, ns string) error {
	_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	return ignoreAlreadyExists(err, "namespace "+ns)
}

func ensureServiceAccount(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	_, err := cs.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}, metav1.CreateOptions{})
	return ignoreAlreadyExists(err, "serviceaccount "+name)
}

func ensureRoleBinding(ctx context.Context, cs kubernetes.Interface, ns, name, clusterRole string, subjects []rbacv1.Subject) error {
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: clusterRole},
		Subjects:   subjects,
	}
	_, err := cs.RbacV1().RoleBindings(ns).Create(ctx, rb, metav1.CreateOptions{})
	return ignoreAlreadyExists(err, "rolebinding "+name)
}

func ignoreAlreadyExists(err error, what string) error {
	if err == nil || apierrors.IsAlreadyExists(err) {
		return nil
	}
	return fmt.Errorf("create %s: %w", what, err)
}

func ptr[T any](v T) *T { return &v }
