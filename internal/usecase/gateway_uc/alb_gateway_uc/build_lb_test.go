package alb_gateway_uc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

func TestBuildLBSpec_FromGateway(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "g1", Namespace: "ns1", UID: "12345678abcd"}}
	effective := &vksv1alpha1.LoadBalancerConfigSpec{
		PackageId: ptr.To("lbp-x"), VpcId: "vpc-1", SubnetId: "sub-1", ZoneId: "HAN-1",
	}
	spec := BuildLBSpec(gw, effective, "k8s-cluster-id")
	assert.Equal(t, "lbp-x", *spec.PackageId)
	assert.Equal(t, "vpc-1", spec.VpcId)
	assert.Equal(t, "sub-1", spec.SubnetId)
	assert.Equal(t, "k8s-cluster-id", *spec.ClusterId)
	assert.Contains(t, spec.LoadBalancerName, "g1")
	assert.Contains(t, spec.LoadBalancerName, "ns1")
}

func TestBuildLBSpec_HonorsExplicitLBName(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "g1", Namespace: "ns1"}}
	effective := &vksv1alpha1.LoadBalancerConfigSpec{LoadBalancerName: "my-explicit-name"}
	spec := BuildLBSpec(gw, effective, "x")
	assert.Equal(t, "my-explicit-name", spec.LoadBalancerName)
}

func TestBuildLBSpec_TypeAlwaysL7(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "n"}}
	effective := &vksv1alpha1.LoadBalancerConfigSpec{}
	spec := BuildLBSpec(gw, effective, "x")
	assert.Equal(t, "Layer 7", string(spec.Type))
}
