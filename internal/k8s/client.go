package k8s

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// InClusterKubeconfigSentinel is the magic value for kubeconfigPath that
// triggers rest.InClusterConfig() lookup. Used by Phase 3's K8s execution
// backend (PRD 03) when roksbnkctl runs inside an ops Pod and gets its
// credentials from the projected service account.
const InClusterKubeconfigSentinel = "in-cluster"

// Client wraps a Kubernetes clientset and the REST config used to build
// it. One Client per command invocation; not safe for concurrent reuse.
type Client struct {
	config    *rest.Config
	clientset *kubernetes.Clientset
}

// NewFromKubeconfigBytes builds a Client from raw kubeconfig YAML.
// Used in v1.x when roksbnkctl fetches the kubeconfig itself via the IBM
// container service SDK.
func NewFromKubeconfigBytes(b []byte) (*Client, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(b)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}
	return &Client{config: cfg, clientset: cs}, nil
}

// NewFromKubeconfigFile builds a Client from a kubeconfig file on disk.
// Honors $KUBECONFIG (colon-separated list, like kubectl).
func NewFromKubeconfigFile(path string) (*Client, error) {
	if path == "" {
		return nil, errors.New("kubeconfig path is empty")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig %s: %w", path, err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}
	return &Client{config: cfg, clientset: cs}, nil
}

// NewFromDefault builds a Client by walking the same lookup chain
// kubectl uses: $KUBECONFIG (first existing path in a colon list) →
// ~/.kube/config. Returns a clear error if nothing's found.
func NewFromDefault() (*Client, error) {
	path := DefaultKubeconfigPath()
	if path == "" {
		return nil, errors.New("no kubeconfig found: set $KUBECONFIG or run `ibmcloud ks cluster config --admin -c <cluster>`")
	}
	return NewFromKubeconfigFile(path)
}

// DefaultKubeconfigPath returns the first existing path in $KUBECONFIG
// (colon-separated), falling back to ~/.kube/config. Empty if neither
// exists.
func DefaultKubeconfigPath() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		// $KUBECONFIG is a list; pick the first that exists.
		for _, p := range filepath.SplitList(v) {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	def := filepath.Join(home, ".kube", "config")
	if _, err := os.Stat(def); err == nil {
		return def
	}
	return ""
}

// Clientset returns the underlying client-go clientset.
func (c *Client) Clientset() *kubernetes.Clientset { return c.clientset }

// RESTConfig returns the rest.Config used to construct the clientset.
// Useful for building secondary clients (dynamic, controller-runtime).
func (c *Client) RESTConfig() *rest.Config { return c.config }

// BuildRESTConfig is the lower-level helper both BuildClientset and
// BuildDynamicClient use; exposed so callers that need a custom
// rest.Config (e.g. SPDY upgrades for exec/port-forward) can build off
// it.
//
// kubeconfigPath semantics:
//   - "" → workspace default via DefaultKubeconfigPath()
//   - "in-cluster" (InClusterKubeconfigSentinel) → rest.InClusterConfig()
//   - any other value → that file path on disk
func BuildRESTConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath == InClusterKubeconfigSentinel {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("in-cluster config: %w", err)
		}
		return cfg, nil
	}
	if kubeconfigPath == "" {
		kubeconfigPath = DefaultKubeconfigPath()
	}
	if kubeconfigPath == "" {
		return nil, errors.New("no kubeconfig found: set $KUBECONFIG or run `roksbnkctl kubeconfig --download`")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig %s: %w", kubeconfigPath, err)
	}
	return cfg, nil
}

// BuildClientset returns a typed client for core + apps + batch + etc.
// kubeconfigPath: empty string → workspace default at
// ~/.roksbnkctl/<ws>/state/kubeconfig (or whatever DefaultKubeconfigPath
// resolves);
// "in-cluster" sentinel → use rest.InClusterConfig() (used by the K8s
// execution backend in Phase 3, PRD 03).
//
// Returns the kubernetes.Interface so callers using fake clientsets in
// tests can substitute drop-in.
func BuildClientset(kubeconfigPath string) (kubernetes.Interface, error) {
	cfg, err := BuildRESTConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}
	return cs, nil
}

// BuildDynamicClient returns a dynamic.Interface for unstructured access
// (necessary for kubectl get <type-not-in-typed-scheme>, CRDs, server-
// side apply via dynamic resource interface, etc.).
func BuildDynamicClient(kubeconfigPath string) (dynamic.Interface, error) {
	cfg, err := BuildRESTConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}
	return dc, nil
}
