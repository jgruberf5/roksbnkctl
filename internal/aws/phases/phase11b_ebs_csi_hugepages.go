package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/smithy-go"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
)

const (
	ebsCSIAddonName          = "aws-ebs-csi-driver"
	ebsCSIAddonActiveTimeout = 10 * time.Minute
	ebsCSIAddonPollInterval  = 15 * time.Second
	hugepagesYAMLPath        = "shared/hugepages-ds.yaml"
	hugepagesDaemonSetName   = "hugepages-setup"
	hugepagesDaemonSetNS     = "kube-system"
	hugepagesReadyTimeout    = 5 * time.Minute
	// hugepagesNodeCapTimeout bounds the wait for kubelet to re-advertise
	// hugepages-2Mi capacity AFTER the DS is Ready. Without this gate,
	// downstream phases proceed before f5-tmm can schedule and the pod fails
	// with "Insufficient hugepages-2Mi" until the next kubelet sync. Matches
	// aws-gpu-setup up.sh:639 chk_hugepages_on_node 300s wait.
	hugepagesNodeCapTimeout = 5 * time.Minute
)

// Phase11bEBSCSIHugepages closes the BNK runtime-prerequisite gaps that slice 7
// did not address:
//
//  1. Install the aws-ebs-csi-driver EKS managed addon (eks:CreateAddon).
//     The AmazonEBSCSIDriverPolicy is already attached to the node role by
//     Phase 07 IAM (slice 5/7 setup), so no IRSA service-account role is
//     needed — the addon uses node-role credentials. EKS 1.30 deprecates the
//     in-tree gp2 provisioner; PVCs against the existing default gp2 SC are
//     dispatched to the EBS CSI driver via CSI migration once this addon is
//     ACTIVE. No custom SC apply needed (matches aws-gpu-setup up.sh:601-631).
//  2. Apply the hugepages-setup DaemonSet which allocates 2Mi hugepages on
//     role=bnk worker nodes (the f5-tmm pod requires hugepages-2Mi capacity).
//  3. Wait for kubelet on the TMM node to re-advertise
//     .status.capacity.hugepages-2Mi >= cl.Bnk.TmmHugepages. DS-Ready is NOT
//     enough — the DS restarts kubelet and capacity surfaces on next sync
//     (~10–30s). Without this gate, Phase 12+ proceed and f5-tmm fails to
//     schedule with "Insufficient hugepages-2Mi". Matches aws-gpu-setup
//     up.sh:639 chk_hugepages_on_node.
//
// D-005: CheckAuthOrDie at entry.
// Lifecycle order: ... Phase11 → Phase11bEBSCSIHugepages → Phase12 ...
func Phase11bEBSCSIHugepages(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name

	// On dry-run, cl.Bnk is optional. Use defaults (matching applyDefaults) for logging/state.
	if dryRun {
		scName := "gp2"    // default storage class (aws-gpu-setup parity)
		hugepages := "4Gi" // default TMM hugepages (matching applyDefaults)
		if cl.Bnk != nil {
			scName = cl.Bnk.StorageClassName
			hugepages = cl.Bnk.TmmHugepages
		}
		fmt.Fprintf(os.Stderr, "[phase 11b] EBS CSI addon + hugepages-ds: cluster=%s sc=%s\n", name, scName)
		fmt.Fprintln(os.Stderr, "[phase 11b] dry-run: would install aws-ebs-csi-driver EKS addon (managed)")
		fmt.Fprintln(os.Stderr, "[phase 11b] dry-run: would apply hugepages-setup DaemonSet in kube-system")
		fmt.Fprintf(os.Stderr, "[phase 11b] dry-run: would wait for TMM node hugepages-2Mi >= %s\n", hugepages)
		st.Set("EBS_CSI_ADDON_STATUS", "dry-run-ACTIVE")
		st.Set("GP3_STORAGE_CLASS", scName)
		st.Set("HUGEPAGES_DS_INSTALLED_AT", "dry-run")
		return nil
	}

	// Non-dry-run: cl.Bnk must be present (validated earlier in phase flow, e.g., by applyDefaults).
	scName := cl.Bnk.StorageClassName // "gp2" by default (aws-gpu-setup parity)
	fmt.Fprintf(os.Stderr, "[phase 11b] EBS CSI addon + hugepages-ds: cluster=%s sc=%s\n", name, scName)

	if clients.K8s == nil || clients.Dynamic == nil {
		return fmt.Errorf("phase11b: k8s clients nil — Phase 11 must run first")
	}

	// 11b.1 — EBS CSI managed addon.
	if err := ensureEBSCSIAddon(ctx, clients.EKS, name); err != nil {
		return fmt.Errorf("phase11b: ebs-csi-driver addon: %w", err)
	}
	st.Set("EBS_CSI_ADDON_STATUS", "ACTIVE")
	st.Set("GP3_STORAGE_CLASS", scName) // records the SC BNK will request (EKS-default gp2)
	fmt.Fprintf(os.Stderr, "[phase 11b] aws-ebs-csi-driver addon ACTIVE (PVCs against %q dispatch via CSI migration)\n", scName)

	// 11b.2 — Hugepages DaemonSet.
	hugepagesYAML, err := k8smanifests.FS.ReadFile(hugepagesYAMLPath)
	if err != nil {
		return fmt.Errorf("phase11b: reading hugepages-ds YAML: %w", err)
	}
	if err := applyRawYAML(ctx, clients, hugepagesYAML); err != nil {
		return fmt.Errorf("phase11b: applying hugepages-ds: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 11b] waiting for hugepages-setup DaemonSet (up to %s)\n", hugepagesReadyTimeout)
	if err := k8swait.WaitForDaemonSetReady(ctx, clients.K8s, hugepagesDaemonSetNS, hugepagesDaemonSetName, hugepagesReadyTimeout); err != nil {
		return fmt.Errorf("phase11b: hugepages DaemonSet not ready: %w", err)
	}
	st.Set("HUGEPAGES_DS_INSTALLED_AT", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintln(os.Stderr, "[phase 11b] hugepages-setup DaemonSet ready")

	// 11b.3 — Wait for kubelet to re-advertise hugepages capacity on the TMM node.
	tmmNode := st.Get("TMM_NODE_NAME")
	if tmmNode == "" {
		return fmt.Errorf("phase11b: TMM_NODE_NAME not in state (Phase 16 must run before Phase 11b)")
	}
	want := cl.Bnk.TmmHugepages
	fmt.Fprintf(os.Stderr, "[phase 11b] waiting for node %s to advertise hugepages-2Mi >= %s (up to %s)\n",
		tmmNode, want, hugepagesNodeCapTimeout)
	if err := k8swait.WaitForNodeHugepagesCapacity(ctx, clients.K8s, tmmNode, want, hugepagesNodeCapTimeout); err != nil {
		return fmt.Errorf("phase11b: node %s hugepages-2Mi capacity: %w", tmmNode, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 11b] node %s advertising hugepages-2Mi >= %s\n", tmmNode, want)

	return st.Save()
}

// Phase11bEBSCSIHugepagesDown deletes the hugepages DS + EBS CSI addon.
// Tolerates NotFound at every step. The EKS-default gp2 StorageClass is NOT
// deleted — it is EKS-owned and lifecycle-tied to the cluster (matches
// aws-gpu-setup which never deletes the default SC).
func Phase11bEBSCSIHugepagesDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 11b down] EBS CSI addon + hugepages-ds: cluster=%s\n", name)

	if clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 11b down] warning: k8s client not available, skipping k8s teardown")
		clearPhase11bState(st)
		return st.Save()
	}

	// 1. Hugepages DS.
	if hugepagesYAML, err := k8smanifests.FS.ReadFile(hugepagesYAMLPath); err == nil {
		if dErr := deleteRawYAML(ctx, clients, hugepagesYAML); dErr != nil {
			fmt.Fprintf(os.Stderr, "[phase 11b down] warning: delete hugepages-ds: %v\n", dErr)
		}
	}

	// 2. EBS CSI addon — best-effort delete. The EKS-managed addon owns its own
	// k8s resources so this single API call cleans up cluster-side too.
	if clients.EKS != nil {
		_, err := clients.EKS.DeleteAddon(ctx, &eks.DeleteAddonInput{
			ClusterName: &name,
			AddonName:   aws.String(ebsCSIAddonName),
		})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ResourceNotFoundException" {
				fmt.Fprintf(os.Stderr, "[phase 11b down] aws-ebs-csi-driver addon already gone\n")
			} else {
				fmt.Fprintf(os.Stderr, "[phase 11b down] warning: delete EBS CSI addon: %v\n", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[phase 11b down] aws-ebs-csi-driver addon delete requested\n")
		}
	}

	clearPhase11bState(st)
	return st.Save()
}

// ensureEBSCSIAddon installs the aws-ebs-csi-driver EKS managed addon
// idempotently and waits for status ACTIVE.
func ensureEBSCSIAddon(ctx context.Context, eksClient EKSAPI, clusterName string) error {
	// Describe first — if exists already, just wait.
	desc, err := eksClient.DescribeAddon(ctx, &eks.DescribeAddonInput{
		ClusterName: &clusterName,
		AddonName:   aws.String(ebsCSIAddonName),
	})
	if err != nil {
		var apiErr smithy.APIError
		if !(errors.As(err, &apiErr) && apiErr.ErrorCode() == "ResourceNotFoundException") {
			return fmt.Errorf("DescribeAddon: %w", err)
		}
		// Not found → create.
		fmt.Fprintf(os.Stderr, "[phase 11b] creating aws-ebs-csi-driver addon\n")
		_, cErr := eksClient.CreateAddon(ctx, &eks.CreateAddonInput{
			ClusterName:      &clusterName,
			AddonName:        aws.String(ebsCSIAddonName),
			ResolveConflicts: ekstypes.ResolveConflictsOverwrite,
		})
		if cErr != nil {
			return fmt.Errorf("CreateAddon %s: %w", ebsCSIAddonName, cErr)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[phase 11b] aws-ebs-csi-driver addon already present (status=%s), waiting for ACTIVE\n",
			desc.Addon.Status)
	}

	// Poll for ACTIVE.
	deadline := time.Now().Add(ebsCSIAddonActiveTimeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		out, dErr := eksClient.DescribeAddon(ctx, &eks.DescribeAddonInput{
			ClusterName: &clusterName,
			AddonName:   aws.String(ebsCSIAddonName),
		})
		if dErr != nil {
			return fmt.Errorf("DescribeAddon during poll: %w", dErr)
		}
		switch out.Addon.Status {
		case ekstypes.AddonStatusActive:
			return nil
		case ekstypes.AddonStatusCreateFailed, ekstypes.AddonStatusDegraded:
			return fmt.Errorf("aws-ebs-csi-driver addon entered terminal state %s", out.Addon.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ebsCSIAddonPollInterval):
		}
	}
	return fmt.Errorf("timed out waiting for aws-ebs-csi-driver addon ACTIVE")
}

func clearPhase11bState(st *state.State) {
	for _, k := range []string{
		"EBS_CSI_ADDON_STATUS",
		"GP3_STORAGE_CLASS",
		"HUGEPAGES_DS_INSTALLED_AT",
	} {
		st.Set(k, "")
	}
}
