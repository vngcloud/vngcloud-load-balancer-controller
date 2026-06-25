package nlb_gateway_uc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

func TestRouteAttachesToListener(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	l := tcpListener("tcp", 6379)
	sec := gwv1.SectionName("tcp")
	otherSec := gwv1.SectionName("other")
	p := gwv1.PortNumber(6379)
	wrongP := gwv1.PortNumber(9999)

	cases := []struct {
		name string
		refs []gwv1.ParentReference
		want bool
	}{
		{"bare gateway ref", []gwv1.ParentReference{{Name: "gw"}}, true},
		{"matching sectionName", []gwv1.ParentReference{{Name: "gw", SectionName: &sec}}, true},
		{"matching port", []gwv1.ParentReference{{Name: "gw", Port: &p}}, true},
		{"wrong sectionName", []gwv1.ParentReference{{Name: "gw", SectionName: &otherSec}}, false},
		{"wrong port", []gwv1.ParentReference{{Name: "gw", Port: &wrongP}}, false},
		{"wrong gateway", []gwv1.ParentReference{{Name: "other-gw"}}, false},
	}
	for _, c := range cases {
		got := routeAttachesToListener(&l4Route{namespace: "prod", parentRefs: c.refs}, gw, &l)
		assert.Equal(t, c.want, got, c.name)
	}
}

func TestOldestRouteForListener_OldestWins(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	l := tcpListener("tcp", 6379)
	older := &l4Route{kind: "TCPRoute", namespace: "prod", name: "a", creation: 100, parentRefs: []gwv1.ParentReference{{Name: "gw"}}}
	newer := &l4Route{kind: "TCPRoute", namespace: "prod", name: "b", creation: 200, parentRefs: []gwv1.ParentReference{{Name: "gw"}}}
	udp := &l4Route{kind: "UDPRoute", namespace: "prod", name: "c", creation: 50, parentRefs: []gwv1.ParentReference{{Name: "gw"}}}

	got := oldestRouteForListener([]*l4Route{newer, older, udp}, gw, &l)
	assert.NotNil(t, got)
	assert.Equal(t, "a", got.name, "oldest TCP route wins; UDP route ignored for a TCP listener")
}

func tcpRouteObj(ns, name, gwName, backend string, port gwv1.PortNumber) *gwv1a2.TCPRoute {
	return &gwv1a2.TCPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: "r-uid"},
		Spec: gwv1a2.TCPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: gwv1.ObjectName(gwName)}}},
			Rules: []gwv1a2.TCPRouteRule{{
				BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: gwv1.ObjectName(backend), Port: &port}}},
			}},
		},
	}
}

// buildLoadBalancerConfig creates a Type=Network LBC with the gateway-uid owner
// label when none exists, resolving subnet/zone to the cluster default.
func TestBuildLoadBalancerConfig_CreatesL4LBC(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw", UID: "gw-uid"},
		Spec:       gwv1.GatewaySpec{Listeners: []gwv1.Listener{tcpListener("tcp", 6379)}},
	}
	port := gwv1.PortNumber(6379)
	route := tcpRouteObj("prod", "redis-route", "gw", "redis", port)

	mockEP := utils.NewMockEndpointResolver(t)
	mockEP.EXPECT().
		ResolveNodePortEndpoints(mock.Anything, mock.Anything, intstr.FromInt(6379), mock.Anything).
		Return([]utils.EndpointAddress{{IP: "10.0.0.1", Port: 31000, Name: "n1"}}, nil).Once()

	// Seed the Gateway into the fake client so writeGatewayStatus's status patch
	// finds it; seed the route so listAttachedRoutes resolves the pool.
	task := newTask(t, gw, mockEP, gw, route)

	mk := task.uc.k8sRepo.(*repository.MockK8sRepository)
	mk.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once() // no existing LBC
	var created *v1alpha1.LoadBalancerConfig
	mk.EXPECT().CreateLoadBalancerConfig(mock.Anything, mock.Anything).
		Run(func(_ context.Context, lbc *v1alpha1.LoadBalancerConfig, _ ...client.CreateOption) {
			created = lbc
		}).Return(nil).Once()

	err := task.buildLoadBalancerConfig(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, created)
	assert.Equal(t, v2.LoadBalancerTypeLayer4, created.Spec.Type)
	assert.Equal(t, "subnet-1", created.Spec.SubnetId)
	assert.Equal(t, "gw-uid", created.Labels[domain.OwnerLabelGatewayUID])
	assert.Equal(t, domain.OwnerKindGateway, created.Labels[domain.LabelOwnerResourceKind])
	assert.Len(t, created.Spec.Listeners, 1)
	assert.Len(t, created.Spec.Pools, 1)
}
