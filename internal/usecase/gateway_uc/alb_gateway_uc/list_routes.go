package alb_gateway_uc

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// listAttachedHTTPRoutes returns HTTPRoutes whose parentRefs reference the
// Gateway and whose namespace + hostname satisfy at least one listener's
// allowedRoutes. Caller still has to check per-listener attachment when
// generating policies.
func (t *defaultGatewayBuildTask) listAttachedHTTPRoutes(ctx context.Context) ([]gwv1.HTTPRoute, error) {
	var list gwv1.HTTPRouteList
	sel := fields.OneTermEqualSelector(shared.IndexHTTPRouteByParentGateway,
		t.gw.Namespace+"/"+t.gw.Name)
	if err := t.uc.k8sClient.List(ctx, &list, client.MatchingFieldsSelector{Selector: sel}); err != nil {
		return nil, fmt.Errorf("list HTTPRoutes attached to %s/%s: %w", t.gw.Namespace, t.gw.Name, err)
	}
	out := make([]gwv1.HTTPRoute, 0, len(list.Items))
	for i := range list.Items {
		nsLabels, err := t.namespaceLabels(ctx, list.Items[i].Namespace)
		if err != nil {
			return nil, err
		}
		if !routeAcceptableNamespace(&list.Items[i], t.gw, nsLabels) {
			continue
		}
		out = append(out, list.Items[i])
	}
	// Stable iteration order. controller-runtime cache returns List items in
	// non-deterministic map order; without this, downstream stable-sort by
	// match specificity inherits that order on ties, and Position assignments
	// flip between reconciles → LBC.Spec churn → reorder loop.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// routeAcceptableNamespace runs the listener-level allowedRoutes.namespaces
// gate at the Gateway level — if no listener allows the route's namespace, the
// route is dropped entirely. Per-listener attachment (hostname / sectionName)
// is finer-grained and runs in policy generation.
//
// Honors NamespacesFromAll, NamespacesFromSame, and NamespacesFromSelector
// (the route's namespace labels, passed in, are matched against the listener's
// selector).
func routeAcceptableNamespace(r *gwv1.HTTPRoute, gw *gwv1.Gateway, routeNsLabels map[string]string) bool {
	for i := range gw.Spec.Listeners {
		l := &gw.Spec.Listeners[i]
		if l.Protocol != gwv1.HTTPProtocolType && l.Protocol != gwv1.HTTPSProtocolType {
			continue
		}
		if l.AllowedRoutes == nil || l.AllowedRoutes.Namespaces == nil || l.AllowedRoutes.Namespaces.From == nil {
			// Default: same-namespace.
			if r.Namespace == gw.Namespace {
				return true
			}
			continue
		}
		switch *l.AllowedRoutes.Namespaces.From {
		case gwv1.NamespacesFromAll:
			return true
		case gwv1.NamespacesFromSame:
			if r.Namespace == gw.Namespace {
				return true
			}
		case gwv1.NamespacesFromSelector:
			if l.AllowedRoutes.Namespaces.Selector == nil {
				continue
			}
			sel, err := metav1.LabelSelectorAsSelector(l.AllowedRoutes.Namespaces.Selector)
			if err != nil {
				continue
			}
			if sel.Matches(labels.Set(routeNsLabels)) {
				return true
			}
		}
	}
	return false
}

// namespaceLabels returns the labels on namespace ns (nil if it doesn't exist),
// used for NamespacesFromSelector matching.
func (t *defaultGatewayBuildTask) namespaceLabels(ctx context.Context, ns string) (map[string]string, error) {
	var n corev1.Namespace
	if err := t.uc.k8sClient.Get(ctx, types.NamespacedName{Name: ns}, &n); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return n.Labels, nil
}

// listenerAcceptsRoute is the per-listener attachment check. It returns true
// if a listener's hostname constraint allows at least one of the route's
// hostnames (or both are empty), and the parentRef sectionName (if any) names
// this listener.
func listenerAcceptsRoute(l *gwv1.Listener, route *gwv1.HTTPRoute, parentRef *gwv1.ParentReference) bool {
	if parentRef.SectionName != nil && string(*parentRef.SectionName) != string(l.Name) {
		return false
	}
	if l.Hostname == nil || string(*l.Hostname) == "" {
		return true
	}
	if len(route.Spec.Hostnames) == 0 {
		return true
	}
	for _, h := range route.Spec.Hostnames {
		if pkggw.HostnameMatches(string(*l.Hostname), string(h)) {
			return true
		}
	}
	return false
}

// matchingRouteHostnames returns the route hostnames that match the listener.
// If the listener has no hostname, route hostnames are returned verbatim
// (empty slice means "accept any host" → caller emits one policy with
// no HOST_NAME rule).
func matchingRouteHostnames(l *gwv1.Listener, route *gwv1.HTTPRoute) []string {
	if l.Hostname == nil || string(*l.Hostname) == "" {
		out := make([]string, 0, len(route.Spec.Hostnames))
		for _, h := range route.Spec.Hostnames {
			out = append(out, string(h))
		}
		return out
	}
	listenerHost := string(*l.Hostname)
	if len(route.Spec.Hostnames) == 0 {
		return []string{listenerHost}
	}
	out := make([]string, 0, len(route.Spec.Hostnames))
	for _, h := range route.Spec.Hostnames {
		if pkggw.HostnameMatches(listenerHost, string(h)) {
			out = append(out, string(h))
		}
	}
	return out
}

// parentRefsForGateway returns the ParentReference entries on a route that
// point at gw. Routes can have multiple parent gateways; we only emit
// policies for the parent matching this reconcile.
func parentRefsForGateway(route *gwv1.HTTPRoute, gw *gwv1.Gateway) []gwv1.ParentReference {
	out := make([]gwv1.ParentReference, 0, len(route.Spec.ParentRefs))
	for i := range route.Spec.ParentRefs {
		p := &route.Spec.ParentRefs[i]
		if p.Kind != nil && *p.Kind != "Gateway" {
			continue
		}
		ns := route.Namespace
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		if ns != gw.Namespace || string(p.Name) != gw.Name {
			continue
		}
		out = append(out, *p)
	}
	return out
}
