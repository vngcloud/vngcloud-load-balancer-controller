package alb_gateway_uc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	repomocks "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
)

func TestMarkAcceptedAndProgrammed_SetsBothConditions(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)

	var captured *gwv1.Gateway
	k8s.EXPECT().PatchMutateStatusGateway(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, gw *gwv1.Gateway, mutate func(context.Context, *gwv1.Gateway) bool) {
			captured = gw
			mutate(context.Background(), gw)
		}).Return(nil)

	uc := newALBUC(k8s)
	gw := newGW("g1", "ns", "uid")
	gw.Generation = 7
	require.NoError(t, uc.markAcceptedAndProgrammed(context.Background(), gw))

	require.NotNil(t, captured)
	have := map[string]metav1.Condition{}
	for _, c := range captured.Status.Conditions {
		have[c.Type] = c
	}
	assert.Equal(t, metav1.ConditionTrue, have["Accepted"].Status)
	assert.Equal(t, "Accepted", have["Accepted"].Reason)
	assert.Equal(t, metav1.ConditionTrue, have["Programmed"].Status)
	assert.Equal(t, int64(7), have["Programmed"].ObservedGeneration)
}

func TestEnqueueParent_BumpsAnnotationOnOurGateway(t *testing.T) {
	rt := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "r1"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{
				ParentRefs: []gwv1.ParentReference{parentRef("", "", "", "g1", "", nil)},
			},
		},
	}
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetHTTPRoute(mock.Anything, types.NamespacedName{Namespace: "ns", Name: "r1"}).Return(rt, nil)
	k8s.EXPECT().GetGateway(mock.Anything, types.NamespacedName{Namespace: "ns", Name: "g1"}).
		Return(newGW("g1", "ns", "uid"), nil)
	k8s.EXPECT().GetGatewayClass(mock.Anything, mock.Anything).
		Return(&gwv1.GatewayClass{Spec: gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController(domain.ControllerNameALB)}}, nil)

	var captured *gwv1.Gateway
	k8s.EXPECT().PatchMutateGateway(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, gw *gwv1.Gateway, mutate func(context.Context, *gwv1.Gateway) bool) {
			captured = gw
			mutate(context.Background(), gw)
		}).Return(nil)

	uc := newALBUC(k8s)
	require.NoError(t, uc.EnqueueParentGatewayForRoute(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "r1"}}))
	require.NotNil(t, captured)
	assert.NotEmpty(t, captured.Annotations[AnnotationRouteRevision])
}

func TestEnqueueParent_SkipsForeignGatewayClass(t *testing.T) {
	rt := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "r1"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{
				ParentRefs: []gwv1.ParentReference{parentRef("", "", "", "g1", "", nil)},
			},
		},
	}
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetHTTPRoute(mock.Anything, mock.Anything).Return(rt, nil)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).Return(newGW("g1", "ns", "uid"), nil)
	k8s.EXPECT().GetGatewayClass(mock.Anything, mock.Anything).
		Return(&gwv1.GatewayClass{Spec: gwv1.GatewayClassSpec{ControllerName: "someone-else.example.com/ctrl"}}, nil)
	// PatchMutateGateway must NOT be called → no expectation registered.

	uc := newALBUC(k8s)
	assert.NoError(t, uc.EnqueueParentGatewayForRoute(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "r1"}}))
}

func TestEnqueueParent_RouteNotFound_ReturnsNil(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetHTTPRoute(mock.Anything, mock.Anything).
		Return(nil, apierrors.NewNotFound(schema.GroupResource{Resource: "httproutes"}, "r1"))

	uc := newALBUC(k8s)
	assert.NoError(t, uc.EnqueueParentGatewayForRoute(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "r1"}}))
}

func TestEnqueueParent_DedupesParentRefs(t *testing.T) {
	rt := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "r1"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{
				ParentRefs: []gwv1.ParentReference{
					parentRef("", "", "", "g1", "h1", nil),
					parentRef("", "", "", "g1", "h2", nil),
				},
			},
		},
	}
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetHTTPRoute(mock.Anything, mock.Anything).Return(rt, nil)
	// Each Get/Patch family expected exactly once across both parentRefs.
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).
		Return(newGW("g1", "ns", "uid"), nil).Once()
	k8s.EXPECT().GetGatewayClass(mock.Anything, mock.Anything).
		Return(&gwv1.GatewayClass{Spec: gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController(domain.ControllerNameALB)}}, nil).Once()
	k8s.EXPECT().PatchMutateGateway(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	uc := newALBUC(k8s)
	assert.NoError(t, uc.EnqueueParentGatewayForRoute(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "r1"}}))
}
