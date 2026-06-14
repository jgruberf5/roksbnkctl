package k8s

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ErrEnvoyNotReady is returned by EnvoyNodePortEndpoint when the Envoy
// data-plane Service or a Ready node is not yet present. Callers MUST
// surface this as a front-end error — never fall back to a default VIP.
var ErrEnvoyNotReady = errors.New("envoy NodePort endpoint not ready")

// EnvoyNodePortEndpoint discovers the NodePort endpoint for an Envoy Gateway
// data-plane Service in the given namespace, selected by the owning Gateway
// labels. Returns a node-private IP and the nodePort mapped from listenerPort.
//
// When gatewayName is non-empty the label
//
//	gateway.envoyproxy.io/owning-gateway-name=<gatewayName>
//
// is added to the selector for a precise match. When empty (single-envoy-deploy
// convenience), only the namespace label is used — valid when exactly one Envoy
// Gateway is deployed in namespace.
//
// Returns ErrEnvoyNotReady (errors.Is-able) when:
//   - no matching Service exists yet
//   - the Service has no nodePort on the listenerPort port
//   - no Ready node is found
func EnvoyNodePortEndpoint(
	ctx context.Context,
	kubeconfigPath string,
	namespace string,
	gatewayName string,
	listenerPort int32,
) (nodeIP string, nodePort int32, err error) {
	cs, err := BuildClientset(kubeconfigPath)
	if err != nil {
		return "", 0, fmt.Errorf("envoy endpoint lookup: build clientset: %w", err)
	}
	return envoyNodePortFromClientset(ctx, cs, namespace, gatewayName, listenerPort)
}

// envoyNodePortFromClientset is the testable inner implementation.
// Accepts a kubernetes.Interface so tests can substitute fake.NewClientset.
func envoyNodePortFromClientset(
	ctx context.Context,
	cs kubernetes.Interface,
	namespace string,
	gatewayName string,
	listenerPort int32,
) (string, int32, error) {
	// Build label selector for the Envoy data-plane Service.
	// Envoy Gateway stamps owning-gateway labels on the generated Service.
	selector := fmt.Sprintf(
		"gateway.envoyproxy.io/owning-gateway-namespace=%s,app.kubernetes.io/managed-by=envoy-gateway",
		namespace,
	)
	if gatewayName != "" {
		selector = fmt.Sprintf(
			"gateway.envoyproxy.io/owning-gateway-namespace=%s,gateway.envoyproxy.io/owning-gateway-name=%s",
			namespace, gatewayName,
		)
	}

	svcs, err := cs.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return "", 0, fmt.Errorf("envoy endpoint: list services: %w", err)
	}
	if len(svcs.Items) == 0 {
		return "", 0, fmt.Errorf("%w: no Envoy data-plane Service found in namespace %q (selector: %s)",
			ErrEnvoyNotReady, namespace, selector)
	}
	// Ambiguous: when no explicit gatewayName was given and the broad selector
	// matches more than one Service, picking Items[0] would be arbitrary.
	// Fail closed so the caller surfaces a clear error rather than silently
	// benchmarking the wrong endpoint.
	if gatewayName == "" && len(svcs.Items) > 1 {
		return "", 0, fmt.Errorf("%w: ambiguous: %d Envoy Gateway Services found in namespace %q with broad selector — pass an explicit gateway name",
			ErrEnvoyNotReady, len(svcs.Items), namespace)
	}

	svc := &svcs.Items[0]
	np := nodePortForListener(svc, listenerPort)
	if np == 0 {
		return "", 0, fmt.Errorf("%w: Service %q has no nodePort for listener port %d",
			ErrEnvoyNotReady, svc.Name, listenerPort)
	}

	// Find the InternalIP of the first Ready node.
	// With externalTrafficPolicy: Cluster any Ready node's IP works.
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("envoy endpoint: list nodes: %w", err)
	}

	ip := firstReadyNodeInternalIP(nodes)
	if ip == "" {
		return "", 0, fmt.Errorf("%w: no Ready node with an InternalIP found", ErrEnvoyNotReady)
	}

	return ip, np, nil
}

// nodePortForListener returns the NodePort of the service port whose Port
// equals listenerPort. Returns 0 when no match or nodePort is unset.
// Single-port services use their only port regardless of listenerPort.
func nodePortForListener(svc *corev1.Service, listenerPort int32) int32 {
	if len(svc.Spec.Ports) == 1 {
		return svc.Spec.Ports[0].NodePort
	}
	for _, p := range svc.Spec.Ports {
		if p.Port == listenerPort {
			return p.NodePort
		}
	}
	return 0
}

// firstReadyNodeInternalIP returns the InternalIP of the first Ready node.
func firstReadyNodeInternalIP(nodes *corev1.NodeList) string {
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if !nodeIsReady(node) {
			continue
		}
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				return addr.Address
			}
		}
	}
	return ""
}

// nodeIsReady reports whether the node's Ready condition is True.
func nodeIsReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
