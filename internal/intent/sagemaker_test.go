package intent

import (
	"strings"
	"testing"
)

// minimalYAMLWithSageMaker builds a cluster.yaml string with ai.sagemaker wired in.
func sageMakerYAML(smBlock string) string {
	return minimalYAML + smBlock
}

// TestSageMaker_DisabledByDefault verifies that omitting the ai: block leaves
// SageMakerEnabled() false and existing examples are unaffected.
func TestSageMaker_DisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", minimalYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SageMakerEnabled() {
		t.Error("SageMakerEnabled() = true for cluster without ai: block, want false")
	}
	if c.AI != nil {
		t.Error("AI field should be nil when ai: block is absent")
	}
}

// TestSageMaker_EnabledParsesFields verifies that the full ai.sagemaker block
// round-trips through Load with correct values and defaults.
func TestSageMaker_EnabledParsesFields(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    instanceType: ml.g5.2xlarge
    scaleToZero: true
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.SageMakerEnabled() {
		t.Error("SageMakerEnabled() = false, want true")
	}
	sm := c.AI.SageMaker
	if sm.Model != "meta-llama/Meta-Llama-3-8B-Instruct" {
		t.Errorf("Model = %q, want meta-llama/Meta-Llama-3-8B-Instruct", sm.Model)
	}
	if sm.InstanceType != "ml.g5.2xlarge" {
		t.Errorf("InstanceType = %q, want ml.g5.2xlarge", sm.InstanceType)
	}
	if !sm.ScaleToZero {
		t.Error("ScaleToZero = false, want true")
	}
}

// TestSageMaker_DefaultInstanceType verifies that instanceType defaults to
// ml.g5.2xlarge when omitted from an enabled block.
func TestSageMaker_DefaultInstanceType(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AI.SageMaker.InstanceType != "ml.g5.2xlarge" {
		t.Errorf("default InstanceType = %q, want ml.g5.2xlarge", c.AI.SageMaker.InstanceType)
	}
}

// TestSageMaker_EnabledFalseIsNoop verifies that ai.sagemaker.enabled: false
// is not an error and SageMakerEnabled() returns false.
func TestSageMaker_EnabledFalseIsNoop(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: false
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SageMakerEnabled() {
		t.Error("SageMakerEnabled() = true for enabled: false, want false")
	}
}

// TestSageMaker_MissingModelRejected verifies that enabled: true without model
// is a validation error.
func TestSageMaker_MissingModelRejected(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for missing model, got nil")
	}
	if !strings.Contains(err.Error(), "ai.sagemaker.model") {
		t.Errorf("error %q should mention ai.sagemaker.model", err.Error())
	}
}

// TestSageMaker_BadInstanceTypeRejected verifies that an invalid instanceType
// (missing ml. prefix) is rejected.
func TestSageMaker_BadInstanceTypeRejected(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    instanceType: g5.2xlarge
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for bad instanceType 'g5.2xlarge', got nil")
	}
	if !strings.Contains(err.Error(), "instanceType") {
		t.Errorf("error %q should mention instanceType", err.Error())
	}
}

// TestSageMaker_ImageURIOverrideParsed verifies that the optional imageUri field
// round-trips through Load: when set it is preserved verbatim; when absent the
// field is empty (zero value) and existing examples are unaffected.
func TestSageMaker_ImageURIOverrideParsed(t *testing.T) {
	const customURI = "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-lmi:v1.0"
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    imageUri: ` + customURI + `
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AI.SageMaker.ImageURI != customURI {
		t.Errorf("ImageURI = %q, want %q", c.AI.SageMaker.ImageURI, customURI)
	}
}

// TestSageMaker_ImageURIAbsentByDefault verifies that imageUri is empty when
// omitted — the override is strictly opt-in and does not affect normal usage.
func TestSageMaker_ImageURIAbsentByDefault(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AI.SageMaker.ImageURI != "" {
		t.Errorf("ImageURI = %q, want empty (no override set)", c.AI.SageMaker.ImageURI)
	}
}

// TestSageMaker_UnknownFieldRejected verifies strict YAML decoding rejects
// unknown fields in the ai.sagemaker block.
func TestSageMaker_UnknownFieldRejected(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    unknownField: oops
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected strict decode error for unknown field, got nil")
	}
}

// TestSageMaker_ExistingExamplesUnaffected verifies that the five non-AI-rig
// example cluster.yaml files load cleanly and do NOT enable SageMaker.
// The ai-rig example is intentionally excluded here — it opts in to SageMaker
// (tested separately in TestSageMaker_AIRigExampleEnabled).
func TestSageMaker_ExistingExamplesUnaffected(t *testing.T) {
	examples := []string{
		"../../examples/external-only/cluster.yaml",
		"../../examples/sriov-external/cluster.yaml",
		"../../examples/tracer/cluster.yaml",
		"../../examples/demo/cluster.yaml",
		"../../examples/full-cluster/cluster.yaml",
	}
	for _, path := range examples {
		t.Run(path, func(t *testing.T) {
			c, err := Load(path)
			if err != nil {
				t.Errorf("Load(%s): %v", path, err)
				return
			}
			// Non-AI-rig examples must not have SageMaker enabled.
			if c.SageMakerEnabled() {
				t.Errorf("Load(%s): SageMakerEnabled() = true, expected false for existing examples", path)
			}
		})
	}
}

// TestSageMaker_AIRigExampleEnabled verifies that examples/ai-rig/cluster.yaml
// loads cleanly and has SageMaker explicitly enabled with the expected fields.
func TestSageMaker_AIRigExampleEnabled(t *testing.T) {
	c, err := Load("../../examples/ai-rig/cluster.yaml")
	if err != nil {
		t.Fatalf("Load(ai-rig/cluster.yaml): %v", err)
	}
	if !c.SageMakerEnabled() {
		t.Error("ai-rig/cluster.yaml: SageMakerEnabled() = false, want true")
	}
	sm := c.AI.SageMaker
	if sm.Model != "meta-llama/Meta-Llama-3-8B-Instruct" {
		t.Errorf("ai-rig model = %q, want meta-llama/Meta-Llama-3-8B-Instruct", sm.Model)
	}
	if sm.InstanceType != "ml.g5.xlarge" {
		t.Errorf("ai-rig instanceType = %q, want ml.g5.xlarge", sm.InstanceType)
	}
	if sm.ScaleToZero {
		t.Error("ai-rig scaleToZero = true, want false")
	}
}

// ---------------------------------------------------------------------------
// GPU-sizing preflight tests (TensorParallelSize / MaxModelLen)
// ---------------------------------------------------------------------------

// TestSageMaker_TPUnset_G5Xlarge verifies that the default 8B config on
// ml.g5.xlarge (the live example instance) passes with tensorParallelSize unset.
// G1 non-regression: the GPU-sizing rule must NOT engage when TP is zero.
func TestSageMaker_TPUnset_G5Xlarge(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    instanceType: ml.g5.xlarge
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err != nil {
		t.Fatalf("ml.g5.xlarge with TP unset should pass, got: %v", err)
	}
}

// TestSageMaker_TPUnset_G5_2Xlarge verifies that the field default ml.g5.2xlarge
// (1 GPU) passes with tensorParallelSize unset. G1 non-regression.
func TestSageMaker_TPUnset_G5_2Xlarge(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    instanceType: ml.g5.2xlarge
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err != nil {
		t.Fatalf("ml.g5.2xlarge with TP unset should pass, got: %v", err)
	}
}

// TestSageMaker_TP4_G5_2Xlarge_Rejected verifies that ml.g5.2xlarge (1 GPU) +
// tensorParallelSize: 4 is rejected by the GPU-sizing preflight.
func TestSageMaker_TP4_G5_2Xlarge_Rejected(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    instanceType: ml.g5.2xlarge
    tensorParallelSize: 4
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("ml.g5.2xlarge + tensorParallelSize=4 should fail (1 GPU < 4), got nil")
	}
	if !strings.Contains(err.Error(), "tensorParallelSize") {
		t.Errorf("error %q should mention tensorParallelSize", err.Error())
	}
	if !strings.Contains(err.Error(), "ml.g5.2xlarge") {
		t.Errorf("error %q should name the instance ml.g5.2xlarge", err.Error())
	}
}

// TestSageMaker_TP40_Rejected verifies that tensorParallelSize > 8 is rejected
// (no g5/g6 SKU has more than 8 GPUs).
func TestSageMaker_TP40_Rejected(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    instanceType: ml.g5.48xlarge
    tensorParallelSize: 40
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("tensorParallelSize=40 should fail (>8), got nil")
	}
	if !strings.Contains(err.Error(), "tensorParallelSize") {
		t.Errorf("error %q should mention tensorParallelSize", err.Error())
	}
}

// TestSageMaker_TPNegative_Rejected verifies that tensorParallelSize < 0 is rejected.
func TestSageMaker_TPNegative_Rejected(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    instanceType: ml.g5.xlarge
    tensorParallelSize: -1
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("tensorParallelSize=-1 should fail, got nil")
	}
	if !strings.Contains(err.Error(), "tensorParallelSize") {
		t.Errorf("error %q should mention tensorParallelSize", err.Error())
	}
}

// TestSageMaker_MaxModelLenNegative_Rejected verifies that maxModelLen < 0 is rejected.
func TestSageMaker_MaxModelLenNegative_Rejected(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    instanceType: ml.g5.xlarge
    maxModelLen: -100
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("maxModelLen=-100 should fail, got nil")
	}
	if !strings.Contains(err.Error(), "maxModelLen") {
		t.Errorf("error %q should mention maxModelLen", err.Error())
	}
}

// TestSageMaker_UnknownInstance_TPSet_FailOpen verifies that an unknown instance
// type (not in smInstanceGPUCount) with tensorParallelSize set passes validation.
// This is the fail-open contract: unknown instances skip the GPU check entirely.
func TestSageMaker_UnknownInstance_TPSet_FailOpen(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: meta-llama/Meta-Llama-3-8B-Instruct
    instanceType: ml.p5.48xlarge
    tensorParallelSize: 4
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err != nil {
		t.Fatalf("unknown instance type ml.p5.48xlarge with TP=4 should pass (fail-open), got: %v", err)
	}
}

// TestSageMaker_Qwen32B_G5_12xlarge_Passes verifies that the canonical Qwen-32B
// example config (ml.g5.12xlarge + tensorParallelSize: 4 + maxModelLen: 8192) passes
// validation. ml.g5.12xlarge has 4 GPUs; TP=4 == 4 → at the limit, passes.
func TestSageMaker_Qwen32B_G5_12xlarge_Passes(t *testing.T) {
	yaml := sageMakerYAML(`
ai:
  sagemaker:
    enabled: true
    model: Qwen/Qwen2.5-32B-Instruct
    instanceType: ml.g5.12xlarge
    tensorParallelSize: 4
    maxModelLen: 8192
    scaleToZero: false
`)
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Qwen-32B example config should pass validation, got: %v", err)
	}
	sm := c.AI.SageMaker
	if sm.TensorParallelSize != 4 {
		t.Errorf("TensorParallelSize = %d, want 4", sm.TensorParallelSize)
	}
	if sm.MaxModelLen != 8192 {
		t.Errorf("MaxModelLen = %d, want 8192", sm.MaxModelLen)
	}
}
