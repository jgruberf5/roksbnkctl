package phases

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	sagemaker_types "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

// mockDescribeEndpointResponse holds a scripted DescribeEndpoint response.
// When the queue is non-empty, each DescribeEndpoint call consumes one entry
// in order; once drained it falls back to the normal in-memory endpoints map.
type mockDescribeEndpointResponse struct {
	out *sagemaker.DescribeEndpointOutput
	err error
}

// mockSageMaker is the test double for SageMakerAPI.
// Records calls and drives idempotency / NotFound error injection.
type mockSageMaker struct {
	// In-memory state registries.
	models       map[string]*sagemaker_types.ModelSummary
	endpointCfgs map[string]*sagemaker.DescribeEndpointConfigOutput
	endpoints    map[string]*sagemaker.DescribeEndpointOutput

	// Scripted DescribeEndpoint response queue (consumed in order before
	// falling back to the in-memory endpoints map). Add entries via
	// enqueueDescribeEndpoint to drive stateful describe sequences in tests.
	describeEndpointQueue []mockDescribeEndpointResponse

	// Captured inputs for assertion in tests.
	createModelInput          *sagemaker.CreateModelInput
	createEndpointConfigInput *sagemaker.CreateEndpointConfigInput
	createEndpointInput       *sagemaker.CreateEndpointInput

	// Per-method call counts.
	createModelCalls          int
	deleteModelCalls          int
	createEndpointConfigCalls int
	deleteEndpointConfigCalls int
	createEndpointCalls       int
	deleteEndpointCalls       int
	describeEndpointCalls     int
}

func newMockSageMaker() *mockSageMaker {
	return &mockSageMaker{
		models:       make(map[string]*sagemaker_types.ModelSummary),
		endpointCfgs: make(map[string]*sagemaker.DescribeEndpointConfigOutput),
		endpoints:    make(map[string]*sagemaker.DescribeEndpointOutput),
	}
}

// enqueueDescribeEndpoint appends a scripted DescribeEndpoint response.
// Entries are consumed in FIFO order; once drained, calls fall through to the
// in-memory endpoints map. Use this to simulate multi-step status sequences
// (e.g. Failed → NotFound → Creating) without mutating the registry directly.
func (m *mockSageMaker) enqueueDescribeEndpoint(out *sagemaker.DescribeEndpointOutput, err error) {
	m.describeEndpointQueue = append(m.describeEndpointQueue, mockDescribeEndpointResponse{out: out, err: err})
}

func mkSageMakerNotFound(resource, name string) error {
	msg := fmt.Sprintf("Could not find %s %q", resource, name)
	return &sagemaker_types.ResourceNotFound{Message: &msg}
}

func (m *mockSageMaker) CreateModel(_ context.Context, in *sagemaker.CreateModelInput, _ ...func(*sagemaker.Options)) (*sagemaker.CreateModelOutput, error) {
	m.createModelCalls++
	m.createModelInput = in
	name := *in.ModelName
	m.models[name] = &sagemaker_types.ModelSummary{ModelName: in.ModelName}
	return &sagemaker.CreateModelOutput{ModelArn: ptr("arn:aws:sagemaker:ap-southeast-2:111122223333:model/" + name)}, nil
}

func (m *mockSageMaker) DescribeModel(_ context.Context, in *sagemaker.DescribeModelInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeModelOutput, error) {
	name := *in.ModelName
	if _, ok := m.models[name]; !ok {
		return nil, mkSageMakerNotFound("model", name)
	}
	return &sagemaker.DescribeModelOutput{ModelName: in.ModelName}, nil
}

func (m *mockSageMaker) DeleteModel(_ context.Context, in *sagemaker.DeleteModelInput, _ ...func(*sagemaker.Options)) (*sagemaker.DeleteModelOutput, error) {
	m.deleteModelCalls++
	name := *in.ModelName
	if _, ok := m.models[name]; !ok {
		return nil, mkSageMakerNotFound("model", name)
	}
	delete(m.models, name)
	return &sagemaker.DeleteModelOutput{}, nil
}

func (m *mockSageMaker) CreateEndpointConfig(_ context.Context, in *sagemaker.CreateEndpointConfigInput, _ ...func(*sagemaker.Options)) (*sagemaker.CreateEndpointConfigOutput, error) {
	m.createEndpointConfigCalls++
	m.createEndpointConfigInput = in
	name := *in.EndpointConfigName
	m.endpointCfgs[name] = &sagemaker.DescribeEndpointConfigOutput{
		EndpointConfigName: in.EndpointConfigName,
		ProductionVariants: in.ProductionVariants,
	}
	return &sagemaker.CreateEndpointConfigOutput{
		EndpointConfigArn: ptr("arn:aws:sagemaker:ap-southeast-2:111122223333:endpoint-config/" + name),
	}, nil
}

func (m *mockSageMaker) DescribeEndpointConfig(_ context.Context, in *sagemaker.DescribeEndpointConfigInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointConfigOutput, error) {
	name := *in.EndpointConfigName
	out, ok := m.endpointCfgs[name]
	if !ok {
		return nil, mkSageMakerNotFound("endpoint-config", name)
	}
	return out, nil
}

func (m *mockSageMaker) DeleteEndpointConfig(_ context.Context, in *sagemaker.DeleteEndpointConfigInput, _ ...func(*sagemaker.Options)) (*sagemaker.DeleteEndpointConfigOutput, error) {
	m.deleteEndpointConfigCalls++
	name := *in.EndpointConfigName
	if _, ok := m.endpointCfgs[name]; !ok {
		return nil, mkSageMakerNotFound("endpoint-config", name)
	}
	delete(m.endpointCfgs, name)
	return &sagemaker.DeleteEndpointConfigOutput{}, nil
}

func (m *mockSageMaker) CreateEndpoint(_ context.Context, in *sagemaker.CreateEndpointInput, _ ...func(*sagemaker.Options)) (*sagemaker.CreateEndpointOutput, error) {
	m.createEndpointCalls++
	m.createEndpointInput = in
	name := *in.EndpointName
	status := sagemaker_types.EndpointStatusCreating
	m.endpoints[name] = &sagemaker.DescribeEndpointOutput{
		EndpointName:       in.EndpointName,
		EndpointConfigName: in.EndpointConfigName,
		EndpointStatus:     status,
	}
	return &sagemaker.CreateEndpointOutput{EndpointArn: ptr("arn:aws:sagemaker:ap-southeast-2:111122223333:endpoint/" + name)}, nil
}

func (m *mockSageMaker) DescribeEndpoint(_ context.Context, in *sagemaker.DescribeEndpointInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointOutput, error) {
	m.describeEndpointCalls++
	// Consume from the scripted queue first (supports stateful test sequences).
	if len(m.describeEndpointQueue) > 0 {
		resp := m.describeEndpointQueue[0]
		m.describeEndpointQueue = m.describeEndpointQueue[1:]
		return resp.out, resp.err
	}
	name := *in.EndpointName
	out, ok := m.endpoints[name]
	if !ok {
		return nil, mkSageMakerNotFound("endpoint", name)
	}
	return out, nil
}

func (m *mockSageMaker) DeleteEndpoint(_ context.Context, in *sagemaker.DeleteEndpointInput, _ ...func(*sagemaker.Options)) (*sagemaker.DeleteEndpointOutput, error) {
	m.deleteEndpointCalls++
	name := *in.EndpointName
	if _, ok := m.endpoints[name]; !ok {
		return nil, mkSageMakerNotFound("endpoint", name)
	}
	delete(m.endpoints, name)
	return &sagemaker.DeleteEndpointOutput{}, nil
}

func (m *mockSageMaker) ListTags(_ context.Context, _ *sagemaker.ListTagsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListTagsOutput, error) {
	return &sagemaker.ListTagsOutput{}, nil
}
