package alb_gateway_uc

import (
	"context"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// BuildSecurityGroups computes the LB-level security-group ID list for a Gateway.
//
// Phase 1: the merged effective LBC.Tags / annotations carry SG configuration the
// existing lbc_uc deploy path already honors, so this is a passthrough that returns
// no extra SGs. The signature exists so future tasks (Phase 3) can add Gateway-level
// SG synthesis (e.g., per-route allowedCidrs aggregation) without touching callers.
func (uc *albGatewayUseCase) BuildSecurityGroups(_ context.Context, _ *vksv1alpha1.LoadBalancerConfigSpec) []string {
	return nil
}
