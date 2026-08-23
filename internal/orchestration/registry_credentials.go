package orchestration

import (
	"fmt"
	"io"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// checkMirrorCredentials refuses an apply that cannot possibly authenticate to
// the mirror, BEFORE anything is installed.
//
// The chart pull picks its credential by falling through a chain, and the last
// arm of that chain uses the literal username "unused" with the cluster's own
// kube token. That arm is correct for the IN-CLUSTER OpenShift registry, which
// authenticates exactly that way. It is never correct for an external host, so
// when the operator has named a username but supplied no password the pull
// reaches an external Artifactory/Harbor as "unused" and is answered:
//
//	response status code 401: : Bad Credentials
//
// Nothing before that point needs the password, so the apply gets as far as
// installing flo and creating IAM trusted profiles — roughly fifteen minutes and
// real cloud resources — before failing on something knowable at the start. And
// the error names neither the setting that is missing nor the file it lives in.
//
// The rule is deliberately narrow: a username with no password. An external
// registry that genuinely allows anonymous pulls sets neither, and the in-cluster
// registry path sets neither, so both stay untouched.
func checkMirrorCredentials(cctx *config.Context, w io.Writer) error {
	ws := cctx.Workspace
	if ws == nil {
		return nil
	}
	// Registry is a POINTER: a workspace that never configured a mirror leaves
	// it nil, and that is the common case off the air-gap path.
	reg := ws.Registry
	if reg == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(reg.Target), "generic") {
		return nil
	}
	host := strings.TrimSpace(reg.GenericHost)
	user := strings.TrimSpace(reg.GenericUsername)
	if host == "" || user == "" {
		return nil
	}
	if strings.TrimSpace(reg.GenericPasswordB64) != "" {
		return nil
	}
	return fmt.Errorf(
		"registry %s is configured with username %q but no password, so the chart pull would "+
			"authenticate as \"unused\" and be refused with 401 Bad Credentials part-way through the "+
			"apply.\n"+
			"  set registry.generic_password_b64 in config.yaml, or ROKSBNKCTL_GENERIC_PASSWORD in the "+
			"environment (it is base64-encoded for you).\n"+
			"  if this registry really does allow anonymous pulls, clear registry.generic_username as well",
		host, user)
}
