package alb_gateway_uc

import (
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// attachedRoute pairs an HTTPRoute with the parentRef entries that resolved to
// the target Gateway. Per-listener filtering is then applied via routeAttachesToListener.
type attachedRoute struct {
	Route   *gwv1.HTTPRoute
	Parents []gwv1.ParentReference
}

// routesAttachedToGateway returns every HTTPRoute whose parentRefs match the
// given Gateway. The matched parentRefs are kept on the result so per-listener
// narrowing (SectionName / Port) can be applied later without re-scanning.
//
// A parentRef matches the Gateway when:
//   - Group is gateway.networking.k8s.io (or unset, the default)
//   - Kind is Gateway (or unset)
//   - Namespace matches the gateway's (defaulting to the route's own namespace)
//   - Name equals the gateway's name
func routesAttachedToGateway(routes []gwv1.HTTPRoute, gw *gwv1.Gateway) []attachedRoute {
	out := make([]attachedRoute, 0, len(routes))
	for i := range routes {
		r := &routes[i]
		matched := matchedParentRefs(r, gw)
		if len(matched) == 0 {
			continue
		}
		out = append(out, attachedRoute{Route: r, Parents: matched})
	}
	return out
}

func matchedParentRefs(route *gwv1.HTTPRoute, gw *gwv1.Gateway) []gwv1.ParentReference {
	var out []gwv1.ParentReference
	for _, p := range route.Spec.ParentRefs {
		if p.Group != nil && *p.Group != "" && string(*p.Group) != gwv1.GroupName {
			continue
		}
		if p.Kind != nil && *p.Kind != "" && string(*p.Kind) != "Gateway" {
			continue
		}
		ns := route.Namespace
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		if ns != gw.Namespace || string(p.Name) != gw.Name {
			continue
		}
		out = append(out, p)
	}
	return out
}

// routeAttachesToListener decides whether an HTTPRoute (already matched to the
// Gateway via routesAttachedToGateway) further attaches to a specific listener,
// honoring:
//   - listener.Protocol must be HTTP or HTTPS (HTTPRoutes don't attach to L4)
//   - listener.AllowedRoutes.Kinds (default: HTTPRoute)
//   - listener.AllowedRoutes.Namespaces (Same / All — Phase 1 does not honor selectors)
//   - parentRef.SectionName narrowing (must equal listener.Name when set)
//   - parentRef.Port narrowing (must equal listener.Port when set)
func routeAttachesToListener(
	listener *gwv1.Listener,
	gwNamespace string,
	matched []gwv1.ParentReference,
	routeNamespace string,
) bool {
	if listener.Protocol != gwv1.HTTPProtocolType && listener.Protocol != gwv1.HTTPSProtocolType {
		return false
	}
	if !allowsHTTPRouteKind(listener.AllowedRoutes) {
		return false
	}
	if !allowsRouteNamespace(listener.AllowedRoutes, gwNamespace, routeNamespace) {
		return false
	}
	for _, p := range matched {
		if p.SectionName != nil && *p.SectionName != "" && string(*p.SectionName) != string(listener.Name) {
			continue
		}
		if p.Port != nil && int32(*p.Port) != int32(listener.Port) {
			continue
		}
		return true
	}
	return false
}

func allowsHTTPRouteKind(ar *gwv1.AllowedRoutes) bool {
	if ar == nil || len(ar.Kinds) == 0 {
		return true
	}
	for _, k := range ar.Kinds {
		if k.Group != nil && *k.Group != "" && string(*k.Group) != gwv1.GroupName {
			continue
		}
		if string(k.Kind) == "HTTPRoute" {
			return true
		}
	}
	return false
}

func allowsRouteNamespace(ar *gwv1.AllowedRoutes, gwNS, routeNS string) bool {
	from := gwv1.NamespacesFromSame
	if ar != nil && ar.Namespaces != nil && ar.Namespaces.From != nil {
		from = *ar.Namespaces.From
	}
	switch from {
	case gwv1.NamespacesFromAll:
		return true
	case gwv1.NamespacesFromSame:
		return routeNS == gwNS
	case gwv1.NamespacesFromSelector:
		// Phase 1: selector not honored — fall back to Same to be conservative.
		return routeNS == gwNS
	default:
		return routeNS == gwNS
	}
}
