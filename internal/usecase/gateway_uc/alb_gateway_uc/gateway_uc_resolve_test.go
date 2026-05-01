package alb_gateway_uc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	repomocks "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
)

func newGWClass(name, controller string, paramRef *gwv1.ParametersReference) *gwv1.GatewayClass {
	return &gwv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: gwv1.GatewayClassSpec{
			ControllerName: gwv1.GatewayController(controller),
			ParametersRef:  paramRef,
		},
	}
}

func newGWWithListeners(name, ns, class string, ls ...gwv1.Listener) *gwv1.Gateway {
	return &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gwv1.GatewaySpec{
			GatewayClassName: gwv1.ObjectName(class),
			Listeners:        ls,
		},
	}
}

func httpListener(name string, port int) gwv1.Listener {
	return gwv1.Listener{
		Name:     gwv1.SectionName(name),
		Protocol: gwv1.HTTPProtocolType,
		Port:     gwv1.PortNumber(port),
	}
}

func TestResolve_GatewayNotFound_ReturnsNilNil(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).
		Return(nil, apierrors.NewNotFound(schema.GroupResource{Resource: "gateways"}, "g1"))

	res, err := newALBUC(k8s).resolveGatewayBuild(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	assert.NoError(t, err)
	assert.Nil(t, res)
}

func TestResolve_WrongControllerName_ReturnsNilNil(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).
		Return(newGWWithListeners("g1", "ns", "other-class", httpListener("h", 80)), nil)
	k8s.EXPECT().GetGatewayClass(mock.Anything, "other-class").
		Return(newGWClass("other-class", "someone-else.example.com/ctrl", nil), nil)

	res, err := newALBUC(k8s).resolveGatewayBuild(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	assert.NoError(t, err)
	assert.Nil(t, res)
}

func TestResolve_NoParametersRef_BuildsDefaultSpec(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).
		Return(newGWWithListeners("g1", "ns", "albcls", httpListener("h", 80)), nil)
	k8s.EXPECT().GetGatewayClass(mock.Anything, "albcls").
		Return(newGWClass("albcls", domain.ControllerNameALB, nil), nil)

	res, err := newALBUC(k8s).resolveGatewayBuild(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.NotNil(t, res.lbSpec)
	assert.Equal(t, "test-cluster", *res.lbSpec.ClusterId)
	assert.Contains(t, res.lbSpec.LoadBalancerName, "g1")
	assert.Len(t, res.lbSpec.Listeners, 1)
}

func TestResolve_ClassAndGatewayParametersRef_AreMerged(t *testing.T) {
	classRef := &gwv1.ParametersReference{
		Group: lbcRefGroup, Kind: lbcRefKind, Name: "class-lbc", Namespace: ptr.To(gwv1.Namespace("ns")),
	}
	gw := newGWWithListeners("g1", "ns", "albcls", httpListener("h", 80))
	gw.Spec.Infrastructure = &gwv1.GatewayInfrastructure{
		ParametersRef: &gwv1.LocalParametersReference{Group: lbcRefGroup, Kind: lbcRefKind, Name: "gw-lbc"},
	}

	classLBC := &vksv1alpha1.LoadBalancerConfig{
		Spec: vksv1alpha1.LoadBalancerConfigSpec{PackageId: ptr.To("class-pkg"), VpcId: "vpc-class"},
	}
	gwLBC := &vksv1alpha1.LoadBalancerConfig{
		Spec: vksv1alpha1.LoadBalancerConfigSpec{PackageId: ptr.To("gw-pkg")}, // VpcId unset → falls back to class
	}

	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).Return(gw, nil)
	k8s.EXPECT().GetGatewayClass(mock.Anything, "albcls").
		Return(newGWClass("albcls", domain.ControllerNameALB, classRef), nil)
	k8s.EXPECT().GetLoadBalancerConfig(mock.Anything, types.NamespacedName{Namespace: "ns", Name: "class-lbc"}).
		Return(classLBC, nil)
	k8s.EXPECT().GetLoadBalancerConfig(mock.Anything, types.NamespacedName{Namespace: "ns", Name: "gw-lbc"}).
		Return(gwLBC, nil)

	res, err := newALBUC(k8s).resolveGatewayBuild(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	require.NoError(t, err)
	require.NotNil(t, res)
	// Gateway wins on conflict (PackageId).
	assert.Equal(t, "gw-pkg", *res.lbSpec.PackageId)
	// Class fills in fields the Gateway didn't set (VpcId).
	assert.Equal(t, "vpc-class", res.lbSpec.VpcId)
}

func TestResolve_InvalidListener_IsSkipped(t *testing.T) {
	gw := newGWWithListeners("g1", "ns", "albcls",
		httpListener("good", 80),
		gwv1.Listener{Name: "bad", Protocol: gwv1.UDPProtocolType, Port: 53}, // unsupported
	)
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).Return(gw, nil)
	k8s.EXPECT().GetGatewayClass(mock.Anything, "albcls").
		Return(newGWClass("albcls", domain.ControllerNameALB, nil), nil)

	res, err := newALBUC(k8s).resolveGatewayBuild(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Len(t, res.lbSpec.Listeners, 1)
	// All controller-managed names carry the "vks-" prefix (vngcloud-side
	// identification + clears the 5-char minimum).
	assert.Equal(t, "vks-good", res.lbSpec.Listeners[0].Name)
}

func TestResolve_BadGroupKind_OnClassRef_ReturnsError(t *testing.T) {
	classRef := &gwv1.ParametersReference{
		Group: "wrong.example", Kind: "ConfigMap", Name: "x",
	}
	gw := newGWWithListeners("g1", "ns", "albcls", httpListener("h", 80))
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).Return(gw, nil)
	k8s.EXPECT().GetGatewayClass(mock.Anything, "albcls").
		Return(newGWClass("albcls", domain.ControllerNameALB, classRef), nil)

	_, err := newALBUC(k8s).resolveGatewayBuild(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	assert.ErrorContains(t, err, "must point to vks.vngcloud.vn/LoadBalancerConfig")
}

func TestResolve_GatewayClassFetchError_Propagates(t *testing.T) {
	gw := newGWWithListeners("g1", "ns", "albcls", httpListener("h", 80))
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).Return(gw, nil)
	k8s.EXPECT().GetGatewayClass(mock.Anything, "albcls").Return(nil, errors.New("api offline"))

	_, err := newALBUC(k8s).resolveGatewayBuild(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	assert.ErrorContains(t, err, "api offline")
}

func TestEnsure_DelegatesToResolver_HappyPath(t *testing.T) {
	k8s := repomocks.NewMockK8sRepository(t)
	k8s.EXPECT().GetGateway(mock.Anything, mock.Anything).
		Return(newGWWithListeners("g1", "ns", "albcls", httpListener("h", 80)), nil)
	k8s.EXPECT().GetGatewayClass(mock.Anything, "albcls").
		Return(newGWClass("albcls", domain.ControllerNameALB, nil), nil)
	// No HTTPRoutes attached → attachHTTPRoutes short-circuits without further list calls.
	k8s.EXPECT().ListHTTPRoute(mock.Anything, mock.Anything).Return(nil)
	// Deploy: no existing LBC → create.
	k8s.EXPECT().ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	k8s.EXPECT().CreateLoadBalancerConfig(mock.Anything, mock.Anything).Return(nil)
	// Status: Accepted=True + Programmed=True patched after deploy.
	k8s.EXPECT().PatchMutateStatusGateway(mock.Anything, mock.Anything, mock.Anything).Return(nil)

	err := newALBUC(k8s).EnsureALBGatewayUseCase(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "g1", Namespace: "ns"}})
	assert.NoError(t, err)
}
