package alb_gateway_uc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	repomocks "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// expectEmptyTGCAndLRC configures the mock so a single ListTargetGroupConfig and
// ListListenerRuleConfig call (in any namespace) returns an empty list. The
// attacher caches per-namespace, so only one call per kind is expected.
func expectEmptyTGCAndLRC(k8s *repomocks.MockK8sRepository) {
	k8s.EXPECT().ListTargetGroupConfig(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *gatewayv1alpha1.TargetGroupConfigList, _ ...client.ListOption) {
			list.Items = nil
		}).Return(nil).Once()
	k8s.EXPECT().ListListenerRuleConfig(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *gatewayv1alpha1.ListenerRuleConfigList, _ ...client.ListOption) {
			list.Items = nil
		}).Return(nil).Once()
}

func TestAttachHTTPRoutes_HappyPath(t *testing.T) {
	gw := newGWWithListeners("g1", "ns", "albcls", httpListener("h", 80))
	route := gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "r1", UID: "uid-r1-aaaaaaaa"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{
				parentRef("", "", "", "g1", "", nil),
			}},
			Rules: []gwv1.HTTPRouteRule{{
				BackendRefs: []gwv1.HTTPBackendRef{
					backendRef("", "", "", "svc", 80, nil),
				},
			}},
		},
	}

	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().ListHTTPRoute(mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *gwv1.HTTPRouteList, _ ...client.ListOption) {
			list.Items = []gwv1.HTTPRoute{route}
		}).Return(nil)
	k8s.EXPECT().ListReferenceGrant(mock.Anything, mock.Anything).Return(nil)
	expectEmptyTGCAndLRC(k8s)

	ep := utils.NewMockEndpointResolver(t)
	ep.EXPECT().ResolveNodePortEndpoints(mock.Anything, mock.Anything, mock.Anything).
		Return([]utils.EndpointAddress{{IP: "1.1.1.1", Port: 30080}}, nil)

	uc := newALBUCWithResolver(k8s, ep)
	lbSpec := &vksv1alpha1.LoadBalancerConfigSpec{
		Listeners: []vksv1alpha1.Listener{{Name: "h"}},
	}
	require.NoError(t, uc.attachHTTPRoutes(context.Background(), gw, lbSpec))
	assert.Len(t, lbSpec.Pools, 1)
	assert.Len(t, lbSpec.Listeners[0].Policies, 1)
	assert.NotEmpty(t, lbSpec.Pools[0].Name)
}

func TestAttachHTTPRoutes_DedupesPoolAcrossListeners(t *testing.T) {
	gw := newGWWithListeners("g1", "ns", "albcls",
		httpListener("h1", 80),
		httpListener("h2", 8080),
	)
	route := gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "r1", UID: "uid-r1-aaaaaaaa"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{
				parentRef("", "", "", "g1", "", nil),
			}},
			Rules: []gwv1.HTTPRouteRule{{
				BackendRefs: []gwv1.HTTPBackendRef{backendRef("", "", "", "svc", 80, nil)},
			}},
		},
	}

	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().ListHTTPRoute(mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *gwv1.HTTPRouteList, _ ...client.ListOption) {
			list.Items = []gwv1.HTTPRoute{route}
		}).Return(nil)
	k8s.EXPECT().ListReferenceGrant(mock.Anything, mock.Anything).Return(nil)
	// Cache hit on TGC/LRC means only one call per ns.
	expectEmptyTGCAndLRC(k8s)

	ep := utils.NewMockEndpointResolver(t)
	// Backend resolves twice (once per listener), but pool name is the same → dedup.
	ep.EXPECT().ResolveNodePortEndpoints(mock.Anything, mock.Anything, mock.Anything).
		Return([]utils.EndpointAddress{{IP: "1.1.1.1", Port: 30080}}, nil).Times(2)

	uc := newALBUCWithResolver(k8s, ep)
	lbSpec := &vksv1alpha1.LoadBalancerConfigSpec{
		Listeners: []vksv1alpha1.Listener{{Name: "h1"}, {Name: "h2"}},
	}
	require.NoError(t, uc.attachHTTPRoutes(context.Background(), gw, lbSpec))
	assert.Len(t, lbSpec.Pools, 1, "same-backend-set pool should be deduped across listeners")
	assert.Len(t, lbSpec.Listeners[0].Policies, 1)
	assert.Len(t, lbSpec.Listeners[1].Policies, 1)
}

func TestAttachHTTPRoutes_BackendResolveError_SkipsRule(t *testing.T) {
	gw := newGWWithListeners("g1", "ns", "albcls", httpListener("h", 80))
	// Cross-namespace ref without ReferenceGrant → resolveBackend returns an error,
	// the rule is skipped (no pool, no policy), but the reconcile succeeds overall.
	route := gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "r1", UID: "uid"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{
				parentRef("", "", "", "g1", "", nil),
			}},
			Rules: []gwv1.HTTPRouteRule{{
				BackendRefs: []gwv1.HTTPBackendRef{backendRef("", "", "other-ns", "svc", 80, nil)},
			}},
		},
	}

	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().ListHTTPRoute(mock.Anything, mock.Anything).
		Run(func(_ context.Context, list *gwv1.HTTPRouteList, _ ...client.ListOption) {
			list.Items = []gwv1.HTTPRoute{route}
		}).Return(nil)
	k8s.EXPECT().ListReferenceGrant(mock.Anything, mock.Anything).Return(nil)
	expectEmptyTGCAndLRC(k8s)

	uc := newALBUCWithResolver(k8s, utils.NewMockEndpointResolver(t))
	lbSpec := &vksv1alpha1.LoadBalancerConfigSpec{
		Listeners: []vksv1alpha1.Listener{{Name: "h"}},
	}
	require.NoError(t, uc.attachHTTPRoutes(context.Background(), gw, lbSpec))
	assert.Empty(t, lbSpec.Pools)
	assert.Empty(t, lbSpec.Listeners[0].Policies)
}

func TestAttachHTTPRoutes_NoAttachedRoutes_ShortCircuits(t *testing.T) {
	gw := newGWWithListeners("g1", "ns", "albcls", httpListener("h", 80))
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().ListHTTPRoute(mock.Anything, mock.Anything).Return(nil)

	uc := newALBUCWithResolver(k8s, utils.NewMockEndpointResolver(t))
	lbSpec := &vksv1alpha1.LoadBalancerConfigSpec{
		Listeners: []vksv1alpha1.Listener{{Name: "h"}},
	}
	require.NoError(t, uc.attachHTTPRoutes(context.Background(), gw, lbSpec))
	assert.Empty(t, lbSpec.Pools)
}
