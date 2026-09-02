package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Issue #281: workspaceKubeTarget preferred the per-phase kubeconfigs and took
// the first one that could ADDRESS the cluster, with no regard for whether its
// credential had expired. Those files carry ~1h IAM tokens that nothing
// rewrites, so an hour after an apply every `k` verb authenticated with a dead
// token against a healthy cluster.
//
// These tests drive workspaceKubeTarget itself rather than credentialLive. A
// test bound to the helper would survive the resolver being changed to stop
// calling it, which is exactly the regression worth guarding.

const testClusterID = "dacluster"

// kubeconfigWithToken is a complete kubeconfig for testClusterID: a cluster
// whose NAME contains the id, a context pointing at it, and a user carrying
// tok. ContextForCluster needs all three -- it rejects IBM's credential-less
// decoy context, so an incomplete fixture would be skipped for the wrong reason
// and the test would pass without proving anything.
func kubeconfigWithToken(tok string) string {
	return `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://c100-e.us-east.containers.cloud.ibm.com:31966
  name: ` + testClusterID + `
contexts:
- context:
    cluster: ` + testClusterID + `
    user: IAM#someone@example.com
  name: ` + testClusterID + `/admin
current-context: ` + testClusterID + `/admin
users:
- name: IAM#someone@example.com
  user:
    token: ` + tok + `
`
}

// scopeFixture builds a workspace with cluster outputs recorded, so
// workspaceKubeTarget gets past its "unknown cluster" early return.
func scopeFixture(t *testing.T) (string, string) {
	t.Helper()
	ws := "scopetest"
	_, wsDir := workspaceFixture(t, ws)

	if err := config.WriteClusterOutputs(ws, &config.ClusterOutputs{
		ClusterID:   testClusterID,
		ClusterName: "da-cluster",
	}); err != nil {
		t.Fatalf("write cluster outputs: %v", err)
	}

	prev := flagWorkspace
	flagWorkspace = ws
	t.Cleanup(func() { flagWorkspace = prev })

	// Keep the ambient default out of the candidate list: a developer's real
	// ~/.kube/config must not decide the outcome of this test.
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "nonexistent"))
	return ws, wsDir
}

// TestResolverPrefersLiveCredentialOverExpiredOne is the regression itself.
// The phase directories are named so the EXPIRED one sorts first: filepath.Glob
// returns sorted paths, so a resolver that takes the first addressing candidate
// picks the dead one. Only expiry-awareness reaches the live file.
func TestResolverPrefersLiveCredentialOverExpiredOne(t *testing.T) {
	_, wsDir := scopeFixture(t)

	dead := phaseKubeconfig(t, wsDir, "aaa_expired",
		kubeconfigWithToken(jwtWithExp(t, time.Now().Add(-time.Hour))))
	live := phaseKubeconfig(t, wsDir, "zzz_live",
		kubeconfigWithToken(jwtWithExp(t, time.Now().Add(2*time.Hour))))

	if dead >= live {
		t.Fatalf("fixture broken: %q must sort before %q for this test to mean anything", dead, live)
	}

	tgt, err := workspaceKubeTarget()
	if err != nil {
		t.Fatalf("workspaceKubeTarget: %v", err)
	}
	if tgt.Path != live {
		t.Errorf("chose %s, want the live credential at %s", tgt.Path, live)
	}
	if tgt.Context == "" {
		t.Error("resolved target carries no context")
	}
}

// TestResolverFallsBackWhenEveryCredentialIsExpired pins the deliberate
// fallback. Refusing here would convert a bad credential into "no kubeconfig
// addresses workspace", which sends the reader hunting a targeting problem that
// does not exist. The command still fails -- but the way it always has, and
// `doctor` names the file.
func TestResolverFallsBackWhenEveryCredentialIsExpired(t *testing.T) {
	_, wsDir := scopeFixture(t)

	first := phaseKubeconfig(t, wsDir, "aaa_expired",
		kubeconfigWithToken(jwtWithExp(t, time.Now().Add(-time.Hour))))
	phaseKubeconfig(t, wsDir, "zzz_also_expired",
		kubeconfigWithToken(jwtWithExp(t, time.Now().Add(-2*time.Hour))))

	tgt, err := workspaceKubeTarget()
	if err != nil {
		t.Fatalf("expected the historical target, got error: %v", err)
	}
	if tgt.Path != first {
		t.Errorf("chose %s, want the first addressing candidate %s", tgt.Path, first)
	}
}

// TestResolverKeepsCredentialsItCannotDate guards the direction of the
// unknown case. MinExpiry errors on a kubeconfig with neither token nor client
// cert -- an exec-plugin or OIDC config, minted at call time. Those work fine.
// If "cannot tell" ever came to mean "expired", this resolver would reject
// every exec-based kubeconfig and the failure would look like a targeting bug.
//
// The exec config must COMPETE with an expired one, and must sort second. A
// first version made it the only candidate; flipping the unknown case to
// "expired" then still returned it via the fallback, so the mutation survived
// and the test proved nothing. It has to be reachable only by being judged live.
func TestResolverKeepsCredentialsItCannotDate(t *testing.T) {
	_, wsDir := scopeFixture(t)

	phaseKubeconfig(t, wsDir, "aaa_expired",
		kubeconfigWithToken(jwtWithExp(t, time.Now().Add(-time.Hour))))

	execCfg := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://c100-e.us-east.containers.cloud.ibm.com:31966
  name: ` + testClusterID + `
contexts:
- context:
    cluster: ` + testClusterID + `
    user: oidc
  name: ` + testClusterID + `/oidc
current-context: ` + testClusterID + `/oidc
users:
- name: oidc
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: kubectl-oidc
`
	only := phaseKubeconfig(t, wsDir, "zzz_exec", execCfg)

	tgt, err := workspaceKubeTarget()
	if err != nil {
		t.Fatalf("workspaceKubeTarget: %v", err)
	}
	if tgt.Path != only {
		t.Errorf("chose %s, want the exec-plugin config %s", tgt.Path, only)
	}
}

// TestExpiredCandidateIsNotSilentlyDropped proves the fallback path reads the
// file rather than reporting success on a path that is gone -- the resolver
// must still be pointing at something a caller can open.
func TestExpiredCandidateIsNotSilentlyDropped(t *testing.T) {
	_, wsDir := scopeFixture(t)
	phaseKubeconfig(t, wsDir, "aaa_expired",
		kubeconfigWithToken(jwtWithExp(t, time.Now().Add(-time.Hour))))

	tgt, err := workspaceKubeTarget()
	if err != nil {
		t.Fatalf("workspaceKubeTarget: %v", err)
	}
	if _, statErr := os.Stat(tgt.Path); statErr != nil {
		t.Fatalf("resolved an unreadable path %s: %v", tgt.Path, statErr)
	}
	if !strings.Contains(tgt.Path, "aaa_expired") {
		t.Errorf("resolved %s, want the expired phase kubeconfig", tgt.Path)
	}
}

// TestResolverRejectsCredentialExpiringWithinSkew pins credentialSelectionSkew.
// A token with seconds left is alive by the clock and useless in practice: it
// dies partway through the command it was chosen for. Without this, setting the
// skew to zero changes nothing any test observes.
func TestResolverRejectsCredentialExpiringWithinSkew(t *testing.T) {
	_, wsDir := scopeFixture(t)

	phaseKubeconfig(t, wsDir, "aaa_expiring",
		kubeconfigWithToken(jwtWithExp(t, time.Now().Add(10*time.Second))))
	live := phaseKubeconfig(t, wsDir, "zzz_live",
		kubeconfigWithToken(jwtWithExp(t, time.Now().Add(2*time.Hour))))

	tgt, err := workspaceKubeTarget()
	if err != nil {
		t.Fatalf("workspaceKubeTarget: %v", err)
	}
	if tgt.Path != live {
		t.Errorf("chose %s, want %s: a credential inside the skew must not be selected", tgt.Path, live)
	}
}
