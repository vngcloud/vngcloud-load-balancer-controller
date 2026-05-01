package alb_gateway_uc

import (
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// BuildLBSpec produces the effective vngcloud LB params for the given Gateway. Starts
// from the merged effective LBC spec and applies Gateway-derived overrides:
//   - Type forced to ALB
//   - ClusterId set
//   - LoadBalancerName defaulted via utils.NameHelper so it stays unique across
//     clusters that share the vngcloud account (matches Ingress / Service).
//
// The returned spec is a fresh copy; the input is not mutated.
func BuildLBSpec(gw *gwv1.Gateway, effective *vksv1alpha1.LoadBalancerConfigSpec, clusterID string) *vksv1alpha1.LoadBalancerConfigSpec {
	out := *effective
	out.Type = loadbalancerv2.LoadBalancerTypeLayer7
	out.ClusterId = ptr.To(clusterID)
	if out.LoadBalancerName == "" {
		// vks_<cluster10>_<ns10>_<name10>_<hash5> — collision-safe, ≤50 chars.
		nh := utils.NewNameHelper(clusterID, domain.KindGateway, gw.Namespace, gw.Name)
		out.LoadBalancerName = nh.GetLoadBalancerDefaultName()
	}
	return &out
}
