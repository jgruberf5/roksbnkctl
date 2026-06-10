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

// mirrorProjects are the OpenShift projects (one per FAR category) the mirror
// pushes into — because the internal registry is flat <project>/<name> and the
// install pulls images from project "images", charts from "charts", etc. These
// are the categories the BOM produces (F5 charts/images/utils + the cert-manager
// and node-labeler deps).
var mirrorProjects = []string{"images", "charts", "utils", "jetstack", "bitnami"}

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

// PushRef is the destination on the route. The OpenShift internal registry is a
// FLAT <project>/<name> registry — imagestream names can't nest — so the FAR
// category is the project: "images/tmm-img" → project "images", stream "tmm-img".
// (This flattening is OpenShift-specific; a generic OCI target keeps the
// configured namespace and nests the category under it, e.g.
// "<host>/<ns>/images/tmm-img".)
func (t *Target) PushRef(a bnkbom.Artifact) string {
	return fmt.Sprintf("%s/%s:%s", t.RouteHost, a.Name, a.Tag)
}

// PushAuth authenticates pushes. The OpenShift registry accepts any username with
// a valid bearer token as the password.
func (t *Target) PushAuth() authn.Authenticator {
	return &authn.Basic{Username: pushSAName, Password: t.PushToken}
}

// ── pull-side endpoints (the install redirect consumes these) ───────────────

// ImagePullRef is where pods pull an image — the in-cluster service, by digest
// when known. Category-as-project: "<svc>/images/tmm-img".
func (t *Target) ImagePullRef(a bnkbom.Artifact) string {
	ref := InternalServiceHost + "/" + a.Name
	if a.Digest != "" {
		return ref + "@" + a.Digest
	}
	return ref + ":" + a.Tag
}

// ChartPullRef is where the host's helm provider pulls a chart over the route.
func (t *Target) ChartPullRef(a bnkbom.Artifact) string {
	return "oci://" + t.RouteHost + "/" + a.Name
}

// ImageHostPath is the image-host root the install redirect points the image
// references at — the bare service host. Category-as-project makes this work with
// the install's conventions unchanged: the FLO chart's image.repository becomes
// "<svc>/images" (it appends the bare image name) and the CNEInstance
// spec.registry.uri becomes "<svc>" (the CNE controller appends "/images/<name>").
func (t *Target) ImageHostPath() string {
	return InternalServiceHost
}

// ChartHostPath is the chart-host root — the bare route (helm repo becomes
// "oci://<route>/charts").
func (t *Target) ChartHostPath() string {
	return t.RouteHost
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

	// 3. Ensure the SA-home namespace + one project per FAR category.
	if err := ensureNamespace(ctx, cs, t.Namespace); err != nil {
		return err
	}
	for _, p := range mirrorProjects {
		if err := ensureNamespace(ctx, cs, p); err != nil {
			return err
		}
	}

	// 4. Push identity: a ServiceAccount + a cluster-wide registry-editor binding
	//    (it pushes into every category project) + a token.
	if err := ensureServiceAccount(ctx, cs, t.Namespace, pushSAName); err != nil {
		return err
	}
	if err := ensureClusterRoleBinding(ctx, cs, "bnk-mirror-pusher-editor", "registry-editor",
		[]rbacv1.Subject{{Kind: "ServiceAccount", Name: pushSAName, Namespace: t.Namespace}}); err != nil {
		return err
	}
	tok, err := cs.CoreV1().ServiceAccounts(t.Namespace).CreateToken(ctx, pushSAName,
		&authnv1.TokenRequest{Spec: authnv1.TokenRequestSpec{ExpirationSeconds: ptr(int64(3600))}}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("mint push token: %w", err)
	}
	t.PushToken = tok.Status.Token

	// 5. Pull RBAC: let every ServiceAccount in the cluster pull from the mirror
	//    projects (cluster-wide image-puller for system:serviceaccounts). The BNK
	//    install spans many namespaces — f5-bnk/f5-utils/f5-app, cert-manager, and
	//    more — and the mirrored images are the cluster's own, so a blanket SA
	//    image-puller is the right scope and avoids per-namespace whack-a-mole
	//    (cert-manager pods hit "authentication required" when only the f5-* SAs
	//    were bound).
	pullers := []rbacv1.Subject{{Kind: "Group", Name: "system:serviceaccounts", APIGroup: "rbac.authorization.k8s.io"}}
	if err := ensureClusterRoleBinding(ctx, cs, "bnk-mirror-pullers", "system:image-puller", pullers); err != nil {
		return err
	}
	return nil
}

func (t *Target) waitForRouteHost(ctx context.Context, dyn dynamic.Interface) (string, error) {
	deadline := time.Now().Add(2 * time.Minute)
	var lastErr error
	for {
		// The default registry route is named "default-route" (the
		// "default-route-openshift-image-registry…" string is its HOST, not its
		// name) and targets the image-registry service. List + match on the
		// target service so we're robust to the route name.
		list, err := dyn.Resource(routeGVR).Namespace("openshift-image-registry").List(ctx, metav1.ListOptions{})
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("no route to the image-registry service in openshift-image-registry yet")
			for i := range list.Items {
				obj := list.Items[i].Object
				to, _, _ := unstructured.NestedString(obj, "spec", "to", "name")
				host, _, _ := unstructured.NestedString(obj, "spec", "host")
				if host != "" && (to == "image-registry" || list.Items[i].GetName() == "default-route") {
					return host, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("registry route not ready within 2m (last err: %v)", lastErr)
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

func ensureClusterRoleBinding(ctx context.Context, cs kubernetes.Interface, name, clusterRole string, subjects []rbacv1.Subject) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: clusterRole},
		Subjects:   subjects,
	}
	_, err := cs.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		return ignoreAlreadyExists(err, "clusterrolebinding "+name)
	}
	// Already exists — reconcile the subject list, which can change between
	// roksbnkctl versions (e.g. the puller widened from the f5-* SAs to all SAs).
	// RoleRef is immutable but never changes here.
	existing, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get clusterrolebinding %s: %w", name, err)
	}
	existing.Subjects = subjects
	if _, err := cs.RbacV1().ClusterRoleBindings().Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update clusterrolebinding %s: %w", name, err)
	}
	return nil
}

func ignoreAlreadyExists(err error, what string) error {
	if err == nil || apierrors.IsAlreadyExists(err) {
		return nil
	}
	return fmt.Errorf("create %s: %w", what, err)
}

func ptr[T any](v T) *T { return &v }
