package phases

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// mockSSM is the test double for SSMAPI.
type mockSSM struct {
	getParameterOut   *ssm.GetParameterOutput
	getParameterErr   error
	getParameterCalls int
}

func (m *mockSSM) GetParameter(_ context.Context, _ *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	m.getParameterCalls++
	if m.getParameterErr != nil {
		return nil, m.getParameterErr
	}
	if m.getParameterOut != nil {
		return m.getParameterOut, nil
	}
	amiID := "ami-mock-al2023"
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: &amiID},
	}, nil
}
