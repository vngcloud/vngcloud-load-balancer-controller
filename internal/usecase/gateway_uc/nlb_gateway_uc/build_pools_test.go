package nlb_gateway_uc

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	gwshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = gwv1.Install(s)
	_ = gwv1a2.Install(s)
	_ = gwv1beta1.Install(s)
	_ = gwv1alpha1.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

// newFakeClient builds a fake client with the L4 route field indexes the
// usecase relies on, seeded with objs.
func newFakeClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithStatusSubresource(&gwv1.Gateway{}, &gwv1a2.TCPRoute{}, &gwv1a2.UDPRoute{}).
		WithIndex(&gwv1a2.TCPRoute{}, gwshared.IndexTCPRouteByParentGateway, gwshared.IndexTCPRouteByParentFunc).
		WithIndex(&gwv1a2.UDPRoute{}, gwshared.IndexUDPRouteByParentGateway, gwshared.IndexUDPRouteByParentFunc).
		WithIndex(&gwv1a2.TCPRoute{}, gwshared.IndexTCPRouteByService, gwshared.IndexTCPRouteByServiceFunc).
		WithIndex(&gwv1a2.UDPRoute{}, gwshared.IndexUDPRouteByService, gwshared.IndexUDPRouteByServiceFunc).
		WithObjects(objs...).
		Build()
}

func newTask(t *testing.T, gw *gwv1.Gateway, ep utils.EndpointResolver, objs ...client.Object) *nlbBuildTask {
	uc := &nlbGatewayUseCase{
		k8sRepo:           repository.NewMockK8sRepository(t),
		vngcloudRepo:      repository.NewMockVngCloudRepository(t),
		k8sClient:         newFakeClient(objs...),
		endpointResolver:  ep,
		clusterId:         "cluster-1",
		defaultZone:       "HCM03-1C",
		defaultNetworkId:  "net-1",
		defaultSubnetId:   "subnet-1",
		defaultSubnetCIDR: "10.0.0.0/24",
	}
	return &nlbBuildTask{
		uc:         uc,
		gw:         gw,
		logger:     logrus.NewEntry(logrus.New()),
		nameHelper: utils.NewNameHelper("cluster-1", "gateway", gw.Namespace, gw.Name),
	}
}

func tcpListener(name string, port int) gwv1.Listener {
	return gwv1.Listener{Name: gwv1.SectionName(name), Protocol: gwv1.TCPProtocolType, Port: gwv1.PortNumber(port)}
}

func TestMapL4Protocol(t *testing.T) {
	tests := []struct {
		proto  gwv1.ProtocolType
		ok     bool
		lproto v2.ListenerProtocol
		hproto v2.HealthCheckProtocol
	}{
		{gwv1.TCPProtocolType, true, v2.ListenerProtocolTCP, v2.HealthCheckProtocolTCP},
		{gwv1.UDPProtocolType, true, v2.ListenerProtocolUDP, v2.HealthCheckProtocolPINGUDP},
		{gwv1.HTTPProtocolType, false, "", ""},
		{gwv1.HTTPSProtocolType, false, "", ""},
		{gwv1.TLSProtocolType, false, "", ""},
	}
	for _, tt := range tests {
		l, _, h, ok := mapL4Protocol(tt.proto)
		assert.Equal(t, tt.ok, ok, string(tt.proto))
		if ok {
			assert.Equal(t, tt.lproto, l)
			assert.Equal(t, tt.hproto, h)
		}
	}
}

func TestSynthesizeL4Pool_NoBackendRefs(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	_, err := task.synthesizeL4Pool(context.Background(),
		&l4Route{kind: "TCPRoute", namespace: "prod", name: "r", uid: "u1"},
		v2.PoolProtocolTCP, v2.HealthCheckProtocolTCP)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no backendRefs")
}

func TestSynthesizeL4Pool_TCP_InstanceMembers(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().
		ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(6379), mock.Anything).
		Return([]utils.EndpointAddress{
			{IP: "10.0.0.1", Port: 31000, Name: "n1"},
			{IP: "10.0.0.2", Port: 31000, Name: "n2"},
		}, nil).Once()

	task := newTask(t, gw, mockEP)
	port := gwv1.PortNumber(6379)
	route := &l4Route{
		kind: "TCPRoute", namespace: "prod", name: "redis-route", uid: "u-tcp",
		backendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "redis", Port: &port}}},
	}
	pool, err := task.synthesizeL4Pool(context.Background(), route, v2.PoolProtocolTCP, v2.HealthCheckProtocolTCP)
	assert.NoError(t, err)
	assert.Equal(t, v2.PoolProtocolTCP, pool.Protocol)
	assert.Equal(t, v2.HealthCheckProtocolTCP, pool.HealthMonitor.Protocol)
	assert.Len(t, pool.Members, 2)
	assert.Equal(t, "10.0.0.1", pool.Members[0].IP)
	assert.Equal(t, 31000, pool.Members[0].Port)
}

func TestSynthesizeL4Pool_UDP_PingHealthCheck(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().
		ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(53), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.9", Port: 32000, Name: "n1"}}, nil).Once()

	task := newTask(t, gw, mockEP)
	port := gwv1.PortNumber(53)
	route := &l4Route{
		kind: "UDPRoute", namespace: "prod", name: "dns-route", uid: "u-udp",
		backendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "coredns", Port: &port}}},
	}
	pool, err := task.synthesizeL4Pool(context.Background(), route, v2.PoolProtocolUDP, v2.HealthCheckProtocolPINGUDP)
	assert.NoError(t, err)
	assert.Equal(t, v2.PoolProtocolUDP, pool.Protocol)
	assert.Equal(t, v2.HealthCheckProtocolPINGUDP, pool.HealthMonitor.Protocol)
	assert.Len(t, pool.Members, 1)
}

// resolveTargetNodeLabels feeds VKSBackendPolicy.TargetNodeLabels into the
// endpoint resolver as a node selector, deciding which nodes become LB members
// in instance mode. Mirrors the Service controller's target-node-labels.
func TestSynthesizeL4Pool_TargetNodeLabelsBecomeSelector(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	bp := backendPolicy("prod", "node-pol", "redis")
	bp.Spec.TargetNodeLabels = map[string]string{"role": "lb", "zone": "a"}

	var captured utils.EndpointResolveOptions
	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().
		ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(6379), mock.Anything).
		Run(func(_ context.Context, _ types.NamespacedName, _ intstr.IntOrString, opts ...utils.EndpointResolveOption) {
			captured.ApplyOptions(opts)
		}).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 31000, Name: "n1"}}, nil).Once()

	task := newTask(t, gw, mockEP, bp)
	port := gwv1.PortNumber(6379)
	route := &l4Route{
		kind: "TCPRoute", namespace: "prod", name: "redis-route", uid: "u-tcp",
		backendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "redis", Port: &port}}},
	}
	_, err := task.synthesizeL4Pool(context.Background(), route, v2.PoolProtocolTCP, v2.HealthCheckProtocolTCP)
	assert.NoError(t, err)
	assert.NotNil(t, captured.NodeSelector)
	// Selector ANDs the labels: a node must carry BOTH to be an LB member.
	assert.True(t, captured.NodeSelector.Matches(labels.Set{"role": "lb", "zone": "a"}))
	assert.False(t, captured.NodeSelector.Matches(labels.Set{"role": "lb"}), "partial match must be rejected")
}

// With no VKSBackendPolicy for the backend, target labels are nil, so the
// selector matches every node (LB members = all nodes) — same default as the
// Service controller when target-node-labels is unset.
func TestSynthesizeL4Pool_NoPolicySelectsAllNodes(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}

	var captured utils.EndpointResolveOptions
	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().
		ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(6379), mock.Anything).
		Run(func(_ context.Context, _ types.NamespacedName, _ intstr.IntOrString, opts ...utils.EndpointResolveOption) {
			captured.ApplyOptions(opts)
		}).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 31000, Name: "n1"}}, nil).Once()

	task := newTask(t, gw, mockEP) // no VKSBackendPolicy seeded
	port := gwv1.PortNumber(6379)
	route := &l4Route{
		kind: "TCPRoute", namespace: "prod", name: "redis-route", uid: "u-tcp",
		backendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "redis", Port: &port}}},
	}
	_, err := task.synthesizeL4Pool(context.Background(), route, v2.PoolProtocolTCP, v2.HealthCheckProtocolTCP)
	assert.NoError(t, err)
	assert.NotNil(t, captured.NodeSelector)
	assert.Empty(t, captured.NodeSelector.String(), "nil target labels -> empty selector")
	assert.True(t, captured.NodeSelector.Matches(labels.Set{"any": "node"}), "empty selector matches all nodes")
}

// A Gateway under the NLB class with only an HTTP (L7) listener has no
// NLB-supported listeners → buildListenersAndPools fails closed.
func TestBuildListenersAndPools_MixedRejectsL7Only(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw", UID: "gw-uid"},
		Spec: gwv1.GatewaySpec{Listeners: []gwv1.Listener{
			{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
		}},
	}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	_, _, err := task.buildListenersAndPools(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no NLB-supported listeners")
}

// A TCP listener with an attached TCPRoute produces one listener + one pool.
func TestBuildListenersAndPools_TCPHappyPath(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw", UID: "gw-uid"},
		Spec:       gwv1.GatewaySpec{Listeners: []gwv1.Listener{tcpListener("tcp", 6379)}},
	}
	port := gwv1.PortNumber(6379)
	route := &gwv1a2.TCPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "redis-route", UID: "r-uid"},
		Spec: gwv1a2.TCPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "gw"}}},
			Rules: []gwv1a2.TCPRouteRule{{
				BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "redis", Port: &port}}},
			}},
		},
	}
	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().
		ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(6379), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 31000, Name: "n1"}}, nil).Once()

	task := newTask(t, gw, mockEP, route)
	pools, listeners, err := task.buildListenersAndPools(context.Background())
	assert.NoError(t, err)
	assert.Len(t, listeners, 1)
	assert.Len(t, pools, 1)
	assert.Equal(t, v2.ListenerProtocolTCP, listeners[0].Protocol)
	assert.Equal(t, int32(6379), listeners[0].ProtocolPort)
	assert.NotNil(t, listeners[0].DefaultPoolName)
	assert.Equal(t, pools[0].Name, *listeners[0].DefaultPoolName)
}
