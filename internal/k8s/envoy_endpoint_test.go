package k8s

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// makeEnvoyService builds a fake Envoy data-plane Service with the owning-gateway
// labels Envoy Gateway stamps on generated Services.
func makeEnvoyService(namespace, gatewayName string, listenerPort, nodePort int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "envoy-perf-proxies-" + gatewayName + "-abc123",
			Namespace: namespace,
			Labels: map[string]string{
				"gateway.envoyproxy.io/owning-gateway-namespace": namespace,
				"gateway.envoyproxy.io/owning-gateway-name":      gatewayName,
				"app.kubernetes.io/managed-by":                   "envoy-gateway",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{
					Port:     listenerPort,
					NodePort: nodePort,
				},
			},
		},
	}
}

// makeReadyNode builds a fake node that is Ready with an InternalIP.
func makeReadyNode(name, internalIP string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: internalIP},
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

// makeNotReadyNode builds a fake node that is NOT Ready.
func makeNotReadyNode(name, internalIP string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: internalIP},
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
		},
	}
}

func TestEnvoyNodePortFromClientset_HappyPath(t *testing.T) {
	svc := makeEnvoyService("perf-proxies", "perf-envoy-ai-rig", 10080, 31234)
	node := makeReadyNode("node-1", "10.0.1.50")
	cs := fake.NewClientset(svc, node)

	ip, np, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "perf-envoy-ai-rig", 10080)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.1.50" {
		t.Errorf("nodeIP = %q, want %q", ip, "10.0.1.50")
	}
	if np != 31234 {
		t.Errorf("nodePort = %d, want 31234", np)
	}
}

func TestEnvoyNodePortFromClientset_NoService_ReturnsNotReady(t *testing.T) {
	node := makeReadyNode("node-1", "10.0.1.50")
	cs := fake.NewClientset(node)

	_, _, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "perf-envoy-ai-rig", 10080)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrEnvoyNotReady) {
		t.Errorf("expected ErrEnvoyNotReady, got: %v", err)
	}
}

func TestEnvoyNodePortFromClientset_ServiceNoNodePort_ReturnsNotReady(t *testing.T) {
	// Service exists but nodePort is 0 (not yet assigned).
	svc := makeEnvoyService("perf-proxies", "perf-envoy-ai-rig", 10080, 0 /* no nodePort */)
	node := makeReadyNode("node-1", "10.0.1.50")
	cs := fake.NewClientset(svc, node)

	_, _, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "perf-envoy-ai-rig", 10080)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrEnvoyNotReady) {
		t.Errorf("expected ErrEnvoyNotReady, got: %v", err)
	}
}

func TestEnvoyNodePortFromClientset_NoReadyNode_ReturnsNotReady(t *testing.T) {
	svc := makeEnvoyService("perf-proxies", "perf-envoy-ai-rig", 10080, 31234)
	notReady := makeNotReadyNode("node-1", "10.0.1.50")
	cs := fake.NewClientset(svc, notReady)

	_, _, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "perf-envoy-ai-rig", 10080)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrEnvoyNotReady) {
		t.Errorf("expected ErrEnvoyNotReady, got: %v", err)
	}
}

func TestEnvoyNodePortFromClientset_MultiPortService_MatchesListenerPort(t *testing.T) {
	// Service with two ports; listenerPort=10080 should pick nodePort=31234.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "envoy-svc",
			Namespace: "perf-proxies",
			Labels: map[string]string{
				"gateway.envoyproxy.io/owning-gateway-namespace": "perf-proxies",
				"gateway.envoyproxy.io/owning-gateway-name":      "perf-envoy-ai-rig",
				"app.kubernetes.io/managed-by":                   "envoy-gateway",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{Port: 8080, NodePort: 30080},
				{Port: 10080, NodePort: 31234},
			},
		},
	}
	node := makeReadyNode("node-1", "10.0.1.50")
	cs := fake.NewClientset(svc, node)

	_, np, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "perf-envoy-ai-rig", 10080)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if np != 31234 {
		t.Errorf("nodePort = %d, want 31234", np)
	}
}

func TestEnvoyNodePortFromClientset_EmptyGatewayName_FallsBackToBroadSelector(t *testing.T) {
	// Empty gatewayName: broad selector by namespace+managed-by label.
	svc := makeEnvoyService("perf-proxies", "perf-envoy-ai-rig", 10080, 31999)
	node := makeReadyNode("node-1", "10.0.2.10")
	cs := fake.NewClientset(svc, node)

	ip, np, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "" /* empty */, 10080)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.2.10" {
		t.Errorf("nodeIP = %q, want %q", ip, "10.0.2.10")
	}
	if np != 31999 {
		t.Errorf("nodePort = %d, want 31999", np)
	}
}

func TestEnvoyNodePortFromClientset_FirstReadyNodeUsed(t *testing.T) {
	// Two nodes: first is Not Ready, second is Ready — should pick second.
	svc := makeEnvoyService("perf-proxies", "perf-envoy-ai-rig", 10080, 31234)
	notReady := makeNotReadyNode("node-1", "10.0.1.10")
	ready := makeReadyNode("node-2", "10.0.1.20")
	cs := fake.NewClientset(svc, notReady, ready)

	ip, _, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "perf-envoy-ai-rig", 10080)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.1.20" {
		t.Errorf("nodeIP = %q, want %q", ip, "10.0.1.20")
	}
}

// ---------------------------------------------------------------------------
// Fix 3: ambiguous selector fail-closed (empty gatewayName + >1 Service)
// ---------------------------------------------------------------------------

func TestEnvoyNodePortFromClientset_EmptyGatewayName_AmbiguousServices_ReturnsNotReady(t *testing.T) {
	// Two Services match the broad label selector with empty gatewayName.
	// The function must return ErrEnvoyNotReady (ambiguous) rather than
	// silently picking Items[0].
	svc1 := makeEnvoyService("perf-proxies", "gateway-a", 10080, 31001)
	svc2 := makeEnvoyService("perf-proxies", "gateway-b", 10080, 31002)
	node := makeReadyNode("node-1", "10.0.1.50")
	cs := fake.NewClientset(svc1, svc2, node)

	_, _, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "" /* empty */, 10080)
	if err == nil {
		t.Fatal("expected ErrEnvoyNotReady for ambiguous match, got nil")
	}
	if !errors.Is(err, ErrEnvoyNotReady) {
		t.Errorf("expected error wrapping ErrEnvoyNotReady, got: %v", err)
	}
}

func TestEnvoyNodePortFromClientset_EmptyGatewayName_SingleService_Succeeds(t *testing.T) {
	// Exactly one Service matches: behavior unchanged — resolves as before.
	svc := makeEnvoyService("perf-proxies", "gateway-a", 10080, 31001)
	node := makeReadyNode("node-1", "10.0.1.50")
	cs := fake.NewClientset(svc, node)

	ip, np, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "" /* empty */, 10080)
	if err != nil {
		t.Fatalf("unexpected error with single matching Service: %v", err)
	}
	if ip != "10.0.1.50" {
		t.Errorf("nodeIP = %q, want %q", ip, "10.0.1.50")
	}
	if np != 31001 {
		t.Errorf("nodePort = %d, want 31001", np)
	}
}

func TestEnvoyNodePortFromClientset_ExplicitGatewayName_MultipleServicesInNS_StillSucceeds(t *testing.T) {
	// When gatewayName is non-empty the precise selector is used;
	// even if other Services exist in the namespace the precise selector
	// matches exactly one → should succeed.
	svc1 := makeEnvoyService("perf-proxies", "gateway-a", 10080, 31001)
	svc2 := makeEnvoyService("perf-proxies", "gateway-b", 10080, 31002)
	node := makeReadyNode("node-1", "10.0.1.50")
	cs := fake.NewClientset(svc1, svc2, node)

	_, np, err := envoyNodePortFromClientset(context.Background(), cs, "perf-proxies", "gateway-a" /* explicit */, 10080)
	if err != nil {
		t.Fatalf("unexpected error with explicit gatewayName: %v", err)
	}
	if np != 31001 {
		t.Errorf("nodePort = %d, want 31001", np)
	}
}

// ---------------------------------------------------------------------------
// nodePortForListener unit tests
// ---------------------------------------------------------------------------

func TestNodePortForListener_SinglePort(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 9999, NodePort: 31500}},
		},
	}
	if got := nodePortForListener(svc, 10080); got != 31500 {
		t.Errorf("nodePortForListener = %d, want 31500", got)
	}
}

func TestNodePortForListener_MultiPortMatch(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Port: 8080, NodePort: 30080},
				{Port: 10080, NodePort: 31234},
			},
		},
	}
	if got := nodePortForListener(svc, 10080); got != 31234 {
		t.Errorf("nodePortForListener = %d, want 31234", got)
	}
}

func TestNodePortForListener_MultiPortNoMatch(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Port: 8080, NodePort: 30080},
				{Port: 9090, NodePort: 30090},
			},
		},
	}
	if got := nodePortForListener(svc, 10080); got != 0 {
		t.Errorf("nodePortForListener = %d, want 0", got)
	}
}
