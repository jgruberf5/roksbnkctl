package orchestration

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// guardUnownedBNKInstall refuses, quickly and by name, when the cluster already
// carries a BNK install that THIS workspace's state knows nothing about.
//
// THE FAILURE IT REPLACES (issue #53). `bnk up` cannot tell "nothing is installed"
// from "something is installed that I do not own". With an empty state it plans a
// full install — `resources_to_add=64` over a cluster that already has all 64 — and
// grinds for roughly thirteen minutes before exiting 1 with a message naming neither
// the cause nor the cluster. Three retries make it worse.
//
// This is not an exotic case. BNK Forge gives each project its own deployment-scoped
// volume, so the second project to adopt a cluster legitimately has no state for a
// BNK install the first project made.
//
// The check is deliberately narrow, because a false positive would block a legitimate
// re-run. It fires only when BOTH are true:
//
//   - this workspace's BNK terraform state holds no managed resources, so the plan
//     would create everything rather than converge; and
//   - the cluster is actually serving a BNK install right now.
//
// A workspace with state converges as before and never reaches this code. If the
// cluster cannot be reached, or its state cannot be read, the guard stays silent:
// this exists to convert a slow confusing failure into a fast clear one, not to add
// a new way for `bnk up` to refuse.
func guardUnownedBNKInstall(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, w io.Writer) error {
	if cctx == nil || cctx.Workspace == nil || tfws == nil {
		return nil
	}
	has, known := bnkStateHasResources(ctx, cctx, tfws)
	if !known || has {
		// Either we own an install (converge normally), or we could not determine what
		// the state holds. Both stay silent — see bnkStateHasResources.
		return nil
	}
	ns := strings.TrimSpace(cctx.Workspace.BNK.FLONamespace)
	if ns == "" {
		ns = "f5-bnk"
	}
	body, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		return nil // cannot look — say nothing
	}
	kc, err := k8s.NewFromKubeconfigBytes(body)
	if err != nil {
		return nil
	}
	pods, err := kc.Clientset().CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=f5-lifecycle-operator",
		Limit:         1,
	})
	if err != nil || len(pods.Items) == 0 {
		return nil // no FLO, or unreadable — not our business to block
	}

	cluster := strings.TrimSpace(cctx.Workspace.Cluster.Name)
	if co, cerr := config.ReadClusterOutputs(cctx.WorkspaceName); cerr == nil && co != nil && co.ClusterName != "" {
		cluster = co.ClusterName
	}
	fmt.Fprintln(w) // separate the refusal from any preceding progress
	return fmt.Errorf(
		"cluster %q already has BNK installed (the F5 Lifecycle Operator is running in namespace %q), "+
			"but workspace %q has no terraform state for it.\n\n"+
			"  Continuing would plan a FULL re-install over a working one — roughly sixty resources\n"+
			"  created on top of themselves — which takes about thirteen minutes to fail and reports\n"+
			"  nothing useful when it does. Refusing now instead.\n\n"+
			"  This is the normal shape when a cluster moves between deployments: each one gets its own\n"+
			"  state, so the second has no record of the first's install.\n\n"+
			"  Options:\n"+
			"    • use the workspace that installed it — its state can converge or remove the install;\n"+
			"    • remove BNK from the cluster first, then re-run here;\n"+
			"    • if the install is genuinely abandoned and you intend to replace it, clear it from the\n"+
			"      cluster deliberately rather than letting an apply collide with it.\n\n"+
			"  `roksbnkctl -w %s bnk status` shows what is on the cluster",
		cluster, ns, cctx.WorkspaceName, cctx.WorkspaceName)
}

// bnkStateHasResources reports whether the BNK phase's terraform state holds any
// managed resources, and whether that answer is KNOWN at all.
//
// The two-value shape is the point. "No local state file" means two entirely
// different things depending on the backend:
//
//   - LOCAL backend — genuinely no state. Reading it as "no resources" is correct and
//     conservative: it only ever leads to the cluster being checked.
//   - S3 backend (PRD 16) — the state lives in object storage. There is no
//     <stateDir>/terraform.tfstate, and .terraform/terraform.tfstate holds backend
//     configuration with no "resources" key at all. A workspace that installed BNK
//     itself would read as empty, its own FLO pod would be found running, and the
//     guard would refuse its every converge — advising the user to "use the workspace
//     that installed it", which is the workspace being refused.
//
// So the on-disk read stays the fast path, and a remote backend that it cannot answer
// for is confirmed with `terraform state list`. writeAndInitSecondPhase has already
// run `init` by the time the guard is reached, so the backend is live. If that also
// fails, the answer is unknown and the guard stays silent — this exists to convert a
// slow confusing failure into a fast clear one, not to invent a new way to refuse.
func bnkStateHasResources(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) (has bool, known bool) {
	stateDir := tfws.StateDir()
	if stateDir == "" {
		return false, false
	}
	if stateFileHasResources(stateDir) {
		return true, true
	}
	if !remoteStateBackend(cctx) {
		return false, true // local backend: absent state really is no state
	}
	out, err := tfws.RunReadOnly(ctx, []string{"state", "list"})
	if err != nil {
		return false, false // cannot tell — say nothing
	}
	return strings.TrimSpace(out) != "", true
}

// stateFileHasResources is the on-disk half: true only when a state file in stateDir
// carries a non-empty "resources" array. Matching on the empty forms is more robust
// than a full parse, which would have to track state-format changes to stay correct.
// A missing or unrecognised file is false — the caller decides what that means, which
// depends entirely on the backend.
func stateFileHasResources(stateDir string) bool {
	for _, name := range []string{"terraform.tfstate", filepath.Join(".terraform", "terraform.tfstate")} {
		b, err := os.ReadFile(filepath.Join(stateDir, name))
		if err != nil || len(b) == 0 {
			continue
		}
		s := string(b)
		if !strings.Contains(s, `"resources"`) {
			continue
		}
		if strings.Contains(s, `"resources": []`) || strings.Contains(s, `"resources":[]`) {
			continue
		}
		return true
	}
	return false
}

// remoteStateBackend reports whether terraform state lives somewhere other than this
// workspace's state dir, in which case its absence on disk proves nothing.
func remoteStateBackend(cctx *config.Context) bool {
	if cctx == nil || cctx.Workspace == nil {
		return false
	}
	switch strings.TrimSpace(cctx.Workspace.State.Backend) {
	case "", "local":
		return false
	default:
		return true
	}
}
