package orchestration

// Second-phase cluster-shared-infra handoff (issues/issue_sprint16_validator.md
// Issue 2 — corrected, round 2; Sprint 23 phase-separation-leak follow-up
// in issues/issue_sprint23_staff.md). The `up` flow applies the SAME root
// terraform (module.roks_cluster + module.testing + the bnk-layer
// modules) across two independent state files. The cluster phase
// (state-cluster/) forces deploy_bnk=false and creates the entire
// cluster-shared network: the cluster VPC, the cluster subnets + public
// gateways, the transit gateway, AND module.testing's client VPC /
// jumphost subnets / jumphost SG. The second (bnk/testing) phase runs
// the same root against its own near-empty state/ with deploy_bnk=true —
// but create_roks_cluster and the testing_create_* toggles are still on,
// so it re-PLANS every cluster-shared resource the cluster phase already
// built, and IBM Cloud rejects the duplicate names.
//
// Round 1 (commit 27f7a02) tried per-resource "use existing" toggles
// (use_existing_cluster_vpc / create_roks_transit_gateway=false /
// testing_create_client_vpc=false). The live `!` verify (run-id
// 20260519-181511) proved that wrong: the VPC reuse worked but the
// second phase still re-created the cluster subnets, the cluster public
// gateways, the transit gateway, the client VPC, the jumphost subnets,
// and the jumphost SG. Chasing a growing list of per-resource flags
// across two modules is the wrong model.
//
// The corrected model is architectural and symmetric with the existing,
// live-proven cluster-phase-override.tfvars mechanism
// (internal/cli/cluster_phase.go): just as the cluster phase has a
// FORCED override that turns the bnk-layer OFF (deploy_bnk=false), the
// second/bnk phase gets a FORCED override that turns cluster-shared
// creation OFF when this workspace already has a cluster-outputs.json
// (i.e. the cluster phase has completed). With that override:
//
//   create_roks_cluster                = false
//     → module.roks_cluster.module.cluster resolves the cluster by name
//       via data.ibm_container_vpc_cluster.existing_cluster (count flips
//       create→data); ZERO cluster subnet / public-gateway / cluster
//       creates; null_resource.cluster_ready count→0.
//   roks_cluster_id_or_name            = "<cluster-outputs.json id/name>"
//     → identity for that existing-cluster data lookup + the downstream
//       roks_cluster_name output (already create_roks_cluster-aware).
//   use_existing_cluster_vpc           = true
//   existing_cluster_vpc_id            = "<cluster-outputs.json vpc_id>"
//     → ibm_is_vpc.cluster_vpc[0] (count = use_existing_cluster_vpc?0:1,
//       NOT gated by create_cluster) flips to
//       data.ibm_is_vpc.existing_cluster_vpc[0]. This is the one round-1
//       piece that genuinely works and is still needed here.
//   create_roks_transit_gateway        = false
//     → ibm_tg_gateway.transit_gateway[0] count→0 (cluster phase already
//       created + connected it).
//   create_roks_registry_cos_instance  = false
//     → module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance
//       has count = var.create_cluster && var.create_cos_instance ? 1 : 0.
//       With create_roks_cluster=false above the create_cluster half is
//       false, so the resource SHOULD already be count=0 — but
//       2026-05-27 live evidence (issues/issue_sprint23_staff.md) shows
//       it landing as managed in trial state anyway. Adding this flag
//       explicitly forces the second count gate off too, so the resource
//       is suppressed by BOTH halves of the && independently. Defense-
//       in-depth against any code path (refresh-on-name, partial-apply
//       carry-over from pre-Sprint-23 workspaces) that might import the
//       cluster-phase COS into trial state.
//   deploy_cert_manager                = false
//     → module.cert_manager's INNER cert-manager submodule has
//       `count = var.enabled ? 1 : 0` on its null_resources +
//       time_sleep. Pre-Sprint-23-round-2 the outer wrapper hardcoded
//       `enabled = true`; that hardcode is now `enabled = var.deploy_cert_manager`
//       and this flag flips it off. The destroy provisioner inside the
//       inner module — `kubectl delete namespace cert-manager` — CANNOT
//       fire when the resource was never created in trial state, which
//       was the actual cluster-damage hazard surfaced by the canada-roks
//       2026-05-27 live verify. cert_manager's outputs become null when
//       enabled=false; flo/cne_instance/license's
//       cert_manager_dependency_id consumers gate on `!= null` (see
//       flo/providers.tf:42 "direct-apply" fallback), so the chain is
//       structurally safe.
//   testing_create_cluster_jumphosts   = false
//   testing_create_tgw_jumphost        = false
//   testing_create_client_vpc          = false
//     → module.testing plans NO client VPC, NO jumphost subnets, NO
//       jumphost SG, NO cluster-jumphost subnets/SG. Those are
//       cluster-shared singletons the cluster phase already created.
//       Sprint 23 also count-gated tls_private_key.jumphost_shared_key
//       on (testing_create_cluster_jumphosts || testing_create_tgw_jumphost)
//       in terraform/modules/testing/main.tf so the key resource itself
//       (previously ungated → always created) flips to count=0 here too.
//       The five jumphost_user_data locals at lines 39/40/188/193/194 of
//       the same file got `length(...) > 0 ? ... : ""` guards in the
//       Sprint 23 round-2 follow-on so they don't blow up on the empty
//       tuple during plan.
//
// Net: the second/bnk phase plan contains the bnk-layer modules
// (flo / cne_instance / license) + existing-cluster DATA lookups ONLY —
// no module.roks_cluster / module.cert_manager / module.testing cluster-shared
// CREATE at all. Not per-toggle whack-a-mole: the second phase no longer
// MANAGES cluster-shared infra.
//
// Scope guard: this override is ONLY layered by the second/trial phase
// (RunTrialUp / RunApply) and ONLY when cluster-outputs.json exists. A
// fresh/empty or legacy-single-state workspace has no cluster-outputs.json
// → no override → the create path is byte-identical to before (validator
// Issue 1's parity gate stays GREEN; cluster-only and bnk-only sub-flows
// unchanged). config.ReadClusterOutputs is read through internal/config
// exactly the way internal/cli/cluster_phase.go writes it, so
// internal/orchestration never imports internal/cli (the one-directional
// boundary asserted in Sprint 16).

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// bnkPhaseOverrideFile is the second/bnk-phase forced override tfvars,
// written into the trial state dir and appended LAST to the var-file
// chain so it wins over config.yaml-derived tfvars and any user
// terraform.tfvars.user — exactly the precedence
// cluster-phase-override.tfvars uses for the cluster phase.
const bnkPhaseOverrideFile = "bnk-phase-override.tfvars"

// testingPhaseOverrideFile is the Sprint 28 testing-phase forced override
// tfvars, written into state-testing/ and appended LAST to the var-file
// chain so it wins over config.yaml-derived tfvars + terraform.tfvars.user
// — symmetric with bnk-phase-override.tfvars / cluster-phase-override.tfvars.
const testingPhaseOverrideFile = "testing-phase-override.tfvars"

// gatewayPhaseOverrideFile is the optional Gateway-phase forced override
// tfvars, written into state-gateway/ and appended LAST to the var-file chain.
// It forces every other phase's creation OFF and deploy_gateway ON, so the
// gateway state manages ONLY the data-plane config CRs + the VXLAN SG rule.
const gatewayPhaseOverrideFile = "gateway-phase-override.tfvars"

// flpPhaseOverrideFile is the optional FLP-phase forced override tfvars, written
// into state-flp/ and appended LAST to the var-file chain. It forces every other
// phase's creation OFF and deploy_flp ON, so the FLP state manages ONLY the F5
// License Proxy.
const flpPhaseOverrideFile = "flp-phase-override.tfvars"

// tgwPhaseOverrideFile is the optional TGW-connection-phase forced override
// tfvars, written into state-tgw/ and appended LAST. It forces every other
// phase's creation OFF and deploy_tgw_connection ON, so the tgw state manages
// ONLY this cluster's connection to a shared Transit Gateway.
const tgwPhaseOverrideFile = "tgw-phase-override.tfvars"

// bnkFLPOverrideFile carries the FLP-licensing tfvars into the BNK phase
// (endpoint + root CA from flp-outputs.json). Appended AFTER the bnk override.
const bnkFLPOverrideFile = "bnk-flp-override.tfvars"

// writeBnkFLPOverride emits the proxy endpoint + decoded root-CA PEM as forced
// tfvars for `bnk up` in f5licenseproxy mode.
//
// Two sources, in priority order:
//
//  1. bnk.flp.external — a FOREIGN proxy, owned by a different workspace/cluster
//     (the shared-licensing-cluster topology). This workspace never runs `flp up`.
//  2. flp-outputs.json — a proxy THIS workspace deployed with `flp up`.
//
// A missing handoff with neither source is an actionable error.
func writeBnkFLPOverride(stateDir, workspace string, ws *config.Workspace) (path, source string, err error) {
	var endpoint, caB64 string

	if ext := flpExternal(ws); ext != nil {
		if ext.URL == "" || ext.RootCAB64 == "" {
			return "", "", fmt.Errorf("bnk.flp.external needs BOTH url and root_ca_b64 — get them from the owning workspace with `roksbnkctl -w <owner> flp output`")
		}
		endpoint, caB64, source = ext.URL, ext.RootCAB64, "bnk.flp.external (a proxy in another cluster)"
	} else {
		out, err := config.ReadFLPOutputs(workspace)
		if err != nil {
			return "", "", fmt.Errorf("license_mode is f5licenseproxy but the F5 License Proxy handoff is missing: %w\n"+
				"  deploy one here with `roksbnkctl flp up`, or point at one in another cluster via bnk.flp.external "+
				"(url + root_ca_b64 from `roksbnkctl -w <owner> flp output`)", err)
		}
		if out.Endpoint == "" || out.RootCAB64 == "" {
			return "", "", fmt.Errorf("flp-outputs.json for workspace %q is incomplete (no endpoint / root CA) — re-run `roksbnkctl flp up`", workspace)
		}
		endpoint, caB64, source = out.Endpoint, out.RootCAB64, "flp-outputs.json (a proxy in this cluster)"
	}

	pem, derr := base64.StdEncoding.DecodeString(caB64)
	if derr != nil {
		return "", "", fmt.Errorf("decoding the FLP root CA from %s: %w", source, derr)
	}
	overridePath := filepath.Join(stateDir, bnkFLPOverrideFile)
	content := fmt.Sprintf(`# Generated by roksbnkctl. Do not edit by hand.
# FLP-licensing handoff for `+"`bnk up`"+`, from %s. Points the BNK License CR at
# the F5 License Proxy. Forced — wins over config.yaml.
flp_license_server_url = %q
license_server_root_ca = %q
`, source, endpoint, string(pem))
	if err := os.WriteFile(overridePath, []byte(content), 0o644); err != nil {
		return "", "", fmt.Errorf("writing bnk-flp override: %w", err)
	}
	return overridePath, source, nil
}

// flpExternal returns the foreign-proxy config, or nil when this workspace owns
// its proxy.
func flpExternal(ws *config.Workspace) *config.BNKFLPExternalCfg {
	if ws == nil || ws.BNK.FLP == nil || ws.BNK.FLP.External == nil {
		return nil
	}
	e := ws.BNK.FLP.External
	if e.URL == "" && e.RootCAB64 == "" {
		return nil // an empty block is not an opt-in
	}
	return e
}

// writeAndInitSecondPhase is the second/trial-phase preamble. It renders
// the normal terraform.tfvars (unchanged create-path render — the round-1
// per-toggle renderer is gone), then, when this workspace already has a
// cluster-outputs.json (the cluster phase completed → we are the SECOND
// phase), writes a bnk-phase-override.tfvars that forces every
// cluster-shared CREATE off (cluster + cluster VPC reuse + transit
// gateway + registry COS + client VPC + both jumphost classes; Sprint 23
// added the explicit registry-COS flag and a count gate on the testing
// module's shared TLS key) and returns its path so the caller appends it
// to the plan/apply var-file chain.
//
// Returns (extraVarFiles, error). extraVarFiles is non-empty ONLY when a
// usable cluster-outputs.json (with a vpc_id) exists; otherwise it is nil
// and the run is byte-identical to the pre-Issue-2 second-phase flow
// (which is itself identical to the create path).
func writeAndInitSecondPhase(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace, workspace string, destroy bool, w io.Writer) ([]string, error) {
	if w == nil {
		w = os.Stderr
	}
	if err := tfws.WriteTFVars(ws); err != nil {
		return nil, fmt.Errorf("writing tfvars: %w", err)
	}
	if tfws.HasUserTFVars() {
		fmt.Fprintf(w, "→ Layering user tfvars from %s (overrides config.yaml-derived values)\n", tfws.UserTFVarsPath())
	}

	co, err := loadReuseClusterOutputs(workspace)
	if err != nil {
		return nil, err
	}

	var extra []string
	if co != nil && co.VPCID != "" {
		overridePath, werr := writeBnkPhaseOverride(tfws, co)
		if werr != nil {
			return nil, werr
		}
		extra = []string{overridePath}
		fmt.Fprintf(w,
			"→ Second-phase handoff: cluster-outputs.json present — forcing cluster-shared infra OFF "+
				"(create_roks_cluster=false, reuse cluster VPC %s + transit gateway + jumphosts). "+
				"The bnk phase deploys only the BNK trial layer onto the existing cluster.\n",
			co.VPCID)
	}

	// FLP licensing: when the workspace opts into f5licenseproxy mode, point the
	// BNK License CR at the deployed proxy. The endpoint + root CA come from the
	// flp phase's flp-outputs.json (written by `flp up`); its absence is an
	// actionable error. license_mode itself is already rendered from config
	// (renderBNKFields); this override adds only the endpoint + CA and is appended
	// LAST so it wins for those keys.
	//
	// Deliberately OUTSIDE the cluster-outputs.json block above. renderBNKFields
	// emits license_mode from config regardless, so gating this on a
	// cluster-outputs.json that a legacy single-state workspace never writes would
	// hand terraform license_mode=f5licenseproxy with an EMPTY proxy URL and no
	// root CA: the apply "succeeds", the CWC never gets the CA, and licensing hangs
	// at LicenseActive with nothing pointing at the missing `flp up`.
	if ws.BNK.LicenseMode == "f5licenseproxy" {
		flpOverride, flpSource, ferr := writeBnkFLPOverride(tfws.StateDir(), workspace, ws)
		switch {
		case ferr != nil && destroy:
			// Teardown does not need the FLP endpoint/CA — the License CR is
			// destroyed regardless, and the flp_* tfvars default to "". `flp down`
			// may already have removed flp-outputs.json (it precedes the composite
			// `down`), so a missing handoff here is expected, not an error.
			fmt.Fprintln(w, "→ FLP licensing: no flp-outputs.json (FLP already down) — skipping the handoff for teardown.")
		case ferr != nil:
			return nil, ferr
		default:
			extra = append(extra, flpOverride)
			fmt.Fprintf(w, "→ FLP licensing: BNK will license via the F5 License Proxy — %s.\n", flpSource)
		}
	}

	fmt.Fprintln(w, "→ terraform init")
	if err := tfws.Init(ctx); err != nil {
		return nil, err
	}
	return extra, nil
}

// clusterIdentity returns the value to feed roks_cluster_id_or_name for
// the existing-cluster data lookup: the cluster ID if recorded, else the
// cluster name (data.ibm_container_vpc_cluster.existing_cluster accepts
// either as `name`). Empty only if cluster-outputs.json is degenerate,
// in which case the caller has already gated on VPCID != "".
func clusterIdentity(co *config.ClusterOutputs) string {
	if co.ClusterID != "" {
		return co.ClusterID
	}
	return co.ClusterName
}

// writeBnkPhaseOverride writes the forced bnk-phase override tfvars into
// the trial state dir and returns its path. The content turns OFF every
// cluster-shared CREATE so module.roks_cluster + module.testing resolve
// the cluster identity by data source instead of re-provisioning the
// network the cluster phase already built. No api_key is ever written
// (it flows via TF_VAR_ibmcloud_api_key, same as every other path).
func writeBnkPhaseOverride(tfws *tf.Workspace, co *config.ClusterOutputs) (string, error) {
	return writeBnkPhaseOverrideAt(tfws.StateDir(), co)
}

// writeBnkPhaseOverrideAt is the pure (dir-only) core of
// writeBnkPhaseOverride — production passes the trial state dir; the
// additive regression test passes a temp dir so it can assert the exact
// forced content without standing up a full terraform workspace.
func writeBnkPhaseOverrideAt(stateDir string, co *config.ClusterOutputs) (string, error) {
	overridePath := filepath.Join(stateDir, bnkPhaseOverrideFile)
	content := fmt.Sprintf(`# Generated by roksbnkctl. Do not edit by hand.
# Second/bnk-phase override (issues/issue_sprint16_validator.md Issue 2,
# round 2; Sprint 23 phase-separation-leak follow-up — added the explicit
# create_roks_registry_cos_instance gate). Sprint 27 REVERSED the Sprint 23
# cert-manager placement: cert-manager is now provider-based (helm_release /
# kubectl_manifest), and those providers cannot obtain cluster credentials
# during cluster CREATION (the cluster doesn't exist yet at plan time), so
# cert-manager deploys in THIS bnk phase (deploy_cert_manager=true below,
# where create_roks_cluster=false → the providers resolve the existing
# cluster). The old destroy-provisioner leak Sprint 23 guarded against is
# gone with Sprint 27's real terraform state (bnk down = clean destroy).
# cluster-outputs.json exists, so the cluster phase already created the
# ENTIRE cluster-shared network (cluster VPC + subnets + public gateways
# + transit gateway + registry COS + the testing client VPC / jumphost
# subnets / jumphost SG / shared SSH key). This phase must NOT re-manage
# that network — it deploys the BNK trial layer (cert-manager + flo +
# cne_instance + license) onto the already-provisioned cluster, consuming
# the cluster identity from cluster-outputs.json via the existing-cluster
# terraform data sources; flo/cne_instance/license depend on cert_manager
# intra-phase (its outputs are real here). Forced (wins
# over config.yaml tfvars + terraform.tfvars.user), symmetric with
# cluster-phase-override.
create_roks_cluster = false
roks_cluster_id_or_name = %q
use_existing_cluster_vpc = true
existing_cluster_vpc_id = %q
create_roks_transit_gateway = false
create_roks_registry_cos_instance = false
deploy_cert_manager = true
testing_create_cluster_jumphosts = false
testing_create_tgw_jumphost = false
testing_create_client_vpc = false
`, clusterIdentity(co), co.VPCID)
	if err := os.WriteFile(overridePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing bnk-phase override: %w", err)
	}
	return overridePath, nil
}

// writeTestingPhaseOverride writes the forced testing-phase override
// tfvars into the testing state dir and returns its path (Sprint 28).
func writeTestingPhaseOverride(tfws *tf.Workspace, co *config.ClusterOutputs) (string, error) {
	return writeTestingPhaseOverrideAt(tfws.StateDir(), co)
}

// writeTestingPhaseOverrideAt is the pure (dir-only) core of
// writeTestingPhaseOverride — production passes the testing state dir; a
// regression test passes a temp dir to assert the exact forced content.
//
// Per the architect's Sprint 28 design §2a the override sets only the
// ARCHITECTURAL-off flags (create_roks_cluster / deploy_bnk /
// deploy_cert_manager / create_roks_transit_gateway /
// create_roks_registry_cos_instance = false) and the VPC/TGW REUSE inputs
// (use_existing_cluster_vpc + existing_cluster_vpc_id +
// roks_cluster_id_or_name + roks_transit_gateway_name). It does NOT pin
// testing_create_tgw_jumphost / testing_create_cluster_jumphosts /
// testing_create_client_vpc — those flow from the user's rendered tfvars,
// so "I only want a TGW jumphost, no cluster jumphosts" keeps working.
//
// roks_transit_gateway_name (the DECLARED root var that feeds the testing
// module's TGW lookup) is emitted only when the handoff carries a non-empty
// name; otherwise the line is omitted and the normal config.yaml render
// supplies it (a `cluster register` without a TGW output).
func writeTestingPhaseOverrideAt(stateDir string, co *config.ClusterOutputs) (string, error) {
	overridePath := filepath.Join(stateDir, testingPhaseOverrideFile)
	tgwLine := ""
	if co.TransitGatewayName != "" {
		// The testing module reads the TGW name from the root's
		// roks_transit_gateway_name (→ module.roks_cluster.transit_gateway_name);
		// testing_transit_gateway_name is a MODULE input, not a root variable, so
		// setting it here only produced an "undeclared variable" warning.
		tgwLine = fmt.Sprintf("roks_transit_gateway_name = %q\n", co.TransitGatewayName)
	}
	content := fmt.Sprintf(`# Generated by roksbnkctl. Do not edit by hand.
# Testing-phase override (Sprint 28 three-phase split). cluster-outputs.json
# exists, so the cluster phase already created the cluster VPC + subnets +
# public gateways + transit gateway + registry COS. This phase provisions
# ONLY the testing jumphosts (pure IBM VPC), consuming the cluster VPC + TGW
# from cluster-outputs.json. It must NOT manage the cluster, cert-manager,
# or any BNK module. Forced (wins over config.yaml tfvars +
# terraform.tfvars.user), symmetric with cluster-phase-override /
# bnk-phase-override. The testing_create_* toggles are intentionally NOT
# pinned here — they flow from the user's rendered tfvars so "only a TGW
# jumphost, no cluster jumphosts" keeps working.
create_roks_cluster = false
roks_cluster_id_or_name = %q
use_existing_cluster_vpc = true
existing_cluster_vpc_id = %q
create_roks_transit_gateway = false
create_roks_registry_cos_instance = false
deploy_bnk = false
deploy_cert_manager = false
%s`, clusterIdentity(co), co.VPCID, tgwLine)
	if err := os.WriteFile(overridePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing testing-phase override: %w", err)
	}
	return overridePath, nil
}

func writeGatewayPhaseOverride(tfws *tf.Workspace, co *config.ClusterOutputs) (string, error) {
	return writeGatewayPhaseOverrideAt(tfws.StateDir(), co)
}

func writeFLPPhaseOverride(tfws *tf.Workspace, co *config.ClusterOutputs, vsiMode bool, clusterAbsent bool) (string, error) {
	return writeFLPPhaseOverrideAt(tfws.StateDir(), co, vsiMode, clusterAbsent)
}

// writeFLPPhaseOverrideAt is the pure (dir-only) core of writeFLPPhaseOverride.
// The FLP phase reuses the cluster from cluster-outputs.json and manages ONLY the
// F5 License Proxy — so the override forces every OTHER phase's creation off
// (cluster, TGW, registry COS, BNK, cert-manager, all testing_create_*,
// deploy_gateway) and deploy_flp ON. Forced (wins over config.yaml tfvars +
// terraform.tfvars.user), symmetric with the other phase overrides.
// writeTGWPhaseOverride writes the forced TGW-connection-phase override tfvars.
// target is the gateway to attach to (name or id); connName is this cluster's
// connection name on the gateway (unique per gateway).
func writeTGWPhaseOverride(tfws *tf.Workspace, co *config.ClusterOutputs, target, connName string) (string, error) {
	return writeTGWPhaseOverrideAt(tfws.StateDir(), co, target, connName)
}

// writeTGWPhaseOverrideAt is the pure (dir-only) core of writeTGWPhaseOverride.
// It forces every other phase OFF and deploy_tgw_connection ON, and passes the
// cluster VPC id from cluster-outputs.json so the connection resolves for a
// created OR a registered cluster. A regression test pins the exact content.
func writeTGWPhaseOverrideAt(stateDir string, co *config.ClusterOutputs, target, connName string) (string, error) {
	overridePath := filepath.Join(stateDir, tgwPhaseOverrideFile)
	content := fmt.Sprintf(`# Generated by roksbnkctl. Do not edit by hand.
# TGW-connection-phase override. The cluster already exists; this phase attaches
# its VPC to an existing Transit Gateway and NOTHING else. Every other phase's
# creation is forced OFF and deploy_tgw_connection ON. Forced — wins over
# config.yaml tfvars + terraform.tfvars.user.
create_roks_cluster = false
roks_cluster_id_or_name = %q
use_existing_cluster_vpc = true
existing_cluster_vpc_id = %q
create_roks_transit_gateway = false
create_roks_registry_cos_instance = false
deploy_bnk = false
deploy_cert_manager = false
testing_create_tgw_jumphost = false
testing_create_cluster_jumphosts = false
testing_create_client_vpc = false
deploy_gateway = false
deploy_flp = false
deploy_tgw_connection = true
tgw_connection_target = %q
tgw_connection_name = %q
`, clusterIdentity(co), co.VPCID, target, connName)
	if err := os.WriteFile(overridePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing tgw-phase override: %w", err)
	}
	return overridePath, nil
}

func writeFLPPhaseOverrideAt(stateDir string, co *config.ClusterOutputs, vsiMode bool, clusterAbsent bool) (string, error) {
	overridePath := filepath.Join(stateDir, flpPhaseOverrideFile)
	// The FLP phase deploys ONE backend: the helm chart (deploy_flp) OR the
	// standalone VSI (deploy_flp_vsi). Both terminate in the same flp_* outputs.
	//
	// Standalone FLP VSI (clusterAbsent): there is NO cluster to adopt. Downstream
	// modules (cert_manager/flo/cne_instance/license/testing) still instantiate and
	// validate roks_cluster_name_or_id as length>0, so feed a non-empty sentinel
	// identity; cluster_absent=true then forces every cluster data-source lookup +
	// kube provider across those modules (and the roks_cluster adopt) to count=0, so
	// the sentinel is never resolved against a real cluster.
	identity := clusterIdentity(co)
	if clusterAbsent && identity == "" {
		identity = "flp-standalone-no-cluster"
	}
	content := fmt.Sprintf(`# Generated by roksbnkctl. Do not edit by hand.
# FLP-phase override. The cluster already exists; this phase installs ONLY the
# F5 License Proxy, reusing the cluster from cluster-outputs.json. Every other
# phase's creation is forced OFF. Forced — wins over config.yaml tfvars +
# terraform.tfvars.user.
create_roks_cluster = false
roks_cluster_id_or_name = %q
use_existing_cluster_vpc = true
existing_cluster_vpc_id = %q
create_roks_transit_gateway = false
create_roks_registry_cos_instance = false
deploy_bnk = false
deploy_cert_manager = false
testing_create_tgw_jumphost = false
testing_create_cluster_jumphosts = false
testing_create_client_vpc = false
deploy_gateway = false
deploy_flp = %v
deploy_flp_vsi = %v
cluster_absent = %v
`, identity, co.VPCID, !vsiMode, vsiMode, clusterAbsent)
	if err := os.WriteFile(overridePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing flp-phase override: %w", err)
	}
	return overridePath, nil
}

// writeGatewayPhaseOverrideAt is the pure (dir-only) core of
// writeGatewayPhaseOverride. The Gateway phase reuses the cluster from
// cluster-outputs.json and manages ONLY the data-plane config — so the
// override forces every OTHER phase's creation off (cluster, TGW, registry
// COS, BNK, cert-manager, all testing_create_*) and deploy_gateway ON. Forced
// (wins over config.yaml tfvars + terraform.tfvars.user), symmetric with the
// bnk-/testing-/cluster-phase overrides. A regression test passes a temp dir
// to assert the exact forced content.
func writeGatewayPhaseOverrideAt(stateDir string, co *config.ClusterOutputs) (string, error) {
	overridePath := filepath.Join(stateDir, gatewayPhaseOverrideFile)
	content := fmt.Sprintf(`# Generated by roksbnkctl. Do not edit by hand.
# Gateway-phase override. The cluster + BNK already exist; this phase applies
# ONLY the data-plane config CRs (Gateway API + SnatPool + Egress + static
# routes) and the VXLAN security-group rule, reusing the cluster from
# cluster-outputs.json. Every other phase's creation is forced OFF and
# deploy_gateway ON. Forced — wins over config.yaml tfvars + terraform.tfvars.user.
create_roks_cluster = false
roks_cluster_id_or_name = %q
use_existing_cluster_vpc = true
existing_cluster_vpc_id = %q
create_roks_transit_gateway = false
create_roks_registry_cos_instance = false
deploy_bnk = false
deploy_cert_manager = false
testing_create_tgw_jumphost = false
testing_create_cluster_jumphosts = false
testing_create_client_vpc = false
deploy_gateway = true
`, clusterIdentity(co), co.VPCID)
	if err := os.WriteFile(overridePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing gateway-phase override: %w", err)
	}
	return overridePath, nil
}

// loadReuseClusterOutputs returns the workspace's ClusterOutputs when a
// cluster-outputs.json exists (the cluster phase completed → we are the
// SECOND phase reusing it), or (nil, nil) when there is none (first/
// cluster phase, a fresh workspace, or a legacy single-state workspace —
// caller renders/plans the create path unchanged). A genuine read/parse
// error is surfaced so the user sees a corrupt file rather than silently
// planning a duplicate create.
func loadReuseClusterOutputs(workspace string) (*config.ClusterOutputs, error) {
	co, err := config.ReadClusterOutputs(workspace)
	if err != nil {
		if err == config.ErrClusterOutputsMissing {
			return nil, nil
		}
		return nil, fmt.Errorf("reading cluster-outputs.json for second-phase handoff: %w", err)
	}
	return co, nil
}
