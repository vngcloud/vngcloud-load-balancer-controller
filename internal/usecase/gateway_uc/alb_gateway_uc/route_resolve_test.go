package alb_gateway_uc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	repomocks "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

//nolint:unparam // test helper kept generic for future cross-group/-kind cases
func backendRef(group, kind, ns, name string, port int32, weight *int32) gwv1.HTTPBackendRef {
	br := gwv1.HTTPBackendRef{
		BackendRef: gwv1.BackendRef{
			BackendObjectReference: gwv1.BackendObjectReference{
				Name: gwv1.ObjectName(name),
				Port: (*gwv1.PortNumber)(ptr.To(port)),
			},
			Weight: weight,
		},
	}
	if group != "" {
		g := gwv1.Group(group)
		br.Group = &g
	}
	if kind != "" {
		k := gwv1.Kind(kind)
		br.Kind = &k
	}
	if ns != "" {
		n := gwv1.Namespace(ns)
		br.Namespace = &n
	}
	return br
}

func TestResolveBackend_SameNS_InstanceMode_Default(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	ep := utils.NewMockEndpointResolver(t)
	ep.EXPECT().ResolveNodePortEndpoints(mock.Anything,
		types.NamespacedName{Namespace: "ns", Name: "svc"}, intstr.FromInt(80), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 30080}, {IP: "10.0.0.2", Port: 30080}}, nil)

	uc := newALBUCWithResolver(k8s, ep)
	be, _, err := uc.resolveBackend(context.Background(), "ns", "r1", "HTTPRoute", nil,
		backendRef("", "", "", "svc", 80, nil), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, be)
	assert.Equal(t, int32(30080), be.Backend.Port)
	assert.Equal(t, int32(1), be.Backend.Weight)
	assert.Len(t, be.Endpoints, 2)
}

func TestResolveBackend_TGCSwitchesToIPMode(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	ep := utils.NewMockEndpointResolver(t)
	ep.EXPECT().ResolvePodEndpoints(mock.Anything,
		types.NamespacedName{Namespace: "ns", Name: "svc"}, intstr.FromInt(8080), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.244.0.1", Port: 8080}}, nil)

	tgcs := []gatewayv1alpha1.TargetGroupConfig{{
		Spec: gatewayv1alpha1.TargetGroupConfigSpec{
			TargetReference: gatewayv1alpha1.TargetReference{Name: "svc"},
			DefaultConfig:   gatewayv1alpha1.TargetGroupProperties{TargetType: ptr.To("ip")},
		},
	}}

	uc := newALBUCWithResolver(k8s, ep)
	be, props, err := uc.resolveBackend(context.Background(), "ns", "r1", "HTTPRoute", nil,
		backendRef("", "", "", "svc", 8080, ptr.To(int32(3))), nil, tgcs)
	require.NoError(t, err)
	require.NotNil(t, be)
	assert.Equal(t, int32(8080), be.Backend.Port)
	assert.Equal(t, int32(3), be.Backend.Weight)
	assert.Equal(t, "ip", *props.TargetType)
}

func TestResolveBackend_CrossNS_NoGrant_ReturnsError(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	ep := utils.NewMockEndpointResolver(t)
	uc := newALBUCWithResolver(k8s, ep)
	_, _, err := uc.resolveBackend(context.Background(), "ns-route", "r1", "HTTPRoute", nil,
		backendRef("", "", "ns-svc", "svc", 80, nil), nil, nil)
	assert.ErrorContains(t, err, "ReferenceGrant")
}

func TestResolveBackend_CrossNS_WithGrant_Resolves(t *testing.T) {
	grant := gwv1beta1.ReferenceGrant{}
	grant.Namespace = "ns-svc"
	grant.Spec.From = []gwv1beta1.ReferenceGrantFrom{{
		Group: gwv1.GroupName, Kind: "HTTPRoute", Namespace: "ns-route",
	}}
	grant.Spec.To = []gwv1beta1.ReferenceGrantTo{{
		Group: "", Kind: "Service",
	}}

	k8s := repomocks.NewMockK8sRepository(t)
	ep := utils.NewMockEndpointResolver(t)
	ep.EXPECT().ResolveNodePortEndpoints(mock.Anything,
		types.NamespacedName{Namespace: "ns-svc", Name: "svc"}, intstr.FromInt(80), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "1.1.1.1", Port: 30080}}, nil)

	uc := newALBUCWithResolver(k8s, ep)
	be, _, err := uc.resolveBackend(context.Background(), "ns-route", "r1", "HTTPRoute", nil,
		backendRef("", "", "ns-svc", "svc", 80, nil),
		[]gwv1beta1.ReferenceGrant{grant}, nil)
	require.NoError(t, err)
	require.NotNil(t, be)
	assert.Equal(t, "ns-svc", be.Backend.Namespace)
}

func TestResolveBackend_UnsupportedKind_ReturnsError(t *testing.T) {
	uc := newALBUCWithResolver(repomocks.NewMockK8sRepository(t), utils.NewMockEndpointResolver(t))
	_, _, err := uc.resolveBackend(context.Background(), "ns", "r1", "HTTPRoute", nil,
		backendRef("", "ConfigMap", "", "x", 80, nil), nil, nil)
	assert.ErrorContains(t, err, "invalid backend kind")
	assert.ErrorIs(t, err, errInvalidBackendKind)
}

func TestResolveBackend_MissingPort_ReturnsError(t *testing.T) {
	uc := newALBUCWithResolver(repomocks.NewMockK8sRepository(t), utils.NewMockEndpointResolver(t))
	br := gwv1.HTTPBackendRef{BackendRef: gwv1.BackendRef{
		BackendObjectReference: gwv1.BackendObjectReference{Name: "svc"},
	}}
	_, _, err := uc.resolveBackend(context.Background(), "ns", "r1", "HTTPRoute", nil, br, nil, nil)
	assert.ErrorContains(t, err, "missing port")
}

func TestResolveBackend_NoEndpoints_ReturnsError(t *testing.T) {
	ep := utils.NewMockEndpointResolver(t)
	ep.EXPECT().ResolveNodePortEndpoints(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	uc := newALBUCWithResolver(repomocks.NewMockK8sRepository(t), ep)
	_, _, err := uc.resolveBackend(context.Background(), "ns", "r1", "HTTPRoute", nil,
		backendRef("", "", "", "svc", 80, nil), nil, nil)
	assert.ErrorContains(t, err, "no endpoints")
}

func TestBuildPoolFromBackends_DefaultsAndOverrides(t *testing.T) {
	be := []BackendEndpoints{{
		Backend:   pkggw.BackendKey{Namespace: "ns", Name: "svc", Port: 30080, Weight: 1},
		Endpoints: []string{"1.1.1.1"},
	}}

	pool := buildPoolFromBackends("p1", be, nil)
	assert.Equal(t, "p1", pool.Name)
	assert.Equal(t, "HTTP", string(pool.Protocol))
	assert.Equal(t, "TCP", string(pool.HealthMonitor.Protocol))
	assert.Len(t, pool.Members, 1)

	props := &gatewayv1alpha1.TargetGroupProperties{
		PoolAlgorithm:       ptr.To("LEAST_CONNECTIONS"),
		EnableStickySession: ptr.To(true),
		EnableTLSEncryption: ptr.To(true),
	}
	pool2 := buildPoolFromBackends("p2", be, props)
	require.NotNil(t, pool2.Algorithm)
	assert.Equal(t, "LEAST_CONNECTIONS", string(*pool2.Algorithm))
	assert.True(t, *pool2.Stickiness)
	assert.True(t, *pool2.TLSEncryption)
}
