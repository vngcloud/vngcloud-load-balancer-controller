package shared

import (
	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

// ResolveTargetGroupProps walks tgcs and returns the most specific
// TargetGroupProperties for the given backend Service + route + optional rule name.
// The returned bool is true when a tie at the chosen specificity level was detected.
//
// Specificity ladder (high → low):
//
//	2 — RouteSpecificConfig matching (Kind, Name, RuleName)
//	1 — RouteSpecificConfig matching (Kind, Name) with no RuleName
//	0 — DefaultConfig
func ResolveTargetGroupProps(
	tgcs []gatewayv1alpha1.TargetGroupConfig,
	serviceName, routeKind, routeName string,
	ruleName *string,
) (gatewayv1alpha1.TargetGroupProperties, bool) {
	var best *gatewayv1alpha1.TargetGroupProperties
	bestSpecificity := -1
	conflict := false

	for i := range tgcs {
		tgc := &tgcs[i]
		if !targetMatchesService(tgc.Spec.TargetReference, serviceName) {
			continue
		}

		// Inspect every route override.
		matched := false
		for _, rsc := range tgc.Spec.RouteConfigurations {
			if rsc.RouteIdentifier.Kind != routeKind || rsc.RouteIdentifier.Name != routeName {
				continue
			}
			specVal := 1
			switch {
			case rsc.RouteIdentifier.RuleName != nil && ruleName != nil && *rsc.RouteIdentifier.RuleName == *ruleName:
				specVal = 2
			case rsc.RouteIdentifier.RuleName != nil:
				continue // rule-named override but doesn't apply to this rule
			}
			matched = true
			if specVal > bestSpecificity {
				cfg := rsc.Config
				best = &cfg
				bestSpecificity = specVal
				conflict = false
			} else if specVal == bestSpecificity {
				conflict = true
			}
		}

		// Default config is a candidate at specificity 0 if no route override matched.
		if !matched && bestSpecificity < 0 {
			cfg := tgc.Spec.DefaultConfig
			best = &cfg
			bestSpecificity = 0
		} else if !matched && bestSpecificity == 0 {
			conflict = true
		}
	}

	if best == nil {
		return gatewayv1alpha1.TargetGroupProperties{}, false
	}
	return *best, conflict
}

func targetMatchesService(ref gatewayv1alpha1.TargetReference, name string) bool {
	if ref.Name != name {
		return false
	}
	if ref.Kind != nil && *ref.Kind != "" && *ref.Kind != "Service" {
		return false
	}
	if ref.Group != nil && *ref.Group != "" {
		return false
	}
	return true
}
