package intent

import (
	"fmt"
	"regexp"
)

// sageMakerInstanceTypeRE validates SageMaker instance type strings (e.g. ml.g5.2xlarge).
var sageMakerInstanceTypeRE = regexp.MustCompile(`^ml\.[a-z][0-9a-z]+\.[a-z0-9]+$`)

// AISpec is the top-level opt-in AI inference block in cluster.yaml.
// When absent (nil) or Enabled=false, all AI-related phases are skipped;
// existing cluster.yaml files that omit this block are completely unaffected.
//
// Example:
//
//	ai:
//	  sagemaker:
//	    enabled: true
//	    model: meta-llama/Meta-Llama-3-8B-Instruct
//	    instanceType: ml.g5.2xlarge
//	    scaleToZero: false
type AISpec struct {
	SageMaker *SageMakerSpec `yaml:"sagemaker,omitempty"`
}

// SageMakerSpec configures the disposable SageMaker LMI endpoint.
// When Enabled is false (or the ai.sagemaker block is omitted), no SageMaker
// resources are created. On up, three AWS resources are created in order:
// SageMaker Model → EndpointConfig → Endpoint. On down, they are deleted
// in reverse order so nothing bills between sessions.
//
// LMI container: DJL-Serving Large Model Inference (DJL 0.36.0 / LMI v25,
// serving engine IS vLLM 0.20.1). Model configuration is passed via
// environment variables (HF_MODEL_ID, ROLLING_BATCH=vllm).
// Instance default: ml.g5.2xlarge ($1.97/hr per spec).
type SageMakerSpec struct {
	// Enabled is the master switch. Default false — omitting the block or
	// setting enabled: false leaves the cluster unaffected.
	Enabled bool `yaml:"enabled"`
	// Model is the Hugging Face model ID, e.g. "meta-llama/Meta-Llama-3-8B-Instruct".
	// Required when Enabled is true.
	Model string `yaml:"model,omitempty"`
	// InstanceType for the SageMaker endpoint. Default "ml.g5.2xlarge".
	InstanceType string `yaml:"instanceType,omitempty"`
	// ScaleToZero configures a managed-instance-scaling endpoint-config with
	// MinInstanceCount=0 (scale to zero when idle). When false (default), the
	// endpoint keeps exactly one instance running.
	ScaleToZero bool `yaml:"scaleToZero,omitempty"`
	// ImageURI, when set, is used VERBATIM as the SageMaker model container image
	// (full URI including registry and tag), bypassing the default DLC construction.
	// Use this to pin a specific LMI DLC tag or substitute a private ECR image.
	// AWS periodically deprecates older DLC tags — set this field when the default
	// tag is no longer available rather than waiting for a code update.
	// Example: "763104351884.dkr.ecr.ap-southeast-2.amazonaws.com/djl-inference:0.36.0-lmi25.0.0-cu130"
	// When empty (default), the phase constructs the URI from the cluster region.
	ImageURI string `yaml:"imageUri,omitempty"`
}

// SageMakerEnabled reports whether the SageMaker endpoint should be
// provisioned. True only when ai.sagemaker.enabled is explicitly true.
func (c *Cluster) SageMakerEnabled() bool {
	return c.AI != nil && c.AI.SageMaker != nil && c.AI.SageMaker.Enabled
}

// validateSageMaker checks the ai.sagemaker block constraints.
// Called from validate() only when AI != nil && AI.SageMaker != nil && Enabled.
//
// Rules:
//   - model must be non-empty when enabled.
//   - instanceType must match instanceTypeRE when set.
func validateSageMaker(s *SageMakerSpec) error {
	if !s.Enabled {
		return nil
	}
	if s.Model == "" {
		return fmt.Errorf("ai.sagemaker.model is required when ai.sagemaker.enabled is true")
	}
	if s.InstanceType != "" && !sageMakerInstanceTypeRE.MatchString(s.InstanceType) {
		return fmt.Errorf("ai.sagemaker.instanceType %q does not match expected pattern (e.g. ml.g5.2xlarge)", s.InstanceType)
	}
	return nil
}

// applySageMakerDefaults fills zero-value SageMaker fields.
// Called from applyDefaults when ai.sagemaker is present.
func applySageMakerDefaults(s *SageMakerSpec) {
	if s.InstanceType == "" {
		s.InstanceType = "ml.g5.2xlarge"
	}
}
