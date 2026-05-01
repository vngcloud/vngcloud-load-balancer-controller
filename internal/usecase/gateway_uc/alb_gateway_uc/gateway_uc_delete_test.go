package alb_gateway_uc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	repomocks "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/errs"
)

func newGW(name, ns, uid string) *gwv1.Gateway {
	return &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID(uid)},
	}
}

func newALBUC(k8s *repomocks.MockK8sRepository) *albGatewayUseCase {
	return &albGatewayUseCase{
		clusterId: "test-cluster",
		k8sRepo:   k8s,
	}
}

func TestDelete_GatewayNotFound_ReturnsNil(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).
		Return(nil, apierrors.NewNotFound(schema.GroupResource{Resource: "gateways"}, "g1"))

	uc := newALBUC(k8s)
	err := uc.DeleteALBGatewayUseCase(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	assert.NoError(t, err)
}

func TestDelete_NoChildResources_ReturnsNil(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).Return(newGW("g1", "ns", "u1"), nil)
	k8s.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *vksv1alpha1.LoadBalancerConfigList, _ ...client.ListOption) {
			list.Items = nil
		}).Return(nil)
	k8s.EXPECT().ListNodeSecurityGroup(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *vksv1alpha1.NodeSecurityGroupList, _ ...client.ListOption) {
			list.Items = nil
		}).Return(nil)

	uc := newALBUC(k8s)
	err := uc.DeleteALBGatewayUseCase(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	assert.NoError(t, err)
}

func TestDelete_ChildLBCStillExists_ReturnsRequeueAfter(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).Return(newGW("g1", "ns", "u1"), nil)
	k8s.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *vksv1alpha1.LoadBalancerConfigList, _ ...client.ListOption) {
			list.Items = []vksv1alpha1.LoadBalancerConfig{{
				ObjectMeta: metav1.ObjectMeta{Name: "lbc-1", Namespace: "ns"},
			}}
		}).Return(nil)
	k8s.EXPECT().DeleteLoadBalancerConfig(mock.Anything, mock.Anything).Return(nil)
	k8s.EXPECT().ListNodeSecurityGroup(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *vksv1alpha1.NodeSecurityGroupList, _ ...client.ListOption) {
			list.Items = nil
		}).Return(nil)

	uc := newALBUC(k8s)
	err := uc.DeleteALBGatewayUseCase(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	var requeue *errs.RequeueNeededAfter
	assert.ErrorAs(t, err, &requeue)
}

func TestDelete_ListErrorsPropagate(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).Return(newGW("g1", "ns", "u1"), nil)
	k8s.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("boom"))
	k8s.EXPECT().ListNodeSecurityGroup(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *vksv1alpha1.NodeSecurityGroupList, _ ...client.ListOption) {
			list.Items = nil
		}).Return(nil)

	uc := newALBUC(k8s)
	err := uc.DeleteALBGatewayUseCase(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	assert.ErrorContains(t, err, "boom")
}
