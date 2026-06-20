package k8s

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A minimal admin-style kubeconfig: one cluster with server +
// certificate-authority-data, one cert-based user, one context.
const adminKubeconfigYAML = `apiVersion: v1
kind: Config
clusters:
- name: mycluster/cre000
  cluster:
    server: https://c1.example.com:31234
    certificate-authority-data: Q0FEQVRB
contexts:
- name: mycluster/cre000-ctx
  context:
    cluster: mycluster/cre000
    user: admin
    namespace: kube-system
current-context: mycluster/cre000-ctx
users:
- name: admin
  user:
    client-certificate-data: Q0VSVA==
    client-key-data: S0VZ
`

func TestBuildTokenKubeconfig(t *testing.T) {
	out, err := BuildTokenKubeconfig([]byte(adminKubeconfigYAML), "tok-123", "mycluster-token")
	if err != nil {
		t.Fatalf("BuildTokenKubeconfig: %v", err)
	}

	// No cert/key material may survive into the token kubeconfig.
	s := string(out)
	for _, banned := range []string{"client-certificate-data", "client-key-data", "client-certificate", "client-key"} {
		if strings.Contains(s, banned) {
			t.Errorf("token kubeconfig leaked %q:\n%s", banned, s)
		}
	}

	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("result not valid yaml: %v", err)
	}

	// Cluster: server + CA-data preserved verbatim.
	cl := doc["clusters"].([]any)[0].(map[string]any)
	inner := cl["cluster"].(map[string]any)
	if got := inner["server"]; got != "https://c1.example.com:31234" {
		t.Errorf("server = %v, want preserved", got)
	}
	if got := inner["certificate-authority-data"]; got != "Q0FEQVRB" {
		t.Errorf("certificate-authority-data = %v, want preserved", got)
	}

	// User: exactly one token user with the given name + token.
	users := doc["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("want 1 user, got %d", len(users))
	}
	u := users[0].(map[string]any)
	if u["name"] != "mycluster-token" {
		t.Errorf("user name = %v, want mycluster-token", u["name"])
	}
	uu := u["user"].(map[string]any)
	if uu["token"] != "tok-123" {
		t.Errorf("user.token = %v, want tok-123", uu["token"])
	}

	// Context points at the token user and preserves the namespace.
	ctx := doc["contexts"].([]any)[0].(map[string]any)["context"].(map[string]any)
	if ctx["user"] != "mycluster-token" {
		t.Errorf("context.user = %v, want mycluster-token", ctx["user"])
	}
	if ctx["namespace"] != "kube-system" {
		t.Errorf("context.namespace = %v, want preserved kube-system", ctx["namespace"])
	}
}

func TestBuildTokenKubeconfig_RejectsMissingCAData(t *testing.T) {
	noCA := `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: https://x:1
    certificate-authority: ca.pem
users: []
`
	if _, err := BuildTokenKubeconfig([]byte(noCA), "t", "c-token"); err == nil {
		t.Fatal("expected error when CA is a file ref (not self-contained), got nil")
	}
}

func TestBuildTokenKubeconfig_DefaultUserName(t *testing.T) {
	out, err := BuildTokenKubeconfig([]byte(adminKubeconfigYAML), "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = yaml.Unmarshal(out, &doc)
	u := doc["users"].([]any)[0].(map[string]any)
	if u["name"] != "mycluster/cre000-token" {
		t.Errorf("default user name = %v, want <clustername>-token", u["name"])
	}
}

func TestRewriteTokens(t *testing.T) {
	tokenKC, err := BuildTokenKubeconfig([]byte(adminKubeconfigYAML), "old-token", "c-token")
	if err != nil {
		t.Fatal(err)
	}
	out, err := RewriteTokens(tokenKC, "new-token")
	if err != nil {
		t.Fatalf("RewriteTokens: %v", err)
	}
	var doc map[string]any
	_ = yaml.Unmarshal(out, &doc)
	uu := doc["users"].([]any)[0].(map[string]any)["user"].(map[string]any)
	if uu["token"] != "new-token" {
		t.Errorf("token = %v, want new-token", uu["token"])
	}
	// Cluster CA/server must be untouched by a token rewrite.
	inner := doc["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)
	if inner["certificate-authority-data"] != "Q0FEQVRB" || inner["server"] != "https://c1.example.com:31234" {
		t.Errorf("RewriteTokens disturbed the cluster block: %v", inner)
	}
}

func TestRewriteTokens_NoTokenUsersErrors(t *testing.T) {
	// The admin (cert) kubeconfig has no token user — rewrite must error
	// rather than silently no-op.
	if _, err := RewriteTokens([]byte(adminKubeconfigYAML), "x"); err == nil {
		t.Fatal("expected error rewriting a kubeconfig with no token users, got nil")
	}
}
