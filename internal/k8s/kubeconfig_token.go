package k8s

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// BuildTokenKubeconfig produces a portable, token-based kubeconfig from an
// admin (cert-based) kubeconfig. It keeps the cluster's public `server` URL,
// carries through `certificate-authority-data` IF the source has one, and
// replaces the user with a single token user named userName carrying the
// supplied IAM bearer token.
//
// The result is fully self-contained: NO file references, NO client
// certificate/key. This is the form BNK Forge registers from and refreshes
// (it re-mints the token from the project credential template), and the one
// ensureFreshKubeconfig refreshes locally.
//
// certificate-authority-data is OPTIONAL: IBM ROKS masters present a
// publicly-trusted TLS cert, so a ROKS admin kubeconfig legitimately has no
// CA and none is needed (system trust validates the server). In that case
// the CA field is omitted entirely. Only `server` is strictly required.
func BuildTokenKubeconfig(adminKubeconfig []byte, token, userName string) ([]byte, error) {
	if token == "" {
		return nil, errors.New("token is empty")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(adminKubeconfig, &doc); err != nil {
		return nil, fmt.Errorf("parsing admin kubeconfig: %w", err)
	}

	clusters, _ := doc["clusters"].([]any)
	if len(clusters) == 0 {
		return nil, errors.New("admin kubeconfig has no clusters")
	}
	c0, _ := clusters[0].(map[string]any)
	if c0 == nil {
		return nil, errors.New("malformed cluster entry")
	}
	clusterName, _ := c0["name"].(string)
	if clusterName == "" {
		clusterName = "cluster"
	}
	inner, _ := c0["cluster"].(map[string]any)
	if inner == nil {
		return nil, errors.New("cluster entry has no `cluster` block")
	}
	server, _ := inner["server"].(string)
	if server == "" {
		return nil, errors.New("cluster has no server URL")
	}
	// certificate-authority-data is OPTIONAL. IBM ROKS master endpoints
	// (*.containers.cloud.ibm.com) present a publicly-trusted TLS cert, so
	// the admin kubeconfig legitimately carries NO certificate-authority-data
	// — system trust validates the server. Carry the CA through when the
	// source has one (private/self-signed clusters); omit the field entirely
	// when it doesn't (never emit an empty value, which kubectl would treat
	// as an empty CA bundle and reject).
	caData, _ := inner["certificate-authority-data"].(string)

	if userName == "" {
		userName = clusterName + "-token"
	}
	// Preserve the current context's namespace if the admin config set one.
	ns := currentContextNamespace(doc)
	if ns == "" {
		ns = "default"
	}

	clusterBlock := map[string]any{"server": server}
	if caData != "" {
		clusterBlock["certificate-authority-data"] = caData
	}
	out := map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []any{
			map[string]any{
				"name":    clusterName,
				"cluster": clusterBlock,
			},
		},
		"contexts": []any{
			map[string]any{
				"name": clusterName,
				"context": map[string]any{
					"cluster":   clusterName,
					"user":      userName,
					"namespace": ns,
				},
			},
		},
		"current-context": clusterName,
		"users": []any{
			map[string]any{
				"name": userName,
				"user": map[string]any{"token": token},
			},
		},
	}
	b, err := yaml.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("emitting token kubeconfig: %w", err)
	}
	return b, nil
}

// BuildCertKubeconfig produces a portable, cert-based kubeconfig from an admin
// kubeconfig: the cluster's public `server` URL, `certificate-authority-data`
// IF the source has one, and the admin client certificate/key. Single context,
// fully self-contained (no file references).
//
// This is the form BNK Forge registers IBM ROKS clusters from. ROKS is Red Hat
// OpenShift: its API server authenticates via OpenShift OAuth tokens or client
// certificates — NOT raw IBM IAM bearer tokens, which it rejects with 401. The
// admin client cert/key authenticate directly, so the forge kubeconfig carries
// them. The freshness gate classifies the result as cert-based and keeps it
// current by re-fetching the admin kubeconfig.
func BuildCertKubeconfig(adminKubeconfig []byte, userName string) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(adminKubeconfig, &doc); err != nil {
		return nil, fmt.Errorf("parsing admin kubeconfig: %w", err)
	}

	clusters, _ := doc["clusters"].([]any)
	if len(clusters) == 0 {
		return nil, errors.New("admin kubeconfig has no clusters")
	}
	c0, _ := clusters[0].(map[string]any)
	if c0 == nil {
		return nil, errors.New("malformed cluster entry")
	}
	clusterName, _ := c0["name"].(string)
	if clusterName == "" {
		clusterName = "cluster"
	}
	inner, _ := c0["cluster"].(map[string]any)
	if inner == nil {
		return nil, errors.New("cluster entry has no `cluster` block")
	}
	server, _ := inner["server"].(string)
	if server == "" {
		return nil, errors.New("cluster has no server URL")
	}
	// certificate-authority-data is OPTIONAL (IBM ROKS public masters carry
	// none — system trust validates). Carry it through only when present;
	// never emit an empty value.
	caData, _ := inner["certificate-authority-data"].(string)

	users, _ := doc["users"].([]any)
	if len(users) == 0 {
		return nil, errors.New("admin kubeconfig has no users")
	}
	u0, _ := users[0].(map[string]any)
	uinner, _ := u0["user"].(map[string]any)
	if uinner == nil {
		return nil, errors.New("admin user has no `user` block")
	}
	clientCert, _ := uinner["client-certificate-data"].(string)
	clientKey, _ := uinner["client-key-data"].(string)
	if clientCert == "" || clientKey == "" {
		return nil, errors.New("admin kubeconfig is not self-contained (missing client-certificate-data/client-key-data)")
	}

	if userName == "" {
		userName = clusterName + "-admin"
	}
	ns := currentContextNamespace(doc)
	if ns == "" {
		ns = "default"
	}

	clusterBlock := map[string]any{"server": server}
	if caData != "" {
		clusterBlock["certificate-authority-data"] = caData
	}
	out := map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []any{
			map[string]any{"name": clusterName, "cluster": clusterBlock},
		},
		"contexts": []any{
			map[string]any{
				"name": clusterName,
				"context": map[string]any{
					"cluster":   clusterName,
					"user":      userName,
					"namespace": ns,
				},
			},
		},
		"current-context": clusterName,
		"users": []any{
			map[string]any{
				"name": userName,
				"user": map[string]any{
					"client-certificate-data": clientCert,
					"client-key-data":         clientKey,
				},
			},
		},
	}
	b, err := yaml.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("emitting cert kubeconfig: %w", err)
	}
	return b, nil
}

// RewriteTokens replaces the bearer token on every token-bearing user in a
// kubeconfig, leaving clusters[].cluster (server + CA) and everything else
// untouched. Used by the refresh gate: only the credential changes. Returns
// an error if there are no token users to refresh (so the caller can fall
// back rather than silently no-op).
func RewriteTokens(kubeconfig []byte, token string) ([]byte, error) {
	if token == "" {
		return nil, errors.New("token is empty")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(kubeconfig, &doc); err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}
	users, _ := doc["users"].([]any)
	n := 0
	for _, u := range users {
		um, _ := u.(map[string]any)
		if um == nil {
			continue
		}
		inner, _ := um["user"].(map[string]any)
		if inner == nil {
			continue
		}
		if _, ok := inner["token"]; ok {
			inner["token"] = token
			n++
		}
	}
	if n == 0 {
		return nil, errors.New("kubeconfig has no token users to refresh")
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("re-emitting kubeconfig: %w", err)
	}
	return b, nil
}

// currentContextNamespace returns the namespace of the kubeconfig's
// current-context, or "" if none is set / resolvable.
func currentContextNamespace(doc map[string]any) string {
	cur, _ := doc["current-context"].(string)
	if cur == "" {
		return ""
	}
	contexts, _ := doc["contexts"].([]any)
	for _, c := range contexts {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		if name, _ := cm["name"].(string); name != cur {
			continue
		}
		inner, _ := cm["context"].(map[string]any)
		if inner == nil {
			return ""
		}
		ns, _ := inner["namespace"].(string)
		return ns
	}
	return ""
}
