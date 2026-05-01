package alb_gateway_uc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	repomocks "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
)

func TestDeployLB_NoExisting_CreatesNew(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *vksv1alpha1.LoadBalancerConfigList, _ ...client.ListOption) {
			list.Items = nil
		}).Return(nil)
	var captured *vksv1alpha1.LoadBalancerConfig
	k8s.EXPECT().CreateLoadBalancerConfig(mock.Anything, mock.Anything).
		Run(func(_ context.Context, lbc *vksv1alpha1.LoadBalancerConfig, _ ...client.CreateOption) {
			captured = lbc
		}).Return(nil)

	uc := newALBUC(k8s)
	uc.defaultSubnetId = "sub-default"
	uc.defaultNetworkId = "vpc-default"
	uc.defaultZone = "HAN-1"
	gw := newGW("g1", "ns", "uid-g1")
	spec := &vksv1alpha1.LoadBalancerConfigSpec{}

	got, err := uc.deployLB(context.Background(), gw, spec)
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, "ns", captured.Namespace)
	assert.Equal(t, "g1-", captured.GenerateName)
	assert.Equal(t, "g1", captured.Labels[domain.LabelOwnerResourceName])
	assert.Equal(t, "uid-g1", captured.Labels[domain.LabelOwnerResourceUid])
	assert.Equal(t, "sub-default", captured.Spec.SubnetId)
	assert.Equal(t, "vpc-default", captured.Spec.VpcId)
	assert.Equal(t, "test-cluster", *captured.Spec.ClusterId)
	assert.Equal(t, "Layer 7", string(captured.Spec.Type))
	assert.Same(t, captured, got)
}

func TestDeployLB_OneExisting_PatchesWhenSpecChanged(t *testing.T) {
	existing := vksv1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: "g1-abc",
			Labels: map[string]string{
				domain.LabelOwnerResourceName: "g1",
				domain.LabelOwnerResourceUid:  "uid-g1",
			},
		},
		Spec: vksv1alpha1.LoadBalancerConfigSpec{SubnetId: "sub-old"},
	}
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *vksv1alpha1.LoadBalancerConfigList, _ ...client.ListOption) {
			list.Items = []vksv1alpha1.LoadBalancerConfig{existing}
		}).Return(nil)
	k8s.EXPECT().PatchLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).Return(nil)

	uc := newALBUC(k8s)
	gw := newGW("g1", "ns", "uid-g1")
	spec := &vksv1alpha1.LoadBalancerConfigSpec{SubnetId: "sub-new", VpcId: "vpc-1", ZoneId: "HAN-1"}

	got, err := uc.deployLB(context.Background(), gw, spec)
	require.NoError(t, err)
	assert.Equal(t, "sub-new", got.Spec.SubnetId)
}

func TestDeployLB_OneExisting_NoPatchWhenUnchanged(t *testing.T) {
	existing := vksv1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: "g1-abc",
			Labels: map[string]string{
				domain.LabelOwnerResourceName: "g1",
				domain.LabelOwnerResourceKind: "",
				domain.LabelOwnerResourceUid:  "uid-g1",
			},
		},
		Spec: vksv1alpha1.LoadBalancerConfigSpec{
			SubnetId: "sub-1", VpcId: "vpc-1", ZoneId: "HAN-1",
			Type: "Layer 7",
		},
	}
	// Mirror what deployLB will set on the in-memory object.
	existingClusterID := "test-cluster"
	existing.Spec.ClusterId = &existingClusterID

	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *vksv1alpha1.LoadBalancerConfigList, _ ...client.ListOption) {
			list.Items = []vksv1alpha1.LoadBalancerConfig{existing}
		}).Return(nil)
	// No PatchLoadBalancerConfig expectation — mockery's t.Cleanup AssertExpectations
	// fails if PatchLoadBalancerConfig gets called.

	uc := newALBUC(k8s)
	gw := newGW("g1", "ns", "uid-g1")
	spec := &vksv1alpha1.LoadBalancerConfigSpec{
		SubnetId: "sub-1", VpcId: "vpc-1", ZoneId: "HAN-1",
		Type:      "Layer 7",
		ClusterId: &existingClusterID,
	}

	got, err := uc.deployLB(context.Background(), gw, spec)
	require.NoError(t, err)
	assert.Equal(t, "sub-1", got.Spec.SubnetId)
}

func TestDeployLB_MultipleExisting_ReturnsError(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *vksv1alpha1.LoadBalancerConfigList, _ ...client.ListOption) {
			list.Items = []vksv1alpha1.LoadBalancerConfig{{}, {}}
		}).Return(nil)

	uc := newALBUC(k8s)
	gw := newGW("g1", "ns", "uid-g1")
	_, err := uc.deployLB(context.Background(), gw, &vksv1alpha1.LoadBalancerConfigSpec{})
	assert.ErrorContains(t, err, "found 2 LoadBalancerConfigs")
}

func TestDeployLB_ListError_Propagates(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("api offline"))

	uc := newALBUC(k8s)
	gw := newGW("g1", "ns", "uid-g1")
	_, err := uc.deployLB(context.Background(), gw, &vksv1alpha1.LoadBalancerConfigSpec{})
	assert.ErrorContains(t, err, "api offline")
}
