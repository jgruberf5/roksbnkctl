package phases

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	sagemaker_types "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/tags"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// lmiDefaultImageSuffix is the DJL-Serving Large Model Inference container image default.
// Serving engine IS vLLM (LMI v25 = DJL 0.36.0 / djl-lmi, vLLM 0.20.1).
// Account 763104351884 is AWS's canonical DLC account.
// Region-specific: the caller substitutes the cluster region.
//
// AWS periodically deprecates older DLC tags; verified-present tags as of 2026-06 in
// account 763104351884 / djl-inference / ap-southeast-2:
//
//	0.36.0-lmi25.0.0-cu130 (default, vLLM 0.20.1)
//	0.35.0-lmi17.0.0-cu128
//	0.34.0-lmi16.0.0-cu128
//
// To pin a different tag without a code change, set ai.sagemaker.imageUri in cluster.yaml.
const lmiDefaultImageSuffix = "763104351884.dkr.ecr.%s.amazonaws.com/djl-inference:0.36.0-lmi25.0.0-cu130"

// PhaseSageMakerUp creates the SageMaker Model → EndpointConfig → Endpoint for
// the configured LMI (vLLM) inference endpoint. Idempotent: DescribeModel /
// DescribeEndpointConfig / DescribeEndpoint before each create; skips if
// already present. Resources are tagged for cluster-ownership so the
// tag-discovery teardown path can find them when local state is lost.
//
// Gated by cl.SageMakerEnabled() — the caller (lifecycle.go) must also gate.
// Dry-run: logs what would be created, writes placeholder state, no SDK mutations.
func PhaseSageMakerUp(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	sm := cl.AI.SageMaker
	name := cl.Metadata.Name
	region := cl.Metadata.Region

	modelName := name + "-lmi-model"
	configName := name + "-lmi-epconfig"
	endpointName := name + "-lmi"

	// Use the caller-supplied image URI verbatim when set; otherwise construct
	// the default DLC URI from the cluster region. The override exists because
	// AWS periodically deprecates older DLC tags — set ai.sagemaker.imageUri
	// in cluster.yaml to pin a specific tag without a code change.
	imageURI := sm.ImageURI
	if imageURI == "" {
		imageURI = fmt.Sprintf(lmiDefaultImageSuffix, region)
	}

	execRoleName := name + "-sagemaker-execution-role"

	if dryRun {
		fmt.Fprintf(os.Stderr, "[sagemaker] dry-run: would create execution role %s\n", execRoleName)
		fmt.Fprintf(os.Stderr, "[sagemaker] dry-run: would create model %s (image=%s, model=%s)\n", modelName, imageURI, sm.Model)
		fmt.Fprintf(os.Stderr, "[sagemaker] dry-run: would create endpoint-config %s (instance=%s, scaleToZero=%v)\n", configName, sm.InstanceType, sm.ScaleToZero)
		fmt.Fprintf(os.Stderr, "[sagemaker] dry-run: would create endpoint %s\n", endpointName)
		st.Set("SAGEMAKER_EXEC_ROLE_NAME", execRoleName)
		st.Set("SAGEMAKER_MODEL_NAME", modelName)
		st.Set("SAGEMAKER_EPCONFIG_NAME", configName)
		st.Set("SAGEMAKER_ENDPOINT_NAME", endpointName)
		return st.Save()
	}

	smTags := smTagsFromMaps(
		tags.Required(name, tags.CompSageMakerEndpoint),
		cl.Tags,
		cl.Metadata.Labels,
	)

	// 0. Ensure SageMaker execution role (required by CreateModel).
	// The role lets SageMaker pull the LMI container image from ECR and write
	// CloudWatch Logs. AmazonSageMakerFullAccess is demo-appropriate; scope to
	// ECR read + CloudWatch Logs + S3 when hardening for production.
	iamTags := tags.IAMTags(
		tags.Required(name, tags.CompSageMakerExecRole),
		cl.Tags,
		cl.Metadata.Labels,
	)
	execRoleARN, err := ensureRole(ctx, clients.IAM, execRoleName, "sagemaker.amazonaws.com",
		[]string{"arn:aws:iam::aws:policy/AmazonSageMakerFullAccess"},
		"",
		iamTags,
	)
	if err != nil {
		return fmt.Errorf("sagemaker up: execution role: %w", err)
	}
	st.Set("SAGEMAKER_EXEC_ROLE_NAME", execRoleName)

	// 1. Ensure Model.
	if err := ensureSageMakerModel(ctx, clients.IAM, clients.SageMaker, modelName, imageURI, sm.Model, execRoleARN, smTags); err != nil {
		return fmt.Errorf("sagemaker up: model: %w", err)
	}
	st.Set("SAGEMAKER_MODEL_NAME", modelName)

	// 2. Ensure EndpointConfig.
	if err := ensureSageMakerEndpointConfig(ctx, clients.SageMaker, configName, modelName, sm.InstanceType, sm.ScaleToZero, smTags); err != nil {
		return fmt.Errorf("sagemaker up: endpoint-config: %w", err)
	}
	st.Set("SAGEMAKER_EPCONFIG_NAME", configName)

	// 3. Ensure Endpoint.
	if err := ensureSageMakerEndpoint(ctx, clients.SageMaker, endpointName, configName, smTags); err != nil {
		return fmt.Errorf("sagemaker up: endpoint: %w", err)
	}
	st.Set("SAGEMAKER_ENDPOINT_NAME", endpointName)

	return st.Save()
}

// PhaseSageMakerDown deletes the SageMaker Endpoint → EndpointConfig → Model →
// execution IAM role. Best-effort: tolerates ValidationException /
// ResourceNotFound / NoSuchEntity for each resource (already deleted). On
// state-loss, falls back to the name-derived defaults.
func PhaseSageMakerDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[sagemaker down] cluster=%s\n", name)

	endpointName := stateOrDefault(st, "SAGEMAKER_ENDPOINT_NAME", name+"-lmi")
	configName := stateOrDefault(st, "SAGEMAKER_EPCONFIG_NAME", name+"-lmi-epconfig")
	modelName := stateOrDefault(st, "SAGEMAKER_MODEL_NAME", name+"-lmi-model")
	execRoleName := stateOrDefault(st, "SAGEMAKER_EXEC_ROLE_NAME", name+"-sagemaker-execution-role")

	// Delete in reverse order: Endpoint first (bills money), then Config, then Model.
	if err := deleteSageMakerEndpoint(ctx, clients.SageMaker, endpointName); err != nil {
		return fmt.Errorf("sagemaker down: endpoint: %w", err)
	}
	st.Set("SAGEMAKER_ENDPOINT_NAME", "")

	if err := deleteSageMakerEndpointConfig(ctx, clients.SageMaker, configName); err != nil {
		return fmt.Errorf("sagemaker down: endpoint-config: %w", err)
	}
	st.Set("SAGEMAKER_EPCONFIG_NAME", "")

	if err := deleteSageMakerModel(ctx, clients.SageMaker, modelName); err != nil {
		return fmt.Errorf("sagemaker down: model: %w", err)
	}
	st.Set("SAGEMAKER_MODEL_NAME", "")

	// Delete execution role (best-effort: tolerates NoSuchEntity).
	// deleteRole detaches all managed policies before deleting the role so the
	// AmazonSageMakerFullAccess attachment is cleaned up automatically.
	fmt.Fprintf(os.Stderr, "[sagemaker down] deleting execution role %s\n", execRoleName)
	if err := deleteRole(ctx, clients.IAM, execRoleName); err != nil && !isNoSuchEntity(err) {
		return fmt.Errorf("sagemaker down: execution role: %w", err)
	}
	st.Set("SAGEMAKER_EXEC_ROLE_NAME", "")

	return st.Save()
}

// --- helpers ---

// isGatedModel returns true when the model ID looks like a gated HuggingFace
// model that requires an access token (heuristic: starts with "meta-llama/").
func isGatedModel(modelID string) bool {
	return len(modelID) >= 10 && modelID[:10] == "meta-llama"
}

func ensureSageMakerModel(ctx context.Context, iamClient IAMAPI, sm SageMakerAPI, modelName, imageURI, hfModelID, execRoleARN string, smTags []sagemaker_types.Tag) error {
	_, err := sm.DescribeModel(ctx, &sagemaker.DescribeModelInput{ModelName: ptr(modelName)})
	if err == nil {
		fmt.Fprintf(os.Stderr, "[sagemaker] model %s already exists, skipping\n", modelName)
		return nil
	}
	if !isSageMakerNotFound(err) {
		return fmt.Errorf("DescribeModel %s: %w", modelName, err)
	}

	hfToken := os.Getenv("HF_TOKEN")
	if hfToken == "" && isGatedModel(hfModelID) {
		fmt.Fprintf(os.Stderr, "[sagemaker] WARNING: HF_TOKEN is not set and model %q appears to be gated — "+
			"the model pull will fail at runtime unless you have accepted the license via an alternative auth method\n", hfModelID)
	}

	modelEnv := map[string]string{
		"HF_MODEL_ID":   hfModelID,
		"ROLLING_BATCH": "vllm",
	}
	if hfToken != "" {
		modelEnv["HF_TOKEN"] = hfToken
	}

	input := &sagemaker.CreateModelInput{
		ModelName:        ptr(modelName),
		ExecutionRoleArn: ptr(execRoleARN),
		PrimaryContainer: &sagemaker_types.ContainerDefinition{
			Image:       ptr(imageURI),
			Environment: modelEnv,
		},
		Tags: smTags,
	}

	// IAM roles propagate eventually — CreateModel may return a ValidationException
	// ("The execution role arn must be specified" or "is not authorized") for a
	// short window after the role is created. Retry with backoff to account for
	// IAM eventual consistency (mirrors Phase07/Phase18 propagation handling).
	const maxAttempts = 5
	backoff := 5 * time.Second
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		fmt.Fprintf(os.Stderr, "[sagemaker] creating model %s (model=%s, attempt=%d)\n", modelName, hfModelID, attempt)
		_, err = sm.CreateModel(ctx, input)
		if err == nil {
			return nil
		}
		if isIAMPropagationError(err) && attempt < maxAttempts {
			fmt.Fprintf(os.Stderr, "[sagemaker] execution role not yet propagated, retrying in %s\n", backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}
		return fmt.Errorf("CreateModel %s: %w", modelName, err)
	}
	return fmt.Errorf("CreateModel %s: %w", modelName, err)
}

// isIAMPropagationError returns true when the SageMaker error is a transient
// ValidationException caused by IAM eventual consistency — i.e., the role was
// just created but SageMaker cannot yet verify it. The caller should retry.
func isIAMPropagationError(err error) bool {
	if err == nil {
		return false
	}
	type coder interface{ ErrorCode() string }
	type messager interface{ ErrorMessage() string }
	var code, msg string
	e := err
	for e != nil {
		if ce, ok := e.(coder); ok {
			code = ce.ErrorCode()
		}
		if me, ok := e.(messager); ok {
			msg = me.ErrorMessage()
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	if code != "ValidationException" {
		return false
	}
	// Known transient IAM-propagation message fragments from SageMaker.
	return strings.Contains(msg, "execution role") ||
		strings.Contains(msg, "is not authorized") ||
		strings.Contains(msg, "cannot be assumed")
}

func ensureSageMakerEndpointConfig(ctx context.Context, sm SageMakerAPI, configName, modelName, instanceType string, scaleToZero bool, smTags []sagemaker_types.Tag) error {
	_, err := sm.DescribeEndpointConfig(ctx, &sagemaker.DescribeEndpointConfigInput{EndpointConfigName: ptr(configName)})
	if err == nil {
		fmt.Fprintf(os.Stderr, "[sagemaker] endpoint-config %s already exists, skipping\n", configName)
		return nil
	}
	if !isSageMakerNotFound(err) {
		return fmt.Errorf("DescribeEndpointConfig %s: %w", configName, err)
	}

	fmt.Fprintf(os.Stderr, "[sagemaker] creating endpoint-config %s (instance=%s, scaleToZero=%v)\n", configName, instanceType, scaleToZero)

	variantName := "AllTraffic"
	input := &sagemaker.CreateEndpointConfigInput{
		EndpointConfigName: ptr(configName),
		ProductionVariants: []sagemaker_types.ProductionVariant{
			{
				VariantName:          ptr(variantName),
				ModelName:            ptr(modelName),
				InstanceType:         sagemaker_types.ProductionVariantInstanceType(instanceType),
				InitialInstanceCount: int32Ptr(1),
			},
		},
		Tags: smTags,
	}

	if scaleToZero {
		// Managed instance scaling with MinInstanceCount=0 lets the endpoint
		// scale to zero when idle. MaxInstanceCount=1 caps provisioned instances
		// to match the demo use-case (single-instance budget control).
		input.ProductionVariants[0].InitialInstanceCount = nil
		input.ProductionVariants[0].ManagedInstanceScaling = &sagemaker_types.ProductionVariantManagedInstanceScaling{
			Status:           sagemaker_types.ManagedInstanceScalingStatusEnabled,
			MinInstanceCount: int32Ptr(0),
			MaxInstanceCount: int32Ptr(1),
		}
	}

	_, err = sm.CreateEndpointConfig(ctx, input)
	if err != nil {
		return fmt.Errorf("CreateEndpointConfig %s: %w", configName, err)
	}
	return nil
}

func ensureSageMakerEndpoint(ctx context.Context, sm SageMakerAPI, endpointName, configName string, smTags []sagemaker_types.Tag) error {
	desc, err := sm.DescribeEndpoint(ctx, &sagemaker.DescribeEndpointInput{EndpointName: ptr(endpointName)})
	if err == nil {
		switch desc.EndpointStatus {
		case sagemaker_types.EndpointStatusInService:
			fmt.Fprintf(os.Stderr, "[sagemaker] endpoint %s already InService, skipping\n", endpointName)
			return nil
		case sagemaker_types.EndpointStatusCreating:
			fmt.Fprintf(os.Stderr, "[sagemaker] endpoint %s is Creating (async, not waiting)\n", endpointName)
			return nil
		case sagemaker_types.EndpointStatusFailed:
			return fmt.Errorf("endpoint %s is in Failed status — check SageMaker console", endpointName)
		default:
			fmt.Fprintf(os.Stderr, "[sagemaker] endpoint %s status=%s (not waiting)\n", endpointName, desc.EndpointStatus)
			return nil
		}
	}
	if !isSageMakerNotFound(err) {
		return fmt.Errorf("DescribeEndpoint %s: %w", endpointName, err)
	}

	fmt.Fprintf(os.Stderr, "[sagemaker] creating endpoint %s (config=%s)\n", endpointName, configName)
	_, err = sm.CreateEndpoint(ctx, &sagemaker.CreateEndpointInput{
		EndpointName:       ptr(endpointName),
		EndpointConfigName: ptr(configName),
		Tags:               smTags,
	})
	if err != nil {
		return fmt.Errorf("CreateEndpoint %s: %w", endpointName, err)
	}
	fmt.Fprintf(os.Stderr, "[sagemaker] endpoint %s create request sent (async creation, check console)\n", endpointName)
	return nil
}

func deleteSageMakerEndpoint(ctx context.Context, sm SageMakerAPI, name string) error {
	fmt.Fprintf(os.Stderr, "[sagemaker down] deleting endpoint %s\n", name)
	_, err := sm.DeleteEndpoint(ctx, &sagemaker.DeleteEndpointInput{EndpointName: ptr(name)})
	if err != nil {
		if isSageMakerNotFound(err) {
			fmt.Fprintf(os.Stderr, "[sagemaker down] endpoint %s already gone\n", name)
			return nil
		}
		return fmt.Errorf("DeleteEndpoint %s: %w", name, err)
	}
	return nil
}

func deleteSageMakerEndpointConfig(ctx context.Context, sm SageMakerAPI, name string) error {
	fmt.Fprintf(os.Stderr, "[sagemaker down] deleting endpoint-config %s\n", name)
	_, err := sm.DeleteEndpointConfig(ctx, &sagemaker.DeleteEndpointConfigInput{EndpointConfigName: ptr(name)})
	if err != nil {
		if isSageMakerNotFound(err) {
			fmt.Fprintf(os.Stderr, "[sagemaker down] endpoint-config %s already gone\n", name)
			return nil
		}
		return fmt.Errorf("DeleteEndpointConfig %s: %w", name, err)
	}
	return nil
}

func deleteSageMakerModel(ctx context.Context, sm SageMakerAPI, name string) error {
	fmt.Fprintf(os.Stderr, "[sagemaker down] deleting model %s\n", name)
	_, err := sm.DeleteModel(ctx, &sagemaker.DeleteModelInput{ModelName: ptr(name)})
	if err != nil {
		if isSageMakerNotFound(err) {
			fmt.Fprintf(os.Stderr, "[sagemaker down] model %s already gone\n", name)
			return nil
		}
		return fmt.Errorf("DeleteModel %s: %w", name, err)
	}
	return nil
}

// isSageMakerNotFound returns true when err is a SageMaker ValidationException
// with "Could not find" or a ResourceNotFound variant. SageMaker uses
// ValidationException (not a dedicated NotFound type) when a named resource
// does not exist — hence the string check alongside the type check.
func isSageMakerNotFound(err error) bool {
	if err == nil {
		return false
	}
	type coder interface{ ErrorCode() string }
	var c coder
	e := err
	for e != nil {
		if ce, ok := e.(coder); ok {
			c = ce
			break
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	if c != nil {
		switch c.ErrorCode() {
		case "ValidationException", "ResourceNotFound", "ResourceNotFoundException":
			return true
		}
	}
	return false
}

// smTagsFromMaps merges tag maps into []sagemaker_types.Tag (SageMaker uses its
// own tag type, distinct from ec2types.Tag).
func smTagsFromMaps(maps ...map[string]string) []sagemaker_types.Tag {
	merged := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	out := make([]sagemaker_types.Tag, 0, len(merged))
	for k, v := range merged {
		k, v := k, v
		out = append(out, sagemaker_types.Tag{Key: &k, Value: &v})
	}
	return out
}

// stateOrDefault returns the state value for key, or fallback when the value is empty.
func stateOrDefault(st *state.State, key, fallback string) string {
	if v := st.Get(key); v != "" {
		return v
	}
	return fallback
}
