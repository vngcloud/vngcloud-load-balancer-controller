package alb_gateway_uc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func parentRef(group, kind, ns, name, section string, port *int32) gwv1.ParentReference {
	p := gwv1.ParentReference{Name: gwv1.ObjectName(name)}
	if group != "" {
		g := gwv1.Group(group)
		p.Group = &g
	}
	if kind != "" {
		k := gwv1.Kind(kind)
		p.Kind = &k
	}
	if ns != "" {
		n := gwv1.Namespace(ns)
		p.Namespace = &n
	}
	if section != "" {
		s := gwv1.SectionName(section)
		p.SectionName = &s
	}
	if port != nil {
		pn := gwv1.PortNumber(*port)
		p.Port = &pn
	}
	return p
}

func newRoute(ns, name string, parents ...gwv1.ParentReference) gwv1.HTTPRoute {
	return gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: parents},
		},
	}
}

func TestRoutesAttachedToGateway_SameNamespace_DefaultGroupKind(t *testing.T) {
	gw := newGWWithListeners("g1", "ns", "albcls")
	routes := []gwv1.HTTPRoute{
		newRoute("ns", "r1", parentRef("", "", "", "g1", "", nil)),
		newRoute("ns", "r2", parentRef("", "", "", "other-gw", "", nil)),
	}
	got := routesAttachedToGateway(routes, gw)
	assert.Len(t, got, 1)
	assert.Equal(t, "r1", got[0].Route.Name)
}

func TestRoutesAttachedToGateway_CrossNamespace_RequiresExplicitNS(t *testing.T) {
	gw := newGWWithListeners("g1", "ns-gw", "albcls")
	routes := []gwv1.HTTPRoute{
		// route in ns-app referencing g1 in ns-gw — only attaches when Namespace is set
		newRoute("ns-app", "rOK", parentRef("", "", "ns-gw", "g1", "", nil)),
		newRoute("ns-app", "rNoNS", parentRef("", "", "", "g1", "", nil)),
	}
	got := routesAttachedToGateway(routes, gw)
	assert.Len(t, got, 1)
	assert.Equal(t, "rOK", got[0].Route.Name)
}

func TestRoutesAttachedToGateway_RejectsWrongGroupOrKind(t *testing.T) {
	gw := newGWWithListeners("g1", "ns", "albcls")
	routes := []gwv1.HTTPRoute{
		newRoute("ns", "rWrongGroup", parentRef("other.example", "Gateway", "", "g1", "", nil)),
		newRoute("ns", "rWrongKind", parentRef("", "Service", "", "g1", "", nil)),
	}
	got := routesAttachedToGateway(routes, gw)
	assert.Empty(t, got)
}

func TestRouteAttachesToListener_HTTPListener_NoFilters(t *testing.T) {
	listener := &gwv1.Listener{Name: "h", Protocol: gwv1.HTTPProtocolType, Port: 80}
	parents := []gwv1.ParentReference{parentRef("", "", "", "g1", "", nil)}
	assert.True(t, routeAttachesToListener(listener, "ns", parents, "ns"))
}

func TestRouteAttachesToListener_RejectsL4Protocols(t *testing.T) {
	for _, proto := range []gwv1.ProtocolType{gwv1.TCPProtocolType, gwv1.UDPProtocolType, gwv1.TLSProtocolType} {
		listener := &gwv1.Listener{Name: "x", Protocol: proto, Port: 1234}
		parents := []gwv1.ParentReference{parentRef("", "", "", "g1", "", nil)}
		assert.False(t, routeAttachesToListener(listener, "ns", parents, "ns"), "proto=%s", proto)
	}
}

func TestRouteAttachesToListener_HonorsSectionName(t *testing.T) {
	listener := &gwv1.Listener{Name: "h", Protocol: gwv1.HTTPProtocolType, Port: 80}
	otherSection := []gwv1.ParentReference{parentRef("", "", "", "g1", "different", nil)}
	matchSection := []gwv1.ParentReference{parentRef("", "", "", "g1", "h", nil)}
	noSection := []gwv1.ParentReference{parentRef("", "", "", "g1", "", nil)}

	assert.False(t, routeAttachesToListener(listener, "ns", otherSection, "ns"))
	assert.True(t, routeAttachesToListener(listener, "ns", matchSection, "ns"))
	assert.True(t, routeAttachesToListener(listener, "ns", noSection, "ns"))
}

func TestRouteAttachesToListener_HonorsPortNarrowing(t *testing.T) {
	listener := &gwv1.Listener{Name: "h", Protocol: gwv1.HTTPProtocolType, Port: 80}
	matchPort := []gwv1.ParentReference{parentRef("", "", "", "g1", "", ptr.To(int32(80)))}
	wrongPort := []gwv1.ParentReference{parentRef("", "", "", "g1", "", ptr.To(int32(443)))}

	assert.True(t, routeAttachesToListener(listener, "ns", matchPort, "ns"))
	assert.False(t, routeAttachesToListener(listener, "ns", wrongPort, "ns"))
}

func TestRouteAttachesToListener_AllowedRoutes_NamespaceSame(t *testing.T) {
	from := gwv1.NamespacesFromSame
	listener := &gwv1.Listener{
		Name: "h", Protocol: gwv1.HTTPProtocolType, Port: 80,
		AllowedRoutes: &gwv1.AllowedRoutes{Namespaces: &gwv1.RouteNamespaces{From: &from}},
	}
	parents := []gwv1.ParentReference{parentRef("", "", "", "g1", "", nil)}
	assert.True(t, routeAttachesToListener(listener, "ns-gw", parents, "ns-gw"))
	assert.False(t, routeAttachesToListener(listener, "ns-gw", parents, "ns-app"))
}

func TestRouteAttachesToListener_AllowedRoutes_NamespaceAll(t *testing.T) {
	from := gwv1.NamespacesFromAll
	listener := &gwv1.Listener{
		Name: "h", Protocol: gwv1.HTTPProtocolType, Port: 80,
		AllowedRoutes: &gwv1.AllowedRoutes{Namespaces: &gwv1.RouteNamespaces{From: &from}},
	}
	parents := []gwv1.ParentReference{parentRef("", "", "", "g1", "", nil)}
	assert.True(t, routeAttachesToListener(listener, "ns-gw", parents, "ns-app"))
}

func TestRouteAttachesToListener_AllowedRoutes_KindFilter(t *testing.T) {
	listener := &gwv1.Listener{
		Name: "h", Protocol: gwv1.HTTPProtocolType, Port: 80,
		AllowedRoutes: &gwv1.AllowedRoutes{
			Kinds: []gwv1.RouteGroupKind{{Kind: gwv1.Kind("TCPRoute")}},
		},
	}
	parents := []gwv1.ParentReference{parentRef("", "", "", "g1", "", nil)}
	assert.False(t, routeAttachesToListener(listener, "ns", parents, "ns"))
}
