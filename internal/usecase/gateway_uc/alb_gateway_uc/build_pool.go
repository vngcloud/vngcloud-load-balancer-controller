package alb_gateway_uc

import (
	"fmt"
	"math"
	"strings"

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
				Name:        memberName(ep, int(b.Backend.Port)),
				IP:          ep,
				Port:        int(b.Backend.Port),
				MonitorPort: int(b.Backend.Port), // CRD-required; default health-check on traffic port
				Weight:      ptr.To(w),
			})
		}
	}
	return out
}

// memberName derives a deterministic, vngcloud-acceptable PoolMember.Name from an
// endpoint (IP, port). vngcloud requires names to match [a-zA-Z0-9_.-] and be 5-50
// chars; every controller-managed name carries the "vks-" prefix.
//
// Example: 10.0.100.3:32428 → "vks-m-10-0-100-3-32428".
func memberName(ip string, port int) string {
	safe := strings.ReplaceAll(ip, ".", "-")
	safe = strings.ReplaceAll(safe, ":", "-") // IPv6 zone separator, defensive
	name := fmt.Sprintf("vks-m-%s-%d", safe, port)
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}
