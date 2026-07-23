package k8s

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// Host+token connection helpers for the `tfx` command family (see PRD
// docs/prd/native-windows-tfx.md). The terraform modules address the cluster as
// a bare API host + bearer token — historically via `curl -sk` — so `tfx` mirrors
// that exact contract instead of requiring a kubeconfig on disk. Keeping the
// rest.Config construction here (next to BuildRESTConfig) keeps every client the
// binary builds in one place and unit-testable.

// RESTConfigFromHostToken builds a rest.Config for a bare kube API host + bearer
// token — the connection model the terraform modules use (kube_host + kube_token).
// insecure skips TLS verification (matches the modules' `curl -k`); when insecure
// is false and caData is non-empty, the server cert is verified against it.
func RESTConfigFromHostToken(host, token string, insecure bool, caData []byte) (*rest.Config, error) {
	if host == "" {
		return nil, fmt.Errorf("kube host is required (pass --kube-host or set KUBE_HOST)")
	}
	if token == "" {
		return nil, fmt.Errorf("kube token is required (set the token env, default KUBE_TOKEN)")
	}
	cfg := &rest.Config{
		Host:        host,
		BearerToken: token,
	}
	switch {
	case insecure:
		cfg.TLSClientConfig = rest.TLSClientConfig{Insecure: true}
	case len(caData) > 0:
		cfg.TLSClientConfig = rest.TLSClientConfig{CAData: caData}
	}
	return cfg, nil
}

// DynamicForHostToken builds a dynamic client from a host + token (see
// RESTConfigFromHostToken). Paired with an explicit GroupVersionResource on the
// caller side, so no discovery/REST-mapper round-trip is needed.
func DynamicForHostToken(host, token string, insecure bool, caData []byte) (dynamic.Interface, error) {
	cfg, err := RESTConfigFromHostToken(host, token, insecure, caData)
	if err != nil {
		return nil, err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}
	return dc, nil
}

// DynamicClientForConfig builds a dynamic client from an already-resolved config.
func DynamicClientForConfig(cfg *rest.Config) (dynamic.Interface, error) {
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}
	return dc, nil
}

// DynamicFromKubeconfigBytes builds a dynamic client from raw kubeconfig bytes
// (e.g. a freshly-fetched admin kubeconfig held only in memory) — no temp file.
func DynamicFromKubeconfigBytes(b []byte) (dynamic.Interface, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(b)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}
	return DynamicClientForConfig(cfg)
}

// RESTMapperForConfig builds a discovery-backed REST mapper — needed to resolve a
// manifest's Kind to its resource for `tfx apply` (server-side apply), where the
// caller has an object, not an explicit GVR.
func RESTMapperForConfig(cfg *rest.Config) (meta.RESTMapper, error) {
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating discovery client: %w", err)
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(memCacheClient{disc}), nil
}
