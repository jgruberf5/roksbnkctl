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
	if bnkStateHasResources(tfws.StateDir()) {
		return nil // we own an install; converge normally
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
// managed resources.
//
// Read off disk rather than via `terraform state list`: this runs before the plan, on
// a path where the workspace may not be initialised yet, and shelling out would be
// both slower and able to fail for reasons that have nothing to do with the question.
// An unreadable or absent state reads as "no resources", which is the conservative
// answer — it only ever leads to the cluster being checked.
func bnkStateHasResources(stateDir string) bool {
	if stateDir == "" {
		return false
	}
	dir := stateDir
	for _, name := range []string{"terraform.tfstate", filepath.Join(".terraform", "terraform.tfstate")} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(b) == 0 {
			continue
		}
		// A state with managed resources has a non-empty "resources" array. Matching
		// on the empty forms is more robust than a full parse, which would have to
		// track state-format changes to stay correct.
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
