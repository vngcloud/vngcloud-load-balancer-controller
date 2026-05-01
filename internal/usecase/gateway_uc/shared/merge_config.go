// Package shared holds Gateway-controller business helpers used by both ALB and NLB use-cases.
package shared

import (
	clone "github.com/huandu/go-clone"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// MergeLBC merges a class-level LBC spec with a gateway-level LBC spec, returning a
// new spec that represents the effective configuration. Either argument may be nil.
//
// Rules:
//   - mode = class.MergingMode (default PreferGateway). Per design, only the class-level
//     LBC's MergingMode field is honored.
//   - Scalar/pointer fields: PreferGateway → gateway value wins if non-nil/non-zero, else class.
//     PreferGatewayClass → class value wins if non-nil/non-zero, else gateway.
//   - LoadBalancerId: gateway-only (class value ignored — class can't pin a single LB).
//   - Listeners[] and Tags: merged by name/key. Each per-item value resolved per mode.
func MergeLBC(class, gw *vksv1alpha1.LoadBalancerConfigSpec) *vksv1alpha1.LoadBalancerConfigSpec {
	if class == nil && gw == nil {
		return &vksv1alpha1.LoadBalancerConfigSpec{}
	}
	if class == nil {
		return clone.Clone(gw).(*vksv1alpha1.LoadBalancerConfigSpec)
	}
	if gw == nil {
		out := clone.Clone(class).(*vksv1alpha1.LoadBalancerConfigSpec)
		out.LoadBalancerId = nil
		return out
	}

	mode := vksv1alpha1.MergingModePreferGateway
	if class.MergingMode != nil {
		mode = *class.MergingMode
	}

	out := clone.Clone(class).(*vksv1alpha1.LoadBalancerConfigSpec)

	out.PackageId = pickPtr(class.PackageId, gw.PackageId, mode)
	out.Scheme = pickPtr(class.Scheme, gw.Scheme, mode)
	out.EnableAutoscale = pickPtr(class.EnableAutoscale, gw.EnableAutoscale, mode)
	out.IsPoc = pickPtr(class.IsPoc, gw.IsPoc, mode)
	out.SubnetId = pickStr(class.SubnetId, gw.SubnetId, mode)
	out.VpcId = pickStr(class.VpcId, gw.VpcId, mode)
	out.LoadBalancerName = pickStr(class.LoadBalancerName, gw.LoadBalancerName, mode)
	out.PrivateSubnetId = pickPtr(class.PrivateSubnetId, gw.PrivateSubnetId, mode)
	out.PrivateZoneId = pickPtr(class.PrivateZoneId, gw.PrivateZoneId, mode)
	out.BackendSubnetId = pickPtr(class.BackendSubnetId, gw.BackendSubnetId, mode)
	out.LoadBalancerId = gw.LoadBalancerId

	out.Tags = mergeStringMap(class.Tags, gw.Tags, mode)
	out.Listeners = mergeListenersByName(class.Listeners, gw.Listeners, mode)

	return out
}

// pickPtr selects between two pointer values. Non-nil wins; mode determines which side
// wins when both are non-nil.
func pickPtr[T any](class, gw *T, mode vksv1alpha1.MergingMode) *T {
	if mode == vksv1alpha1.MergingModePreferGatewayClass {
		if class != nil {
			return class
		}
		return gw
	}
	if gw != nil {
		return gw
	}
	return class
}

func pickStr(class, gw string, mode vksv1alpha1.MergingMode) string {
	if mode == vksv1alpha1.MergingModePreferGatewayClass {
		if class != "" {
			return class
		}
		return gw
	}
	if gw != "" {
		return gw
	}
	return class
}

func mergeStringMap(class, gw map[string]string, mode vksv1alpha1.MergingMode) map[string]string {
	if class == nil && gw == nil {
		return nil
	}
	out := map[string]string{}
	// Seed with the lower-precedence side.
	if mode == vksv1alpha1.MergingModePreferGatewayClass {
		for k, v := range gw {
			out[k] = v
		}
		for k, v := range class {
			out[k] = v
		}
	} else {
		for k, v := range class {
			out[k] = v
		}
		for k, v := range gw {
			out[k] = v
		}
	}
	return out
}

func mergeListenersByName(class, gw []vksv1alpha1.Listener, mode vksv1alpha1.MergingMode) []vksv1alpha1.Listener {
	byName := map[string]vksv1alpha1.Listener{}
	if mode == vksv1alpha1.MergingModePreferGatewayClass {
		for _, l := range gw {
			byName[l.Name] = l
		}
		for _, l := range class {
			byName[l.Name] = l
		}
	} else {
		for _, l := range class {
			byName[l.Name] = l
		}
		for _, l := range gw {
			byName[l.Name] = l
		}
	}
	out := make([]vksv1alpha1.Listener, 0, len(byName))
	for _, l := range byName {
		out = append(out, l)
	}
	return out
}
