package phases

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/tags"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	lbcReleaseName   = "aws-load-balancer-controller"
	lbcChartName     = "aws-load-balancer-controller"
	lbcChartRepo     = "https://aws.github.io/eks-charts"
	lbcNamespace     = "kube-system"
	lbcIAMPolicyPath = "addons/lb-controller/iam_policy.json"

	lbcHelmInstallTimeout    = 10 * time.Minute
	lbcDeployTimeout         = 90 * time.Second
	lbcNLBDeprovisionTimeout = 4 * time.Minute
)

// lbcHelmInstaller is the testability interface for Phase 14b Helm operations.
// Production injects realLBCHelmInstaller (HTTPS pull); tests inject a fake.
// Mirrors the helmInstaller interface in phase14_flo_helm.go but uses HTTPS repo
// instead of OCI, so PullAndLoad takes repoURL+chartName instead of an OCI ref.
type lbcHelmInstaller interface {
	// List returns releases matching the filter in the given namespace.
	List(namespace, filter string) ([]*release.Release, error)
	// Install installs a new Helm release.
	Install(releaseName, namespace string, ch *chart.Chart, values map[string]interface{}) (*release.Release, error)
	// Upgrade upgrades an existing Helm release.
	Upgrade(releaseName, namespace string, ch *chart.Chart, values map[string]interface{}) (*release.Release, error)
	// Uninstall removes a Helm release (IgnoreNotFound).
	Uninstall(releaseName, namespace string) error
	// PullAndLoadHTTPS pulls a chart from an HTTPS Helm repo and returns it loaded.
	PullAndLoadHTTPS(repoURL, chartName, version string) (*chart.Chart, error)
}

// realLBCHelmInstaller implements lbcHelmInstaller using the real Helm SDK.
type realLBCHelmInstaller struct {
	actionConfig *action.Configuration
	settings     *cli.EnvSettings
}

func (r *realLBCHelmInstaller) List(namespace, filter string) ([]*release.Release, error) {
	l := action.NewList(r.actionConfig)
	l.Filter = filter
	l.AllNamespaces = false
	l.SetStateMask()
	return l.Run()
}

func (r *realLBCHelmInstaller) Install(releaseName, namespace string, ch *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	inst := action.NewInstall(r.actionConfig)
	inst.ReleaseName = releaseName
	inst.Namespace = namespace
	inst.Wait = true
	inst.Timeout = lbcHelmInstallTimeout
	inst.CreateNamespace = false
	return inst.Run(ch, values)
}

func (r *realLBCHelmInstaller) Upgrade(releaseName, namespace string, ch *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	upg := action.NewUpgrade(r.actionConfig)
	upg.Namespace = namespace
	upg.Wait = true
	upg.Timeout = lbcHelmInstallTimeout
	rel, err := upg.Run(releaseName, ch, values)
	if err != nil {
		return nil, err
	}
	return rel, nil
}

func (r *realLBCHelmInstaller) Uninstall(releaseName, _ string) error {
	uns := action.NewUninstall(r.actionConfig)
	uns.IgnoreNotFound = true
	_, err := uns.Run(releaseName)
	return err
}

func (r *realLBCHelmInstaller) PullAndLoadHTTPS(repoURL, chartName, version string) (*chart.Chart, error) {
	tmpDir, err := os.MkdirTemp("", "lbc-chart-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir for chart pull: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pull := action.NewPullWithOpts(action.WithConfig(r.actionConfig))
	pull.Settings = r.settings
	pull.Version = version
	pull.DestDir = tmpDir
	pull.Untar = true
	pull.RepoURL = repoURL
	if _, err := pull.Run(chartName); err != nil {
		return nil, fmt.Errorf("pull chart %s@%s from %s: %w", chartName, version, repoURL, err)
	}

	chartDir := tmpDir + "/" + chartName
	ch, err := loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("load chart from %s: %w", chartDir, err)
	}
	return ch, nil
}

// Phase14bLBController installs the AWS Load Balancer Controller via Helm (HTTPS
// eks-charts repo, NOT OCI) with IRSA and data-path subnet tagging.
//
// Steps (all idempotent):
//  1. Check addons.lbController.enabled (default OFF). Skip if disabled.
//  2. Read embedded IAM policy JSON. Create customer-managed policy (idempotent).
//  3. Create IRSA role with OIDC-federated trust (GetRole→CreateRole→AttachRolePolicy).
//  4. Tag BNK_EXT_SUBNET with kubernetes.io/role/internal-elb=1 and
//     kubernetes.io/cluster/<name>=shared (idempotent; describe-then-skip).
//  5. Helm install/upgrade/no-op aws-load-balancer-controller from the HTTPS repo.
//  6. Persist state keys.
//
// D-005: CheckAuthOrDie is called at entry.
func Phase14bLBController(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 14b] AWS LB Controller: cluster=%s\n", name)

	// ── Gate: check if enabled (default OFF) ─────────────────────────────────
	var lbcSpec *intent.LBControllerSpec
	if cl.Addons != nil {
		lbcSpec = cl.Addons.LBController
	}
	if !lbcSpec.Enabled() {
		fmt.Fprintln(os.Stderr, "[phase 14b] AWS LB Controller disabled (addons.lbController.enabled not true), skipping")
		return nil
	}

	lbcVersion := lbcSpec.LBControllerVersion()
	irsaRoleName := name + "-lb-controller-irsa"
	policyName := name + "-lb-controller-iam-policy"

	// Read the embedded IAM policy JSON (even in dry-run, to surface embed errors early).
	policyJSON, err := k8smanifests.FS.ReadFile(lbcIAMPolicyPath)
	if err != nil {
		return fmt.Errorf("phase14b: reading embedded IAM policy %s: %w", lbcIAMPolicyPath, err)
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 14b] dry-run: would install %s chart=%s@%s in %s\n",
			lbcReleaseName, lbcChartName, lbcVersion, lbcNamespace)
		fmt.Fprintf(os.Stderr, "[phase 14b] dry-run: would create IAM policy %s\n", policyName)
		fmt.Fprintf(os.Stderr, "[phase 14b] dry-run: would create IRSA role %s\n", irsaRoleName)
		fmt.Fprintf(os.Stderr, "[phase 14b] dry-run: would tag BNK_EXT_SUBNET with kubernetes.io/role/internal-elb=1 and kubernetes.io/cluster/%s=shared\n", name)
		st.Set("LB_CONTROLLER_RELEASE_NAME", lbcReleaseName)
		st.Set("LB_CONTROLLER_VERSION", lbcVersion)
		st.Set("LB_CONTROLLER_IAM_ROLE_ARN", "arn:aws:iam::dry-run:role/"+irsaRoleName)
		st.Set("LB_CONTROLLER_POLICY_ARN", "arn:aws:iam::dry-run:policy/"+policyName)
		st.Set("LB_CONTROLLER_INSTALLED_AT", "dry-run")
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase14b: Clients.K8s is nil — call clients.AttachK8s(kubeconfigPath) after phase 11")
	}

	// ── Step 1: ensure customer-managed IAM policy ────────────────────────────
	policyARN, err := ensureLBCPolicyIdempotent(ctx, clients.IAM, st, name, policyName, string(policyJSON), cl.Tags, cl.Metadata.Labels)
	if err != nil {
		return fmt.Errorf("phase14b: IAM policy: %w", err)
	}
	st.Set("LB_CONTROLLER_POLICY_ARN", policyARN)
	fmt.Fprintf(os.Stderr, "[phase 14b] IAM policy ARN: %s\n", policyARN)

	// ── Step 2: ensure IRSA role ──────────────────────────────────────────────
	oidcProviderARN := st.Get("OIDC_PROVIDER_ARN")
	if oidcProviderARN == "" {
		return fmt.Errorf("phase14b: OIDC_PROVIDER_ARN not in state — Phase18IRSAOIDC must run first")
	}
	accountID := extractAccountID(oidcProviderARN)
	// Derive oidcHost from OIDC_PROVIDER_ARN: strip "arn:aws:iam::<acct>:oidc-provider/" prefix.
	oidcHost := strings.TrimPrefix(oidcProviderARN, "arn:aws:iam::"+accountID+":oidc-provider/")

	roleARN, err := ensureLBCIRSARole(ctx, clients.IAM, name, irsaRoleName, oidcHost, accountID,
		lbcNamespace, lbcReleaseName, policyARN, cl.Tags, cl.Metadata.Labels)
	if err != nil {
		return fmt.Errorf("phase14b: IRSA role: %w", err)
	}
	st.Set("LB_CONTROLLER_IAM_ROLE_ARN", roleARN)
	fmt.Fprintf(os.Stderr, "[phase 14b] IRSA role ARN: %s\n", roleARN)

	// ── Step 3: tag BNK_EXT_SUBNET ───────────────────────────────────────────
	extSubnetID := st.Get("BNK_EXT_SUBNET")
	if extSubnetID == "" {
		return fmt.Errorf("phase14b: BNK_EXT_SUBNET not in state — network phases must run first")
	}
	if err := ensureLBCSubnetTags(ctx, clients.EC2, extSubnetID, name); err != nil {
		return fmt.Errorf("phase14b: subnet tagging: %w", err)
	}

	// ── Step 4: Helm install/upgrade ─────────────────────────────────────────
	vpcID := st.Get("VPC_ID")

	valuesMap := map[string]interface{}{
		"clusterName": name,
		"region":      cl.Metadata.Region,
		"vpcId":       vpcID,
		"serviceAccount": map[string]interface{}{
			"create": true,
			"name":   lbcReleaseName,
			"annotations": map[string]interface{}{
				"eks.amazonaws.com/role-arn": roleARN,
			},
		},
	}

	h, err := buildLBCHelmInstaller(st)
	if err != nil {
		return fmt.Errorf("phase14b: building helm installer: %w", err)
	}

	if err := runLBCHelmInstall(ctx, h, lbcVersion, valuesMap, clients); err != nil {
		return fmt.Errorf("phase14b: helm: %w", err)
	}

	// ── Persist state ─────────────────────────────────────────────────────────
	st.Set("LB_CONTROLLER_RELEASE_NAME", lbcReleaseName)
	st.Set("LB_CONTROLLER_VERSION", lbcVersion)
	st.Set("LB_CONTROLLER_INSTALLED_AT", time.Now().UTC().Format(time.RFC3339))
	return st.Save()
}

// Phase14bLBControllerDown tears down the AWS Load Balancer Controller.
//
// Down order (matches TASK.md spec):
//  1. Delete any type:LoadBalancer Services in kube-system (deprovisions NLBs WHILE
//     the controller is still running). Best-effort: timeout/error → warn + continue.
//     MUST run FIRST — Helm uninstall stops the controller; any NLB still live after
//     that will orphan (AWS DependencyViolation when the subnet/VPC is deleted).
//  2. Helm uninstall + wait for Deployment gone.
//  3. Remove BNK_EXT_SUBNET tags.
//  4. DetachRolePolicy + DeletePolicy.
//  5. deleteRole.
//
// All steps are NotFound/already-gone tolerant. Down never hard-fails on partial
// state; it logs warnings and continues so the cluster can always be torn down.
//
// Note: this down phase always tears down the IRSA role and IAM policy regardless
// of the --keep-irsa flag. Phase14b's IRSA is controller-scoped (not shared with
// the CNE IRSA role managed by Phase18). A full --keep-irsa plumb-through for
// Phase14b is out of scope for Slice 1 and is intentionally deferred; the divergence
// from Phase18IrsaOidcDown's --keep-irsa behaviour is a documented decision, not a gap.
func Phase14bLBControllerDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 14b down] AWS LB Controller: cluster=%s\n", name)

	// ── Step 1: delete LoadBalancer Services (deprovision NLBs before Helm uninstall) ──
	// This MUST run before Helm uninstall. Helm uninstall stops the controller but does
	// NOT delete existing NLBs. Any live NLB after the controller is gone will orphan and
	// block AWS subnet/VPC deletion with DependencyViolation. See docs/LESSONS.md.
	if clients.K8s != nil {
		nlbCtx, nlbCancel := context.WithTimeout(ctx, lbcNLBDeprovisionTimeout)
		defer nlbCancel()
		deleteLBCServices(nlbCtx, clients, name)
	} else {
		fmt.Fprintln(os.Stderr, "[phase 14b down] warning: K8s client not available, skipping LoadBalancer Service deletion — any NLBs may require manual cleanup")
	}

	// ── Step 2: Helm uninstall ────────────────────────────────────────────────
	if clients.K8s != nil {
		h, err := buildLBCHelmInstaller(st)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[phase 14b down] warning: helm installer build error: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[phase 14b down] helm uninstall %s\n", lbcReleaseName)
			if err := h.Uninstall(lbcReleaseName, lbcNamespace); err != nil {
				fmt.Fprintf(os.Stderr, "[phase 14b down] warning: helm uninstall: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[phase 14b down] helm uninstall %s complete\n", lbcReleaseName)
			}

			// Wait briefly for controller Deployment to terminate.
			waitCtx, cancel := context.WithTimeout(ctx, lbcDeployTimeout)
			defer cancel()
			_ = waitForDeploymentGone(waitCtx, clients, lbcNamespace, lbcReleaseName)
		}
	}

	// ── Step 3: remove subnet tags ────────────────────────────────────────────
	extSubnetID := st.Get("BNK_EXT_SUBNET")
	if extSubnetID != "" && clients.EC2 != nil {
		if err := removeLBCSubnetTags(ctx, clients.EC2, extSubnetID, name); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 14b down] warning: removing subnet tags: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[phase 14b down] subnet tags removed from %s\n", extSubnetID)
		}
	}

	// ── Step 4: DetachRolePolicy + DeletePolicy ───────────────────────────────
	policyARN := st.Get("LB_CONTROLLER_POLICY_ARN")
	irsaRoleName := name + "-lb-controller-irsa"

	if policyARN == "" {
		// FIX 2: warn explicitly when state is absent so operator can find+delete
		// the orphaned policy manually if needed. The deterministic name is
		// <cluster>-lb-controller-iam-policy. Mirror Phase18's OIDC_PROVIDER_ARN log.
		fmt.Fprintf(os.Stderr, "[phase 14b down] warning: LB_CONTROLLER_POLICY_ARN not in state — IAM policy %s-lb-controller-iam-policy may be orphaned; check AWS console\n", name)
	} else if clients.IAM != nil {
		// Detach from role first.
		if _, err := clients.IAM.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  ptr(irsaRoleName),
			PolicyArn: ptr(policyARN),
		}); err != nil && !isNoSuchEntity(err) {
			fmt.Fprintf(os.Stderr, "[phase 14b down] warning: DetachRolePolicy %s: %v\n", policyARN, err)
		}
		// Delete the customer-managed policy (deleteRole alone does NOT delete it).
		if _, err := clients.IAM.DeletePolicy(ctx, &iam.DeletePolicyInput{
			PolicyArn: ptr(policyARN),
		}); err != nil && !isNoSuchEntity(err) {
			fmt.Fprintf(os.Stderr, "[phase 14b down] warning: DeletePolicy %s: %v\n", policyARN, err)
		} else {
			fmt.Fprintf(os.Stderr, "[phase 14b down] deleted IAM policy %s\n", policyARN)
		}
	}

	// ── Step 5: delete IAM role ───────────────────────────────────────────────
	if clients.IAM != nil {
		if err := deleteRole(ctx, clients.IAM, irsaRoleName); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 14b down] warning: deleteRole %s: %v\n", irsaRoleName, err)
		} else {
			fmt.Fprintf(os.Stderr, "[phase 14b down] deleted IRSA role %s\n", irsaRoleName)
		}
	}

	clearPhase14bState(st)
	return st.Save()
}

// deleteLBCServices lists and deletes all type:LoadBalancer Services in kube-system,
// then polls until each Service's loadBalancer.ingress clears (NLB deprovisioned).
// Best-effort: on timeout or list/delete error, logs a WARNING and returns — the
// caller (Phase14bLBControllerDown) continues to Helm uninstall regardless.
// NotFound on delete is treated as success (already gone).
// No mutations are performed when dryRun is active (callers gate on dryRun before
// calling Phase14bLBControllerDown, so dryRun is not threaded in here).
func deleteLBCServices(ctx context.Context, clients *Clients, clusterName string) {
	svcList, err := clients.K8s.CoreV1().Services(lbcNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[phase 14b down] warning: listing Services in %s: %v — any NLBs may require manual cleanup\n", lbcNamespace, err)
		return
	}

	var lbServices []corev1.Service
	for _, svc := range svcList.Items {
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
			lbServices = append(lbServices, svc)
		}
	}

	if len(lbServices) == 0 {
		fmt.Fprintf(os.Stderr, "[phase 14b down] no type:LoadBalancer Services found in %s — no NLBs to deprovision\n", lbcNamespace)
		return
	}

	for _, svc := range lbServices {
		svcName := svc.Name
		fmt.Fprintf(os.Stderr, "[phase 14b down] deleting LoadBalancer Service %s/%s (triggers NLB deprovision)\n", lbcNamespace, svcName)
		delErr := clients.K8s.CoreV1().Services(lbcNamespace).Delete(ctx, svcName, metav1.DeleteOptions{})
		if delErr != nil {
			// NotFound = already gone; treat as success.
			if strings.Contains(delErr.Error(), "not found") {
				fmt.Fprintf(os.Stderr, "[phase 14b down] Service %s/%s already gone\n", lbcNamespace, svcName)
				continue
			}
			fmt.Fprintf(os.Stderr, "[phase 14b down] warning: delete Service %s/%s: %v — NLB may be orphaned, check AWS console\n", lbcNamespace, svcName, delErr)
			continue
		}

		// Poll until the Service disappears (controller deprovisioned NLB and k8s GC'd it).
		fmt.Fprintf(os.Stderr, "[phase 14b down] waiting for Service %s/%s to disappear (NLB deprovision)\n", lbcNamespace, svcName)
		for {
			select {
			case <-ctx.Done():
				fmt.Fprintf(os.Stderr, "[phase 14b down] warning: timed out waiting for Service %s/%s to disappear — NLB may still be live; check AWS console and delete manually if needed\n", lbcNamespace, svcName)
				return
			default:
			}
			_, getErr := clients.K8s.CoreV1().Services(lbcNamespace).Get(ctx, svcName, metav1.GetOptions{})
			if getErr != nil && strings.Contains(getErr.Error(), "not found") {
				fmt.Fprintf(os.Stderr, "[phase 14b down] Service %s/%s gone (NLB deprovisioned)\n", lbcNamespace, svcName)
				break
			}
			select {
			case <-ctx.Done():
				fmt.Fprintf(os.Stderr, "[phase 14b down] warning: timed out waiting for Service %s/%s to disappear — NLB may still be live; check AWS console and delete manually if needed\n", lbcNamespace, svcName)
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// ensureLBCPolicyIdempotent creates a customer-managed IAM policy, idempotently.
// On first run: creates the policy and returns its ARN.
// On re-run: reads ARN from state, verifies it still exists.
// If EntityAlreadyExists and no prior state: returns error with guidance.
func ensureLBCPolicyIdempotent(ctx context.Context, iamClient IAMAPI, st *state.State, clusterName, policyName, policyDocument string,
	extraTags, labels map[string]string) (string, error) {

	// Fast path: policy ARN already in state from prior run.
	if existingARN := st.Get("LB_CONTROLLER_POLICY_ARN"); existingARN != "" {
		_, getErr := iamClient.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: ptr(existingARN)})
		if getErr == nil {
			fmt.Fprintf(os.Stderr, "[phase 14b] IAM policy %s already in state and verified, skipping create\n", existingARN)
			return existingARN, nil
		}
		if !isNoSuchEntity(getErr) {
			return "", fmt.Errorf("GetPolicy %s: %w", existingARN, getErr)
		}
		// ARN in state but policy gone — fall through to create.
		fmt.Fprintf(os.Stderr, "[phase 14b] IAM policy %s in state but not found — recreating\n", existingARN)
	}

	iamTagSlice := tags.IAMTags(
		tags.Required(clusterName, tags.CompLBControllerPolicy),
		extraTags,
		labels,
	)

	out, err := iamClient.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     ptr(policyName),
		PolicyDocument: ptr(policyDocument),
		Tags:           iamTagSlice,
	})
	if err != nil {
		if !isEntityAlreadyExists(err) {
			return "", fmt.Errorf("iam:CreatePolicy %s: %w", policyName, err)
		}
		// Policy already exists but not in state (partial-failure re-run scenario).
		// Derive the deterministic ARN and verify it with GetPolicy, then recover.
		// This matches the arn:aws: partition convention used throughout this file.
		recoveredARN := "arn:aws:iam::" + extractAccountID(st.Get("OIDC_PROVIDER_ARN")) + ":policy/" + policyName
		if _, getErr := iamClient.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: ptr(recoveredARN)}); getErr != nil {
			return "", fmt.Errorf("IAM policy %s exists (EntityAlreadyExists) but GetPolicy %s failed: %w", policyName, recoveredARN, getErr)
		}
		fmt.Fprintf(os.Stderr, "[phase 14b] IAM policy %s already exists (EntityAlreadyExists) — recovered ARN %s, continuing\n", policyName, recoveredARN)
		return recoveredARN, nil
	}

	arn := *out.Policy.Arn
	fmt.Fprintf(os.Stderr, "[phase 14b] created IAM policy: %s\n", arn)
	return arn, nil
}

// ensureLBCIRSARole creates the IRSA role for the AWS LB Controller (idempotent).
// Uses the same GetRole→CreateRole(federated)→AttachRolePolicy shape as ensureIRSARole
// in phase18, but attaches a customer-managed policy instead of inline.
func ensureLBCIRSARole(ctx context.Context, iamClient IAMAPI, clusterName, roleName,
	oidcHost, accountID, namespace, saName, policyARN string,
	extraTags, labels map[string]string) (string, error) {

	getOut, err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: ptr(roleName)})
	if err != nil && !isNoSuchEntity(err) {
		return "", fmt.Errorf("GetRole %s: %w", roleName, err)
	}

	iamTagSlice := tags.IAMTags(
		tags.Required(clusterName, tags.CompLBControllerIRSARole),
		extraTags,
		labels,
	)

	var roleARN string
	if err == nil {
		roleARN = *getOut.Role.Arn
		fmt.Fprintf(os.Stderr, "[phase 14b] IRSA role %s already exists, skipping create\n", roleName)
	} else {
		trustPolicy, trustErr := oidcFederatedTrustPolicy(oidcHost, accountID, namespace, saName)
		if trustErr != nil {
			return "", fmt.Errorf("building IRSA trust policy: %w", trustErr)
		}
		createOut, createErr := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 ptr(roleName),
			AssumeRolePolicyDocument: ptr(trustPolicy),
			Tags:                     iamTagSlice,
		})
		if createErr != nil {
			return "", fmt.Errorf("iam:CreateRole %s: %w", roleName, createErr)
		}
		roleARN = *createOut.Role.Arn
		fmt.Fprintf(os.Stderr, "[phase 14b] created IRSA role %s (%s)\n", roleName, roleARN)
	}

	// Attach the customer-managed policy. AttachRolePolicy is idempotent.
	if _, err := iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  ptr(roleName),
		PolicyArn: ptr(policyARN),
	}); err != nil {
		return "", fmt.Errorf("AttachRolePolicy %s → %s: %w", policyARN, roleName, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 14b] attached policy %s to role %s\n", policyARN, roleName)

	return roleARN, nil
}

// ensureLBCSubnetTags tags BNK_EXT_SUBNET with both internal-elb and cluster tags.
// Idempotent: describes existing tags and skips CreateTags if both already present.
func ensureLBCSubnetTags(ctx context.Context, ec2c EC2API, subnetID, clusterName string) error {
	descOut, err := ec2c.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetID},
	})
	if err != nil {
		return fmt.Errorf("ec2:DescribeSubnets %s: %w", subnetID, err)
	}
	if len(descOut.Subnets) == 0 {
		return fmt.Errorf("subnet %s not found", subnetID)
	}

	existingTags := make(map[string]string)
	for _, t := range descOut.Subnets[0].Tags {
		if t.Key != nil && t.Value != nil {
			existingTags[*t.Key] = *t.Value
		}
	}

	internalELBKey := "kubernetes.io/role/internal-elb"
	clusterKey := "kubernetes.io/cluster/" + clusterName

	if existingTags[internalELBKey] == "1" && existingTags[clusterKey] == "shared" {
		fmt.Fprintf(os.Stderr, "[phase 14b] subnet %s already has both LB controller tags, skipping\n", subnetID)
		return nil
	}

	if _, err := ec2c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{subnetID},
		Tags: []ec2types.Tag{
			{Key: ptr(internalELBKey), Value: ptr("1")},
			{Key: ptr(clusterKey), Value: ptr("shared")},
		},
	}); err != nil {
		return fmt.Errorf("ec2:CreateTags on subnet %s: %w", subnetID, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 14b] tagged subnet %s with %s=1 and %s=shared\n",
		subnetID, internalELBKey, clusterKey)
	return nil
}

// removeLBCSubnetTags removes the two LB controller subnet tags.
// Tolerates subnet-NotFound (already deleted on down).
func removeLBCSubnetTags(ctx context.Context, ec2c EC2API, subnetID, clusterName string) error {
	internalELBKey := "kubernetes.io/role/internal-elb"
	clusterKey := "kubernetes.io/cluster/" + clusterName

	_, err := ec2c.DeleteTags(ctx, &ec2.DeleteTagsInput{
		Resources: []string{subnetID},
		Tags: []ec2types.Tag{
			{Key: ptr(internalELBKey), Value: ptr("1")},
			{Key: ptr(clusterKey), Value: ptr("shared")},
		},
	})
	if err != nil && ignoreNotFound(err) != nil {
		return fmt.Errorf("ec2:DeleteTags on subnet %s: %w", subnetID, err)
	}
	return nil
}

// buildLBCHelmInstaller creates the Helm installer for the LBC (HTTPS repo, no OCI login).
func buildLBCHelmInstaller(st *state.State) (lbcHelmInstaller, error) {
	kubeconfigPath := st.Get("KUBECONFIG_PATH")

	settings := cli.New()
	if kubeconfigPath != "" {
		settings.KubeConfig = kubeconfigPath
	}

	actionConfig := new(action.Configuration)
	logFn := func(format string, v ...interface{}) {
		fmt.Fprintf(os.Stderr, "[phase 14b][helm] "+format+"\n", v...)
	}
	if err := actionConfig.Init(settings.RESTClientGetter(), lbcNamespace, "secret", logFn); err != nil {
		return nil, fmt.Errorf("init helm action config: %w", err)
	}

	return &realLBCHelmInstaller{
		actionConfig: actionConfig,
		settings:     settings,
	}, nil
}

// runLBCHelmInstall handles the list→install-or-upgrade sequence for the LBC.
// Extracted for testability (accepts lbcHelmInstaller interface).
func runLBCHelmInstall(ctx context.Context, h lbcHelmInstaller, version string,
	valuesMap map[string]interface{}, clients *Clients) error {

	// Pull the chart from the HTTPS repo.
	fmt.Fprintf(os.Stderr, "[phase 14b] pulling chart %s@%s from %s\n", lbcChartName, version, lbcChartRepo)
	ch, err := h.PullAndLoadHTTPS(lbcChartRepo, lbcChartName, version)
	if err != nil {
		return fmt.Errorf("pulling LBC chart: %w", err)
	}

	releases, err := h.List(lbcNamespace, "^"+lbcReleaseName+"$")
	if err != nil {
		return fmt.Errorf("listing helm releases: %w", err)
	}

	if len(releases) == 0 {
		fmt.Fprintf(os.Stderr, "[phase 14b] installing %s v%s in namespace %s\n", lbcReleaseName, version, lbcNamespace)
		if _, err := h.Install(lbcReleaseName, lbcNamespace, ch, valuesMap); err != nil {
			return fmt.Errorf("helm install %s: %w", lbcReleaseName, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 14b] helm install %s complete\n", lbcReleaseName)
	} else {
		existing := releases[0]
		deployedVersion := ""
		if existing.Chart != nil && existing.Chart.Metadata != nil {
			deployedVersion = existing.Chart.Metadata.Version
		}
		alreadyDeployed := existing.Info != nil && existing.Info.Status == release.StatusDeployed
		valuesUnchanged := helmValuesEqual(existing.Config, valuesMap)

		if alreadyDeployed && deployedVersion == version && valuesUnchanged {
			fmt.Fprintf(os.Stderr, "[phase 14b] release %s already at v%s with unchanged values — skipping upgrade\n", lbcReleaseName, version)
		} else {
			fmt.Fprintf(os.Stderr, "[phase 14b] upgrading %s (deployed=%q, desired=%q, valuesMatch=%v)\n",
				lbcReleaseName, deployedVersion, version, valuesUnchanged)
			if _, err := h.Upgrade(lbcReleaseName, lbcNamespace, ch, valuesMap); err != nil {
				return fmt.Errorf("helm upgrade %s: %w", lbcReleaseName, err)
			}
			fmt.Fprintf(os.Stderr, "[phase 14b] helm upgrade %s complete\n", lbcReleaseName)
		}
	}

	// Wait briefly for controller Deployment to become available (best-effort).
	// IAM-propagation race (cf. WS-E1 SageMaker lesson) means the controller may
	// CrashLoop on first start with WebIdentityErr before the role is assumable.
	// We don't fail the phase on timeout — just log a warning.
	fmt.Fprintf(os.Stderr, "[phase 14b] waiting for controller deployment to become available (up to %s)\n", lbcDeployTimeout)
	waitCtx, cancel := context.WithTimeout(ctx, lbcDeployTimeout)
	defer cancel()
	for {
		select {
		case <-waitCtx.Done():
			fmt.Fprintf(os.Stderr, "[phase 14b] warning: controller deployment not yet available (IAM propagation may still be in progress)\n")
			return nil
		default:
		}
		available, _, err := k8swait.DeploymentReplicaStatus(waitCtx, clients.K8s, lbcNamespace, lbcReleaseName)
		if err == nil && available > 0 {
			fmt.Fprintf(os.Stderr, "[phase 14b] controller deployment available (%d replicas)\n", available)
			return nil
		}
		// If the deployment doesn't exist at all (not found), stop polling immediately
		// rather than waiting for the full timeout. In unit tests this exits quickly.
		if err != nil && strings.Contains(err.Error(), "not found") {
			fmt.Fprintf(os.Stderr, "[phase 14b] warning: controller deployment not yet found (may be pending)\n")
			return nil
		}
		select {
		case <-waitCtx.Done():
			fmt.Fprintf(os.Stderr, "[phase 14b] warning: controller deployment not yet available (IAM propagation may still be in progress)\n")
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// clearPhase14bState zeroes all phase 14b state keys.
func clearPhase14bState(st *state.State) {
	for _, k := range []string{
		"LB_CONTROLLER_RELEASE_NAME",
		"LB_CONTROLLER_VERSION",
		"LB_CONTROLLER_IAM_ROLE_ARN",
		"LB_CONTROLLER_POLICY_ARN",
		"LB_CONTROLLER_INSTALLED_AT",
	} {
		st.Set(k, "")
	}
}
