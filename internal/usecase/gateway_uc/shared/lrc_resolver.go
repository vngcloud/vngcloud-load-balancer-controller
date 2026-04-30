package shared

import (
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

const (
	LRCGroup = "gateway.vks.vngcloud.vn"
	LRCKind  = "ListenerRuleConfig"
)

// ExtractLRCRefsFromFilters returns the names of all ListenerRuleConfig
// ExtensionRefs found in the given filters list (same-namespace only).
func ExtractLRCRefsFromFilters(filters []gwv1.HTTPRouteFilter) []string {
	var names []string
	for _, f := range filters {
		if f.Type != gwv1.HTTPRouteFilterExtensionRef || f.ExtensionRef == nil {
			continue
		}
		if string(f.ExtensionRef.Group) == LRCGroup && string(f.ExtensionRef.Kind) == LRCKind {
			names = append(names, string(f.ExtensionRef.Name))
		}
	}
	return names
}

// FindLRC looks up a ListenerRuleConfig by name within a slice (already namespace-filtered).
func FindLRC(lrcs []gatewayv1alpha1.ListenerRuleConfig, name string) (*gatewayv1alpha1.ListenerRuleConfig, bool) {
	for i := range lrcs {
		if lrcs[i].Name == name {
			return &lrcs[i], true
		}
	}
	return nil, false
}
