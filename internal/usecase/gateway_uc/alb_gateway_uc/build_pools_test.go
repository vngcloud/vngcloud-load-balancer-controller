package alb_gateway_uc

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// newTaskWithEndpointResolver builds a task where uc.endpointResolver is set
// to the supplied mock, and the fake client has the HTTPRoute field index.
func newTaskWithEndpointResolver(
	t *testing.T,
	gw *gwv1.Gateway,
	epResolver *utils.MockEndpointResolver,
	objs ...interface{},
) *defaultGatewayBuildTask {
	s := newTestScheme()
	builder := newFakeClientWithHTTPRouteIndex(gw)
	_ = objs // extra objects not needed for pool tests — routes are added to the index separately

	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{
		k8sRepo:          mockK8s,
		vngcloudRepo:     mockVng,
		k8sClient:        builder,
		endpointResolver: epResolver,
		clusterId:        "cluster-1",
		defaultZone:      "HCM03-1C",
		defaultNetworkId: "net-1",
		defaultSubnetId:  "subnet-1",
	}
	_ = s
	return &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name),
	}
}

// TestSynthesizePool_NoBackendRefs tests that a rule with no backendRefs returns error.
func TestSynthesizePool_NoBackendRefs(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)
	route := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r", UID: "uid-1"}}
	rule := gwv1.HTTPRouteRule{}
	_, err := task.synthesizePool(context.Background(), route, 0, rule)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no backendRefs")
}

// TestSynthesizePool_AllZeroWeightDropped tests that zero-weight backends are all dropped.
func TestSynthesizePool_AllZeroWeightDropped(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)
	route := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r", UID: "uid-1"}}
	zeroWeight := int32(0)
	port := gwv1.PortNumber(80)
	rule := gwv1.HTTPRouteRule{
		BackendRefs: []gwv1.HTTPBackendRef{{
			BackendRef: gwv1.BackendRef{
				BackendObjectReference: gwv1.BackendObjectReference{Name: "svc", Port: &port},
				Weight:                 &zeroWeight,
			},
		}},
	}
	_, err := task.synthesizePool(context.Background(), route, 0, rule)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all backends were dropped")
}

// TestSynthesizePool_HappyPath_InstanceMode tests the instance-mode pool synthesis
// where the backend gets resolved via ResolveNodePortEndpoints.
func TestSynthesizePool_HappyPath_InstanceMode(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	mockEP := utils.NewMockEndpointResolver(t)

	// No VKSBackendPolicy or VKSHealthCheckPolicy present for "svc1" → resolveTargetType
	// defaults to instance, resolveTargetNodeLabels returns nil. Then ResolveNodePortEndpoints.
	mockEP.EXPECT().
		ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(80), mock.Anything).
		Return([]utils.EndpointAddress{
			{IP: "10.0.0.1", Port: 30080, Name: "node-1"},
			{IP: "10.0.0.2", Port: 30080, Name: "node-2"},
		}, nil).
		Once()

	task := newTaskWithEndpointResolver(t, gw, mockEP)
	route := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r", UID: "uid-42"}}
	port := gwv1.PortNumber(80)
	rule := gwv1.HTTPRouteRule{
		BackendRefs: []gwv1.HTTPBackendRef{{
			BackendRef: gwv1.BackendRef{
				BackendObjectReference: gwv1.BackendObjectReference{Name: "svc1", Port: &port},
			},
		}},
	}

	pool, err := task.synthesizePool(context.Background(), route, 0, rule)
	assert.NoError(t, err)
	assert.NotNil(t, pool)
	assert.Len(t, pool.Members, 2)
	assert.Equal(t, "10.0.0.1", pool.Members[0].IP)
}

// resolveTargetNodeLabels must reach the resolver: synthesizePool turns
// VKSBackendPolicy.TargetNodeLabels into a node selector passed to
// ResolveNodePortEndpoints (instance mode). Mirrors the NLB wiring test.
func TestSynthesizePool_TargetNodeLabelsBecomeSelector(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	bp := makeBackendPolicy("prod", "bp", "svc1", nil, nil, nil, nil)
	bp.Spec.TargetNodeLabels = map[string]string{"role": "lb", "zone": "a"}

	var captured utils.EndpointResolveOptions
	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().
		ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(80), mock.Anything).
		Run(func(_ context.Context, _ types.NamespacedName, _ intstr.IntOrString, opts ...utils.EndpointResolveOption) {
			captured.ApplyOptions(opts)
		}).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 30080, Name: "node-1"}}, nil).Once()

	uc := &albGatewayUseCase{
		k8sRepo:          repository.NewMockK8sRepository(t),
		vngcloudRepo:     repository.NewMockVngCloudRepository(t),
		k8sClient:        fake.NewClientBuilder().WithScheme(newTestScheme()).WithRuntimeObjects(bp).Build(),
		endpointResolver: mockEP,
		clusterId:        "cluster-1",
	}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name),
	}

	port := gwv1.PortNumber(80)
	route := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r", UID: "uid-nl"}}
	rule := gwv1.HTTPRouteRule{
		BackendRefs: []gwv1.HTTPBackendRef{{
			BackendRef: gwv1.BackendRef{
				BackendObjectReference: gwv1.BackendObjectReference{Name: "svc1", Port: &port},
			},
		}},
	}
	_, err := task.synthesizePool(context.Background(), route, 0, rule)
	assert.NoError(t, err)
	assert.NotNil(t, captured.NodeSelector)
	// Selector ANDs the labels: a node must carry BOTH to be an LB member.
	assert.True(t, captured.NodeSelector.Matches(labels.Set{"role": "lb", "zone": "a"}))
	assert.False(t, captured.NodeSelector.Matches(labels.Set{"role": "lb"}), "partial match must be rejected")
}

// TestSynthesizePool_CrossNamespaceSkipped tests that cross-namespace backends
// are skipped with a warning.
func TestSynthesizePool_CrossNamespaceSkipped(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)
	route := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r", UID: "uid-1"}}
	otherNs := gwv1.Namespace("other-ns")
	port := gwv1.PortNumber(80)
	rule := gwv1.HTTPRouteRule{
		BackendRefs: []gwv1.HTTPBackendRef{{
			BackendRef: gwv1.BackendRef{
				BackendObjectReference: gwv1.BackendObjectReference{
					Name:      "svc-cross-ns",
					Port:      &port,
					Namespace: &otherNs,
				},
			},
		}},
	}
	_, err := task.synthesizePool(context.Background(), route, 0, rule)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all backends were dropped")
}

// TestBuildPoolsAndPolicies_NoRoutes tests the empty-routes case.
func TestBuildPoolsAndPolicies_NoRoutes(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	// Build task using a fake client with the HTTPRoute index (no routes added).
	s := newTestScheme()
	_ = s
	fakeClient := newFakeClientWithHTTPRouteIndex(gw)
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fakeClient,
	}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name),
	}

	pools, policies, err := task.buildPoolsAndPolicies(context.Background())
	assert.NoError(t, err)
	assert.Empty(t, pools)
	assert.Empty(t, policies)
}

// TestBuildPoolsAndPolicies_WithRoute tests the case where one HTTPRoute with
// one rule is attached to the gateway.
func TestBuildPoolsAndPolicies_WithRoute(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	// Build an HTTPRoute that references this gateway.
	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "route-1", UID: "uid-r1"},
	}
	port := gwv1.PortNumber(80)
	route.Spec.ParentRefs = []gwv1.ParentReference{{Name: "gw"}}
	route.Spec.Rules = []gwv1.HTTPRouteRule{{
		BackendRefs: []gwv1.HTTPBackendRef{{
			BackendRef: gwv1.BackendRef{
				BackendObjectReference: gwv1.BackendObjectReference{Name: "svc1", Port: &port},
			},
		}},
	}}

	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().
		ResolveNodePortEndpoints(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 30080, Name: "node-1"}}, nil).
		Once()

	// Build the indexed fake client with the route.
	s := newTestScheme()
	_ = s
	fakeClient := newFakeClientWithHTTPRouteIndex(gw, route)
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{
		k8sRepo:          mockK8s,
		vngcloudRepo:     mockVng,
		k8sClient:        fakeClient,
		endpointResolver: mockEP,
	}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name),
	}

	pools, policies, err := task.buildPoolsAndPolicies(context.Background())
	assert.NoError(t, err)
	assert.Len(t, pools, 1)
	// One listener (http) accepted the route → at least one policy entry.
	assert.NotEmpty(t, policies["http"])
}

// TestBuildPoolsAndPolicies_UnsupportedMatch tests that rules with header/method
// matches are skipped with a warning.
func TestBuildPoolsAndPolicies_UnsupportedMatch(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "route-bad", UID: "uid-bad"},
	}
	route.Spec.ParentRefs = []gwv1.ParentReference{{Name: "gw"}}
	method := gwv1.HTTPMethod("GET")
	route.Spec.Rules = []gwv1.HTTPRouteRule{{
		Matches:     []gwv1.HTTPRouteMatch{{Method: &method}},
		BackendRefs: []gwv1.HTTPBackendRef{{}},
	}}

	fakeClient := newFakeClientWithHTTPRouteIndex(gw, route)
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name),
	}

	pools, policies, err := task.buildPoolsAndPolicies(context.Background())
	// Skipped with warning; no error, but no pools or policies produced.
	assert.NoError(t, err)
	assert.Empty(t, pools)
	assert.Empty(t, policies)
}

// TestListAttachedHTTPRoutes_WithAndWithoutIndex exercises the route filtering
// logic in listAttachedHTTPRoutes. Routes not referencing the gateway are filtered.
func TestListAttachedHTTPRoutes_WithRoutes(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	// Route targeting this gateway in same namespace.
	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r1"},
	}
	route.Spec.ParentRefs = []gwv1.ParentReference{{Name: gwv1.ObjectName("gw")}}

	// Route in a different namespace — routeAcceptableNamespace should drop it
	// since the listener has default (same-namespace) policy.
	routeOtherNS := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "staging", Name: "r2"},
	}
	routeOtherNS.Spec.ParentRefs = []gwv1.ParentReference{{Name: gwv1.ObjectName("gw")}}

	_ = shared.IndexHTTPRouteByParentGateway // ensure constant is accessible
	fakeClient := newFakeClientWithHTTPRouteIndex(gw, route, routeOtherNS)
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name),
	}

	routes, err := task.listAttachedHTTPRoutes(context.Background())
	assert.NoError(t, err)
	// Only the same-namespace route should pass the namespace filter.
	assert.Len(t, routes, 1)
	assert.Equal(t, "r1", routes[0].Name)
}
