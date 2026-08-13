package orchestration

import (
	"fmt"
	"io"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The service account the CNE controller's Trusted Profile is linked to changed
// its DEFAULT, from the Helm-shaped
//
//	f5-cne-controller-<flo_namespace>-f5-cne-controller-serviceaccount
//
// to plain "f5-cne-controller".
//
// WHY THIS NEEDS A WARNING RATHER THAN NOTHING. The name is not cosmetic — it is
// the subject of an IBM IAM trusted-profile link and the subject of a privileged
// SCC ClusterRoleBinding. Changing it on a workspace that already has a cluster
// makes terraform destroy and recreate the link (IAM links have no update API)
// and delete the old ClusterRoleBinding for a new one under a different key.
//
// If the account the CNE controller actually runs as is still the long name,
// both changes leave it without privileged SCC and without the profile. Neither
// failure appears at apply: the plan succeeds, and the controller loses its VPC
// permissions at its next pod restart, hours later, complaining about something
// else entirely.
//
// So it warns, once, only where the change can bite — an existing cluster whose
// config is silent about the account and is therefore about to adopt the new
// default. A first run has nothing to change and says nothing.
const legacyTrustedProfileSAFormat = "f5-cne-controller-%s-f5-cne-controller-serviceaccount"

func guardTrustedProfileSADefault(cctx *config.Context, w io.Writer) error {
	if cctx == nil || cctx.Workspace == nil {
		return nil
	}
	// An explicit value is a decision already made; say nothing.
	if tp := cctx.Workspace.BNK.TrustedProfile; tp != nil && strings.TrimSpace(tp.ServiceAccount) != "" {
		return nil
	}
	out, err := config.ReadClusterOutputs(cctx.WorkspaceName)
	if err != nil || out == nil || out.ClusterID == "" {
		return nil // no cluster yet — nothing to change under it
	}

	ns := strings.TrimSpace(cctx.Workspace.BNK.FLONamespace)
	if ns == "" {
		ns = "f5-bnk"
	}
	fmt.Fprintf(w, "! bnk.trusted_profile.service_account is unset, so this run uses the NEW default\n"+
		"  \"f5-cne-controller\". Cluster %q already exists, and earlier releases linked the\n"+
		"  CNE controller's Trusted Profile to %q instead.\n\n"+
		"  Applying will recreate the IAM link and replace the privileged-SCC binding under\n"+
		"  the new name. If the controller's service account is still the old one, it loses\n"+
		"  both — silently at apply, visibly at the next pod restart.\n\n"+
		"  Confirm the account with:\n"+
		"      roksbnkctl -w %s k get sa -n %s\n\n"+
		"  and pin whichever it is:\n"+
		"      bnk:\n"+
		"        trusted_profile:\n"+
		"          service_account: <the account that exists>\n",
		out.ClusterName, fmt.Sprintf(legacyTrustedProfileSAFormat, ns), cctx.WorkspaceName, ns)
	return nil
}
