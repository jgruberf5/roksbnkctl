package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/doctor"
)

// jwtWithExp builds a token whose payload carries only an exp claim. The
// signature is not verified by anything here — the check reads the claim, it does
// not authenticate — so a fixed placeholder is honest rather than a shortcut.
func jwtWithExp(t *testing.T, exp time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]int64{"exp": exp.Unix()})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc(payload) + ".not-a-real-signature"
}

// phaseKubeconfig writes a file at the exact shape terraform produces:
// <ws>/state/kubeconfig/<phase>/<hash>_<clusterid>_k8sconfig/config.yml
func phaseKubeconfig(t *testing.T, wsDir, phase, body string) string {
	t.Helper()
	dir := filepath.Join(wsDir, "state", "kubeconfig", phase, "abc123_dacluster_k8sconfig")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func tokenKubeconfig(tok string) string {
	return fmt.Sprintf(`apiVersion: v1
clusters:
- cluster:
    server: https://c100-e.us-east.containers.cloud.ibm.com:31966
  name: smcli
kind: Config
users:
- name: IAM#someone@example.com
  user:
    token: %s
`, tok)
}

// workspaceFixture points config.BaseDir at a temp dir and returns the workspace
// directory inside it.
func workspaceFixture(t *testing.T, name string) (*config.Context, string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, base)
	wsDir := filepath.Join(base, name)
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	return &config.Context{WorkspaceName: name, Workspace: &config.Workspace{}}, wsDir
}

// #277. An expired phase kubeconfig produces only
// "Unauthorized" / "the server has asked for the client to provide credentials",
// naming no file and no cause. The cluster is healthy, the IBM login is fine and
// ~/.kube/config works, so every obvious explanation is wrong — which is why
// diagnosing it from scratch took about an hour.
func TestDoctorReportsAnExpiredPhaseKubeconfig(t *testing.T) {
	cctx, wsDir := workspaceFixture(t, "ws1")
	phaseKubeconfig(t, wsDir, "flo", tokenKubeconfig(jwtWithExp(t, time.Now().Add(-6*24*time.Hour))))

	c, ok := staleKubeconfigCheck(cctx)
	if !ok {
		t.Fatal("no check emitted for a workspace holding an expired phase kubeconfig")
	}
	if c.Status != doctor.StatusWarning {
		t.Errorf("status = %q, want warning", c.Status)
	}
	for _, want := range []string{"EXPIRED", "Nothing reads these", "kubeconfig --download"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("the detail does not mention %q — someone reading only this line still has\n"+
				"to work out what to do:\n%s", want, c.Detail)
		}
	}
}

// Fresh tokens must not be reported as a problem, or the check becomes noise and
// stops being read — which is how the next real warning gets skimmed past.
func TestDoctorDoesNotWarnOnFreshPhaseKubeconfigs(t *testing.T) {
	cctx, wsDir := workspaceFixture(t, "ws2")
	phaseKubeconfig(t, wsDir, "flo", tokenKubeconfig(jwtWithExp(t, time.Now().Add(30*time.Minute))))

	c, ok := staleKubeconfigCheck(cctx)
	if !ok {
		t.Fatal("expected a check for a workspace with cached kubeconfigs")
	}
	if c.Status != doctor.StatusOK {
		t.Errorf("status = %q, want ok — the token has 30 minutes left:\n%s", c.Status, c.Detail)
	}
}

// A cert-based kubeconfig has no token to expire. Guessing an expiry for one
// would report a working credential as broken, and a check that cries wolf is
// worse than no check.
func TestDoctorIgnoresCertBasedKubeconfigs(t *testing.T) {
	cctx, wsDir := workspaceFixture(t, "ws3")
	phaseKubeconfig(t, wsDir, "flo", `apiVersion: v1
kind: Config
users:
- name: admin
  user:
    client-certificate-data: Zm9v
    client-key-data: YmFy
`)
	if _, ok := staleKubeconfigCheck(cctx); ok {
		t.Error("a cert-based kubeconfig was reported on; it carries no token and cannot expire")
	}
}

// A workspace that has never applied has no cached kubeconfigs, and must produce
// no row at all rather than an empty or confusing one.
func TestDoctorSaysNothingWhenThereAreNoPhaseKubeconfigs(t *testing.T) {
	cctx, _ := workspaceFixture(t, "ws4")
	if _, ok := staleKubeconfigCheck(cctx); ok {
		t.Error("a workspace with no cached kubeconfigs still produced a row")
	}
}

// Mixed is the realistic case mid-apply: the check must count both and warn.
func TestDoctorCountsBothStaleAndFresh(t *testing.T) {
	cctx, wsDir := workspaceFixture(t, "ws5")
	phaseKubeconfig(t, wsDir, "flo", tokenKubeconfig(jwtWithExp(t, time.Now().Add(-time.Hour))))
	phaseKubeconfig(t, wsDir, "license", tokenKubeconfig(jwtWithExp(t, time.Now().Add(time.Hour))))

	c, ok := staleKubeconfigCheck(cctx)
	if !ok || c.Status != doctor.StatusWarning {
		t.Fatalf("ok=%v status=%q, want a warning", ok, c.Status)
	}
	if !strings.Contains(c.Detail, "1 of 2") {
		t.Errorf("the count should distinguish stale from total, got:\n%s", c.Detail)
	}
}

// An opaque or malformed token is not a JWT and its expiry cannot be read.
// Reporting it either way would be a guess.
func TestDoctorIgnoresATokenItCannotParse(t *testing.T) {
	cctx, wsDir := workspaceFixture(t, "ws6")
	phaseKubeconfig(t, wsDir, "flo", tokenKubeconfig("an-opaque-token-not-a-jwt"))
	if _, ok := staleKubeconfigCheck(cctx); ok {
		t.Error("an unparseable token was reported on; its expiry is unknown, not expired")
	}
}
