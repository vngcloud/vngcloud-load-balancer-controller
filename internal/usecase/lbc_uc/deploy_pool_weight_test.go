package lbc_uc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// checkIfPoolMemberExist must treat weight as part of member equality, so that
// a weight change (e.g. 80 -> 20) is detected and an update is triggered.
func TestCheckIfPoolMemberExist_WeightAware(t *testing.T) {
	task := &defaultModelDeployTask{}

	tests := []struct {
		name   string
		list   []v1alpha1.PoolMember
		member v1alpha1.PoolMember
		want   bool
	}{
		{
			name: "same identity but different weight is not equal",
			list: []v1alpha1.PoolMember{
				{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80)},
			},
			member: v1alpha1.PoolMember{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(20)},
			want:   false,
		},
		{
			name: "same identity and same weight is equal",
			list: []v1alpha1.PoolMember{
				{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80)},
			},
			member: v1alpha1.PoolMember{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80)},
			want:   true,
		},
		{
			name: "nil weight equals default weight (1), avoiding a reconcile loop",
			list: []v1alpha1.PoolMember{
				{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(1)},
			},
			member: v1alpha1.PoolMember{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: nil},
			want:   true,
		},
		{
			name: "nil weight does not match a non-default weight",
			list: []v1alpha1.PoolMember{
				{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80)},
			},
			member: v1alpha1.PoolMember{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: nil},
			want:   false,
		},
		{
			name: "both nil weight is equal",
			list: []v1alpha1.PoolMember{
				{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: nil},
			},
			member: v1alpha1.PoolMember{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: nil},
			want:   true,
		},
		{
			name: "different identity is not equal",
			list: []v1alpha1.PoolMember{
				{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80)},
			},
			member: v1alpha1.PoolMember{IP: "10.0.0.2", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80)},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := task.checkIfPoolMemberExist(tt.list, &tt.member)
			assert.Equal(t, tt.want, got)
		})
	}
}

// checkIfPoolMemberExistByAddress matches purely on identity (IP+Port+MonitorPort),
// ignoring weight. It is used for de-duplication in mergePoolMembers and for the
// "can we cover this member" check during LB/pool deletion.
func TestCheckIfPoolMemberExistByAddress_IgnoresWeight(t *testing.T) {
	task := &defaultModelDeployTask{}

	list := []v1alpha1.PoolMember{
		{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80)},
	}

	// Same identity, different weight: still considered "exists by address".
	got := task.checkIfPoolMemberExistByAddress(list, &v1alpha1.PoolMember{
		IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(20),
	})
	assert.True(t, got, "weight must be ignored for identity matching")

	// Different identity: not found.
	got = task.checkIfPoolMemberExistByAddress(list, &v1alpha1.PoolMember{
		IP: "10.0.0.9", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80),
	})
	assert.False(t, got)
}

// comparePoolMembers must report a difference when only the weight changes,
// so the controller pushes the new weights to vLB.
func TestComparePoolMembers_DetectsWeightChange(t *testing.T) {
	task := &defaultModelDeployTask{}

	current := []v1alpha1.PoolMember{
		{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(50)},
		{IP: "10.0.0.2", Port: 8080, MonitorPort: 8080, Weight: ptr.To(50)},
	}
	desired := []v1alpha1.PoolMember{
		{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80)},
		{IP: "10.0.0.2", Port: 8080, MonitorPort: 8080, Weight: ptr.To(20)},
	}

	// equal=false means "needs update"
	assert.False(t, task.comparePoolMembers(context.Background(), desired, current),
		"weight change must be detected as a difference")
}

// mergePoolMembers must carry the desired (spec) weight, not the current
// cloud weight, when a member's identity matches an existing member.
func TestMergePoolMembers_PrefersSpecWeight(t *testing.T) {
	task := &defaultModelDeployTask{}

	created := []v1alpha1.PoolMember{
		{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(50)},
		{IP: "10.0.0.2", Port: 8080, MonitorPort: 8080, Weight: ptr.To(50)},
	}
	current := []v1alpha1.PoolMember{
		{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(50)},
		{IP: "10.0.0.2", Port: 8080, MonitorPort: 8080, Weight: ptr.To(50)},
	}
	spec := []v1alpha1.PoolMember{
		{IP: "10.0.0.1", Port: 8080, MonitorPort: 8080, Weight: ptr.To(80)},
		{IP: "10.0.0.2", Port: 8080, MonitorPort: 8080, Weight: ptr.To(20)},
	}

	merged := task.mergePoolMembers(context.Background(), created, current, spec)

	assert.Len(t, merged, 2)
	weights := map[string]int{}
	for _, m := range merged {
		if m.Weight != nil {
			weights[m.IP] = *m.Weight
		}
	}
	assert.Equal(t, 80, weights["10.0.0.1"], "merged member must use spec weight")
	assert.Equal(t, 20, weights["10.0.0.2"], "merged member must use spec weight")
}
