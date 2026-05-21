package phases

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// mockIAM is a test double for IAMAPI.
// Counters track mutating calls; in-memory registries simulate the AWS side.
type mockIAM struct {
	// In-memory state.
	roles            map[string]*iamtypes.Role            // role name → role
	profiles         map[string]*iamtypes.InstanceProfile // profile name → profile
	attachedPolicies map[string]map[string]bool           // role name → set of attached policy ARNs
	inlinePolicies   map[string][]string                  // role name → slice of inline policy names
	oidcProviders    map[string]string                    // oidc provider ARN → url

	// Per-method call counts.
	createRoleCalls               int
	deleteRoleCalls               int
	createInstanceProfileCalls    int
	deleteInstanceProfileCalls    int
	addRoleToInstanceProfileCalls int
	attachRolePolicyCalls         int
	detachRolePolicyCalls         int
	putRolePolicyCalls            int
	deleteRolePolicyCalls         int
	createOIDCCalls               int
	deleteOIDCCalls               int

	// Configurable errors.
	createRoleErr               error
	getRoleErr                  error
	getInstanceProfileErr       error
	addRoleToInstanceProfileErr error
	getOIDCErr                  error
	createOIDCErr               error
}

func newMockIAM() *mockIAM {
	return &mockIAM{
		roles:            make(map[string]*iamtypes.Role),
		profiles:         make(map[string]*iamtypes.InstanceProfile),
		attachedPolicies: make(map[string]map[string]bool),
		inlinePolicies:   make(map[string][]string),
		oidcProviders:    make(map[string]string),
	}
}

// mkNoSuchEntity returns an *iamtypes.NoSuchEntityException for testing.
func mkNoSuchEntity(msg string) error {
	return &iamtypes.NoSuchEntityException{Message: &msg}
}

func (m *mockIAM) GetRole(_ context.Context, in *iam.GetRoleInput, _ ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	if m.getRoleErr != nil {
		return nil, m.getRoleErr
	}
	role, ok := m.roles[*in.RoleName]
	if !ok {
		return nil, mkNoSuchEntity("role not found: " + *in.RoleName)
	}
	return &iam.GetRoleOutput{Role: role}, nil
}

func (m *mockIAM) CreateRole(_ context.Context, in *iam.CreateRoleInput, _ ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
	m.createRoleCalls++
	if m.createRoleErr != nil {
		return nil, m.createRoleErr
	}
	arn := "arn:aws:iam::111122223333:role/" + *in.RoleName
	role := &iamtypes.Role{RoleName: in.RoleName, Arn: &arn}
	m.roles[*in.RoleName] = role
	m.attachedPolicies[*in.RoleName] = make(map[string]bool)
	m.inlinePolicies[*in.RoleName] = nil
	return &iam.CreateRoleOutput{Role: role}, nil
}

func (m *mockIAM) DeleteRole(_ context.Context, in *iam.DeleteRoleInput, _ ...func(*iam.Options)) (*iam.DeleteRoleOutput, error) {
	m.deleteRoleCalls++
	delete(m.roles, *in.RoleName)
	delete(m.attachedPolicies, *in.RoleName)
	delete(m.inlinePolicies, *in.RoleName)
	return &iam.DeleteRoleOutput{}, nil
}

func (m *mockIAM) ListAttachedRolePolicies(_ context.Context, in *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	if _, ok := m.roles[*in.RoleName]; !ok {
		return nil, mkNoSuchEntity("role not found: " + *in.RoleName)
	}
	arns := m.attachedPolicies[*in.RoleName]
	policies := make([]iamtypes.AttachedPolicy, 0, len(arns))
	for arn := range arns {
		arn := arn
		policies = append(policies, iamtypes.AttachedPolicy{PolicyArn: &arn})
	}
	return &iam.ListAttachedRolePoliciesOutput{AttachedPolicies: policies}, nil
}

func (m *mockIAM) AttachRolePolicy(_ context.Context, in *iam.AttachRolePolicyInput, _ ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
	m.attachRolePolicyCalls++
	if m.attachedPolicies[*in.RoleName] == nil {
		m.attachedPolicies[*in.RoleName] = make(map[string]bool)
	}
	m.attachedPolicies[*in.RoleName][*in.PolicyArn] = true
	return &iam.AttachRolePolicyOutput{}, nil
}

func (m *mockIAM) DetachRolePolicy(_ context.Context, in *iam.DetachRolePolicyInput, _ ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error) {
	m.detachRolePolicyCalls++
	if arns, ok := m.attachedPolicies[*in.RoleName]; ok {
		delete(arns, *in.PolicyArn)
	}
	return &iam.DetachRolePolicyOutput{}, nil
}

func (m *mockIAM) ListRolePolicies(_ context.Context, in *iam.ListRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	if _, ok := m.roles[*in.RoleName]; !ok {
		return nil, mkNoSuchEntity("role not found: " + *in.RoleName)
	}
	names := m.inlinePolicies[*in.RoleName]
	if names == nil {
		names = []string{}
	}
	return &iam.ListRolePoliciesOutput{PolicyNames: names}, nil
}

func (m *mockIAM) PutRolePolicy(_ context.Context, in *iam.PutRolePolicyInput, _ ...func(*iam.Options)) (*iam.PutRolePolicyOutput, error) {
	m.putRolePolicyCalls++
	// Add to inline policies if not already present.
	for _, name := range m.inlinePolicies[*in.RoleName] {
		if name == *in.PolicyName {
			return &iam.PutRolePolicyOutput{}, nil
		}
	}
	m.inlinePolicies[*in.RoleName] = append(m.inlinePolicies[*in.RoleName], *in.PolicyName)
	return &iam.PutRolePolicyOutput{}, nil
}

func (m *mockIAM) DeleteRolePolicy(_ context.Context, in *iam.DeleteRolePolicyInput, _ ...func(*iam.Options)) (*iam.DeleteRolePolicyOutput, error) {
	m.deleteRolePolicyCalls++
	policies := m.inlinePolicies[*in.RoleName]
	for i, name := range policies {
		if name == *in.PolicyName {
			m.inlinePolicies[*in.RoleName] = append(policies[:i], policies[i+1:]...)
			break
		}
	}
	return &iam.DeleteRolePolicyOutput{}, nil
}

func (m *mockIAM) GetInstanceProfile(_ context.Context, in *iam.GetInstanceProfileInput, _ ...func(*iam.Options)) (*iam.GetInstanceProfileOutput, error) {
	if m.getInstanceProfileErr != nil {
		return nil, m.getInstanceProfileErr
	}
	profile, ok := m.profiles[*in.InstanceProfileName]
	if !ok {
		return nil, mkNoSuchEntity("instance profile not found: " + *in.InstanceProfileName)
	}
	return &iam.GetInstanceProfileOutput{InstanceProfile: profile}, nil
}

func (m *mockIAM) CreateInstanceProfile(_ context.Context, in *iam.CreateInstanceProfileInput, _ ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
	m.createInstanceProfileCalls++
	arn := "arn:aws:iam::111122223333:instance-profile/" + *in.InstanceProfileName
	profile := &iamtypes.InstanceProfile{
		InstanceProfileName: in.InstanceProfileName,
		Arn:                 &arn,
	}
	m.profiles[*in.InstanceProfileName] = profile
	return &iam.CreateInstanceProfileOutput{InstanceProfile: profile}, nil
}

func (m *mockIAM) DeleteInstanceProfile(_ context.Context, in *iam.DeleteInstanceProfileInput, _ ...func(*iam.Options)) (*iam.DeleteInstanceProfileOutput, error) {
	m.deleteInstanceProfileCalls++
	delete(m.profiles, *in.InstanceProfileName)
	return &iam.DeleteInstanceProfileOutput{}, nil
}

func (m *mockIAM) AddRoleToInstanceProfile(_ context.Context, in *iam.AddRoleToInstanceProfileInput, _ ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error) {
	m.addRoleToInstanceProfileCalls++
	if m.addRoleToInstanceProfileErr != nil {
		return nil, m.addRoleToInstanceProfileErr
	}
	if profile, ok := m.profiles[*in.InstanceProfileName]; ok {
		roleName := *in.RoleName
		profile.Roles = append(profile.Roles, iamtypes.Role{RoleName: &roleName})
	}
	return &iam.AddRoleToInstanceProfileOutput{}, nil
}

func (m *mockIAM) RemoveRoleFromInstanceProfile(_ context.Context, in *iam.RemoveRoleFromInstanceProfileInput, _ ...func(*iam.Options)) (*iam.RemoveRoleFromInstanceProfileOutput, error) {
	if profile, ok := m.profiles[*in.InstanceProfileName]; ok {
		filtered := profile.Roles[:0]
		for _, r := range profile.Roles {
			if r.RoleName == nil || *r.RoleName != *in.RoleName {
				filtered = append(filtered, r)
			}
		}
		profile.Roles = filtered
	}
	return &iam.RemoveRoleFromInstanceProfileOutput{}, nil
}

func (m *mockIAM) ListInstanceProfilesForRole(_ context.Context, in *iam.ListInstanceProfilesForRoleInput, _ ...func(*iam.Options)) (*iam.ListInstanceProfilesForRoleOutput, error) {
	var result []iamtypes.InstanceProfile
	for _, profile := range m.profiles {
		for _, r := range profile.Roles {
			if r.RoleName != nil && *r.RoleName == *in.RoleName {
				result = append(result, *profile)
				break
			}
		}
	}
	return &iam.ListInstanceProfilesForRoleOutput{InstanceProfiles: result}, nil
}

func (m *mockIAM) TagRole(_ context.Context, _ *iam.TagRoleInput, _ ...func(*iam.Options)) (*iam.TagRoleOutput, error) {
	return &iam.TagRoleOutput{}, nil
}

func (m *mockIAM) TagInstanceProfile(_ context.Context, _ *iam.TagInstanceProfileInput, _ ...func(*iam.Options)) (*iam.TagInstanceProfileOutput, error) {
	return &iam.TagInstanceProfileOutput{}, nil
}

// OIDC provider stubs (slice 7+).

func (m *mockIAM) CreateOpenIDConnectProvider(_ context.Context, in *iam.CreateOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.CreateOpenIDConnectProviderOutput, error) {
	m.createOIDCCalls++
	if m.createOIDCErr != nil {
		return nil, m.createOIDCErr
	}
	url := ""
	if in.Url != nil {
		url = *in.Url
	}
	arn := "arn:aws:iam::111122223333:oidc-provider/" + url
	m.oidcProviders[arn] = url
	return &iam.CreateOpenIDConnectProviderOutput{OpenIDConnectProviderArn: &arn}, nil
}

func (m *mockIAM) GetOpenIDConnectProvider(_ context.Context, in *iam.GetOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.GetOpenIDConnectProviderOutput, error) {
	if m.getOIDCErr != nil {
		return nil, m.getOIDCErr
	}
	arn := ""
	if in.OpenIDConnectProviderArn != nil {
		arn = *in.OpenIDConnectProviderArn
	}
	url, ok := m.oidcProviders[arn]
	if !ok {
		msg := fmt.Sprintf("OIDC provider not found: %s", arn)
		return nil, &iamtypes.NoSuchEntityException{Message: &msg}
	}
	return &iam.GetOpenIDConnectProviderOutput{Url: &url}, nil
}

func (m *mockIAM) DeleteOpenIDConnectProvider(_ context.Context, in *iam.DeleteOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.DeleteOpenIDConnectProviderOutput, error) {
	m.deleteOIDCCalls++
	arn := ""
	if in.OpenIDConnectProviderArn != nil {
		arn = *in.OpenIDConnectProviderArn
	}
	delete(m.oidcProviders, arn)
	return &iam.DeleteOpenIDConnectProviderOutput{}, nil
}

func (m *mockIAM) TagOpenIDConnectProvider(_ context.Context, _ *iam.TagOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.TagOpenIDConnectProviderOutput, error) {
	return &iam.TagOpenIDConnectProviderOutput{}, nil
}
