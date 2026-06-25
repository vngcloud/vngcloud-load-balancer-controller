package nlb_gateway_uc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// --- builders ---

func svcTargetRef(svc string) gwv1a2.LocalPolicyTargetReference {
	return gwv1a2.LocalPolicyTargetReference{Group: "", Kind: "Service", Name: gwv1a2.ObjectName(svc)}
}

func backendPolicy(ns, name, svc string) *gwv1alpha1.VKSBackendPolicy {
	return &gwv1alpha1.VKSBackendPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1alpha1.VKSBackendPolicySpec{
			TargetRefs: []gwv1alpha1.LocalPolicyTargetReference{svcTargetRef(svc)},
		},
	}
}

func healthPolicy(ns, name, svc string) *gwv1alpha1.VKSHealthCheckPolicy {
	return &gwv1alpha1.VKSHealthCheckPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1alpha1.VKSHealthCheckPolicySpec{
			TargetRefs: []gwv1alpha1.LocalPolicyTargetReference{svcTargetRef(svc)},
		},
	}
}

func svcObj(ns, name string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
}

// --- pool synthesis: weighted, zero-weight, ip target ---

func TestSynthesizeL4Pool_WeightedBackends(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(80), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 30001, Name: "a"}}, nil).Once()
	mockEP.EXPECT().ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(81), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.2", Port: 30002, Name: "b"}}, nil).Once()

	task := newTask(t, gw, mockEP)
	p80, p81 := gwv1.PortNumber(80), gwv1.PortNumber(81)
	w90, w10 := int32(90), int32(10)
	route := &l4Route{
		kind: "TCPRoute", namespace: "prod", name: "r", uid: "u",
		backendRefs: []gwv1.BackendRef{
			{BackendObjectReference: gwv1.BackendObjectReference{Name: "a", Port: &p80}, Weight: &w90},
			{BackendObjectReference: gwv1.BackendObjectReference{Name: "b", Port: &p81}, Weight: &w10},
		},
	}
	pool, err := task.synthesizeL4Pool(context.Background(), route, v2.PoolProtocolTCP, v2.HealthCheckProtocolTCP)
	assert.NoError(t, err)
	assert.Len(t, pool.Members, 2)
	weights := map[int]bool{}
	for _, m := range pool.Members {
		assert.NotNil(t, m.Weight)
		weights[*m.Weight] = true
	}
	assert.Len(t, weights, 2, "90:10 split -> two distinct scaled member weights")
}

func TestSynthesizeL4Pool_ZeroWeightDropped(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	mockEP := utils.NewMockEndpointResolver(t)
	// Only the non-zero-weight backend is resolved.
	mockEP.EXPECT().ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(80), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 30001, Name: "a"}}, nil).Once()

	task := newTask(t, gw, mockEP)
	p80, p81 := gwv1.PortNumber(80), gwv1.PortNumber(81)
	w0, w1 := int32(0), int32(1)
	route := &l4Route{
		kind: "TCPRoute", namespace: "prod", name: "r", uid: "u",
		backendRefs: []gwv1.BackendRef{
			{BackendObjectReference: gwv1.BackendObjectReference{Name: "a", Port: &p80}, Weight: &w1},
			{BackendObjectReference: gwv1.BackendObjectReference{Name: "z", Port: &p81}, Weight: &w0},
		},
	}
	pool, err := task.synthesizeL4Pool(context.Background(), route, v2.PoolProtocolTCP, v2.HealthCheckProtocolTCP)
	assert.NoError(t, err)
	assert.Len(t, pool.Members, 1)
	assert.Equal(t, "10.0.0.1", pool.Members[0].IP)
}

func TestSynthesizeL4Pool_IPTargetType(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	mockEP := utils.NewMockEndpointResolver(t)
	// targetType=ip -> ResolvePodEndpoints (not NodePort).
	mockEP.EXPECT().ResolvePodEndpoints(mock.Anything, mock.Anything, intstr.FromInt(80), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "192.168.0.5", Port: 80, Name: "pod-a"}}, nil).Once()

	bp := backendPolicy("prod", "bp", "a")
	bp.Spec.TargetType = ptr.To("ip")
	task := newTask(t, gw, mockEP, bp)

	p80 := gwv1.PortNumber(80)
	route := &l4Route{
		kind: "TCPRoute", namespace: "prod", name: "r", uid: "u",
		backendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "a", Port: &p80}}},
	}
	pool, err := task.synthesizeL4Pool(context.Background(), route, v2.PoolProtocolTCP, v2.HealthCheckProtocolTCP)
	assert.NoError(t, err)
	assert.Len(t, pool.Members, 1)
	assert.Equal(t, "192.168.0.5", pool.Members[0].IP)
	assert.Equal(t, 80, pool.Members[0].Port)
}

// --- overlays ---

func TestApplyBackendPolicyToPool(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	bp := backendPolicy("prod", "bp", "a")
	bp.Spec.PoolAlgorithm = ptr.To("LEAST_CONNECTIONS")
	bp.Spec.Stickiness = ptr.To(true)
	task := newTask(t, gw, utils.NewMockEndpointResolver(t), bp)

	pool := &v1alpha1.Pool{}
	assert.NoError(t, task.applyBackendPolicyToPool(context.Background(), pool, "prod", "a"))
	assert.NotNil(t, pool.Algorithm)
	assert.Equal(t, v2.PoolAlgorithm("LEAST_CONNECTIONS"), *pool.Algorithm)
	assert.NotNil(t, pool.Stickiness)
	assert.True(t, *pool.Stickiness)
}

func TestApplyBackendPolicyToPool_ProxyProtocolOnTCP(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	bp := backendPolicy("prod", "bp", "a")
	bp.Spec.ProxyProtocol = ptr.To(true)
	task := newTask(t, gw, utils.NewMockEndpointResolver(t), bp)

	pool := &v1alpha1.Pool{Protocol: v2.PoolProtocolTCP}
	assert.NoError(t, task.applyBackendPolicyToPool(context.Background(), pool, "prod", "a"))
	assert.Equal(t, v2.PoolProtocolProxy, pool.Protocol, "TCP pool switches to PROXY when proxyProtocol=true")
}

func TestApplyBackendPolicyToPool_ProxyProtocolIgnoredOnUDP(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	bp := backendPolicy("prod", "bp", "a")
	bp.Spec.ProxyProtocol = ptr.To(true)
	task := newTask(t, gw, utils.NewMockEndpointResolver(t), bp)

	pool := &v1alpha1.Pool{Protocol: v2.PoolProtocolUDP}
	assert.NoError(t, task.applyBackendPolicyToPool(context.Background(), pool, "prod", "a"))
	assert.Equal(t, v2.PoolProtocolUDP, pool.Protocol, "UDP pool is left untouched")
}

func TestApplyBackendPolicyToPool_NoPolicyNoChange(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	pool := &v1alpha1.Pool{}
	assert.NoError(t, task.applyBackendPolicyToPool(context.Background(), pool, "prod", "a"))
	assert.Nil(t, pool.Algorithm)
	assert.Nil(t, pool.Stickiness)
}

func TestApplyHealthCheckPolicyToPool_PortAndThresholds(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	hp := healthPolicy("prod", "hp", "a")
	hp.Spec.Protocol = "TCP"
	hp.Spec.Interval = &metav1.Duration{Duration: 15 * time.Second}
	hp.Spec.Timeout = &metav1.Duration{Duration: 4 * time.Second}
	hp.Spec.HealthyThreshold = ptr.To(int32(2))
	hp.Spec.UnhealthyThreshold = ptr.To(int32(3))
	hp.Spec.Port = ptr.To(int32(8888))
	task := newTask(t, gw, utils.NewMockEndpointResolver(t), hp)

	pool := &v1alpha1.Pool{
		HealthMonitor: v1alpha1.PoolHealthMonitor{Protocol: v2.HealthCheckProtocolTCP},
		Members:       []v1alpha1.PoolMember{{IP: "10.0.0.1", Port: 30001, MonitorPort: 30001}},
	}
	assert.NoError(t, task.applyHealthCheckPolicyToPool(context.Background(), pool, "prod", "a"))
	assert.Equal(t, v2.HealthCheckProtocolTCP, pool.HealthMonitor.Protocol)
	assert.Equal(t, 15, *pool.HealthMonitor.Interval)
	assert.Equal(t, 4, *pool.HealthMonitor.Timeout)
	assert.Equal(t, 2, *pool.HealthMonitor.HealthyThreshold)
	assert.Equal(t, 3, *pool.HealthMonitor.UnhealthyThreshold)
	assert.Equal(t, 8888, pool.Members[0].MonitorPort, "port override applies to every member")
}

func TestApplyHealthCheckPolicyToPool_HTTPProbeOverTCPPool(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	hp := healthPolicy("prod", "hp", "a")
	hp.Spec.Protocol = "HTTP"
	hp.Spec.HTTPHealthCheck = &gwv1alpha1.VKSHTTPHealthCheck{Path: ptr.To("/healthz")}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t), hp)

	pool := &v1alpha1.Pool{HealthMonitor: v1alpha1.PoolHealthMonitor{Protocol: v2.HealthCheckProtocolTCP}}
	assert.NoError(t, task.applyHealthCheckPolicyToPool(context.Background(), pool, "prod", "a"))
	hm := pool.HealthMonitor
	assert.Equal(t, v2.HealthCheckProtocolHTTP, hm.Protocol)
	assert.NotNil(t, hm.HealthCheckMethod, "HTTP probe requires method")
	assert.NotNil(t, hm.HttpVersion, "HTTP probe requires httpVersion")
	assert.Equal(t, "/healthz", *hm.HealthCheckPath)
	assert.NotNil(t, hm.SuccessCode, "HTTP probe requires successCode")
}

func TestApplyListenerPolicy(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{
		Spec: gwv1alpha1.VKSGatewayPolicySpec{
			TimeoutClient: &metav1.Duration{Duration: 30 * time.Second},
			TimeoutMember: &metav1.Duration{Duration: 60 * time.Second},
			AllowedCIDRs:  []string{"0.0.0.0/0", "10.0.0.0/8"},
		},
	}
	l := &v1alpha1.Listener{}
	task.applyListenerPolicy(l)
	assert.Equal(t, int32(30), *l.TimeoutClient)
	assert.Equal(t, int32(60), *l.TimeoutMember)
	assert.NotNil(t, l.AllowedCidrs)
	assert.Equal(t, "0.0.0.0/0,10.0.0.0/8", *l.AllowedCidrs)
}

// --- LB-level spec ---

func TestApplyLoadBalancerSpec(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{
		Spec: gwv1alpha1.VKSGatewayPolicySpec{
			LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{
				Scheme: ptr.To("Internet"),
				IsPOC:  ptr.To(false),
				Tags:   map[string]string{"env": "test"},
			},
		},
	}
	lbc := &v1alpha1.LoadBalancerConfig{}
	task.applyLoadBalancerSpec(lbc)
	assert.NotNil(t, lbc.Spec.Scheme)
	assert.Equal(t, v2.LoadBalancerScheme("Internet"), *lbc.Spec.Scheme)
	assert.NotNil(t, lbc.Spec.IsPoc)
	assert.False(t, *lbc.Spec.IsPoc)
	assert.Equal(t, "test", lbc.Spec.Tags["env"])
}

// InterVPC needs both the private subnet and the client-subnet zone; the
// zone maps to LBC.Spec.PrivateZoneId (a common.Zone). Mirrors the Service
// controller's private-subnet-id + private-zone-id annotations.
func TestApplyLoadBalancerSpec_InterVPC(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{
		Spec: gwv1alpha1.VKSGatewayPolicySpec{
			LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{
				Scheme:          ptr.To("InterVPC"),
				PrivateSubnetID: ptr.To("subnet-client"),
				PrivateZoneID:   ptr.To("HCM03-1A"),
			},
		},
	}
	lbc := &v1alpha1.LoadBalancerConfig{}
	task.applyLoadBalancerSpec(lbc)
	assert.Equal(t, v2.LoadBalancerScheme("InterVPC"), *lbc.Spec.Scheme)
	assert.Equal(t, "subnet-client", *lbc.Spec.PrivateSubnetId)
	if assert.NotNil(t, lbc.Spec.PrivateZoneId) {
		assert.Equal(t, "HCM03-1A", string(*lbc.Spec.PrivateZoneId))
	}
}

func TestApplyLoadBalancerSpec_NilPolicyClears(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t)) // unscopedPolicy nil
	lbc := &v1alpha1.LoadBalancerConfig{Spec: v1alpha1.LoadBalancerConfigSpec{Scheme: ptr.To(v2.LoadBalancerScheme("Internet"))}}
	task.applyLoadBalancerSpec(lbc)
	assert.Nil(t, lbc.Spec.Scheme, "no policy -> create-only fields cleared")
}

func TestResolveSubnetAndZone_Default(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	sub, net, zone, cidr, err := task.resolveSubnetAndZone(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "subnet-1", sub)
	assert.Equal(t, "net-1", net)
	assert.Equal(t, "HCM03-1C", zone)
	assert.Equal(t, "10.0.0.0/24", cidr)
}

func TestResolveSubnetAndZone_ExplicitSubnetID(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{
		Spec: gwv1alpha1.VKSGatewayPolicySpec{
			LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{SubnetID: ptr.To("subnet-custom")},
		},
	}
	sub, _, _, _, err := task.resolveSubnetAndZone(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "subnet-custom", sub)
}

func TestResolveSubnetAndZone_PreferZoneEqualsDefault(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{
		Spec: gwv1alpha1.VKSGatewayPolicySpec{
			LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{PreferZoneID: ptr.To("HCM03-1C")},
		},
	}
	sub, _, zone, _, err := task.resolveSubnetAndZone(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "subnet-1", sub)
	assert.Equal(t, "HCM03-1C", zone)
}

func TestResolveLoadBalancerName(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	assert.NotEmpty(t, task.resolveLoadBalancerName(), "default generated name")

	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{
		Spec: gwv1alpha1.VKSGatewayPolicySpec{
			LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{LoadBalancerName: ptr.To("my-nlb")},
		},
	}
	assert.Equal(t, "my-nlb", task.resolveLoadBalancerName())
}

// --- routes & status ---

func TestListAttachedRoutes_TCPandUDP(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	p := gwv1.PortNumber(80)
	tcp := &gwv1a2.TCPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "t"},
		Spec: gwv1a2.TCPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "gw"}}},
			Rules: []gwv1a2.TCPRouteRule{{BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "a", Port: &p}}}}}},
	}
	udp := &gwv1a2.UDPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "u"},
		Spec: gwv1a2.UDPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "gw"}}},
			Rules: []gwv1a2.UDPRouteRule{{BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "b", Port: &p}}}}}},
	}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t), tcp, udp)
	routes, err := task.listAttachedRoutes(context.Background())
	assert.NoError(t, err)
	assert.Len(t, routes, 2)
	kinds := map[string]bool{}
	for _, r := range routes {
		kinds[r.kind] = true
	}
	assert.True(t, kinds["TCPRoute"])
	assert.True(t, kinds["UDPRoute"])
}

func TestRouteResolvedRefs_Resolved(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t), svcObj("prod", "a"))
	p := gwv1.PortNumber(80)
	r := &l4Route{kind: "TCPRoute", namespace: "prod", name: "r",
		backendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "a", Port: &p}}}}
	status, reason, _ := task.routeResolvedRefs(context.Background(), r)
	assert.Equal(t, metav1.ConditionTrue, status)
	assert.Equal(t, string(gwv1.RouteReasonResolvedRefs), reason)
}

func TestRouteResolvedRefs_BackendNotFound(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t)) // no Service seeded
	p := gwv1.PortNumber(80)
	r := &l4Route{kind: "TCPRoute", namespace: "prod", name: "r",
		backendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "missing", Port: &p}}}}
	status, reason, _ := task.routeResolvedRefs(context.Background(), r)
	assert.Equal(t, metav1.ConditionFalse, status)
	assert.Equal(t, string(gwv1.RouteReasonBackendNotFound), reason)
}

func TestRouteResolvedRefs_CrossNsRefNotPermitted(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t)) // no ReferenceGrant
	p := gwv1.PortNumber(80)
	otherNS := gwv1.Namespace("other")
	r := &l4Route{kind: "TCPRoute", namespace: "prod", name: "r",
		backendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "a", Namespace: &otherNS, Port: &p}}}}
	status, reason, _ := task.routeResolvedRefs(context.Background(), r)
	assert.Equal(t, metav1.ConditionFalse, status)
	assert.Equal(t, string(gwv1.RouteReasonRefNotPermitted), reason)
}

func TestRouteAttaches_MultiListener(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
		Spec: gwv1.GatewaySpec{Listeners: []gwv1.Listener{
			{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80}, // unsupported -> ignored
			tcpListener("tcp", 6379),
		}},
	}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	sec := gwv1.SectionName("tcp")
	r := &l4Route{kind: "TCPRoute", namespace: "prod", parentRefs: []gwv1.ParentReference{{Name: "gw", SectionName: &sec}}}
	assert.True(t, task.routeAttaches(r))
}

func TestListenerStatuses_MixedProtocols(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
		Spec: gwv1.GatewaySpec{Listeners: []gwv1.Listener{
			tcpListener("tcp", 6379),
			{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
		}},
	}
	task := newTask(t, gw, utils.NewMockEndpointResolver(t))
	ls := task.listenerStatuses()
	assert.Len(t, ls, 2)
	byName := map[gwv1.SectionName]gwv1.ListenerStatus{}
	for _, s := range ls {
		byName[s.Name] = s
	}
	assert.Equal(t, metav1.ConditionTrue, condStatus(byName["tcp"].Conditions, string(gwv1.ListenerConditionAccepted)))
	assert.Equal(t, metav1.ConditionFalse, condStatus(byName["http"].Conditions, string(gwv1.ListenerConditionAccepted)))
	assert.Equal(t, string(gwv1.ListenerReasonUnsupportedProtocol), condReason(byName["http"].Conditions, string(gwv1.ListenerConditionAccepted)))
}

func TestBuildListenersAndPools_UDPHappyPath(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw", UID: "gw-uid"},
		Spec: gwv1.GatewaySpec{Listeners: []gwv1.Listener{
			{Name: "udp", Protocol: gwv1.UDPProtocolType, Port: 5353},
		}},
	}
	p := gwv1.PortNumber(5353)
	route := &gwv1a2.UDPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "dns", UID: "r-uid"},
		Spec: gwv1a2.UDPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "gw"}}},
			Rules: []gwv1a2.UDPRouteRule{{BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "dns", Port: &p}}}}}},
	}
	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(5353), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 32000, Name: "n"}}, nil).Once()

	task := newTask(t, gw, mockEP, route)
	pools, listeners, err := task.buildListenersAndPools(context.Background())
	assert.NoError(t, err)
	assert.Len(t, listeners, 1)
	assert.Equal(t, v2.ListenerProtocolUDP, listeners[0].Protocol)
	assert.Len(t, pools, 1)
	assert.Equal(t, v2.PoolProtocolUDP, pools[0].Protocol)
	assert.Equal(t, v2.HealthCheckProtocolPINGUDP, pools[0].HealthMonitor.Protocol)
}

// A listener without an attached route is skipped; another with a route is kept.
func TestBuildListenersAndPools_ListenerWithoutRouteSkipped(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw", UID: "gw-uid"},
		Spec: gwv1.GatewaySpec{Listeners: []gwv1.Listener{
			tcpListener("with", 6379),
			tcpListener("without", 7000),
		}},
	}
	p := gwv1.PortNumber(6379)
	sec := gwv1.SectionName("with")
	route := &gwv1a2.TCPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r", UID: "r-uid"},
		Spec: gwv1a2.TCPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "gw", SectionName: &sec}}},
			Rules: []gwv1a2.TCPRouteRule{{BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "a", Port: &p}}}}}},
	}
	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(6379), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 30001, Name: "n"}}, nil).Once()

	task := newTask(t, gw, mockEP, route)
	_, listeners, err := task.buildListenersAndPools(context.Background())
	assert.NoError(t, err)
	assert.Len(t, listeners, 1, "only the listener with an attached route produces a cloud listener")
	assert.Equal(t, int32(6379), listeners[0].ProtocolPort)
}

// --- small helpers ---

func condStatus(conds []metav1.Condition, t string) metav1.ConditionStatus {
	for _, c := range conds {
		if c.Type == t {
			return c.Status
		}
	}
	return metav1.ConditionUnknown
}

func condReason(conds []metav1.Condition, t string) string {
	for _, c := range conds {
		if c.Type == t {
			return c.Reason
		}
	}
	return ""
}
