package alb_gateway_uc

import (
	"math"

	"k8s.io/utils/ptr"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// BackendEndpoints groups one HTTPRoute backendRef with its resolved endpoints.
// Endpoints are pod IPs (target-type=ip) or node IPs (target-type=instance).
type BackendEndpoints struct {
	Backend   pkggw.BackendKey
	Endpoints []string
}

// SynthesizeMembers maps weighted backends to a flat list of pool members with
// integer weights scaled so the cross-backend traffic ratio matches the requested
// HTTPRoute backendRef weights.
//
// Algorithm:
//  1. Per-endpoint share = backend.Weight / count(endpoints).
//  2. Find the smallest non-zero share; rescale every endpoint share by min⁻¹.
//  3. Round to nearest int; floor at 1; cap at 100 to avoid huge integer weights.
//
// Backends with zero weight or zero endpoints are skipped (caller is expected
// to surface them as ResolvedRefs=False elsewhere if needed).
func SynthesizeMembers(in []BackendEndpoints) []vksv1alpha1.PoolMember {
	if len(in) == 0 {
		return nil
	}
	minShare := math.MaxFloat64
	for _, b := range in {
		if len(b.Endpoints) == 0 || b.Backend.Weight <= 0 {
			continue
		}
		share := float64(b.Backend.Weight) / float64(len(b.Endpoints))
		if share < minShare {
			minShare = share
		}
	}
	if minShare == math.MaxFloat64 {
		minShare = 1
	}

	out := make([]vksv1alpha1.PoolMember, 0)
	for _, b := range in {
		if b.Backend.Weight <= 0 || len(b.Endpoints) == 0 {
			continue
		}
		share := float64(b.Backend.Weight) / float64(len(b.Endpoints))
		w := int(math.Round(share / minShare))
		if w < 1 {
			w = 1
		}
		if w > 100 {
			w = 100
		}
		for _, ep := range b.Endpoints {
			out = append(out, vksv1alpha1.PoolMember{
				IP:     ep,
				Port:   int(b.Backend.Port),
				Weight: ptr.To(w),
			})
		}
	}
	return out
}
