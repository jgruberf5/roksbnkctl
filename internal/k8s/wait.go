package k8s

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// WaitForDeploymentReady polls apps/v1 Deployment in ns/name until
// Status.AvailableReplicas == Status.Replicas (both > 0). Returns an error if
// the deadline passes before the Deployment is ready.
//
// Interval is 5 s. timeout is the caller-supplied deadline.
func WaitForDeploymentReady(ctx context.Context, clientset kubernetes.Interface, ns, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		d, err := clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			// Deployment may not exist yet — keep polling.
			return false, nil //nolint:nilerr
		}
		desired := d.Status.Replicas
		available := d.Status.AvailableReplicas
		if desired > 0 && available == desired {
			return true, nil
		}
		return false, nil
	})
}

// CertificateGVR is the GroupVersionResource for cert-manager Certificate CRs.
// Exported so phases and tests can reference it without duplicating the constant.
var CertificateGVR = schema.GroupVersionResource{
	Group:    "cert-manager.io",
	Version:  "v1",
	Resource: "certificates",
}

// WaitForCertificateReady polls cert-manager.io/v1 Certificate in ns/name
// via the dynamic client until the Ready condition is True. Returns an error
// if the deadline passes.
//
// Interval is 5 s. timeout is the caller-supplied deadline.
func WaitForCertificateReady(ctx context.Context, dyn dynamic.Interface, ns, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		obj, err := dyn.Resource(CertificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		return IsCertificateReady(obj.Object), nil
	})
}

// IsCertificateReady walks the unstructured status.conditions slice to find
// a condition with type=Ready and status=True. Exported for use by phases and tests.
func IsCertificateReady(obj map[string]interface{}) bool {
	status, ok := obj["status"].(map[string]interface{})
	if !ok {
		return false
	}
	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t != "Ready" {
			continue
		}
		if s, _ := cond["status"].(string); s == "True" {
			return true
		}
	}
	return false
}

// WaitForNamespaceGone polls until namespace ns no longer exists (for cleanup).
// Tolerates NotFound immediately. Returns nil if gone within timeout.
func WaitForNamespaceGone(ctx context.Context, clientset kubernetes.Interface, ns string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		_, err := clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if err != nil {
			// Any error (including NotFound) means the namespace is gone or unreachable.
			return true, nil
		}
		return false, nil
	})
}

// WaitForDaemonSetReady polls apps/v1 DaemonSet in ns/name until
// Status.NumberReady == Status.DesiredNumberScheduled (both > 0).
// Returns an error if the deadline passes before the DaemonSet is ready.
//
// Interval is 5 s. timeout is the caller-supplied deadline.
func WaitForDaemonSetReady(ctx context.Context, clientset kubernetes.Interface, ns, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		ds, err := clientset.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		desired := ds.Status.DesiredNumberScheduled
		ready := ds.Status.NumberReady
		if desired > 0 && ready == desired {
			return true, nil
		}
		return false, nil
	})
}

// WaitForNodeHugepagesCapacity polls the named Node until its
// .status.capacity["hugepages-2Mi"] is >= want. Used by Phase 11b after the
// hugepages-setup DaemonSet has restarted kubelet, since DS-Ready does NOT
// imply kubelet has re-advertised hugepages capacity yet (kubelet picks up
// the new sysfs value on next sync).
//
// want is a Kubernetes-style quantity string (e.g. "4Gi"). Parse via
// resource.MustParse on call.
func WaitForNodeHugepagesCapacity(ctx context.Context, clientset kubernetes.Interface, nodeName, want string, timeout time.Duration) error {
	wantQty, err := resource.ParseQuantity(want)
	if err != nil {
		return fmt.Errorf("parse hugepages quantity %q: %w", want, err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr — keep polling, node might be transiently unreachable
		}
		got, ok := node.Status.Capacity["hugepages-2Mi"]
		if !ok {
			return false, nil
		}
		// node.status.capacity is map[corev1.ResourceName]resource.Quantity
		return got.Cmp(wantQty) >= 0, nil
	})
}

// WaitForCRDExists polls apiextensions.k8s.io/v1 CustomResourceDefinition until
// the named CRD is visible in the API server. Used to confirm cert-manager CRDs
// are established before applying the cert chain.
func WaitForCRDExists(ctx context.Context, dyn dynamic.Interface, crdName string, timeout time.Duration) error {
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		_, err := dyn.Resource(crdGVR).Get(ctx, crdName, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		return true, nil
	})
}

// WaitForUnstructuredCondition polls a namespaced CR via the dynamic client until
// the string field at jsonPath (e.g. "status.state") equals expectedValue.
// jsonPath must use dot notation, e.g. "status.state" → unstructured.NestedString(obj, "status", "state").
// Only single-level nesting under status is supported (sufficient for BNK CRs).
//
// Polls every 5 s. Returns nil on match; returns an error containing the last-seen
// value if the context deadline / timeout expires.
//
// Compatible with WaitForCRDExists / WaitForCertificateReady patterns.
func WaitForUnstructuredCondition(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name, jsonPath, expectedValue string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Parse jsonPath into nested field keys (e.g. "status.state" → ["status","state"]).
	fields := splitDotPath(jsonPath)

	var lastSeen string
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		obj, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		val, _, _ := unstructured.NestedString(obj.Object, fields...)
		lastSeen = val
		return val == expectedValue, nil
	})
	if err != nil {
		return fmt.Errorf("WaitForUnstructuredCondition: %s/%s %s: timeout waiting for %q, last seen %q: %w",
			namespace, name, jsonPath, expectedValue, lastSeen, err)
	}
	return nil
}

// splitDotPath splits a dot-separated jsonPath string into field segments.
// "status.state" → ["status", "state"].
func splitDotPath(path string) []string {
	if path == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		parts = append(parts, path[start:])
	}
	return parts
}

// certReplicaStatus is used by Phase 13 postflight check (pure read).
// It returns (available, desired, error).
func DeploymentReplicaStatus(ctx context.Context, clientset kubernetes.Interface, ns, name string) (available, desired int32, err error) {
	d, err := clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("get deployment %s/%s: %w", ns, name, err)
	}
	return d.Status.AvailableReplicas, d.Status.Replicas, nil
}
