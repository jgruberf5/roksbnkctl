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
	ebsCSIAddonName           = "aws-ebs-csi-driver"
	ebsCSIAddonActiveTimeout  = 10 * time.Minute
	ebsCSIAddonPollInterval   = 15 * time.Second
	hugepagesYAMLPath         = "shared/hugepages-ds.yaml"
	hugepagesDaemonSetName    = "hugepages-setup"
	hugepagesDaemonSetNS      = "kube-system"
	hugepagesReadyTimeout     = 5 * time.Minute
	gp3StorageClassYAMLPath   = "shared/gp3-storage-class.yaml"
	gp3StorageClassName       = "gp3"
	gp3DefaultAnnotationFalse = "false"
)

// Phase11bEBSCSIHugepages closes the BNK runtime-prerequisite gaps that slice 7
// did not address:
//
//  1. Install the aws-ebs-csi-driver EKS managed addon (eks:CreateAddon).
//     The AmazonEBSCSIDriverPolicy is already attached to the node role by
//     Phase 07 IAM (slice 5/7 setup), so no IRSA service-account role is
//     needed — the addon uses node-role credentials.
//  2. Create the gp3 StorageClass (BnkSpec.StorageClassName default).
//  3. Apply the hugepages-setup DaemonSet which allocates 2Mi hugepages on
//     role=bnk worker nodes (the f5-tmm pod requires hugepages-2Mi capacity).
//
// D-005: CheckAuthOrDie at entry.
// Slice 7 lifecycle order: ... Phase11 → Phase11bEBSCSIHugepages → Phase12 ...
func Phase11bEBSCSIHugepages(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 11b] EBS CSI addon + gp3 SC + hugepages-ds: cluster=%s\n", name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 11b] dry-run: would install aws-ebs-csi-driver EKS addon (managed)")
		fmt.Fprintln(os.Stderr, "[phase 11b] dry-run: would create gp3 StorageClass (ebs.csi.aws.com)")
		fmt.Fprintln(os.Stderr, "[phase 11b] dry-run: would apply hugepages-setup DaemonSet in kube-system")
		st.Set("EBS_CSI_ADDON_STATUS", "dry-run-ACTIVE")
		st.Set("GP3_STORAGE_CLASS", "dry-run-gp3")
		st.Set("HUGEPAGES_DS_INSTALLED_AT", "dry-run")
		return nil
	}

	if clients.K8s == nil || clients.Dynamic == nil {
		return fmt.Errorf("phase11b: k8s clients nil — Phase 11 must run first")
	}

	// 11b.1 — EBS CSI managed addon.
	if err := ensureEBSCSIAddon(ctx, clients.EKS, name); err != nil {
		return fmt.Errorf("phase11b: ebs-csi-driver addon: %w", err)
	}
	st.Set("EBS_CSI_ADDON_STATUS", "ACTIVE")
	fmt.Fprintln(os.Stderr, "[phase 11b] aws-ebs-csi-driver addon ACTIVE")

	// 11b.2 — gp3 StorageClass.
	gp3YAML, err := k8smanifests.FS.ReadFile(gp3StorageClassYAMLPath)
	if err != nil {
		return fmt.Errorf("phase11b: reading gp3 SC YAML: %w", err)
	}
	if err := applyRawYAML(ctx, clients.Dynamic, gp3YAML); err != nil {
		return fmt.Errorf("phase11b: applying gp3 StorageClass: %w", err)
	}
	st.Set("GP3_STORAGE_CLASS", gp3StorageClassName)
	fmt.Fprintf(os.Stderr, "[phase 11b] gp3 StorageClass applied (provisioner=ebs.csi.aws.com)\n")

	// 11b.3 — Hugepages DaemonSet.
	hugepagesYAML, err := k8smanifests.FS.ReadFile(hugepagesYAMLPath)
	if err != nil {
		return fmt.Errorf("phase11b: reading hugepages-ds YAML: %w", err)
	}
	if err := applyRawYAML(ctx, clients.Dynamic, hugepagesYAML); err != nil {
		return fmt.Errorf("phase11b: applying hugepages-ds: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 11b] waiting for hugepages-setup DaemonSet (up to %s)\n", hugepagesReadyTimeout)
	if err := k8swait.WaitForDaemonSetReady(ctx, clients.K8s, hugepagesDaemonSetNS, hugepagesDaemonSetName, hugepagesReadyTimeout); err != nil {
		return fmt.Errorf("phase11b: hugepages DaemonSet not ready: %w", err)
	}
	st.Set("HUGEPAGES_DS_INSTALLED_AT", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintln(os.Stderr, "[phase 11b] hugepages-setup DaemonSet ready (node hugepages-2Mi advertised)")

	return st.Save()
}

// Phase11bEBSCSIHugepagesDown deletes the hugepages DS + gp3 SC + EBS CSI addon.
// Tolerates NotFound at every step.
func Phase11bEBSCSIHugepagesDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 11b down] EBS CSI + gp3 SC + hugepages-ds: cluster=%s\n", name)

	if clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 11b down] warning: k8s client not available, skipping k8s teardown")
		clearPhase11bState(st)
		return st.Save()
	}

	// 1. Hugepages DS.
	if hugepagesYAML, err := k8smanifests.FS.ReadFile(hugepagesYAMLPath); err == nil {
		if dErr := deleteRawYAML(ctx, clients.Dynamic, hugepagesYAML); dErr != nil {
			fmt.Fprintf(os.Stderr, "[phase 11b down] warning: delete hugepages-ds: %v\n", dErr)
		}
	}

	// 2. gp3 StorageClass.
	if gp3YAML, err := k8smanifests.FS.ReadFile(gp3StorageClassYAMLPath); err == nil {
		if dErr := deleteRawYAML(ctx, clients.Dynamic, gp3YAML); dErr != nil {
			fmt.Fprintf(os.Stderr, "[phase 11b down] warning: delete gp3 SC: %v\n", dErr)
		}
	}

	// 3. EBS CSI addon — best-effort delete. The EKS-managed addon owns its own
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
