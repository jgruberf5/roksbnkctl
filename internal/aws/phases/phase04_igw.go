package phases

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/tags"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// Phase04IGW creates the Internet Gateway and attaches it to the VPC.
// Idempotent: skips creation and re-attaches if already present.
func Phase04IGW(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	vpcID := st.Get("VPC_ID")
	if vpcID == "" {
		return fmt.Errorf("phase04: VPC_ID not in state (run phase02 first)")
	}

	fmt.Fprintf(os.Stderr, "[phase 04] igw: cluster=%s vpc=%s\n", name, vpcID)

	existing, err := findIGWByTag(ctx, clients.EC2, name)
	if err != nil {
		return fmt.Errorf("phase04: listing IGWs by tag: %w", err)
	}

	var igwID string
	if existing != "" {
		fmt.Fprintf(os.Stderr, "[phase 04] igw %s already exists, skipping create\n", existing)
		igwID = existing
	} else if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 04] dry-run: would create IGW and attach to %s\n", vpcID)
		st.Set("IGW_ID", "dry-run-igw")
		return nil
	} else {
		resourceTags := tags.Merge(
			tags.Required(name, tags.CompIGW),
			cl.Tags,
			cl.Metadata.Labels,
		)
		out, err := clients.EC2.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{
			TagSpecifications: []ec2types.TagSpecification{
				tagSpecification(ec2types.ResourceTypeInternetGateway, resourceTags),
			},
		})
		if err != nil {
			return fmt.Errorf("phase04: ec2:CreateInternetGateway: %w", err)
		}
		igwID = *out.InternetGateway.InternetGatewayId
		fmt.Fprintf(os.Stderr, "[phase 04] created IGW %s\n", igwID)
	}

	// Ensure attached to VPC (idempotent — error if already attached is swallowed).
	if err := ensureIGWAttached(ctx, clients.EC2, igwID, vpcID); err != nil {
		return fmt.Errorf("phase04: attaching IGW %s to VPC %s: %w", igwID, vpcID, err)
	}

	st.Set("IGW_ID", igwID)
	return st.Save()
}

// Phase04IGWDown detaches and deletes the IGW. Tolerates "already gone".
func Phase04IGWDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name

	igwID := st.Get("IGW_ID")
	if igwID == "" {
		var err error
		igwID, err = findIGWByTag(ctx, clients.EC2, name)
		if err != nil {
			return fmt.Errorf("phase04 down: tag-discovery: %w", err)
		}
	}
	if igwID == "" {
		fmt.Fprintf(os.Stderr, "[phase 04 down] IGW already gone\n")
		return nil
	}

	vpcID := st.Get("VPC_ID")
	if vpcID == "" {
		// Tag-discovery path: state carries no VPC_ID, so read the attachment
		// from the IGW itself. Silently skipping the detach here used to make
		// the delete below fail with DependencyViolation (IGW still attached)
		// whenever down ran from tag-discovery.
		out, err := clients.EC2.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
			InternetGatewayIds: []string{igwID},
		})
		if err == nil && len(out.InternetGateways) > 0 &&
			len(out.InternetGateways[0].Attachments) > 0 &&
			out.InternetGateways[0].Attachments[0].VpcId != nil {
			vpcID = *out.InternetGateways[0].Attachments[0].VpcId
		}
	}
	fmt.Fprintf(os.Stderr, "[phase 04 down] detaching+deleting IGW %s\n", igwID)

	// Detach if still attached.
	if vpcID != "" {
		_, err := clients.EC2.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
			InternetGatewayId: ptr(igwID),
			VpcId:             ptr(vpcID),
		})
		if err := ignoreNotFound(err); err != nil {
			// Swallow "not attached" error too.
			if !isDetachError(err) {
				return fmt.Errorf("phase04 down: ec2:DetachInternetGateway: %w", err)
			}
		}
	}

	_, err := clients.EC2.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
		InternetGatewayId: ptr(igwID),
	})
	if err := ignoreNotFound(err); err != nil {
		return fmt.Errorf("phase04 down: ec2:DeleteInternetGateway: %w", err)
	}

	st.Set("IGW_ID", "")
	return st.Save()
}

// findIGWByTag returns the IGW ID tagged awsbnkctl:cluster=name, or "".
func findIGWByTag(ctx context.Context, ec2c EC2API, clusterName string) (string, error) {
	out, err := ec2c.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{tags.ClusterFilter(clusterName)},
	})
	if err != nil {
		return "", err
	}
	if len(out.InternetGateways) == 0 {
		return "", nil
	}
	return *out.InternetGateways[0].InternetGatewayId, nil
}

// ensureIGWAttached attaches igwID to vpcID if not already attached.
func ensureIGWAttached(ctx context.Context, ec2c EC2API, igwID, vpcID string) error {
	// Describe to check current attachments.
	out, err := ec2c.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{
			{Name: ptr("internet-gateway-id"), Values: []string{igwID}},
		},
	})
	if err != nil {
		return err
	}
	if len(out.InternetGateways) > 0 {
		for _, att := range out.InternetGateways[0].Attachments {
			if att.VpcId != nil && *att.VpcId == vpcID {
				fmt.Fprintf(os.Stderr, "[phase 04] IGW already attached to %s\n", vpcID)
				return nil
			}
		}
	}
	_, err = ec2c.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: ptr(igwID),
		VpcId:             ptr(vpcID),
	})
	return err
}

// isDetachError reports whether the error is an "already detached" style
// error that should be swallowed during destroy.
func isDetachError(err error) bool {
	if err == nil {
		return false
	}
	type coder interface{ ErrorCode() string }
	e := err
	for e != nil {
		if ce, ok := e.(coder); ok {
			switch ce.ErrorCode() {
			case "Gateway.NotAttached", "InvalidInternetGatewayID.NotFound":
				return true
			}
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
