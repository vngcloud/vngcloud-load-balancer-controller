package alb_gateway_uc

import (
	"fmt"

	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// BuildLBSpec produces the effective vngcloud LB params for the given Gateway. Starts
// from the merged effective LBC spec and applies Gateway-derived overrides:
//   - Type forced to ALB
//   - ClusterId set
//   - LoadBalancerName defaulted to "<prefix>-gw-<ns>-<name>" when not in LBC
//
// The returned spec is a fresh copy; the input is not mutated.
func BuildLBSpec(gw *gwv1.Gateway, effective *vksv1alpha1.LoadBalancerConfigSpec, clusterID string) *vksv1alpha1.LoadBalancerConfigSpec {
	out := *effective
	out.Type = loadbalancerv2.LoadBalancerTypeLayer7
	out.ClusterId = ptr.To(clusterID)
	if out.LoadBalancerName == "" {
		out.LoadBalancerName = fmt.Sprintf("%s-gw-%s-%s", domain.DEFAULT_LB_PREFIX_NAME, gw.Namespace, gw.Name)
	}
	return &out
}
