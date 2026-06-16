package phases

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	sagemaker_types "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// makeSageMakerCluster builds a minimal *intent.Cluster with SageMaker enabled.
func makeSageMakerCluster(scaleToZero bool) *intent.Cluster {
	return &intent.Cluster{
		Metadata: intent.Metadata{
			Name:   "test-rig",
			Region: "ap-southeast-2",
		},
		AI: &intent.AISpec{
			SageMaker: &intent.SageMakerSpec{
				Enabled:      true,
				Model:        "meta-llama/Meta-Llama-3-8B-Instruct",
				InstanceType: "ml.g5.2xlarge",
				ScaleToZero:  scaleToZero,
			},
		},
	}
}

// makeTestState creates a temp-dir state for tests.
func makeTestState(t *testing.T) *state.State {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	return st
}

// makeSageMakerClients returns a *Clients with both SageMaker and IAM mocks set.
func makeSageMakerClients(sm *mockSageMaker, iamMock *mockIAM) *Clients {
	return &Clients{SageMaker: sm, IAM: iamMock}
}

// TestPhaseSageMakerUp_HappyPath verifies the full create path: execution role +
// Model → EndpointConfig → Endpoint each get exactly one Create call, and state
// keys are written. The CreateModel input must carry a non-empty ExecutionRoleArn.
func TestPhaseSageMakerUp_HappyPath(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	iamMock := newMockIAM()
	clients := makeSageMakerClients(sm, iamMock)

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	// Execution role must have been created.
	if iamMock.createRoleCalls != 1 {
		t.Errorf("createRoleCalls = %d, want 1 (execution role)", iamMock.createRoleCalls)
	}
	if sm.createModelCalls != 1 {
		t.Errorf("createModelCalls = %d, want 1", sm.createModelCalls)
	}
	if sm.createEndpointConfigCalls != 1 {
		t.Errorf("createEndpointConfigCalls = %d, want 1", sm.createEndpointConfigCalls)
	}
	if sm.createEndpointCalls != 1 {
		t.Errorf("createEndpointCalls = %d, want 1", sm.createEndpointCalls)
	}

	// ExecutionRoleArn must be non-empty on CreateModel.
	if sm.createModelInput == nil {
		t.Fatal("createModelInput is nil")
	}
	if sm.createModelInput.ExecutionRoleArn == nil || *sm.createModelInput.ExecutionRoleArn == "" {
		t.Error("CreateModel: ExecutionRoleArn is empty — SageMaker will reject this")
	}

	// State keys written.
	if st.Get("SAGEMAKER_EXEC_ROLE_NAME") == "" {
		t.Error("SAGEMAKER_EXEC_ROLE_NAME not set in state")
	}
	if st.Get("SAGEMAKER_MODEL_NAME") == "" {
		t.Error("SAGEMAKER_MODEL_NAME not set in state")
	}
	if st.Get("SAGEMAKER_EPCONFIG_NAME") == "" {
		t.Error("SAGEMAKER_EPCONFIG_NAME not set in state")
	}
	if st.Get("SAGEMAKER_ENDPOINT_NAME") == "" {
		t.Error("SAGEMAKER_ENDPOINT_NAME not set in state")
	}
}

// TestPhaseSageMakerUp_Idempotent verifies that running up twice only creates
// each resource once (Describe returns existing → skip create). The execution
// role must also be created only once (GetRole returns it on the second run).
func TestPhaseSageMakerUp_Idempotent(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	iamMock := newMockIAM()
	clients := makeSageMakerClients(sm, iamMock)

	// First run — creates all resources.
	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("first up: %v", err)
	}

	// Advance the endpoint to InService so the second run finds it healthy.
	endpointName := st.Get("SAGEMAKER_ENDPOINT_NAME")
	sm.endpoints[endpointName].EndpointStatus = sagemaker_types.EndpointStatusInService

	// Second run — everything already exists, no creates.
	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("second up: %v", err)
	}

	if iamMock.createRoleCalls != 1 {
		t.Errorf("idempotency: createRoleCalls = %d, want 1 (execution role created once)", iamMock.createRoleCalls)
	}
	if sm.createModelCalls != 1 {
		t.Errorf("idempotency: createModelCalls = %d, want 1 (no second create)", sm.createModelCalls)
	}
	if sm.createEndpointConfigCalls != 1 {
		t.Errorf("idempotency: createEndpointConfigCalls = %d, want 1", sm.createEndpointConfigCalls)
	}
	if sm.createEndpointCalls != 1 {
		t.Errorf("idempotency: createEndpointCalls = %d, want 1", sm.createEndpointCalls)
	}
}

// TestPhaseSageMakerDown_HappyPath verifies that down deletes Endpoint →
// EndpointConfig → Model → execution IAM role in order and clears state keys.
func TestPhaseSageMakerDown_HappyPath(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	iamMock := newMockIAM()
	clients := makeSageMakerClients(sm, iamMock)

	// Pre-populate via up.
	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("up: %v", err)
	}

	if err := PhaseSageMakerDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("PhaseSageMakerDown: %v", err)
	}

	if sm.deleteEndpointCalls != 1 {
		t.Errorf("deleteEndpointCalls = %d, want 1", sm.deleteEndpointCalls)
	}
	if sm.deleteEndpointConfigCalls != 1 {
		t.Errorf("deleteEndpointConfigCalls = %d, want 1", sm.deleteEndpointConfigCalls)
	}
	if sm.deleteModelCalls != 1 {
		t.Errorf("deleteModelCalls = %d, want 1", sm.deleteModelCalls)
	}
	// Execution role must be deleted.
	if iamMock.deleteRoleCalls != 1 {
		t.Errorf("deleteRoleCalls = %d, want 1 (execution role deleted)", iamMock.deleteRoleCalls)
	}

	// State keys cleared.
	if v := st.Get("SAGEMAKER_ENDPOINT_NAME"); v != "" {
		t.Errorf("SAGEMAKER_ENDPOINT_NAME = %q after down, want empty", v)
	}
	if v := st.Get("SAGEMAKER_MODEL_NAME"); v != "" {
		t.Errorf("SAGEMAKER_MODEL_NAME = %q after down, want empty", v)
	}
	if v := st.Get("SAGEMAKER_EXEC_ROLE_NAME"); v != "" {
		t.Errorf("SAGEMAKER_EXEC_ROLE_NAME = %q after down, want empty", v)
	}
}

// TestPhaseSageMakerDown_ToleratesNotFound verifies that down is best-effort:
// calling down on a cluster where resources were already deleted (or never
// created) must not return an error — including the execution role.
func TestPhaseSageMakerDown_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker() // empty — nothing pre-populated
	iamMock := newMockIAM()  // empty — role does not exist
	clients := makeSageMakerClients(sm, iamMock)

	// No prior up — all Describe/Delete calls will return NotFound.
	if err := PhaseSageMakerDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("down on non-existent resources: %v", err)
	}
}

// TestPhaseSageMakerDown_ToleratesPartialState verifies that down handles
// state-loss (state keys missing) by falling back to name-derived defaults
// and tolerating NotFound — including the execution role.
func TestPhaseSageMakerDown_ToleratesPartialState(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	// Only endpoint key set; model and config missing — simulates partial state.
	st.Set("SAGEMAKER_ENDPOINT_NAME", "test-rig-lmi")
	// Registry is empty — resources already gone.
	sm := newMockSageMaker()
	iamMock := newMockIAM()
	clients := makeSageMakerClients(sm, iamMock)

	if err := PhaseSageMakerDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("down with partial state: %v", err)
	}
}

// TestPhaseSageMakerUp_ScaleToZero verifies the scale-to-zero endpoint-config
// shape: ManagedInstanceScaling.MinInstanceCount must be 0.
func TestPhaseSageMakerUp_ScaleToZero(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(true)
	st := makeTestState(t)
	sm := newMockSageMaker()
	clients := makeSageMakerClients(sm, newMockIAM())

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp scaleToZero: %v", err)
	}

	cfg := sm.createEndpointConfigInput
	if cfg == nil {
		t.Fatal("createEndpointConfigInput is nil — CreateEndpointConfig was not called")
	}
	if len(cfg.ProductionVariants) == 0 {
		t.Fatal("ProductionVariants is empty")
	}
	v := cfg.ProductionVariants[0]
	if v.ManagedInstanceScaling == nil {
		t.Fatal("scaleToZero=true: ManagedInstanceScaling must be set")
	}
	if v.ManagedInstanceScaling.MinInstanceCount == nil || *v.ManagedInstanceScaling.MinInstanceCount != 0 {
		t.Errorf("scaleToZero: MinInstanceCount = %v, want 0", v.ManagedInstanceScaling.MinInstanceCount)
	}
	if v.ManagedInstanceScaling.Status != sagemaker_types.ManagedInstanceScalingStatusEnabled {
		t.Errorf("scaleToZero: Status = %v, want Enabled", v.ManagedInstanceScaling.Status)
	}
	// InitialInstanceCount must be nil when scaleToZero (SDK rejects if set + managed scaling).
	if v.InitialInstanceCount != nil {
		t.Errorf("scaleToZero: InitialInstanceCount = %v, want nil", *v.InitialInstanceCount)
	}
}

// TestPhaseSageMakerUp_ModelEnvVars verifies the LMI container environment:
// HF_MODEL_ID must be set to the configured model; ROLLING_BATCH must be "vllm".
func TestPhaseSageMakerUp_ModelEnvVars(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	clients := makeSageMakerClients(sm, newMockIAM())

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	if sm.createModelInput == nil {
		t.Fatal("createModelInput is nil")
	}
	env := sm.createModelInput.PrimaryContainer.Environment
	if env["HF_MODEL_ID"] != cl.AI.SageMaker.Model {
		t.Errorf("HF_MODEL_ID = %q, want %q", env["HF_MODEL_ID"], cl.AI.SageMaker.Model)
	}
	if env["ROLLING_BATCH"] != "vllm" {
		t.Errorf("ROLLING_BATCH = %q, want vllm", env["ROLLING_BATCH"])
	}
}

// TestPhaseSageMakerUp_DefaultImageURI verifies that when no imageUri override is
// set, CreateModel uses the default DLC image URI with the cluster region substituted.
func TestPhaseSageMakerUp_DefaultImageURI(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	clients := makeSageMakerClients(sm, newMockIAM())

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	if sm.createModelInput == nil {
		t.Fatal("createModelInput is nil")
	}
	wantImage := "763104351884.dkr.ecr.ap-southeast-2.amazonaws.com/djl-inference:0.36.0-lmi25.0.0-cu130"
	if got := *sm.createModelInput.PrimaryContainer.Image; got != wantImage {
		t.Errorf("default image URI = %q, want %q", got, wantImage)
	}
}

// TestPhaseSageMakerUp_ImageURIOverride verifies that when imageUri is set in
// SageMakerSpec, CreateModel uses the override verbatim, bypassing the default
// DLC construction entirely.
func TestPhaseSageMakerUp_ImageURIOverride(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	cl.AI.SageMaker.ImageURI = "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-custom-lmi:v1.2.3"
	st := makeTestState(t)
	sm := newMockSageMaker()
	clients := makeSageMakerClients(sm, newMockIAM())

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	if sm.createModelInput == nil {
		t.Fatal("createModelInput is nil")
	}
	wantImage := "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-custom-lmi:v1.2.3"
	if got := *sm.createModelInput.PrimaryContainer.Image; got != wantImage {
		t.Errorf("override image URI = %q, want %q", got, wantImage)
	}
}

// TestPhaseSageMakerUp_HFToken verifies that when HF_TOKEN is set in the
// environment, the CreateModel input's container environment carries
// HF_TOKEN with that value.
func TestPhaseSageMakerUp_HFToken(t *testing.T) {
	awsmw.ResetForTest()
	t.Setenv("HF_TOKEN", "hf_testtoken123")
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	clients := makeSageMakerClients(sm, newMockIAM())

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	if sm.createModelInput == nil {
		t.Fatal("createModelInput is nil")
	}
	env := sm.createModelInput.PrimaryContainer.Environment
	if got := env["HF_TOKEN"]; got != "hf_testtoken123" {
		t.Errorf("HF_TOKEN in model env = %q, want %q", got, "hf_testtoken123")
	}
	// Core vars must still be present.
	if env["HF_MODEL_ID"] != cl.AI.SageMaker.Model {
		t.Errorf("HF_MODEL_ID = %q, want %q", env["HF_MODEL_ID"], cl.AI.SageMaker.Model)
	}
	if env["ROLLING_BATCH"] != "vllm" {
		t.Errorf("ROLLING_BATCH = %q, want vllm", env["ROLLING_BATCH"])
	}
}

// TestPhaseSageMakerUp_HFTokenAbsent verifies that when HF_TOKEN is unset,
// the CreateModel env does NOT contain HF_TOKEN (no empty key).
func TestPhaseSageMakerUp_HFTokenAbsent(t *testing.T) {
	awsmw.ResetForTest()
	// Ensure HF_TOKEN is absent for this test.
	t.Setenv("HF_TOKEN", "")
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	clients := makeSageMakerClients(sm, newMockIAM())

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	if sm.createModelInput == nil {
		t.Fatal("createModelInput is nil")
	}
	env := sm.createModelInput.PrimaryContainer.Environment
	if _, ok := env["HF_TOKEN"]; ok {
		t.Errorf("HF_TOKEN present in model env when unset, want absent (value=%q)", env["HF_TOKEN"])
	}
}

// TestPhaseSageMakerUp_Tags verifies that the CreateModel call carries the
// expected awsbnkctl:cluster and awsbnkctl:managed tags.
func TestPhaseSageMakerUp_Tags(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	cl.Tags = map[string]string{"env": "test"}
	st := makeTestState(t)
	sm := newMockSageMaker()
	clients := makeSageMakerClients(sm, newMockIAM())

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	if sm.createModelInput == nil {
		t.Fatal("createModelInput is nil")
	}
	tagMap := make(map[string]string)
	for _, tag := range sm.createModelInput.Tags {
		if tag.Key != nil && tag.Value != nil {
			tagMap[*tag.Key] = *tag.Value
		}
	}
	if tagMap["awsbnkctl:cluster"] != cl.Metadata.Name {
		t.Errorf("tag awsbnkctl:cluster = %q, want %q", tagMap["awsbnkctl:cluster"], cl.Metadata.Name)
	}
	if tagMap["awsbnkctl:managed"] != "true" {
		t.Errorf("tag awsbnkctl:managed = %q, want true", tagMap["awsbnkctl:managed"])
	}
	if tagMap["env"] != "test" {
		t.Errorf("tag env = %q, want test", tagMap["env"])
	}
}

// TestPhaseSageMakerUp_DryRun verifies that dry-run writes placeholder state
// without making any SDK calls (neither SageMaker nor IAM).
func TestPhaseSageMakerUp_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	iamMock := newMockIAM()
	clients := makeSageMakerClients(sm, iamMock)

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("dry-run up: %v", err)
	}

	if sm.createModelCalls != 0 || sm.createEndpointConfigCalls != 0 || sm.createEndpointCalls != 0 {
		t.Errorf("dry-run: unexpected SageMaker SDK calls (model=%d, config=%d, endpoint=%d)",
			sm.createModelCalls, sm.createEndpointConfigCalls, sm.createEndpointCalls)
	}
	if iamMock.createRoleCalls != 0 {
		t.Errorf("dry-run: unexpected IAM createRoleCalls = %d, want 0", iamMock.createRoleCalls)
	}
	// Placeholder state should be written.
	if st.Get("SAGEMAKER_EXEC_ROLE_NAME") == "" {
		t.Error("dry-run: SAGEMAKER_EXEC_ROLE_NAME not set")
	}
	if st.Get("SAGEMAKER_ENDPOINT_NAME") == "" {
		t.Error("dry-run: SAGEMAKER_ENDPOINT_NAME not set")
	}
}

// TestPhaseSageMakerUp_ExecutionRoleARNPassedToModel verifies the core fix:
// CreateModel must receive a non-empty ExecutionRoleArn matching the role
// that was created by ensureRole.
func TestPhaseSageMakerUp_ExecutionRoleARNPassedToModel(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	iamMock := newMockIAM()
	clients := makeSageMakerClients(sm, iamMock)

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	if sm.createModelInput == nil {
		t.Fatal("CreateModel was not called")
	}
	if sm.createModelInput.ExecutionRoleArn == nil || *sm.createModelInput.ExecutionRoleArn == "" {
		t.Fatal("CreateModel.ExecutionRoleArn is empty — fix is not applied")
	}
	// The ARN must reference the role name we expect.
	wantRoleName := cl.Metadata.Name + "-sagemaker-execution-role"
	if !strings.Contains(*sm.createModelInput.ExecutionRoleArn, wantRoleName) {
		t.Errorf("ExecutionRoleArn = %q, want it to contain %q", *sm.createModelInput.ExecutionRoleArn, wantRoleName)
	}
}

// --- B2 tests ---

// TestEnsureSageMakerModel_ServedModelName verifies that CreateModel carries
// OPTION_SERVED_MODEL_NAME == lmiServedModelName so the LMI endpoint serves the
// model under the same name as the in-cluster vLLM leg, enabling apples-to-apples
// forge benchmark comparison.
func TestEnsureSageMakerModel_ServedModelName(t *testing.T) {
	awsmw.ResetForTest()
	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	clients := makeSageMakerClients(sm, newMockIAM())

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	if sm.createModelInput == nil {
		t.Fatal("createModelInput is nil — CreateModel was not called")
	}
	env := sm.createModelInput.PrimaryContainer.Environment
	got, ok := env["OPTION_SERVED_MODEL_NAME"]
	if !ok {
		t.Fatal("OPTION_SERVED_MODEL_NAME not set in model environment")
	}
	if got != lmiServedModelName {
		t.Errorf("OPTION_SERVED_MODEL_NAME = %q, want %q", got, lmiServedModelName)
	}
	if lmiServedModelName != "llama3" {
		t.Errorf("lmiServedModelName const = %q, want \"llama3\"", lmiServedModelName)
	}
}

// --- B1 tests ---

// TestEnsureSageMakerEndpoint_FailedAutoRecovers verifies the IAM-propagation
// auto-recovery path: when DescribeEndpoint returns Failed, ensureSageMakerEndpoint
// must delete the endpoint, wait for NotFound, then recreate it — returning nil.
//
// Mock sequence for DescribeEndpoint:
//  1. Returns Failed (first Describe, triggers recovery)
//  2. Returns NotFound (deletion-wait poll — deletion complete)
//  3. Returns Creating (final Describe after recreate — create path saw NotFound
//     from the endpoint registry, then CreateEndpoint registered it as Creating)
//
// The mock's DeleteEndpoint removes from the registry, so the deletion-wait poll
// naturally falls through to NotFound from the registry (no queue entry needed).
func TestEnsureSageMakerEndpoint_FailedAutoRecovers(t *testing.T) {
	awsmw.ResetForTest()
	sm := newMockSageMaker()

	endpointName := "test-ep"
	configName := "test-cfg"

	// Pre-register the endpoint as Failed in the registry.
	sm.endpoints[endpointName] = &sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(endpointName),
		EndpointStatus: sagemaker_types.EndpointStatusFailed,
	}

	// Script the first Describe call to return Failed (consumed before registry).
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(endpointName),
		EndpointStatus: sagemaker_types.EndpointStatusFailed,
	}, nil)
	// After delete the registry entry is gone, so the deletion-wait poll will
	// naturally return NotFound from the registry (no additional queue needed).

	err := ensureSageMakerEndpoint(context.Background(), sm, endpointName, configName, nil)
	if err != nil {
		t.Fatalf("ensureSageMakerEndpoint: expected nil (auto-recovery), got %v", err)
	}

	// Delete must have been called once (the recovery delete).
	if sm.deleteEndpointCalls != 1 {
		t.Errorf("deleteEndpointCalls = %d, want 1", sm.deleteEndpointCalls)
	}
	// Create must have been called once (the recreate).
	if sm.createEndpointCalls != 1 {
		t.Errorf("createEndpointCalls = %d, want 1", sm.createEndpointCalls)
	}
}

// --- Bug 4 proactive IAM propagation wait tests ---

// TestPhaseSageMakerUp_FreshRole_WaitsForPropagation verifies that when the
// SageMaker execution role is freshly created on this up run, PhaseSageMakerUp
// calls smIAMPropagationWaitFn before CreateModel/CreateEndpoint. The test
// injects a no-op wait so no real sleep occurs, but records that the wait fired.
func TestPhaseSageMakerUp_FreshRole_WaitsForPropagation(t *testing.T) {
	awsmw.ResetForTest()

	waitCalled := false
	old := smIAMPropagationWaitFn
	smIAMPropagationWaitFn = func(_ context.Context) error {
		waitCalled = true
		return nil
	}
	defer func() { smIAMPropagationWaitFn = old }()

	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	iamMock := newMockIAM() // empty — role does not exist yet (fresh create)
	clients := makeSageMakerClients(sm, iamMock)

	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseSageMakerUp: %v", err)
	}

	if !waitCalled {
		t.Error("smIAMPropagationWaitFn was NOT called on a fresh role create — IAM propagation race unaddressed")
	}
	// CreateModel must still have been called (wait → proceed, not abort).
	if sm.createModelCalls != 1 {
		t.Errorf("createModelCalls = %d, want 1 (model created after wait)", sm.createModelCalls)
	}
}

// TestPhaseSageMakerUp_ExistingRole_SkipsWait verifies that when the SageMaker
// execution role already existed before up was called (idempotent re-run),
// PhaseSageMakerUp does NOT call smIAMPropagationWaitFn — keeping re-runs fast.
func TestPhaseSageMakerUp_ExistingRole_SkipsWait(t *testing.T) {
	awsmw.ResetForTest()

	waitCalled := false
	old := smIAMPropagationWaitFn
	smIAMPropagationWaitFn = func(_ context.Context) error {
		waitCalled = true
		return nil
	}
	defer func() { smIAMPropagationWaitFn = old }()

	cl := makeSageMakerCluster(false)
	st := makeTestState(t)
	sm := newMockSageMaker()
	iamMock := newMockIAM()
	clients := makeSageMakerClients(sm, iamMock)

	// First run creates the role and all resources.
	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("first up: %v", err)
	}

	// Advance endpoint to InService so the second run finds everything healthy.
	endpointName := st.Get("SAGEMAKER_ENDPOINT_NAME")
	sm.endpoints[endpointName].EndpointStatus = sagemaker_types.EndpointStatusInService

	// Reset the wait sentinel before the second run.
	waitCalled = false

	// Second run — role already exists; wait must NOT fire.
	if err := PhaseSageMakerUp(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("second up: %v", err)
	}

	if waitCalled {
		t.Error("smIAMPropagationWaitFn was called on an idempotent re-run — adds unnecessary latency")
	}
}

// TestEnsureSageMakerEndpoint_FailedAfterRecovery verifies that when the endpoint
// is still Failed after one recovery attempt, ensureSageMakerEndpoint returns a
// hard error so a genuinely-broken config surfaces rather than looping.
//
// Scripted DescribeEndpoint queue:
//  1. Failed (triggers first recovery delete+recreate)
//  2. NotFound error (deletion-wait poll — confirms deletion complete)
//  3. Failed (consumed by recovered=true inner call — triggers hard error)
func TestEnsureSageMakerEndpoint_FailedAfterRecovery(t *testing.T) {
	awsmw.ResetForTest()
	sm := newMockSageMaker()

	endpointName := "test-ep-bad"
	configName := "test-cfg"

	// Pre-register the endpoint so DeleteEndpoint succeeds (not NotFound).
	sm.endpoints[endpointName] = &sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(endpointName),
		EndpointStatus: sagemaker_types.EndpointStatusFailed,
	}

	// Entry 1: first Describe → Failed → triggers recovery.
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(endpointName),
		EndpointStatus: sagemaker_types.EndpointStatusFailed,
	}, nil)
	// Entry 2: deletion-wait poll → NotFound error → deletion confirmed.
	sm.enqueueDescribeEndpoint(nil, mkSageMakerNotFound("endpoint", endpointName))
	// Entry 3: recovered=true inner Describe → Failed → hard error returned.
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(endpointName),
		EndpointStatus: sagemaker_types.EndpointStatusFailed,
	}, nil)

	err := ensureSageMakerEndpoint(context.Background(), sm, endpointName, configName, nil)
	if err == nil {
		t.Fatal("expected an error for persistently-Failed endpoint after recovery, got nil")
	}
	if !strings.Contains(err.Error(), "Failed") {
		t.Errorf("error = %q, want it to mention Failed status", err.Error())
	}
}

// ---------------------------------------------------------------------------
// waitSageMakerEndpointInService tests
// ---------------------------------------------------------------------------

// TestWaitSageMakerEndpointInService_CreatingThenInService drives the mock
// through Creating → InService and asserts the waiter returns nil.
//
// Queue: Creating (poll 1) → InService (poll 2).
func TestWaitSageMakerEndpointInService_CreatingThenInService(t *testing.T) {
	sm := newMockSageMaker()
	ep := "test-ep"

	// Poll 1 → Creating.
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(ep),
		EndpointStatus: sagemaker_types.EndpointStatusCreating,
	}, nil)
	// Poll 2 → InService.
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(ep),
		EndpointStatus: sagemaker_types.EndpointStatusInService,
	}, nil)

	err := waitSageMakerEndpointInService(context.Background(), sm, ep, 30*time.Second, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("waitSageMakerEndpointInService: expected nil, got %v", err)
	}
}

// TestWaitSageMakerEndpointInService_Timeout asserts that an endpoint that
// never leaves Creating eventually causes a timeout error.
func TestWaitSageMakerEndpointInService_Timeout(t *testing.T) {
	sm := newMockSageMaker()
	ep := "test-ep-timeout"

	// Pre-register so DescribeEndpoint always returns Creating (queue exhausted
	// → falls through to in-memory registry).
	sm.endpoints[ep] = &sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(ep),
		EndpointStatus: sagemaker_types.EndpointStatusCreating,
	}

	err := waitSageMakerEndpointInService(context.Background(), sm, ep, 10*time.Millisecond, 1*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want it to mention timed out", err.Error())
	}
}

// TestWaitSageMakerEndpointInService_TerminalBad asserts that a terminal-bad
// status (Failed) causes an immediate error without waiting out the timeout.
func TestWaitSageMakerEndpointInService_TerminalBad(t *testing.T) {
	sm := newMockSageMaker()
	ep := "test-ep-failed"

	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(ep),
		EndpointStatus: sagemaker_types.EndpointStatusFailed,
	}, nil)

	// Use a long timeout — the error should arrive long before it expires.
	err := waitSageMakerEndpointInService(context.Background(), sm, ep, 10*time.Minute, 1*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for terminal-bad Failed status, got nil")
	}
	if !strings.Contains(err.Error(), "terminal status") {
		t.Errorf("error = %q, want 'terminal status'", err.Error())
	}
	if !strings.Contains(err.Error(), "Failed") {
		t.Errorf("error = %q, want it to mention Failed", err.Error())
	}
}

// ---------------------------------------------------------------------------
// RedeploySageMakerEndpointCold tests
// ---------------------------------------------------------------------------

// TestRedeploySageMakerEndpointCold_HappyPath drives the mock through the
// full cold-redeploy sequence and asserts the correct call order and counts.
//
// Queue:
//  1. DescribeEndpoint → InService (captures configName = "test-ep-config")
//  2. waitSageMakerEndpointDeleted poll → NotFound (deletion confirmed)
//  3. waitSageMakerEndpointInService poll → InService (no sleep: first poll returns InService)
//
// The test does NOT drive Creating → InService for the InService waiter to avoid
// the 15 s poll interval hardcoded in RedeploySageMakerEndpointCold. The
// Creating → InService transition is tested independently by
// TestWaitSageMakerEndpointInService_CreatingThenInService.
func TestRedeploySageMakerEndpointCold_HappyPath(t *testing.T) {
	sm := newMockSageMaker()
	ep := "test-ep"
	configName := "test-ep-config"

	// Pre-register so DeleteEndpoint finds the endpoint.
	sm.endpoints[ep] = &sagemaker.DescribeEndpointOutput{
		EndpointName:       ptr(ep),
		EndpointConfigName: ptr(configName),
		EndpointStatus:     sagemaker_types.EndpointStatusInService,
	}

	// Entry 1: initial DescribeEndpoint → InService with configName.
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:       ptr(ep),
		EndpointConfigName: ptr(configName),
		EndpointStatus:     sagemaker_types.EndpointStatusInService,
	}, nil)
	// Entry 2: deletion-wait poll → NotFound (deletion confirmed).
	sm.enqueueDescribeEndpoint(nil, mkSageMakerNotFound("endpoint", ep))
	// Entry 3: waitInService poll → InService immediately (no inter-poll sleep).
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(ep),
		EndpointStatus: sagemaker_types.EndpointStatusInService,
	}, nil)

	err := RedeploySageMakerEndpointCold(context.Background(), sm, ep)
	if err != nil {
		t.Fatalf("RedeploySageMakerEndpointCold: %v", err)
	}

	// DeleteEndpoint must have been called exactly once.
	if sm.deleteEndpointCalls != 1 {
		t.Errorf("deleteEndpointCalls = %d, want 1", sm.deleteEndpointCalls)
	}
	// CreateEndpoint must have been called exactly once.
	if sm.createEndpointCalls != 1 {
		t.Errorf("createEndpointCalls = %d, want 1", sm.createEndpointCalls)
	}
	// Endpoint NAME must be unchanged (forge target URL stays stable).
	if sm.createEndpointInput == nil {
		t.Fatal("createEndpointInput is nil")
	}
	if sm.createEndpointInput.EndpointName == nil || *sm.createEndpointInput.EndpointName != ep {
		t.Errorf("CreateEndpoint EndpointName = %v, want %q", sm.createEndpointInput.EndpointName, ep)
	}
	// EndpointConfigName must match the one captured from DescribeEndpoint
	// (not a hardcoded convention — proved by using a distinct name here).
	if sm.createEndpointInput.EndpointConfigName == nil || *sm.createEndpointInput.EndpointConfigName != configName {
		t.Errorf("CreateEndpoint EndpointConfigName = %v, want %q", sm.createEndpointInput.EndpointConfigName, configName)
	}
}

// TestRedeploySageMakerEndpointCold_WaitInServiceTerminalBad asserts that when
// the wait-InService waiter sees a terminal-bad status (Failed) after CreateEndpoint,
// the error propagates from RedeploySageMakerEndpointCold with "wait-InService".
//
// Queue:
//  1. initial Describe → InService (captures configName)
//  2. deletion-wait poll → NotFound (deletion confirmed)
//  3. waitInService poll → Failed (terminal-bad → immediate error)
func TestRedeploySageMakerEndpointCold_WaitInServiceTerminalBad(t *testing.T) {
	sm := newMockSageMaker()
	ep := "test-ep-is-fail"
	configName := "test-ep-is-fail-config"

	// Pre-register so DeleteEndpoint finds and removes the endpoint.
	sm.endpoints[ep] = &sagemaker.DescribeEndpointOutput{
		EndpointName:       ptr(ep),
		EndpointConfigName: ptr(configName),
		EndpointStatus:     sagemaker_types.EndpointStatusInService,
	}

	// Entry 1: initial Describe → InService.
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:       ptr(ep),
		EndpointConfigName: ptr(configName),
		EndpointStatus:     sagemaker_types.EndpointStatusInService,
	}, nil)
	// Entry 2: deletion-wait poll → NotFound (deletion confirmed).
	sm.enqueueDescribeEndpoint(nil, mkSageMakerNotFound("endpoint", ep))
	// Entry 3: waitInService poll → Failed (terminal-bad, immediate error).
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:   ptr(ep),
		EndpointStatus: sagemaker_types.EndpointStatusFailed,
	}, nil)

	err := RedeploySageMakerEndpointCold(context.Background(), sm, ep)
	if err == nil {
		t.Fatal("expected error when wait-InService hits terminal-bad status, got nil")
	}
	if !strings.Contains(err.Error(), "wait-InService") {
		t.Errorf("error = %q, want 'wait-InService'", err.Error())
	}
}

// TestRedeploySageMakerEndpointCold_WaitInServiceTimeout asserts that an
// InService-wait timeout propagates through RedeploySageMakerEndpointCold as a
// wrapped error containing "wait-InService".
//
// RedeploySageMakerEndpointCold calls waitSageMakerEndpointInService with a
// hardcoded 20m timeout and 15s interval — neither is injectable without
// changing the production signature. To exercise the timeout path without
// waiting real time, we pass a context whose deadline expires in 1ms.
// waitSageMakerEndpointInService selects on ctx.Done() during the inter-poll
// sleep and returns ctx.Err() (context.DeadlineExceeded) when the context
// expires, which RedeploySageMakerEndpointCold wraps as "wait-InService: ...".
//
// Queue:
//  1. initial DescribeEndpoint → InService (captures configName)
//  2. deletion-wait poll → NotFound (deletion confirmed)
//  3. (queue exhausted) — CreateEndpoint registers endpoint as Creating in the
//     registry; the InService waiter reads registry on each poll → Creating
//     → ctx deadline fires during the inter-poll sleep → returns deadline error.
func TestRedeploySageMakerEndpointCold_WaitInServiceTimeout(t *testing.T) {
	sm := newMockSageMaker()
	ep := "test-ep-is-timeout"
	configName := "test-ep-is-timeout-config"

	// Pre-register so DeleteEndpoint finds and removes the endpoint.
	sm.endpoints[ep] = &sagemaker.DescribeEndpointOutput{
		EndpointName:       ptr(ep),
		EndpointConfigName: ptr(configName),
		EndpointStatus:     sagemaker_types.EndpointStatusInService,
	}

	// Entry 1: initial Describe → InService (captures configName).
	sm.enqueueDescribeEndpoint(&sagemaker.DescribeEndpointOutput{
		EndpointName:       ptr(ep),
		EndpointConfigName: ptr(configName),
		EndpointStatus:     sagemaker_types.EndpointStatusInService,
	}, nil)
	// Entry 2: deletion-wait poll → NotFound (deletion confirmed).
	sm.enqueueDescribeEndpoint(nil, mkSageMakerNotFound("endpoint", ep))
	// After CreateEndpoint the mock registers ep as Creating in the registry.
	// The InService waiter polls the registry on every call (queue is now empty)
	// and keeps seeing Creating. The context deadline fires during the 15s
	// inter-poll sleep, cutting the wait short and returning ctx.Err().

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := RedeploySageMakerEndpointCold(ctx, sm, ep)
	if err == nil {
		t.Fatal("expected error from InService timeout through RedeploySageMakerEndpointCold, got nil")
	}
	// The error must be wrapped by the wait-InService stage — confirming the
	// timeout propagates through the full function (not swallowed or short-circuited).
	if !strings.Contains(err.Error(), "wait-InService") {
		t.Errorf("error = %q, want it to contain 'wait-InService'", err.Error())
	}
	// delete and create must still have fired (the timeout occurs in the post-create wait,
	// not in the delete/create steps themselves).
	if sm.deleteEndpointCalls != 1 {
		t.Errorf("deleteEndpointCalls = %d, want 1", sm.deleteEndpointCalls)
	}
	if sm.createEndpointCalls != 1 {
		t.Errorf("createEndpointCalls = %d, want 1", sm.createEndpointCalls)
	}
}

// TestRedeploySageMakerEndpointCold_DescribeEndpointError asserts that a
// non-NotFound API error from the initial DescribeEndpoint call propagates
// through RedeploySageMakerEndpointCold as a wrapped error containing
// "DescribeEndpoint", and that DeleteEndpoint and CreateEndpoint are never
// called (fail-closed: no mutation when the initial capture step errors).
func TestRedeploySageMakerEndpointCold_DescribeEndpointError(t *testing.T) {
	sm := newMockSageMaker()
	ep := "test-ep-api-err"

	// Enqueue a non-NotFound API error (e.g. AccessDeniedException) for the
	// initial DescribeEndpoint call.
	apiErr := fmt.Errorf("AccessDeniedException: User is not authorized to perform: sagemaker:DescribeEndpoint")
	sm.enqueueDescribeEndpoint(nil, apiErr)

	err := RedeploySageMakerEndpointCold(context.Background(), sm, ep)
	if err == nil {
		t.Fatal("expected error from DescribeEndpoint API failure, got nil")
	}
	// The error must be wrapped with "DescribeEndpoint" — the exact wrapping
	// applied by RedeploySageMakerEndpointCold: "cold redeploy %s: DescribeEndpoint: %w".
	if !strings.Contains(err.Error(), "DescribeEndpoint") {
		t.Errorf("error = %q, want it to contain 'DescribeEndpoint'", err.Error())
	}
	// Fail-closed: must not proceed to delete or create.
	if sm.deleteEndpointCalls != 0 {
		t.Errorf("deleteEndpointCalls = %d, want 0 (must not delete when initial Describe fails)", sm.deleteEndpointCalls)
	}
	if sm.createEndpointCalls != 0 {
		t.Errorf("createEndpointCalls = %d, want 0 (must not create when initial Describe fails)", sm.createEndpointCalls)
	}
}
