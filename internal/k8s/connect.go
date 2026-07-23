package k8s

import (
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
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
