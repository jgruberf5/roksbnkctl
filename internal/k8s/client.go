package k8s

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
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
// (colon-separated), falling back to ~/.kube/config and then to the
// roksbnkctl base's .kube/config. Empty if none exist.
//
// The final <roksbnkctl-base>/.kube/config fallback matches the writable
// location tf.Open exports as $KUBECONFIG and the admin-kubeconfig writer
// targets when $HOME isn't writable (the runner case): a later, separate
// `roksbnkctl k …` invocation doesn't run through tf.Open, so $KUBECONFIG
// is unset there and ~/.kube/config never got written — this fallback is
// how those reads still find the fetched config.
func DefaultKubeconfigPath() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		// $KUBECONFIG is a list; pick the first that exists.
		for _, p := range filepath.SplitList(v) {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		def := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(def); err == nil {
			return def
		}
	}
	if base, err := roksbnkctlBaseDir(); err == nil {
		def := filepath.Join(base, ".kube", "config")
		if _, err := os.Stat(def); err == nil {
			return def
		}
	}
	return ""
}

// KubeconfigWritePath returns where roksbnkctl should WRITE a freshly
// fetched admin kubeconfig. Unlike DefaultKubeconfigPath (which returns
// only paths that already exist), this returns a target even when nothing
// exists yet, choosing the first writable candidate:
//
//  1. the first entry of $KUBECONFIG, if set (tf.Open points this at the
//     writable <base>/.kube/config);
//  2. ~/.kube/config, if $HOME resolves;
//  3. <roksbnkctl-base>/.kube/config (always writable — the runner case).
//
// Callers MkdirAll the parent before writing.
func KubeconfigWritePath() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		if list := filepath.SplitList(v); len(list) > 0 && list[0] != "" {
			return list[0]
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".kube", "config")
	}
	if base, err := roksbnkctlBaseDir(); err == nil {
		return filepath.Join(base, ".kube", "config")
	}
	return ""
}

// roksbnkctlBaseDir resolves the roksbnkctl root the same way
// config.BaseDir does — $ROKSBNKCTL_HOME, else $HOME/.roksbnkctl. Kept as
// a tiny local helper (rather than importing internal/config) so the k8s
// package stays free of the config dependency.
func roksbnkctlBaseDir() (string, error) {
	if v := os.Getenv("ROKSBNKCTL_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".roksbnkctl"), nil
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
	return BuildRESTConfigForContext(kubeconfigPath, "")
}

// BuildRESTConfigForContext is BuildRESTConfig with an explicit context
// override. kubeContext == "" keeps the file's current-context (the
// historical behaviour); a non-empty value pins the context, which is how
// workspace-scoped callers guarantee they address the workspace's OWN
// cluster even when the kubeconfig carries several and its current-context
// selects a different one.
func BuildRESTConfigForContext(kubeconfigPath, kubeContext string) (*rest.Config, error) {
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
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig %s: %w", kubeconfigPath, err)
	}
	return cfg, nil
}

// BuildClientset returns a typed client for core + apps + batch + etc.
// kubeconfigPath: empty string → whatever DefaultKubeconfigPath resolves, which
// is $KUBECONFIG's first existing entry, then ~/.kube/config;
// "in-cluster" sentinel → use rest.InClusterConfig() (used by the K8s
// execution backend in Phase 3, PRD 03).
//
// It is NOT ~/.roksbnkctl/<ws>/state/kubeconfig. That directory holds the
// kubeconfigs the IBM terraform provider writes as a side effect of config_dir,
// which nothing reads: the providers take host/token from the data source's
// attributes, and those are re-read on every plan. This comment used to name that
// path as the default, which is precisely the wrong place to send someone
// debugging a credential problem (#277).
//
// Returns the kubernetes.Interface so callers using fake clientsets in
// tests can substitute drop-in.
func BuildClientset(kubeconfigPath string) (kubernetes.Interface, error) {
	return BuildClientsetForContext(kubeconfigPath, "")
}

// BuildClientsetForContext is BuildClientset with an explicit context
// override (see BuildRESTConfigForContext).
func BuildClientsetForContext(kubeconfigPath, kubeContext string) (kubernetes.Interface, error) {
	cfg, err := BuildRESTConfigForContext(kubeconfigPath, kubeContext)
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

// BuildRESTMapper returns a discovery-backed RESTMapper that resolves a
// GroupKind (+ optional version) to its GroupVersionResource — so callers can
// query arbitrary CRDs (Gateway-API, F5SPK CRs) by Kind without hardcoding
// plurals. Paired with BuildDynamicClient for unstructured CR reads.
func BuildRESTMapper(kubeconfigPath string) (meta.RESTMapper, error) {
	cfg, err := BuildRESTConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating discovery client: %w", err)
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(memCacheClient{disc}), nil
}
