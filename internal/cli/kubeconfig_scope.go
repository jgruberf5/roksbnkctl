package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

// kubeTarget is the kubeconfig file + context that address a specific
// workspace's cluster.
type kubeTarget struct {
	Path    string
	Context string
}

// workspaceKubeTarget resolves the kubeconfig that talks to the CURRENT
// workspace's (-w) cluster.
//
// Why this exists: every kubeconfig roksbnkctl can reach is AMBIENT, not
// workspace-scoped — $KUBECONFIG, ~/.kube/config, and even the minted
// forge config (~/.roksbnkctl/forge/kubeconfig.yaml) are single files
// shared by every workspace. So with two workspaces up, `roksbnkctl -w a k
// get ...` would read whichever cluster was configured LAST, and `-w a k
// delete ...` would delete in it. The -w flag has to actually select the
// cluster.
//
// Resolution prefers files that are definitionally this workspace's:
//
//  1. the admin kubeconfigs terraform writes under the workspace's own
//     state dirs (single-cluster, always this workspace);
//  2. the forge kubeconfig, if it happens to name this cluster;
//  3. the ambient default ($KUBECONFIG / ~/.kube/config), if it carries
//     this cluster — pinned to the matching CONTEXT, so a multi-cluster
//     file whose current-context points elsewhere is still addressed
//     correctly rather than silently talking to the wrong cluster.
//
// A workspace with no known cluster id (no cluster-outputs.json — a
// workspace that predates the file, or one whose cluster isn't up) returns
// an empty target: callers then fall back to the historical ambient
// behaviour, so nothing that works today starts failing.
func workspaceKubeTarget() (kubeTarget, error) {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return kubeTarget{}, err
	}
	ws := cctx.WorkspaceName

	co, err := config.ReadClusterOutputs(ws)
	if err != nil || co == nil || co.ClusterID == "" {
		return kubeTarget{}, nil // unknown cluster — legacy ambient behaviour
	}
	id := co.ClusterID

	// Two passes in one loop: take the first candidate that both addresses the
	// cluster AND still holds a live credential; remember the first that merely
	// addresses it, in case none is live.
	var stale kubeTarget
	for _, cand := range kubeconfigCandidates(ws) {
		data, rerr := os.ReadFile(cand)
		if rerr != nil {
			continue
		}
		// Accept a candidate only if it can AUTHENTICATE to this cluster, not
		// merely mention it: IBM's kubeconfigs carry a decoy context that names
		// the cluster id but has no user, which would authenticate as
		// system:anonymous and get every request forbidden.
		kctx := k8s.ContextForCluster(data, id)
		if kctx == "" {
			continue
		}
		tgt := kubeTarget{Path: cand, Context: kctx}
		if credentialLive(data) {
			return tgt, nil
		}
		if stale.Path == "" {
			stale = tgt
		}
	}

	// Everything that addresses the cluster is expired. Return the same target
	// this function has always returned rather than inventing a new failure:
	// the command still fails, but it fails the way it did before, and `doctor`
	// names the file (#277). Refusing here would turn a bad credential into
	// "no kubeconfig addresses workspace", which is a worse lie.
	if stale.Path != "" {
		return stale, nil
	}

	return kubeTarget{}, fmt.Errorf(
		"no kubeconfig addresses workspace %q (cluster %s/%s): the active kubeconfig points at a different cluster.\n"+
			"Run `roksbnkctl -w %s kubeconfig --download` to fetch it",
		ws, co.ClusterName, id, ws)
}

// kubeconfigCandidates lists every kubeconfig that MIGHT address the
// workspace, most-specific first. Terraform writes one admin kubeconfig per
// module under <state-dir>/kubeconfig/<module>/<hash>_<cluster-id>_k8sconfig/
// config.yml; those are single-cluster files for exactly this workspace, so
// they win over anything ambient.
func kubeconfigCandidates(ws string) []string {
	var out []string

	stateDirs := []func(string) (string, error){
		config.WorkspaceStateDir,
		config.WorkspaceClusterStateDir,
		config.WorkspaceFLPStateDir,
		config.WorkspaceTestingStateDir,
		config.WorkspaceGatewayStateDir,
	}
	for _, fn := range stateDirs {
		dir, err := fn(ws)
		if err != nil {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(dir, "kubeconfig", "*", "*", "config.yml"))
		if err != nil {
			continue
		}
		out = append(out, matches...)
	}

	if p, err := config.ForgeKubeconfigPath(); err == nil {
		out = append(out, p)
	}
	if p := k8s.DefaultKubeconfigPath(); p != "" {
		out = append(out, p)
	}
	return out
}

// credentialSelectionSkew keeps workspaceKubeTarget from choosing a credential
// that is technically alive but will die mid-command. A minute is well under
// the ~1h token life, so it never rejects a freshly minted one.
const credentialSelectionSkew = time.Minute

// credentialLive reports whether a kubeconfig's credential is good for at least
// credentialSelectionSkew longer. It exists because ContextForCluster answers
// "can this file address the cluster", which an expired file still can: the
// per-phase kubeconfigs terraform writes carry ~1h IAM tokens that nothing
// rewrites, and preferring one an hour later is issue #281.
//
// An unreadable expiry means USABLE, not expired. MinExpiry errors when a
// kubeconfig carries neither a token nor a client cert -- an exec-plugin or
// OIDC config, whose credential is minted at call time and cannot be judged
// from the file. Those are perfectly good; treating "cannot tell" as dead would
// reject them and break auth methods that work today. The check may only ever
// demote a credential it can PROVE is expired.
func credentialLive(data []byte) bool {
	exp, err := k8s.MinExpiry(data)
	if err != nil {
		return true
	}
	return time.Until(exp) > credentialSelectionSkew
}
