package alb_gateway_uc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func makeGateway(ns, name string, protocols ...gwv1.ProtocolType) *gwv1.Gateway {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
	}
	for i, p := range protocols {
		gw.Spec.Listeners = append(gw.Spec.Listeners, gwv1.Listener{
			Name:     gwv1.SectionName("listener-" + string(rune('a'+i))),
			Protocol: p,
			Port:     gwv1.PortNumber(80 + i),
		})
	}
	return gw
}

func makeRoute(ns, name string, parentNs, parentName string, hostnames ...string) *gwv1.HTTPRoute {
	parentNsTyped := gwv1.Namespace(parentNs)
	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
	}
	route.Spec.ParentRefs = []gwv1.ParentReference{{
		Namespace: &parentNsTyped,
		Name:      gwv1.ObjectName(parentName),
	}}
	for _, h := range hostnames {
		route.Spec.Hostnames = append(route.Spec.Hostnames, gwv1.Hostname(h))
	}
	return route
}

func TestRouteAcceptableNamespace(t *testing.T) {
	gw := makeGateway("prod", "my-gw", gwv1.HTTPProtocolType)

	t.Run("same namespace accepted by default", func(t *testing.T) {
		route := makeRoute("prod", "r", "prod", "my-gw")
		assert.True(t, routeAcceptableNamespace(route, gw, nil))
	})
	t.Run("different namespace rejected by default", func(t *testing.T) {
		route := makeRoute("staging", "r", "prod", "my-gw")
		assert.False(t, routeAcceptableNamespace(route, gw, nil))
	})
	t.Run("NamespacesFromAll accepts any namespace", func(t *testing.T) {
		gwAll := makeGateway("prod", "my-gw", gwv1.HTTPProtocolType)
		from := gwv1.NamespacesFromAll
		gwAll.Spec.Listeners[0].AllowedRoutes = &gwv1.AllowedRoutes{
			Namespaces: &gwv1.RouteNamespaces{From: &from},
		}
		route := makeRoute("other-ns", "r", "prod", "my-gw")
		assert.True(t, routeAcceptableNamespace(route, gwAll, nil))
	})
	t.Run("NamespacesFromSame rejects other namespace", func(t *testing.T) {
		gwSame := makeGateway("prod", "my-gw", gwv1.HTTPProtocolType)
		from := gwv1.NamespacesFromSame
		gwSame.Spec.Listeners[0].AllowedRoutes = &gwv1.AllowedRoutes{
			Namespaces: &gwv1.RouteNamespaces{From: &from},
		}
		route := makeRoute("other-ns", "r", "prod", "my-gw")
		assert.False(t, routeAcceptableNamespace(route, gwSame, nil))
	})
	t.Run("NamespacesFromSame accepts same namespace", func(t *testing.T) {
		gwSame := makeGateway("prod", "my-gw", gwv1.HTTPProtocolType)
		from := gwv1.NamespacesFromSame
		gwSame.Spec.Listeners[0].AllowedRoutes = &gwv1.AllowedRoutes{
			Namespaces: &gwv1.RouteNamespaces{From: &from},
		}
		route := makeRoute("prod", "r", "prod", "my-gw")
		assert.True(t, routeAcceptableNamespace(route, gwSame, nil))
	})
	t.Run("NamespacesFromSelector matches the route namespace labels", func(t *testing.T) {
		gwSel := makeGateway("prod", "my-gw", gwv1.HTTPProtocolType)
		from := gwv1.NamespacesFromSelector
		gwSel.Spec.Listeners[0].AllowedRoutes = &gwv1.AllowedRoutes{
			Namespaces: &gwv1.RouteNamespaces{
				From:     &from,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "blue"}},
			},
		}
		route := makeRoute("any-ns", "r", "prod", "my-gw")
		assert.True(t, routeAcceptableNamespace(route, gwSel, map[string]string{"team": "blue"}), "matching labels accepted")
		assert.False(t, routeAcceptableNamespace(route, gwSel, map[string]string{"team": "red"}), "non-matching labels rejected")
		assert.False(t, routeAcceptableNamespace(route, gwSel, nil), "no labels rejected")
	})
	t.Run("non-HTTP listeners are skipped", func(t *testing.T) {
		gwTCP := makeGateway("prod", "my-gw", gwv1.TCPProtocolType)
		route := makeRoute("prod", "r", "prod", "my-gw")
		// No HTTP/HTTPS listeners → no listener can accept
		assert.False(t, routeAcceptableNamespace(route, gwTCP, nil))
	})
}

func TestListenerAcceptsRoute(t *testing.T) {
	listener := &gwv1.Listener{
		Name:     "http",
		Protocol: gwv1.HTTPProtocolType,
		Port:     80,
	}
	parentRef := gwv1.ParentReference{Name: "my-gw"}

	t.Run("no sectionName and no hostname constraint: accepted", func(t *testing.T) {
		route := makeRoute("prod", "r", "prod", "my-gw", "example.com")
		assert.True(t, listenerAcceptsRoute(listener, route, &parentRef))
	})

	t.Run("sectionName matches listener name: accepted", func(t *testing.T) {
		sn := gwv1.SectionName("http")
		ref := gwv1.ParentReference{Name: "my-gw", SectionName: &sn}
		route := makeRoute("prod", "r", "prod", "my-gw")
		assert.True(t, listenerAcceptsRoute(listener, route, &ref))
	})

	t.Run("sectionName mismatch: rejected", func(t *testing.T) {
		sn := gwv1.SectionName("https")
		ref := gwv1.ParentReference{Name: "my-gw", SectionName: &sn}
		route := makeRoute("prod", "r", "prod", "my-gw")
		assert.False(t, listenerAcceptsRoute(listener, route, &ref))
	})

	t.Run("listener hostname constraint matches route hostname", func(t *testing.T) {
		h := gwv1.Hostname("example.com")
		l := &gwv1.Listener{Hostname: &h, Protocol: gwv1.HTTPProtocolType}
		route := makeRoute("prod", "r", "prod", "my-gw", "example.com")
		assert.True(t, listenerAcceptsRoute(l, route, &parentRef))
	})

	t.Run("listener hostname constraint does not match route hostname: rejected", func(t *testing.T) {
		h := gwv1.Hostname("other.com")
		l := &gwv1.Listener{Hostname: &h, Protocol: gwv1.HTTPProtocolType}
		route := makeRoute("prod", "r", "prod", "my-gw", "example.com")
		assert.False(t, listenerAcceptsRoute(l, route, &parentRef))
	})

	t.Run("listener has hostname but route has no hostnames: accepted (route inherits)", func(t *testing.T) {
		h := gwv1.Hostname("example.com")
		l := &gwv1.Listener{Hostname: &h, Protocol: gwv1.HTTPProtocolType}
		route := makeRoute("prod", "r", "prod", "my-gw") // no hostnames
		assert.True(t, listenerAcceptsRoute(l, route, &parentRef))
	})

	t.Run("no listener hostname: accepted regardless of route hostnames", func(t *testing.T) {
		route := makeRoute("prod", "r", "prod", "my-gw", "foo.com", "bar.com")
		assert.True(t, listenerAcceptsRoute(listener, route, &parentRef))
	})
}

func TestMatchingRouteHostnames(t *testing.T) {
	t.Run("no listener hostname, no route hostnames: empty", func(t *testing.T) {
		l := &gwv1.Listener{Protocol: gwv1.HTTPProtocolType}
		route := makeRoute("prod", "r", "prod", "gw")
		got := matchingRouteHostnames(l, route)
		assert.Empty(t, got)
	})
	t.Run("no listener hostname, route has hostnames: all returned", func(t *testing.T) {
		l := &gwv1.Listener{Protocol: gwv1.HTTPProtocolType}
		route := makeRoute("prod", "r", "prod", "gw", "a.com", "b.com")
		got := matchingRouteHostnames(l, route)
		assert.ElementsMatch(t, []string{"a.com", "b.com"}, got)
	})
	t.Run("listener hostname, no route hostnames: listener hostname returned", func(t *testing.T) {
		h := gwv1.Hostname("example.com")
		l := &gwv1.Listener{Hostname: &h, Protocol: gwv1.HTTPProtocolType}
		route := makeRoute("prod", "r", "prod", "gw")
		got := matchingRouteHostnames(l, route)
		assert.Equal(t, []string{"example.com"}, got)
	})
	t.Run("literal listener matches exact route hostname", func(t *testing.T) {
		h := gwv1.Hostname("example.com")
		l := &gwv1.Listener{Hostname: &h, Protocol: gwv1.HTTPProtocolType}
		route := makeRoute("prod", "r", "prod", "gw", "example.com", "other.com")
		got := matchingRouteHostnames(l, route)
		assert.Equal(t, []string{"example.com"}, got)
	})
	t.Run("wildcard listener matches wildcard route hostname", func(t *testing.T) {
		h := gwv1.Hostname("*.example.com")
		l := &gwv1.Listener{Hostname: &h, Protocol: gwv1.HTTPProtocolType}
		route := makeRoute("prod", "r", "prod", "gw", "*.example.com")
		got := matchingRouteHostnames(l, route)
		assert.Contains(t, got, "*.example.com")
	})
}

// makeRouteWithParentRef builds an HTTPRoute with an explicit ParentReference.
// ParentRefs lives in the embedded CommonRouteSpec so must be set after construction.
func makeRouteWithParentRef(ns, name string, refs ...gwv1.ParentReference) *gwv1.HTTPRoute {
	r := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	r.Spec.ParentRefs = refs
	return r
}

func TestParentRefsForGateway(t *testing.T) {
	gw := makeGateway("prod", "my-gw", gwv1.HTTPProtocolType)
	gw.Name = "my-gw"
	gw.Namespace = "prod"

	t.Run("matching ref included", func(t *testing.T) {
		ns := gwv1.Namespace("prod")
		route := makeRouteWithParentRef("prod", "r", gwv1.ParentReference{
			Namespace: &ns,
			Name:      "my-gw",
		})
		refs := parentRefsForGateway(route, gw)
		assert.Len(t, refs, 1)
	})

	t.Run("different gateway name excluded", func(t *testing.T) {
		ns := gwv1.Namespace("prod")
		route := makeRouteWithParentRef("prod", "r", gwv1.ParentReference{
			Namespace: &ns,
			Name:      "other-gw",
		})
		refs := parentRefsForGateway(route, gw)
		assert.Empty(t, refs)
	})

	t.Run("different namespace excluded", func(t *testing.T) {
		ns := gwv1.Namespace("staging")
		route := makeRouteWithParentRef("prod", "r", gwv1.ParentReference{
			Namespace: &ns,
			Name:      "my-gw",
		})
		refs := parentRefsForGateway(route, gw)
		assert.Empty(t, refs)
	})

	t.Run("non-Gateway kind excluded", func(t *testing.T) {
		ns := gwv1.Namespace("prod")
		kindService := gwv1.Kind("Service")
		route := makeRouteWithParentRef("prod", "r", gwv1.ParentReference{
			Namespace: &ns,
			Name:      "my-gw",
			Kind:      &kindService,
		})
		refs := parentRefsForGateway(route, gw)
		assert.Empty(t, refs)
	})

	t.Run("nil namespace uses route namespace", func(t *testing.T) {
		// route.Namespace == gw.Namespace → matches
		route := makeRouteWithParentRef("prod", "r", gwv1.ParentReference{
			Name: "my-gw",
		})
		refs := parentRefsForGateway(route, gw)
		assert.Len(t, refs, 1)
	})
}

func TestParentRefsForGateway_WithSectionName(t *testing.T) {
	gw := makeGateway("prod", "my-gw", gwv1.HTTPProtocolType)
	sn := gwv1.SectionName("http")
	ns := gwv1.Namespace("prod")
	route := makeRouteWithParentRef("prod", "r", gwv1.ParentReference{
		Namespace:   &ns,
		Name:        "my-gw",
		SectionName: &sn,
	})
	refs := parentRefsForGateway(route, gw)
	assert.Len(t, refs, 1)
	assert.Equal(t, gwv1.SectionName("http"), *refs[0].SectionName)
}

func TestBuildListenerPolicies_RedirectFilter(t *testing.T) {
	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r"},
	}
	route.UID = "test-uid-1234"

	host := gwv1.PreciseHostname("new.com")
	rule := gwv1.HTTPRouteRule{
		Filters: []gwv1.HTTPRouteFilter{{
			Type: gwv1.HTTPRouteFilterRequestRedirect,
			RequestRedirect: &gwv1.HTTPRequestRedirectFilter{
				Scheme:   ptr.To("https"),
				Hostname: &host,
			},
		}},
	}

	policies := newTestTask(t, &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "gw"}}).buildListenerPolicies(route, 0, rule, []string{"example.com"}, "pool-1")
	assert.Len(t, policies, 1)
	// RedirectPoolName should be nil when redirect filter is applied
	assert.Nil(t, policies[0].policy.RedirectPoolName)
	assert.NotNil(t, policies[0].policy.RedirectUrl)
}

func TestBuildListenerPolicies_DefaultPoolRedirect(t *testing.T) {
	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r"},
	}
	route.UID = "test-uid-1234"

	rule := gwv1.HTTPRouteRule{
		Matches: []gwv1.HTTPRouteMatch{{Path: &gwv1.HTTPPathMatch{Value: ptr.To("/api")}}},
	}

	policies := newTestTask(t, &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "gw"}}).buildListenerPolicies(route, 0, rule, []string{"example.com"}, "pool-1")
	assert.Len(t, policies, 1)
	assert.NotNil(t, policies[0].policy.RedirectPoolName)
	assert.Equal(t, "pool-1", *policies[0].policy.RedirectPoolName)
}

func TestBuildListenerPolicies_EmptyMatch(t *testing.T) {
	route := &gwv1.HTTPRoute{}
	route.UID = "uid"
	// empty Matches → treated as single default match
	rule := gwv1.HTTPRouteRule{}
	policies := newTestTask(t, &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "gw"}}).buildListenerPolicies(route, 0, rule, []string{}, "my-pool")
	// no hosts + no matches → 1 policy (empty host, empty match)
	assert.Len(t, policies, 1)
}
