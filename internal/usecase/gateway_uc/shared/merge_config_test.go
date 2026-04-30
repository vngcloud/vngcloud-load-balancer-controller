package shared_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
)

func TestMergeLBC_PreferGateway_DefaultMode(t *testing.T) {
	class := &vksv1alpha1.LoadBalancerConfigSpec{PackageId: ptr.To("class-pkg"), Tags: map[string]string{"a": "1"}}
	gw := &vksv1alpha1.LoadBalancerConfigSpec{PackageId: ptr.To("gw-pkg"), Tags: map[string]string{"a": "2", "b": "3"}}
	out := shared.MergeLBC(class, gw)
	assert.Equal(t, "gw-pkg", *out.PackageId)
	assert.Equal(t, "2", out.Tags["a"])
	assert.Equal(t, "3", out.Tags["b"])
}

func TestMergeLBC_PreferGatewayClass(t *testing.T) {
	mode := vksv1alpha1.MergingModePreferGatewayClass
	class := &vksv1alpha1.LoadBalancerConfigSpec{PackageId: ptr.To("class-pkg"), MergingMode: &mode, Tags: map[string]string{"a": "1"}}
	gw := &vksv1alpha1.LoadBalancerConfigSpec{PackageId: ptr.To("gw-pkg"), Tags: map[string]string{"a": "2", "b": "3"}}
	out := shared.MergeLBC(class, gw)
	assert.Equal(t, "class-pkg", *out.PackageId)
	assert.Equal(t, "1", out.Tags["a"])
	assert.Equal(t, "3", out.Tags["b"])
}

func TestMergeLBC_LoadBalancerIDIgnoredAtClassLevel(t *testing.T) {
	class := &vksv1alpha1.LoadBalancerConfigSpec{LoadBalancerId: ptr.To("CLASS-FORBIDDEN")}
	gw := &vksv1alpha1.LoadBalancerConfigSpec{LoadBalancerId: ptr.To("gw-id")}
	out := shared.MergeLBC(class, gw)
	assert.Equal(t, "gw-id", *out.LoadBalancerId)
}

func TestMergeLBC_NilGateway_StripsLoadBalancerID(t *testing.T) {
	class := &vksv1alpha1.LoadBalancerConfigSpec{PackageId: ptr.To("class-pkg"), LoadBalancerId: ptr.To("CLASS-FORBIDDEN")}
	out := shared.MergeLBC(class, nil)
	assert.Equal(t, "class-pkg", *out.PackageId)
	assert.Nil(t, out.LoadBalancerId)
}

func TestMergeLBC_NilClass(t *testing.T) {
	gw := &vksv1alpha1.LoadBalancerConfigSpec{PackageId: ptr.To("gw-pkg")}
	out := shared.MergeLBC(nil, gw)
	assert.Equal(t, "gw-pkg", *out.PackageId)
}

func TestMergeLBC_BothNil(t *testing.T) {
	out := shared.MergeLBC(nil, nil)
	assert.NotNil(t, out)
	assert.Nil(t, out.PackageId)
}

func TestMergeLBC_ListenersMergedByName(t *testing.T) {
	class := &vksv1alpha1.LoadBalancerConfigSpec{
		Listeners: []vksv1alpha1.Listener{{Name: "l1", AllowedCidrs: ptr.To("10.0.0.0/8")}},
	}
	gw := &vksv1alpha1.LoadBalancerConfigSpec{
		Listeners: []vksv1alpha1.Listener{{Name: "l1", AllowedCidrs: ptr.To("0.0.0.0/0")}, {Name: "l2"}},
	}
	out := shared.MergeLBC(class, gw)
	var l1, l2 *vksv1alpha1.Listener
	for i := range out.Listeners {
		if out.Listeners[i].Name == "l1" {
			l1 = &out.Listeners[i]
		}
		if out.Listeners[i].Name == "l2" {
			l2 = &out.Listeners[i]
		}
	}
	assert.NotNil(t, l1)
	assert.NotNil(t, l2)
	assert.Equal(t, "0.0.0.0/0", *l1.AllowedCidrs)
}
