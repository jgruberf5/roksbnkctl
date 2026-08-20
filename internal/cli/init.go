package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/naming"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
	"github.com/jgruberf5/roksbnkctl/internal/sshkey"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// githubRepoPattern matches a GitHub-shaped "owner/repo" slug. Used by
// the init prompt to decide whether a user-typed TF source is a GitHub
// repo or a local path. Must match the full input.
var githubRepoPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*/[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// looksLikeGitHubRepo reports whether s matches the "owner/repo" pattern.
// Anything else (paths, URLs, blank) is treated as a local path.
func looksLikeGitHubRepo(s string) bool {
	return githubRepoPattern.MatchString(strings.TrimSpace(s))
}

// resolveLocalTFSource is the cli-layer thin wrapper over the single
// path-normalization chokepoint (orchestration.NormalizeLocalPath). It
// exists so the in-package tests that pin the --tf-source local
// relative-path behavior (Sprint 12 Issue 2) call a stable local
// symbol; the canonical normalization lives in orchestration and is
// applied exactly once at command entry (the root PersistentPreRunE →
// resolveInvocationContext), NOT re-derived per call site.
//
// Only reached for the local TF source form — the embedded/github
// branches (split off via the "embedded" literal and looksLikeGitHubRepo)
// never build a local Path, so there is no URL or owner/repo input to
// guard against here.
func resolveLocalTFSource(path string) (string, error) {
	return orchestration.NormalizeLocalPath(path)
}

// envHasAPIKey reports whether any of the env vars the resolution chain
// honours is set. Used by `roksbnkctl init` to decide whether to opportunistically
// persist the resolved key into the workspace — env-driven setups don't
// need persistent storage; they have it already.
func envHasAPIKey() bool {
	for _, v := range []string{"IBMCLOUD_API_KEY", "IC_API_KEY", "TF_VAR_ibmcloud_api_key", "TF_VAR_IBMCLOUD_API_KEY", "TF_VAR_IC_API_KEY"} {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

const (
	// defaultTFRepo is the source roksbnkctl drives by default. Per the
	// PRD's "unified tag stream" decision, roksbnkctl pins to the latest
	// release of this repo at init time.
	defaultTFRepo = "jgruberf5/ibmcloud_terraform_bigip_next_for_kubernetes_2_3"

	// initTimeout caps the network operations init does (IAM verify,
	// resource group lookup, GitHub release resolution). Prompts run
	// outside the timeout so users can take their time typing.
	initTimeout = 60 * time.Second
)

// runInit implements `roksbnkctl init` — interactive setup that writes the
// workspace's config.yaml and (if no global pointer is set) sets the
// current_workspace pointer.
//
// Behaviours:
//   - If --upgrade-tf and the workspace exists, just bumps tf_source.ref.
//   - If the workspace exists and --upgrade-tf is not set, prompts to
//     overwrite (existing values become the default for each prompt).
//   - If --config-file <path|url> is supplied, config.yaml is seeded from it
//     and the interview is skipped; --non-interactive builds config.yaml from
//     the environment alone (the argv+env runner path).
//   - If stdin is not a TTY, accepts every default — usable from CI as
//     long as IBMCLOUD_API_KEY and the existing config (or workspace
//     name) provide enough context.
func runInit(cmd *cobra.Command, _ []string) error {
	if err := rejectOnFlag("init"); err != nil {
		return err
	}
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}

	// No -w and no current pointer (first run, or every workspace was deleted).
	// config.New no longer falls back to a phantom "default", so ask for a name
	// here — non-interactive runs take the bootstrap "default".
	if cctx.WorkspaceName == "" {
		name := promptString("Workspace name", config.DefaultWorkspace)
		if err := config.ValidateName(name); err != nil {
			return err
		}
		cctx, err = config.New(name)
		if err != nil {
			return err
		}
	}

	// --upgrade-tf is the cheap path: re-resolve TF source on existing config.
	if flagUpgradeTF {
		if cctx.Workspace == nil {
			return fmt.Errorf("workspace %q does not exist; run `roksbnkctl init` (without --upgrade-tf) to create it", cctx.WorkspaceName)
		}
		ctx, cancel := contextWithTimeout(cmdContext(cmd), initTimeout)
		defer cancel()
		return runUpgradeTF(ctx, cctx)
	}

	// Existing workspace: re-init RESUMES the interview to complete/update it,
	// pre-filling defaults from the saved config (initDefaults reads it below).
	// This is how a PARTIAL workspace — one left by a failed first init — gets
	// finished, so the framing is "complete/update", not "overwrite".
	if cctx.Workspace != nil {
		fmt.Fprintf(os.Stderr, "Workspace %q already exists — re-running setup to complete/update it.\n", cctx.WorkspaceName)
		if !promptYesNo("Continue and update this workspace?", true) {
			return errors.New("aborted")
		}
	}

	// --config-file (Sprint 30 Issue 2): non-interactive seed of config.yaml —
	// validate + write, skipping the interview. The overwrite confirmation above
	// still gates a re-init.
	if flagInitConfigFile != "" {
		return runInitFromConfigFile(cctx)
	}

	// --non-interactive (no --config-file): assemble config.yaml from the
	// ROKSBNKCTL_* / IBMCLOUD_API_KEY env vars alone — the argv+env runner path
	// (CI / BNK Forge container step), no TTY, no seed file to stage.
	if flagInitNonInteractive {
		return runInitFromEnv(cctx)
	}

	fmt.Fprintf(os.Stderr, "Setting up workspace %q\n\n", cctx.WorkspaceName)

	// Existing values become defaults; otherwise PRD-stated defaults.
	dRegion, dRG, _, dOCP, dWorkers, dCreate := initDefaults(cctx)

	// API key — env, then keychain, then prompt; offer to save on prompt.
	resolver := &cred.Resolver{Workspace: cctx.WorkspaceName}
	apiKey, err := resolver.IBMCloudAPIKey(cmdContext(cmd))
	if err != nil {
		return fmt.Errorf("resolving API key: %w", err)
	}

	// Bootstrap region only to construct the client for credential verification
	// + the region/cluster listings (Verify, ListRegions, ListClusters,
	// ResolveResourceGroup are global or use a fixed host). The FINAL region is
	// chosen in the interview below — a menu when creating a cluster, the
	// chosen cluster's own region when reusing one.
	region := dRegion

	// The interview is interactive, so the flow context must NOT carry a
	// wall-clock deadline — slow human answers between prompts would otherwise
	// expire the API calls (the resource-group lookup in particular). Each
	// network call is bounded individually instead: SDK calls via apiCtx,
	// raw-REST calls by the ibm http client's own per-request 60s timeout.
	//
	// Derived from the command's context, not Background: WithCancel adds no
	// deadline, so the interview keeps its unbounded budget while Ctrl-C still
	// reaches every call made under it.
	ctx, cancel := context.WithCancel(cmdContext(cmd))
	defer cancel()

	fmt.Fprintln(os.Stderr, "\n→ Verifying IBM Cloud credentials...")
	ic, err := ibm.New(apiKey, region)
	if err != nil {
		return err
	}
	vctx, vcancel := apiCtx(ctx)
	id, err := ic.Verify(vctx)
	vcancel()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ %s\n\n", id)

	// Persist a PARTIAL workspace now (region + a resolvable API key), BEFORE the
	// interview's fallible steps (cluster listing, the FAR/COS check). Historically
	// the only save was at the very end, so any earlier failure left NO workspace
	// and manual commands like `roksbnkctl cos` had nothing to resolve. With this,
	// a failed init still leaves a usable workspace, and re-running `init` completes
	// it. Skipped on a re-init (the workspace already exists). Best-effort.
	if cctx.Workspace == nil {
		partial := &config.Workspace{IBMCloud: config.IBMCloudCfg{Region: region}}
		if serr := config.SaveWorkspace(cctx.WorkspaceName, partial); serr == nil {
			persistAPIKey(cctx.WorkspaceName, apiKey)
			fmt.Fprintf(os.Stderr, "✓ Partial workspace saved (%q) — completes on a successful init\n\n", cctx.WorkspaceName)
		} else {
			fmt.Fprintf(os.Stderr, "warning: could not pre-save workspace: %v\n\n", serr)
		}
	}

	// From here on the workspace is persisted, so Ctrl-C exits cleanly and leaves it
	// to finish later — rather than dropping the operator into a half-answered
	// interview or a defaulted config. (A dedicated handler, so it fires only on a
	// real SIGINT, not the root context's normal end-of-run cancel.)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "\n\n^C interrupted — workspace %q is saved. Re-run `roksbnkctl init -w %s` to finish it.\n",
			cctx.WorkspaceName, cctx.WorkspaceName)
		os.Exit(130)
	}()

	// The account-aware interview: create-vs-reuse, region + existing-cluster
	// menus pulled from the credentials, resource toggles, and the optional
	// testing client (with its own region).
	choices, ierr := runAccountInterview(ctx, ic, cctx, region, dOCP, dWorkers, dCreate)
	if ierr != nil {
		return ierr
	}
	region = choices.Region
	cluster := choices.Cluster
	prefix := choices.Prefix
	resources := choices.Resources

	// Resource group (global; region-independent): prompt after the
	// cluster/region choice.
	rgName := promptString("Resource group", dRG)
	rgCtx, rgCancel := apiCtx(ctx)
	rgID, err := ic.ResolveResourceGroup(rgCtx, rgName)
	rgCancel()
	if err != nil {
		return fmt.Errorf("verifying resource group: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ Resource group %q (id %s)\n\n", rgName, rgID)

	// Resolve the testing jumphost SSH key: use an existing IBM Cloud key, or
	// generate + store + upload one (in every region a jumphost uses).
	if err := resolveTestingSSHKey(ctx, ic, cctx.WorkspaceName, region, resources, rgID); err != nil {
		return err
	}

	tfCfg, err := promptTFSource(ctx, cctx)
	if err != nil {
		return err
	}

	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{
			Region:        region,
			ResourceGroup: rgName,
		},
		Cluster:   cluster,
		Prefix:    prefix,
		Resources: resources,
		TFSource:  tfCfg,
	}

	// BNK supply-chain knobs (Sprint 29): the manifest version + the FAR auth
	// tarball name. Asked only when BNK is being deployed; seeded with the
	// terraform-default values and stored in config.yaml so `registry` and the
	// BNK phase run from the workspace without flags.
	if resources != nil && resources.BNK.Create {
		ws.BNK.ManifestVersion = promptString("BNK manifest version", config.DefaultManifestVersion)
		ws.BNK.FarAuthFile = promptString("FAR auth file (in the orchestration COS bucket)", config.DefaultFARAuthFile)
		ws.BNK.SubscriptionJWTFile = promptString("Subscription JWT file (in the orchestration COS bucket)", config.DefaultSubscriptionJWTFile)
		// Verify the orchestration COS actually holds those artefacts; offer to
		// provision the instance/bucket + upload from local files when it doesn't,
		// so the BNK phase has what it needs. Interactive path only.
		if err := ensureFARSupplyChain(ctx, ic, apiKey, rgID, ws); err != nil {
			return err
		}
		// Optional F5 License Proxy. Default no → BNK licenses with the
		// subscription JWT as before (unchanged). Yes → set FLP mode + an flp
		// block; the operator then runs `roksbnkctl flp up` before `bnk up`.
		if promptYesNo("License via an F5 License Proxy (FLP)?", false) {
			ws.BNK.LicenseMode = "f5licenseproxy"
			if promptYesNo("Deploy the FLP as a standalone VSI appliance? (No = in-cluster helm chart)", false) {
				// ── Standalone VSI appliance: region, VPC (pick/create), SSH key. ──
				flpRegion := promptString("FLP VSI region", region)
				vpcID := pickOrCreateFLPVPC(ctx, ic, flpRegion, rgID)
				zone := promptString("FLP VSI zone", flpRegion+"-1")
				sshKey := resolveFLPVSISSHKey(ctx, ic, cctx.WorkspaceName, flpRegion, rgID)
				fip := promptYesNo("Attach a floating IP for remote `flp status` + web-UI access?", true)
				ws.BNK.FLP = &config.BNKFLPCfg{
					Mode: "vsi",
					VSI:  &config.BNKFLPVSICfg{VPC: vpcID, Zone: zone, SSHKey: sshKey, FloatingIP: &fip},
				}
			} else {
				// ── In-cluster helm chart. ──
				ns := promptString("FLP namespace", config.DefaultFLPNamespace)
				ws.BNK.FLP = &config.BNKFLPCfg{Namespace: ns}
				// Onto THIS workspace's cluster, or a different running ROKS cluster?
				if !promptYesNo("Install onto THIS workspace's cluster?", true) {
					pickRunningClusterForFLP(ctx, ic, cctx, ws)
				}
			}
		}

		// Optional per-zone data-plane networking. Default no → the module's
		// install-guide defaults apply (bnk.network stays unset). Yes → interview the
		// six subnet CIDRs + TMM self-IPs per AZ, seeded from the saved config
		// (re-init) or the guide defaults, so accepting every prompt reproduces the
		// default layout and the operator changes only what their fabric requires.
		if promptYesNo("Customize BNK networking (per-zone subnets + TMM self-IPs)?", false) {
			var prior *config.BNKNetworkCfg
			if cctx.Workspace != nil {
				prior = cctx.Workspace.BNK.Network
			}
			ws.BNK.Network = promptBNKNetwork(prior)
		}
	}

	// --override-from-env (Sprint 30 Issue 4) on the interactive path too:
	// overlay env vars onto the interview-built config before persisting.
	if flagInitOverrideFromEnv {
		if applied := config.OverrideFromEnv(ws); len(applied) > 0 {
			fmt.Fprintf(os.Stderr, "✓ Applied %d override(s) from environment: %s\n", len(applied), strings.Join(applied, ", "))
		}
	}

	// Show the resolved name plan so the operator sees exactly what
	// roksbnkctl will ask IBM Cloud to create (or reuse).
	printNamePlan(os.Stderr, ws)
	if err := config.SaveWorkspace(cctx.WorkspaceName, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	cfgPath, _ := config.WorkspaceConfigPath(cctx.WorkspaceName)
	fmt.Fprintf(os.Stderr, "\n✓ Wrote %s\n", cfgPath)

	// Persist the API key for future runs (idempotent — no-op if the partial-save
	// path above already stored it, or it's in env/keychain).
	persistAPIKey(cctx.WorkspaceName, apiKey)

	// Initialising a workspace selects it — the user's environment follows the
	// freshly-configured workspace without a separate `ws use`.
	if err := config.SetCurrent(cctx.WorkspaceName); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not set current workspace: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "✓ Current workspace: %s\n", cctx.WorkspaceName)
	}

	fmt.Fprintln(os.Stderr, "\nNext: roksbnkctl up")
	return nil
}

// promptBNKNetwork interviews the per-zone BNK data-plane networking: the external
// and internal VLAN subnet CIDRs, the internal SNAT and VIP CIDRs, and the external
// and internal TMM self-IPs. These render as cneinstance_network_zones and drive the
// cloud-network-mapping ConfigMap plus the external/internal F5SPKVlan CRs' selfip_v4s.
// Each field is seeded from the saved config (re-init) or the install-guide default,
// so accepting every prompt reproduces the guide layout for that AZ.
func promptBNKNetwork(prior *config.BNKNetworkCfg) *config.BNKNetworkCfg {
	seed := config.DefaultBNKNetworkZones
	if prior != nil && len(prior.Zones) == len(seed) {
		seed = prior.Zones
	}

	// ZONES FIRST. The self-IP prefix length used to be asked BEFORE these, with
	// the instruction "match your VLAN CIDRs" — CIDRs the operator had not been
	// shown yet. The default 24 was offered, accepted, and then contradicted by
	// the subnets entered seconds later, with nothing reconciling the two. That
	// produced /24 self-IPs on /23 VLANs, which surfaces as unreachable
	// neighbours rather than as a configuration error.
	zones := make([]config.BNKZoneCfg, len(seed))
	for i, d := range seed {
		fmt.Fprintf(os.Stderr, "\n  Availability zone %d of %d:\n", i+1, len(seed))
		zones[i] = config.BNKZoneCfg{
			ExtVLANCIDR:    promptString(fmt.Sprintf("  zone %d external VLAN subnet CIDR", i+1), d.ExtVLANCIDR),
			IntVLANCIDR:    promptString(fmt.Sprintf("  zone %d internal VLAN subnet CIDR", i+1), d.IntVLANCIDR),
			IntSNATCIDR:    promptString(fmt.Sprintf("  zone %d internal SNAT CIDR", i+1), d.IntSNATCIDR),
			IntVIPCIDR:     promptString(fmt.Sprintf("  zone %d internal VIP CIDR", i+1), d.IntVIPCIDR),
			ExternalSelfIP: promptString(fmt.Sprintf("  zone %d external TMM self-IP", i+1), d.ExternalSelfIP),
			InternalSelfIP: promptString(fmt.Sprintf("  zone %d internal TMM self-IP", i+1), d.InternalSelfIP),
		}
	}

	// Network-wide TMM knobs, now asked with the zones on screen.
	prefixDef, routesDef := config.DefaultVLANPrefixLen, config.DefaultTMMK8SRoutes
	if prior != nil {
		if prior.VLANPrefixLen != nil {
			prefixDef = *prior.VLANPrefixLen
		}
		if prior.TMMK8SRoutes != "" {
			routesDef = prior.TMMK8SRoutes
		}
	}
	// A SUGGESTION, not a derivation. Offered only when every VLAN CIDR just
	// entered agrees on one mask — if they disagree there is no single right
	// answer, and inventing one would be worse than the stale default. A saved
	// value from a previous run always wins: it was chosen deliberately, and
	// overriding it here would undo that on every re-init.
	//
	// The operator can still type anything. A prefix that deliberately disagrees
	// with the subnet is a legitimate tool — it changes what TMM treats as
	// directly connected, and static routes then steer the remainder to force a
	// specific traffic pattern. Nothing here validates the two against each
	// other, by design.
	// Suggest whenever the CIDRs agree — including on re-init.
	//
	// This used to be gated on `prior.VLANPrefixLen == nil`, which never held
	// after the first interview (the constructor below always writes a value), so
	// the suggestion was suppressed in exactly the case that matters: an operator
	// re-running init to change /24 subnets to /23 was offered the saved 24 under
	// a label claiming it came from the CIDRs above. That is #67 again, from the
	// other direction.
	//
	// A saved value still wins when the CIDRs do NOT agree — there is no single
	// right suggestion then, and the previous deliberate choice is better than a
	// constant.
	label := "  self-IP prefix length (F5SPKVlan spec.prefixlen_v4; override to force a routed pattern)"
	if n, ok := commonVLANPrefixLen(zones); ok {
		prefixDef = n
		label = "  self-IP prefix length (F5SPKVlan spec.prefixlen_v4; suggested from the VLAN CIDRs above — override to force a routed pattern)"
	}
	prefixLen := promptInt(label, prefixDef)
	tmmRoutes := promptString("  Kubernetes pod CIDR TMM routes to (TMM_K8S_ROUTES; the cluster's pod subnet)", routesDef)

	// Carry the per-VLAN overrides through. They are not prompted for — they are
	// the unusual case — but a re-init that silently dropped them would revert
	// the external or internal self-IP mask on a live cluster, which is the exact
	// failure this batch exists to prevent.
	cfg := &config.BNKNetworkCfg{Zones: zones, VLANPrefixLen: &prefixLen, TMMK8SRoutes: tmmRoutes}
	if prior != nil {
		cfg.VLANPrefixLenExternal = prior.VLANPrefixLenExternal
		cfg.VLANPrefixLenInternal = prior.VLANPrefixLenInternal
	}
	return cfg
}

// commonVLANPrefixLen returns the mask shared by every external and internal
// VLAN CIDR across the zones, and whether one exists.
//
// Only the VLAN CIDRs are considered: the self-IPs sit on those, not on the SNAT
// or VIP ranges, which are routed and legitimately sized differently.
//
// Returns false on any disagreement or unparseable entry, so the caller falls
// back rather than suggesting a mask that is right for only some of the zones.
// This is a prompt default and nothing more — the value it seeds stays freely
// overridable, and no code path derives the mask from the CIDRs.
func commonVLANPrefixLen(zones []config.BNKZoneCfg) (int, bool) {
	found := -1
	for _, z := range zones {
		for _, c := range []string{z.ExtVLANCIDR, z.IntVLANCIDR} {
			_, ipnet, err := net.ParseCIDR(strings.TrimSpace(c))
			if err != nil || ipnet == nil {
				return 0, false
			}
			ones, bits := ipnet.Mask.Size()
			if bits != 32 {
				return 0, false // IPv4 only; prefixlen_v4 has no meaning otherwise
			}
			if found == -1 {
				found = ones
			} else if found != ones {
				return 0, false // zones disagree — no single right suggestion
			}
		}
	}
	if found == -1 {
		return 0, false
	}
	return found, true
}

// persistAPIKey saves the API key to the workspace keychain (or the config.yaml
// fallback) when it isn't already resolvable from the environment or keychain.
// Best-effort and idempotent, so it is safe to call early — right after the
// partial-workspace save, so `roksbnkctl cos` on a half-finished workspace can
// authenticate — and again at the end of init.
func persistAPIKey(workspace, apiKey string) {
	if envHasAPIKey() || config.APIKeyInKeychain(workspace) {
		return
	}
	dest, perr := config.SaveAPIKeyForWorkspace(workspace, apiKey)
	if perr == nil {
		fmt.Fprintf(os.Stderr, "✓ API key persisted in %s\n", dest)
	} else {
		fmt.Fprintf(os.Stderr, "warning: could not persist API key: %v\n", perr)
		fmt.Fprintln(os.Stderr, "  set IBMCLOUD_API_KEY in a .env file or shell to skip the prompt next run")
	}
}

// allCreateResources returns the toggle set printNamePlan assumes for a
// workspace with no `resources:` block. It defers to config.DefaultResources so
// the NAMES printed match what would actually be built — it previously inlined
// an all-true set and would have listed a TGW jumphost and client VPC for a
// workspace that no longer creates either.
func allCreateResources() *config.ResourcesCfg {
	return config.DefaultResources()
}

// accountInterview is the result of the interactive, account-aware init
// interview: the chosen region, the cluster (created or reused), the validated
// prefix, and the populated resources block (which carries the testing
// client's region when one was added).
type accountInterview struct {
	Region    string
	Cluster   config.ClusterCfg
	Prefix    string
	Resources *config.ResourcesCfg
}

// runAccountInterview drives the post-credential interview. It first asks
// whether to create a new ROKS cluster:
//
//   - create: pick a region from the account's available regions, choose a
//     prefix, OpenShift version, and workers-per-zone (floored at 1 — a ROKS
//     cluster spans all 3 AZs, so the minimum is 3 workers total).
//   - reuse: pick from the account's running OpenShift clusters; the region and
//     name come from the chosen cluster, and cluster-outputs.json is populated
//     so the BNK/Testing phases reuse it.
//
// Then the resource toggles, and an optional testing client whose region is
// chosen from the same region list. ic is used only for the (TTY-only) region
// and cluster menus, so a non-interactive run never dials the network here.
func runAccountInterview(ctx context.Context, ic *ibm.Client, cctx *config.Context, dRegion, dOCP string, dWorkers int, dCreate bool) (*accountInterview, error) {
	res := &config.ResourcesCfg{}
	out := &accountInterview{Resources: res, Region: dRegion}

	if promptYesNo("Create a new ROKS cluster?", dCreate) {
		out.Region = pickRegion(ctx, ic, "Region for the new cluster", dRegion)

		prefix, err := promptPrefix(cctx)
		if err != nil {
			return nil, err
		}
		out.Prefix = prefix

		// A ROKS cluster spans all three AZs, so the minimum is one worker per
		// zone (3 total). Clamp anything lower.
		workers := promptInt("Workers per zone (min 1; cluster spans all 3 AZs)", dWorkers)
		if workers < 1 {
			fmt.Fprintln(os.Stderr, "  (minimum is 1 worker per zone — a ROKS cluster spans 3 AZs, so 3 workers total)")
			workers = 1
		}
		// Public gateways = worker Internet egress. Default yes (current behavior).
		// No → a private, disconnected cluster: no egress, so the operator must supply
		// private connectivity (VPEs / private service endpoints) for image pulls and
		// IBM Cloud services. Recorded explicitly so the choice is visible in config.yaml.
		pubGW := promptYesNo("Attach public gateways for worker Internet egress? (No = private/disconnected cluster)", true)
		if !pubGW {
			fmt.Fprintln(os.Stderr, "  ⚠ private cluster: no worker egress — you must provide private connectivity (VPEs / private")
			fmt.Fprintln(os.Stderr, "    service endpoints) for image pulls + IBM Cloud services, and mirror BNK into a registry")
			fmt.Fprintln(os.Stderr, "    the cluster can reach privately (roksbnkctl registry replicate).")
		}
		out.Cluster = config.ClusterCfg{
			Create:           true,
			Name:             naming.Derive(prefix).ClusterName,
			OpenShiftVersion: promptString("OpenShift version", dOCP),
			WorkersPerZone:   workers,
			PublicGateway:    &pubGW,
		}
		res.RegistryCOS.Create = promptYesNo("Create registry COS instance?", true)
		if !res.RegistryCOS.Create {
			res.RegistryCOS.Existing = promptString("Existing COS instance name", "")
		}

		// Cluster VPC. Declining discovers the region's existing VPCs to build the
		// new cluster into — so multiple clusters (workspaces) can share one VPC.
		// The recorded value is the VPC id (ResourcesCfg.ClusterVPC.Existing), which
		// renders as use_existing_cluster_vpc + existing_cluster_vpc_id. A new cluster
		// must have a VPC, so if none is selected we fall back to creating one.
		vpcQuotaHint(ctx, ic, out.Region)
		res.ClusterVPC.Create = promptYesNo("Create a new cluster VPC?", true)
		if !res.ClusterVPC.Create {
			res.ClusterVPC.Existing = pickExistingVPC(ctx, ic, out.Region, "Use an existing cluster VPC")
			if res.ClusterVPC.Existing == "" {
				fmt.Fprintln(os.Stderr, "  (no VPC selected — a new cluster needs a VPC, so one will be created)")
				res.ClusterVPC.Create = true
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "\n→ Listing running OpenShift clusters...")
		clusters, err := ic.ListClusters(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing clusters: %w", err)
		}
		if len(clusters) == 0 {
			return nil, errors.New("no running OpenShift (ROKS) clusters found in this account — create one instead, or check that your API key has access")
		}
		labels := make([]string, len(clusters))
		for i, cl := range clusters {
			labels[i] = fmt.Sprintf("%s  (%s, %s)", cl.Name, cl.Region, cl.MasterKubeVersion)
		}
		chosen := clusters[promptSelect("Choose an existing cluster", labels, 0)]
		out.Region = chosen.Region
		out.Cluster = config.ClusterCfg{Create: false, Name: chosen.Name}

		prefix, err := prefixLoop(naming.SanitizeToPrefix(chosen.Name))
		if err != nil {
			return nil, err
		}
		out.Prefix = prefix
		res.RegistryCOS.Create = false

		// Record the cluster so BNK/Testing reuse it (mirrors `cluster register`).
		if err := registerReusedCluster(ctx, ic, cctx.WorkspaceName, chosen.Name); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not record cluster details (%v)\n  run `roksbnkctl cluster register %s` before `roksbnkctl up`\n", err, chosen.Name)
		}
	}

	// Transit gateway. When declined, capture an EXISTING gateway to attach the
	// cluster to — by name or id — so multiple clusters can share one gateway.
	// Blank is fine: the cluster is left unattached and can be connected later
	// with `roksbnkctl tgw connect <name-or-id>`.
	tgwQuotaHint(ctx, ic)
	res.TransitGateway.Create = promptYesNo("Create Transit Gateway?", true)
	if !res.TransitGateway.Create {
		res.TransitGateway.Existing = pickExistingTransitGateway(ctx, ic, "Attach an existing Transit Gateway")
	}

	// In-cluster services.
	res.CertManager.Create = promptYesNo("Install cert-manager?", true)
	res.BNK.Create = promptYesNo("Deploy BIG-IP Next (BNK)?", true)

	// Optional testing client (TGW jumphost + client VPC), in a region of its
	// own — prompted again from the account's region list.
	if promptYesNo("Add a testing client?", false) {
		res.TGWJumphost.Create = true
		res.ClientRegion = pickRegion(ctx, ic, "Region to install the test client in", out.Region)
		res.ClientVPC.Create = promptYesNo("Create a new client VPC for it?", true)
		if !res.ClientVPC.Create {
			res.ClientVPC.Existing = promptString("Existing client VPC name", "")
		}
		// The TGW jumphost needs a gateway to reach the cluster across. If the
		// operator declined to create one and didn't already name an existing one
		// above, ask now — the jumphost can't work without it.
		if !res.TransitGateway.Create && res.TransitGateway.Existing == "" {
			res.TransitGateway.Existing = pickExistingTransitGateway(ctx, ic, "Attach an existing Transit Gateway (required for the testing jumphost)")
		}
	} else {
		res.TGWJumphost.Create = false
		res.ClientVPC.Create = false
	}

	// Per-zone cluster jumphosts (default off).
	res.ClusterJumphosts.Create = promptYesNo("Create per-zone cluster jumphosts?", false)

	// SSH key for the jumphosts — only when a jumphost is enabled. The name is
	// captured here; the check/generate/upload happens once the resource group
	// is resolved (resolveTestingSSHKey).
	if res.TGWJumphost.Create || res.ClusterJumphosts.Create {
		res.TestingSSHKeyName = promptString("SSH key name for the testing jumphosts", out.Prefix+"-jumphost")
	}

	return out, nil
}

// resolveTestingSSHKey resolves the IBM Cloud VPC SSH key for the testing
// jumphosts (res.TestingSSHKeyName). An existing key is reused (and replicated
// into any region a jumphost uses but where it's missing); otherwise roksbnkctl
// generates an ed25519 keypair, stores the private key in the workspace ssh/ dir
// (offering to copy it into ~/.ssh), and uploads the public key. No-op when no
// jumphost is enabled or no key name was given.
func resolveTestingSSHKey(ctx context.Context, ic *ibm.Client, workspace, clusterRegion string, res *config.ResourcesCfg, rgID string) error {
	if res == nil || res.TestingSSHKeyName == "" {
		return nil
	}
	keyName := res.TestingSSHKeyName

	// Regions a jumphost lives in — the key must resolve by name in each.
	var regions []string
	seen := map[string]bool{}
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			regions = append(regions, r)
		}
	}
	if res.ClusterJumphosts.Create {
		add(clusterRegion)
	}
	if res.TGWJumphost.Create {
		cr := res.ClientRegion
		if cr == "" {
			cr = clusterRegion
		}
		add(cr)
	}
	if len(regions) == 0 {
		return nil
	}

	// Existence per region (one spinner for the batch of checks).
	var existingPub string
	var missing []string
	if err := spin(fmt.Sprintf("Checking IBM Cloud for SSH key %q", keyName), func() error {
		for _, r := range regions {
			k, e := ic.GetSSHKeyByName(ctx, r, keyName)
			if e != nil {
				return fmt.Errorf("in %s: %w", r, e)
			}
			if k != nil && k.PublicKey != "" {
				existingPub = k.PublicKey
			} else {
				missing = append(missing, r)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("checking SSH key %q: %w", keyName, err)
	}

	// Reuse an existing key, replicating into any missing region.
	if existingPub != "" {
		for _, r := range missing {
			r := r
			if err := spin(fmt.Sprintf("Replicating SSH key %q into %s", keyName, r), func() error {
				_, e := ic.CreateSSHKey(ctx, r, keyName, existingPub, rgID)
				return e
			}); err != nil {
				return fmt.Errorf("replicating SSH key %q to %s: %w", keyName, r, err)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "✓ Using existing SSH key %q (replicated into %s)\n", keyName, strings.Join(missing, ", "))
		} else {
			fmt.Fprintf(os.Stderr, "✓ Using existing SSH key %q\n", keyName)
		}
		return nil
	}

	// Absent everywhere — generate, store, upload.
	pub, priv, err := sshkey.Generate()
	if err != nil {
		return err
	}
	sshDir, err := config.WorkspaceSSHDir(workspace)
	if err != nil {
		return err
	}
	privPath, err := sshkey.Write(sshDir, keyName, priv, pub)
	if err != nil {
		return fmt.Errorf("storing SSH key: %w", err)
	}
	for _, r := range regions {
		r := r
		if err := spin(fmt.Sprintf("Uploading the public key to %s", r), func() error {
			_, e := ic.CreateSSHKey(ctx, r, keyName, pub, rgID)
			return e
		}); err != nil {
			return fmt.Errorf("uploading SSH key to %s: %w", r, err)
		}
	}
	fmt.Fprintf(os.Stderr, "✓ Generated SSH key %q → %s (uploaded to %s)\n", keyName, privPath, strings.Join(regions, ", "))

	if promptYesNo(fmt.Sprintf("Copy the private key to ~/.ssh/%s?", keyName), true) {
		created, err := copyKeyToUserSSH(sshDir, keyName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not copy to ~/.ssh (%v)\n", err)
		}
		// Record only the files we actually wrote, so `ws delete` can remove
		// them without ever touching a pre-existing ~/.ssh file the user owned.
		res.CopiedSSHKeyFiles = created
	}
	return nil
}

// copyKeyToUserSSH copies <name>{,.pub} from srcDir into ~/.ssh, never
// overwriting an existing file. Returns the basenames it ACTUALLY wrote (a
// pre-existing file is skipped and not included) so the caller can record
// exactly what to clean up at `ws delete` time.
func copyKeyToUserSSH(srcDir, name string) (created []string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dstDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return nil, err
	}
	for _, f := range []struct {
		name string
		perm os.FileMode
	}{{name, 0o600}, {name + ".pub", 0o644}} {
		dst := filepath.Join(dstDir, f.name)
		if _, err := os.Stat(dst); err == nil {
			fmt.Fprintf(os.Stderr, "  ~/.ssh/%s already exists — not overwriting\n", f.name)
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, f.name))
		if err != nil {
			return created, err
		}
		if err := os.WriteFile(dst, data, f.perm); err != nil {
			return created, err
		}
		created = append(created, f.name)
	}
	if len(created) > 0 {
		fmt.Fprintf(os.Stderr, "  ✓ copied to ~/.ssh/%s\n", name)
	}
	return created, nil
}

// pickRegion shows the account's available regions as a menu and returns the
// chosen name, defaulting to def. TTY-only: a non-interactive run returns def
// without dialing the API (keeps init scriptable). On a list error it falls
// back to the built-in region list so init never hard-fails offline.
// pickExistingTransitGateway discovers the account's transit gateways and lets the
// operator choose one to attach the cluster to (returns its name; "" = attach later
// with `tgw connect`). Falls back to a free-text name/ID prompt when discovery
// fails; reports (and skips) when the account has none.
func pickExistingTransitGateway(ctx context.Context, ic *ibm.Client, label string) string {
	lctx, cancel := apiCtx(ctx)
	tgws, err := ic.ListTransitGateways(lctx)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (could not list transit gateways: %v)\n", err)
		return promptString(label+" — name or ID (blank = attach later)", "")
	}
	if len(tgws) == 0 {
		fmt.Fprintln(os.Stderr, "  (no existing transit gateways in this account — attach one later with `tgw connect`)")
		return ""
	}
	fmt.Fprintln(os.Stderr, "  Existing transit gateways in this account:")
	for i, g := range tgws {
		fmt.Fprintf(os.Stderr, "    %2d) %-28s (%s, %s)\n", i+1, g.Name, g.Location, g.Status)
	}
	choice := promptInt(label+" — pick a number (0 = none / attach later)", 0)
	if choice <= 0 || choice > len(tgws) {
		return ""
	}
	return tgws[choice-1].Name
}

// pickExistingVPC discovers the VPCs in a region and lets the operator choose one
// for the new cluster to build into — enabling multiple clusters (workspaces) to
// share one VPC. Mirrors pickExistingTransitGateway, with two differences: VPCs
// are regional (so it takes a region), and it returns the VPC *ID* (not name),
// because ResourcesCfg.ClusterVPC.Existing is rendered as existing_cluster_vpc_id.
// Returns "" for none/skip. Falls back to a free-text VPC-id prompt when discovery
// fails; reports (and skips) when the region has none.
// pickOrCreateFLPVPC lets the operator pick an existing VPC in region for the
// standalone FLP appliance, or create a new one. Returns the VPC id (empty on
// error/none). A freshly created VPC still needs a Transit Gateway attachment to
// be reachable from a cluster in another VPC — noted to the operator.
func pickOrCreateFLPVPC(ctx context.Context, ic *ibm.Client, region, rgID string) string {
	lctx, cancel := apiCtx(ctx)
	vpcs, err := ic.ListVPCs(lctx, region)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (could not list VPCs: %v)\n", err)
		return promptString("FLP VPC id", "")
	}
	labels := make([]string, 0, len(vpcs)+1)
	for _, v := range vpcs {
		labels = append(labels, fmt.Sprintf("%s (%s)", v.Name, v.Status))
	}
	labels = append(labels, "＋ create a new VPC")
	sel := promptSelect(fmt.Sprintf("VPC for the FLP appliance in %s", region), labels, 0)
	if sel >= 0 && sel < len(vpcs) {
		return vpcs[sel].ID
	}
	name := promptString("New VPC name", "bnk-svc-vpc")
	var created *ibm.VPC
	if err := spin(fmt.Sprintf("Creating VPC %q in %s", name, region), func() error {
		var e error
		created, e = ic.CreateVPC(ctx, region, name, rgID)
		return e
	}); err != nil || created == nil {
		fmt.Fprintf(os.Stderr, "  could not create VPC: %v\n", err)
		return ""
	}
	fmt.Fprintf(os.Stderr, "✓ Created VPC %q (%s). Attach it to your Transit Gateway before the cluster can reach the FLP: `roksbnkctl tgw connect`.\n", created.Name, created.ID)
	return created.ID
}

// resolveFLPVSISSHKey resolves a VPC SSH key for the standalone FLP VSI in region
// (reuse if it already exists, else generate + upload), so an operator can SSH
// into the licensing appliance to inspect Vault/podman/logs or recover it.
// Returns the key name ("" = no key attached).
func resolveFLPVSISSHKey(ctx context.Context, ic *ibm.Client, workspace, region, rgID string) string {
	keyName := promptString("SSH key for the FLP VSI (existing name; blank = none)", workspace+"-flp")
	if keyName == "" {
		return ""
	}
	kctx, cancel := apiCtx(ctx)
	existing, _ := ic.GetSSHKeyByName(kctx, region, keyName)
	cancel()
	if existing != nil && existing.PublicKey != "" {
		fmt.Fprintf(os.Stderr, "✓ Using existing SSH key %q in %s\n", keyName, region)
		return keyName
	}
	if !promptYesNo(fmt.Sprintf("SSH key %q not found in %s — generate + upload it now?", keyName, region), true) {
		return ""
	}
	pub, priv, err := sshkey.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  could not generate key: %v\n", err)
		return ""
	}
	sshDir, err := config.WorkspaceSSHDir(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		return ""
	}
	privPath, err := sshkey.Write(sshDir, keyName, priv, pub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  storing SSH key: %v\n", err)
		return ""
	}
	if err := spin(fmt.Sprintf("Uploading the public key to %s", region), func() error {
		_, e := ic.CreateSSHKey(ctx, region, keyName, pub, rgID)
		return e
	}); err != nil {
		fmt.Fprintf(os.Stderr, "  uploading SSH key: %v\n", err)
		return ""
	}
	fmt.Fprintf(os.Stderr, "✓ Generated SSH key %q → %s (uploaded to %s)\n", keyName, privPath, region)
	if promptYesNo(fmt.Sprintf("Copy the private key to ~/.ssh/%s?", keyName), true) {
		if _, err := copyKeyToUserSSH(sshDir, keyName); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not copy to ~/.ssh (%v)\n", err)
		}
	}
	return keyName
}

// pickRunningClusterForFLP points the workspace at an existing running ROKS
// cluster for an in-cluster FLP install (instead of this workspace's own
// cluster): the operator picks from the account's running clusters and the
// workspace adopts it (create=false + register), mirroring `cluster register`.
func pickRunningClusterForFLP(ctx context.Context, ic *ibm.Client, cctx *config.Context, ws *config.Workspace) {
	fmt.Fprintln(os.Stderr, "\n→ Listing running OpenShift clusters...")
	lctx, cancel := apiCtx(ctx)
	clusters, err := ic.ListClusters(lctx)
	cancel()
	if err != nil || len(clusters) == 0 {
		fmt.Fprintln(os.Stderr, "  (no running clusters found — the FLP will target this workspace's cluster)")
		return
	}
	labels := make([]string, len(clusters))
	for i, cl := range clusters {
		labels[i] = fmt.Sprintf("%s  (%s, %s)", cl.Name, cl.Region, cl.State)
	}
	chosen := clusters[promptSelect("Choose a running ROKS cluster to license", labels, 0)]
	ws.Cluster.Create = false
	ws.Cluster.Name = chosen.Name
	if err := registerReusedCluster(ctx, ic, cctx.WorkspaceName, chosen.Name); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not record cluster (%v) — run `roksbnkctl cluster register %s`\n", err, chosen.Name)
	} else {
		fmt.Fprintf(os.Stderr, "✓ FLP will license adopted cluster %q\n", chosen.Name)
	}
}

func pickExistingVPC(ctx context.Context, ic *ibm.Client, region, label string) string {
	lctx, cancel := apiCtx(ctx)
	vpcs, err := ic.ListVPCs(lctx, region)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (could not list VPCs: %v)\n", err)
		return promptString(label+" — VPC id (blank = create a new one)", "")
	}
	if len(vpcs) == 0 {
		fmt.Fprintf(os.Stderr, "  (no existing VPCs in %s — a new one will be created)\n", region)
		return ""
	}
	fmt.Fprintf(os.Stderr, "  Existing VPCs in %s:\n", region)
	for i, v := range vpcs {
		fmt.Fprintf(os.Stderr, "    %2d) %-28s (%s)\n", i+1, v.Name, v.Status)
	}
	choice := promptInt(label+" — pick a number (0 = none / create a new one)", 0)
	if choice <= 0 || choice > len(vpcs) {
		return ""
	}
	return vpcs[choice-1].ID
}

// vpcQuotaHint / tgwQuotaHint print current usage vs the default IBM quota just
// before the create-VPC / create-TGW prompts, so the operator can choose "adopt
// existing" BEFORE a full region/account fails the apply ~40 minutes into a cluster
// build. Best-effort and silent on error (the interview already dials the API for
// its pickers, so the extra call is cheap and TTY-only).
func vpcQuotaHint(ctx context.Context, ic *ibm.Client, region string) {
	if ic == nil || region == "" || !isTTY() {
		return
	}
	lctx, cancel := apiCtx(ctx)
	vpcs, err := ic.ListVPCs(lctx, region)
	cancel()
	if err != nil {
		return
	}
	if n := len(vpcs); n >= ibm.VPCQuotaPerRegion {
		fmt.Fprintf(os.Stderr, "  VPCs in %s: %d/%d -- at the default limit; a new one will FAIL. Answer 'n' to adopt an existing VPC.\n", region, n, ibm.VPCQuotaPerRegion)
	} else {
		fmt.Fprintf(os.Stderr, "  VPCs in %s: %d/%d\n", region, n, ibm.VPCQuotaPerRegion)
	}
}

func tgwQuotaHint(ctx context.Context, ic *ibm.Client) {
	if ic == nil || !isTTY() {
		return
	}
	lctx, cancel := apiCtx(ctx)
	tgws, err := ic.ListTransitGateways(lctx)
	cancel()
	if err != nil {
		return
	}
	if n := len(tgws); n >= ibm.TGWQuotaPerAccount {
		fmt.Fprintf(os.Stderr, "  Transit Gateways: %d/%d (account-wide) -- at the default limit; a new one will FAIL. Answer 'n' to attach an existing gateway.\n", n, ibm.TGWQuotaPerAccount)
	} else {
		fmt.Fprintf(os.Stderr, "  Transit Gateways: %d/%d (account-wide)\n", n, ibm.TGWQuotaPerAccount)
	}
}

func pickRegion(ctx context.Context, ic *ibm.Client, label, def string) string {
	if !isTTY() {
		return def
	}
	regions, err := ic.ListRegions(ctx)
	if err != nil || len(regions) == 0 {
		if err != nil {
			fmt.Fprintf(os.Stderr, "  (could not list regions: %v; using the built-in list)\n", err)
		}
		regions = ibm.CuratedRegions()
	}
	labels := make([]string, len(regions))
	defIdx := 0
	for i, r := range regions {
		labels[i] = r.Name
		if r.Name == def {
			defIdx = i
		}
	}
	return regions[promptSelect(label, labels, defIdx)].Name
}

// registerReusedCluster populates cluster-outputs.json for a reused cluster so
// the BNK/Testing phases find it (mirrors `roksbnkctl cluster register`). The
// registry COS is recorded best-effort — a hand-built cluster may not have the
// roksbnkctl-convention `<cluster>-cos` instance.
func registerReusedCluster(ctx context.Context, ic *ibm.Client, workspace, clusterArg string) error {
	info, err := ic.GetCluster(ctx, clusterArg)
	if err != nil {
		return err
	}
	vpc := info.VPCID()
	if vpc == "" {
		return fmt.Errorf("cluster %q has no VPC (vpc-gen2 only)", info.Name)
	}
	out := &config.ClusterOutputs{
		ClusterName:      info.Name,
		ClusterID:        info.ID,
		Region:           info.Region,
		ResourceGroupID:  info.ResourceGroupID,
		VPCID:            vpc,
		MasterURL:        info.MasterURL,
		OpenShiftVersion: info.MasterKubeVersion,
		Source:           "cluster-register",
	}
	if cos, err := ic.GetCOSInstanceByName(ctx, info.Name+"-cos"); err == nil {
		out.RegistryCOSCRN = cos.CRN
		out.RegistryCOSName = cos.Name
	}
	return config.WriteClusterOutputs(workspace, out)
}

// promptPrefix runs the prefix loop: default = the existing workspace's
// prefix (re-init) else SanitizeToPrefix(workspaceName); prompt →
// ValidatePrefix; on failure print the actionable message + re-prompt. In a
// non-TTY run it validates the default and hard-errors if it is invalid
// (mirroring the non-interactive cred-resolver pattern). Loops a bounded
// number of times so a pathological TTY can't spin forever.
func promptPrefix(cctx *config.Context) (string, error) {
	def := naming.SanitizeToPrefix(cctx.WorkspaceName)
	if cctx.Workspace != nil && cctx.Workspace.Prefix != "" {
		def = cctx.Workspace.Prefix
	}
	return prefixLoop(def)
}

// prefixLoop prompts for a workspace prefix with the given default, validating
// via naming.ValidatePrefix and re-prompting on failure. Non-TTY validates the
// default and hard-errors if invalid. Shared by promptPrefix (create path) and
// the reuse path (default = sanitized existing-cluster name).
func prefixLoop(def string) (string, error) {
	label := fmt.Sprintf("Workspace prefix (≤ %d chars)", naming.MaxPrefixLen())

	if !isTTY() {
		if err := naming.ValidatePrefix(def); err != nil {
			return "", fmt.Errorf("non-interactive init: default prefix %q is invalid: %w", def, err)
		}
		return def, nil
	}

	for attempt := 0; attempt < 100; attempt++ {
		candidate := promptString(label, def)
		if err := naming.ValidatePrefix(candidate); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %v\n", err)
			continue
		}
		return candidate, nil
	}
	return "", errors.New("too many invalid prefix attempts")
}

// printNamePlan writes the resolved naming.Plan to w so the operator sees
// the generated (or reused) resource names before the config is saved. A
// legacy empty-prefix workspace prints nothing (the upstream module
// defaults still apply).
func printNamePlan(w io.Writer, ws *config.Workspace) {
	if ws.Prefix == "" {
		return
	}
	plan := naming.Derive(ws.Prefix)
	res := ws.Resources
	if res == nil {
		res = allCreateResources()
	}

	fmt.Fprintf(w, "\nResolved resource names for prefix %q:\n", ws.Prefix)
	clusterName := plan.ClusterName
	if !ws.Cluster.Create {
		clusterName = ws.Cluster.Name + "  (existing)"
	}
	fmt.Fprintf(w, "  cluster                %s\n", clusterName)
	// The cluster VPC is created under the derived name by default; it is adopted
	// only when Create=false AND an id is given (the exact render condition in
	// tf/vars.go), so a zero-value toggle still means "create", not "not managed".
	clusterVPC := plan.ClusterVPCName
	if !res.ClusterVPC.Create && res.ClusterVPC.Existing != "" {
		clusterVPC = res.ClusterVPC.Existing + "  (existing)"
	}
	fmt.Fprintf(w, "  cluster VPC            %s\n", clusterVPC)
	fmt.Fprintf(w, "  registry COS instance  %s\n", planNameOrExisting(res.RegistryCOS, plan.COSInstanceName))
	fmt.Fprintf(w, "  transit gateway        %s\n", planNameOrExisting(res.TransitGateway, plan.TransitGatewayName))
	if res.TGWJumphost.Create {
		fmt.Fprintf(w, "  TGW jumphost           %s\n", plan.TGWJumphostName)
		fmt.Fprintf(w, "  client VPC             %s\n", planNameOrExisting(res.ClientVPC, plan.ClientVPCName))
	}
	if res.ClusterJumphosts.Create {
		fmt.Fprintf(w, "  cluster jumphosts      %s-<zone>\n", plan.ClusterJumphostPrefix)
	}
}

// planNameOrExisting renders the derived name when the toggle creates the
// resource, or the operator's existing name (annotated) when it reuses one.
func planNameOrExisting(t config.ResourceToggle, derived string) string {
	if t.Create {
		return derived
	}
	if t.Existing != "" {
		return t.Existing + "  (existing)"
	}
	return "(not managed)"
}

// runUpgradeTF re-resolves the TF source ref against the workspace's
// existing repo (or accepts --tf-source for a local override) and
// rewrites the workspace config. No prompts.
//
// For embedded sources there's nothing to upgrade — the TF version is
// whatever the binary ships, so update via `roksbnkctl self update` (or
// reinstall) rather than --upgrade-tf.
func runUpgradeTF(ctx context.Context, cctx *config.Context) error {
	if flagTFSource != "" {
		// Local-path override. flagTFSource is already normalized to an
		// absolute path by the single chokepoint (root PersistentPreRunE
		// → resolveInvocationContext) so the value pinned into
		// config.yaml stays stable regardless of the per-phase terraform
		// state dir CWD it's later used from (Sprint 12 Issue 2, retired
		// as a class). No per-call-site re-derivation.
		tfCfg := config.TFSourceCfg{Type: "local", Path: flagTFSource}
		return saveTFSourceUpdate(cctx, tfCfg)
	}
	switch cctx.Workspace.TFSource.Type {
	case "", "embedded":
		fmt.Fprintln(os.Stderr, "TF source is embedded — its version is tied to the roksbnkctl binary.")
		fmt.Fprintln(os.Stderr, "Update via `roksbnkctl self update` (or reinstall) to pick up newer HCL.")
		return nil
	case "github":
		repo := cctx.Workspace.TFSource.Repo
		if repo == "" {
			repo = defaultTFRepo
		}
		tfCfg, err := resolveLatestRelease(ctx, repo)
		if err != nil {
			return err
		}
		return saveTFSourceUpdate(cctx, tfCfg)
	case "local":
		fmt.Fprintln(os.Stderr, "TF source is a local path — nothing to re-resolve. Pass --tf-source <path> to change it.")
		return nil
	default:
		return fmt.Errorf("unknown TF source type %q in workspace config", cctx.Workspace.TFSource.Type)
	}
}

// saveTFSourceUpdate writes a new TF source into the workspace config,
// or no-ops if it matches what's already there. Used by --upgrade-tf.
func saveTFSourceUpdate(cctx *config.Context, tfCfg config.TFSourceCfg) error {
	if cctx.Workspace.TFSource == tfCfg {
		fmt.Fprintf(os.Stderr, "TF source already at %s\n", refDescription(tfCfg))
		return nil
	}
	prev := cctx.Workspace.TFSource
	cctx.Workspace.TFSource = tfCfg
	if err := config.SaveWorkspace(cctx.WorkspaceName, cctx.Workspace); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ TF source updated %s → %s\n", refDescription(prev), refDescription(tfCfg))
	return nil
}

// promptTFSource asks the user where Terraform should pull from. Accepts
// either a GitHub `owner/repo` slug (resolves to that repo's latest
// release) or any other input (treated as a local filesystem path).
//
// --tf-source short-circuits the prompt with a local override, matching
// the existing flag's behaviour.
//
// On re-init, the existing workspace's TF source is the default — users
// just press Enter to keep it.
//
// Default for fresh workspaces is "embedded" — the HCL bundled into the
// roksbnkctl binary. Most users want this; install one binary, get matched
// CLI + TF together with no separate fetch step.
func promptTFSource(ctx context.Context, cctx *config.Context) (config.TFSourceCfg, error) {
	if flagTFSource != "" {
		// flagTFSource is already normalized to an absolute path by the
		// single chokepoint (root PersistentPreRunE →
		// resolveInvocationContext) so the value pinned into config.yaml
		// stays stable regardless of the per-phase terraform state dir
		// CWD (Sprint 12 Issue 2). No per-call-site re-derivation.
		src := flagTFSource
		cfg := config.TFSourceCfg{Type: "local", Path: src}
		fmt.Fprintf(os.Stderr, "✓ TF source: local path %s\n", src)
		return cfg, nil
	}

	// Compute the prompt default. Existing workspace's setting wins,
	// otherwise "embedded".
	def := "embedded"
	if cctx.Workspace != nil {
		switch cctx.Workspace.TFSource.Type {
		case "github":
			if cctx.Workspace.TFSource.Repo != "" {
				def = cctx.Workspace.TFSource.Repo
			}
		case "local":
			if cctx.Workspace.TFSource.Path != "" {
				def = cctx.Workspace.TFSource.Path
			}
		}
	}

	fmt.Fprintln(os.Stderr, "\nTerraform source — leave as 'embedded' to use the HCL bundled in roksbnkctl,")
	fmt.Fprintln(os.Stderr, "or supply owner/repo for a GitHub release, or a path for a local checkout.")
	input := promptString("TF source", def)

	if input == "" || input == "embedded" {
		fmt.Fprintln(os.Stderr, "✓ TF source: embedded (bundled with roksbnkctl)")
		return config.TFSourceCfg{Type: "embedded"}, nil
	}

	if looksLikeGitHubRepo(input) {
		cfg, err := resolveLatestRelease(ctx, input)
		if err != nil {
			return config.TFSourceCfg{}, err
		}
		return cfg, nil
	}

	// Anything that's not "embedded" or GitHub-shaped is treated as a local path.
	fmt.Fprintf(os.Stderr, "✓ TF source: local path %s\n", input)
	return config.TFSourceCfg{Type: "local", Path: input}, nil
}

// resolveLatestRelease queries GitHub for the latest release of repo and
// returns a fully-formed TFSourceCfg pinned to that tag.
func resolveLatestRelease(ctx context.Context, repo string) (config.TFSourceCfg, error) {
	fmt.Fprintf(os.Stderr, "→ Resolving latest release of %s...\n", repo)
	ref, err := tf.ResolveLatestRelease(ctx, repo)
	if err != nil {
		return config.TFSourceCfg{}, fmt.Errorf("resolving TF source from GitHub: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ TF source: %s@%s\n", repo, ref)
	return config.TFSourceCfg{Type: "github", Repo: repo, Ref: ref}, nil
}

// initDefaults returns prompt defaults: existing workspace values first,
// PRD-stated defaults second. Workspace may be nil (fresh init).
func initDefaults(cctx *config.Context) (region, rg, cluster, ocp string, workers int, create bool) {
	region, rg, cluster, ocp = "ca-tor", "default", "bnk-demo", "4.20"
	workers, create = 1, true
	if cctx.Workspace == nil {
		return
	}
	if v := cctx.Workspace.IBMCloud.Region; v != "" {
		region = v
	}
	if v := cctx.Workspace.IBMCloud.ResourceGroup; v != "" {
		rg = v
	}
	if v := cctx.Workspace.Cluster.Name; v != "" {
		cluster = v
	}
	if v := cctx.Workspace.Cluster.OpenShiftVersion; v != "" {
		ocp = v
	}
	if v := cctx.Workspace.Cluster.WorkersPerZone; v != 0 {
		workers = v
	}
	create = cctx.Workspace.Cluster.Create
	return
}

// refDescription renders a TFSourceCfg for log output.
func refDescription(c config.TFSourceCfg) string {
	switch c.Type {
	case "", "embedded":
		return "embedded"
	case "github":
		return fmt.Sprintf("%s@%s", c.Repo, c.Ref)
	case "local":
		return fmt.Sprintf("local:%s", c.Path)
	default:
		return "<unknown>"
	}
}

// contextWithTimeout bounds one of init's network ops. It derives from the
// caller's context rather than Background so the bound is a ceiling, not a
// detachment: the call still times out on its own, and Ctrl-C still reaches it.
func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// apiCtx bounds a single SDK network call within the (deadline-free) interview
// context: a hung call still times out after initTimeout, but the human's
// answer time between prompts never counts against it. Raw-REST calls are
// already bounded by the ibm package's http client per-request timeout.
func apiCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, initTimeout)
}

// spin runs fn while animating a one-line spinner after "→ label" on stderr,
// leaving a "→ label... ✓" (or ✗) trace when it finishes. On a non-TTY it just
// prints the label line and runs fn (no animation). Used for the SSH-key network
// round-trips so a slow IBM Cloud call doesn't look hung.
func spin(label string, fn func() error) error {
	if !isTTY() {
		fmt.Fprintf(os.Stderr, "→ %s...\n", label)
		return fn()
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		const frames = `|/-\`
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-t.C:
				fmt.Fprintf(os.Stderr, "\r→ %s... %c", label, frames[i%len(frames)])
			}
		}
	}()
	err := fn()
	close(stop)
	<-done
	mark := "✓"
	if err != nil {
		mark = "✗"
	}
	fmt.Fprintf(os.Stderr, "\r\033[K→ %s... %s\n", label, mark)
	return err
}
