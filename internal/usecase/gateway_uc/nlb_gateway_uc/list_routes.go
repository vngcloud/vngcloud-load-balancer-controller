package nlb_gateway_uc

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

// l4Route is a protocol-agnostic view of a TCPRoute or UDPRoute so the listener
// attachment and pool-building logic doesn't branch on kind. backendRefs are
// flattened across all of the route's rules (L4 routes usually have one rule).
type l4Route struct {
	kind        string // "TCPRoute" | "UDPRoute"
	namespace   string
	name        string
	uid         string
	creation    int64 // creationTimestamp unix seconds, for oldest-wins
	parentRefs  []gwv1.ParentReference
	backendRefs []gwv1.BackendRef
}

// listAttachedRoutes returns every TCPRoute and UDPRoute whose parentRefs
// reference this Gateway. Per-listener selection happens in oldestRouteForListener.
func (t *nlbBuildTask) listAttachedRoutes(ctx context.Context) ([]*l4Route, error) {
	key := t.gw.Namespace + "/" + t.gw.Name
	out := make([]*l4Route, 0)

	var tcp gwv1a2.TCPRouteList
	if err := t.uc.k8sClient.List(ctx, &tcp,
		client.MatchingFieldsSelector{Selector: fields.OneTermEqualSelector(shared.IndexTCPRouteByParentGateway, key)},
	); err != nil {
		return nil, fmt.Errorf("list TCPRoutes attached to %s: %w", key, err)
	}
	for i := range tcp.Items {
		r := &tcp.Items[i]
		out = append(out, &l4Route{
			kind: "TCPRoute", namespace: r.Namespace, name: r.Name, uid: string(r.UID),
			creation:    r.CreationTimestamp.Unix(),
			parentRefs:  r.Spec.ParentRefs,
			backendRefs: flattenTCPBackendRefs(r),
		})
	}

	var udp gwv1a2.UDPRouteList
	if err := t.uc.k8sClient.List(ctx, &udp,
		client.MatchingFieldsSelector{Selector: fields.OneTermEqualSelector(shared.IndexUDPRouteByParentGateway, key)},
	); err != nil {
		return nil, fmt.Errorf("list UDPRoutes attached to %s: %w", key, err)
	}
	for i := range udp.Items {
		r := &udp.Items[i]
		out = append(out, &l4Route{
			kind: "UDPRoute", namespace: r.Namespace, name: r.Name, uid: string(r.UID),
			creation:    r.CreationTimestamp.Unix(),
			parentRefs:  r.Spec.ParentRefs,
			backendRefs: flattenUDPBackendRefs(r),
		})
	}
	return out, nil
}

func flattenTCPBackendRefs(r *gwv1a2.TCPRoute) []gwv1.BackendRef {
	out := make([]gwv1.BackendRef, 0)
	for i := range r.Spec.Rules {
		out = append(out, r.Spec.Rules[i].BackendRefs...)
	}
	return out
}

func flattenUDPBackendRefs(r *gwv1a2.UDPRoute) []gwv1.BackendRef {
	out := make([]gwv1.BackendRef, 0)
	for i := range r.Spec.Rules {
		out = append(out, r.Spec.Rules[i].BackendRefs...)
	}
	return out
}

// oldestRouteForListener returns the oldest L4 route (by creationTimestamp, then
// namespace/name) whose kind matches the listener protocol and whose parentRef
// attaches to this listener. An L4 listener has a single default pool, so when
// several routes target one listener the oldest wins (GEP-style determinism).
func oldestRouteForListener(routes []*l4Route, gw *gwv1.Gateway, l *gwv1.Listener) *l4Route {
	wantKind := ""
	switch l.Protocol {
	case gwv1.TCPProtocolType:
		wantKind = "TCPRoute"
	case gwv1.UDPProtocolType:
		wantKind = "UDPRoute"
	default:
		return nil
	}

	matches := make([]*l4Route, 0)
	for _, r := range routes {
		if r.kind != wantKind {
			continue
		}
		if routeAttachesToListener(r, gw, l) {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].creation != matches[j].creation {
			return matches[i].creation < matches[j].creation
		}
		if matches[i].namespace != matches[j].namespace {
			return matches[i].namespace < matches[j].namespace
		}
		return matches[i].name < matches[j].name
	})
	return matches[0]
}

// routeAttachesToListener reports whether one of the route's parentRefs targets
// this Gateway listener: same Gateway, and the parentRef's sectionName (if set)
// names this listener, and the parentRef's port (if set) equals this listener's.
func routeAttachesToListener(r *l4Route, gw *gwv1.Gateway, l *gwv1.Listener) bool {
	for i := range r.parentRefs {
		p := &r.parentRefs[i]
		if p.Kind != nil && *p.Kind != "Gateway" {
			continue
		}
		ns := r.namespace
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		if ns != gw.Namespace || string(p.Name) != gw.Name {
			continue
		}
		if p.SectionName != nil && string(*p.SectionName) != string(l.Name) {
			continue
		}
		if p.Port != nil && int32(*p.Port) != int32(l.Port) {
			continue
		}
		return true
	}
	return false
}
