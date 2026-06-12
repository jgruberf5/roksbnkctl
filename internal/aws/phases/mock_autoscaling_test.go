package phases

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

// mockAutoScaling is the test double for AutoScalingAPI.
//
// It maintains a map of ASG-name → Activity slices so tests can pre-populate
// failure/success signals. DescribeAutoScalingGroups uses the same ASG map,
// keyed by the "kubernetes.io/cluster/<name>" tag filter that phase10 uses.
// The instance count in each ASG entry drives the "no instance launched" check.
type mockAutoScaling struct {
	// asgs maps ASG name → AutoScalingGroup descriptor (includes Instances slice).
	asgs map[string]*astypes.AutoScalingGroup

	// activities maps ASG name → scaling activities to return.
	activities map[string][]astypes.Activity

	// tagIndex maps EKS nodegroup tag (kubernetes.io/cluster/<cluster>/nodegroup/<ng>)
	// → ASG name, so DescribeAutoScalingGroups can find the right ASG by tag filter.
	tagIndex map[string]string

	// Call counters for assertion.
	describeGroupCalls    int
	describeActivityCalls int
}

func newMockAutoScaling() *mockAutoScaling {
	return &mockAutoScaling{
		asgs:       make(map[string]*astypes.AutoScalingGroup),
		activities: make(map[string][]astypes.Activity),
		tagIndex:   make(map[string]string),
	}
}

// addASG registers an ASG in the mock. instanceCount controls how many instances
// are in-service (0 = nothing launched yet). activities are returned by
// DescribeScalingActivities in the order provided.
func (m *mockAutoScaling) addASG(asgName string, instanceCount int, acts []astypes.Activity) {
	instances := make([]astypes.Instance, instanceCount)
	for i := range instances {
		id := "i-mock-0000000" + string(rune('0'+i))
		instances[i] = astypes.Instance{InstanceId: &id, LifecycleState: astypes.LifecycleStateInService}
	}
	m.asgs[asgName] = &astypes.AutoScalingGroup{
		AutoScalingGroupName: &asgName,
		Instances:            instances,
	}
	m.activities[asgName] = acts
}

// linkNodegroup registers the mapping that DescribeAutoScalingGroups uses when
// phase10 queries by the EKS managed-nodegroup tag key/value pair. EKS sets:
//
//	Key:   "eks:nodegroup-name"
//	Value: <nodegroup name>
//
// and we look for the ASG whose tag matches. The mock implements a simplified
// version: callers register tag-value → ASG-name pairs via addTagIndex.
func (m *mockAutoScaling) addTagIndex(tagValue, asgName string) {
	m.tagIndex[tagValue] = asgName
}

// DescribeAutoScalingGroups finds the ASG matching the filter tags. Phase10
// uses filters of the form:
//
//	Filter{Name:"tag:eks:nodegroup-name", Values:["<ngName>"]}
//
// The mock resolves via tagIndex[filterValue] → ASG name.
func (m *mockAutoScaling) DescribeAutoScalingGroups(
	_ context.Context,
	in *autoscaling.DescribeAutoScalingGroupsInput,
	_ ...func(*autoscaling.Options),
) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	m.describeGroupCalls++

	// If explicit ASG names requested, look those up directly.
	if len(in.AutoScalingGroupNames) > 0 {
		var groups []astypes.AutoScalingGroup
		for _, name := range in.AutoScalingGroupNames {
			if asg, ok := m.asgs[name]; ok {
				groups = append(groups, *asg)
			}
		}
		return &autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: groups}, nil
	}

	// Filter-based lookup: find the ASG whose tag index matches.
	for _, f := range in.Filters {
		if f.Name == nil {
			continue
		}
		for _, v := range f.Values {
			if asgName, ok := m.tagIndex[v]; ok {
				if asg, ok2 := m.asgs[asgName]; ok2 {
					return &autoscaling.DescribeAutoScalingGroupsOutput{
						AutoScalingGroups: []astypes.AutoScalingGroup{*asg},
					}, nil
				}
			}
		}
	}
	return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
}

// DescribeScalingActivities returns the pre-seeded activities for the given ASG.
func (m *mockAutoScaling) DescribeScalingActivities(
	_ context.Context,
	in *autoscaling.DescribeScalingActivitiesInput,
	_ ...func(*autoscaling.Options),
) (*autoscaling.DescribeScalingActivitiesOutput, error) {
	m.describeActivityCalls++
	var acts []astypes.Activity
	if in.AutoScalingGroupName != nil {
		acts = m.activities[*in.AutoScalingGroupName]
	}
	return &autoscaling.DescribeScalingActivitiesOutput{Activities: acts}, nil
}

// mkFailedActivity returns a scaling activity with StatusCode=Failed and the
// given message string — used by tests to simulate capacity errors.
func mkFailedActivity(msg string) astypes.Activity {
	id := "activity-" + msg[:min(8, len(msg))]
	return astypes.Activity{
		ActivityId:           &id,
		AutoScalingGroupName: ptr("asg-placeholder"),
		Cause:                ptr("test cause"),
		StartTime:            nil,
		StatusCode:           astypes.ScalingActivityStatusCodeFailed,
		StatusMessage:        &msg,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
