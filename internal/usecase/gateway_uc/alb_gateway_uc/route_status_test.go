package alb_gateway_uc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func httpListener(name, hostname string) gwv1.Listener {
	l := gwv1.Listener{Name: gwv1.SectionName(name), Protocol: gwv1.HTTPProtocolType, Port: 80}
	if hostname != "" {
		h := gwv1.Hostname(hostname)
		l.Hostname = &h
	}
	return l
}

func gwWithListeners(ns, name string, ls ...gwv1.Listener) *gwv1.Gateway {
	return &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       gwv1.GatewaySpec{Listeners: ls},
	}
}

func routeWith(ns, name string, hostnames []string, rules []gwv1.HTTPRouteRule) *gwv1.HTTPRoute {
	hs := make([]gwv1.Hostname, 0, len(hostnames))
	for _, h := range hostnames {
		hs = append(hs, gwv1.Hostname(h))
	}
	return &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: gwv1.ObjectName("gw")}}},
			Hostnames:       hs,
			Rules:           rules,
		},
	}
}

func backendRule(svcName string, opts ...func(*gwv1.BackendObjectReference)) gwv1.HTTPRouteRule {
	ref := gwv1.BackendObjectReference{Name: gwv1.ObjectName(svcName), Port: ptr.To(gwv1.PortNumber(80))}
	for _, o := range opts {
		o(&ref)
	}
	return gwv1.HTTPRouteRule{
		Matches:     []gwv1.HTTPRouteMatch{{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchPathPrefix), Value: ptr.To("/")}}},
		BackendRefs: []gwv1.HTTPBackendRef{{BackendRef: gwv1.BackendRef{BackendObjectReference: ref}}},
	}
}

func TestRouteAcceptedCondition(t *testing.T) {
	parent := gwv1.ParentReference{Name: "gw"}

	t.Run("accepts when a listener matches", func(t *testing.T) {
		gw := gwWithListeners("prod", "gw", httpListener("http", ""))
		r := routeWith("prod", "r", nil, []gwv1.HTTPRouteRule{backendRule("echo")})
		c := routeAcceptedCondition(gw, r, parent, 1)
		assert.Equal(t, metav1.ConditionTrue, c.Status)
		assert.Equal(t, string(gwv1.RouteReasonAccepted), c.Reason)
	})

	t.Run("hostname mismatch -> NoMatchingListenerHostname", func(t *testing.T) {
		gw := gwWithListeners("prod", "gw", httpListener("http", "app.example.com"))
		r := routeWith("prod", "r", []string{"other.example.org"}, []gwv1.HTTPRouteRule{backendRule("echo")})
		c := routeAcceptedCondition(gw, r, parent, 1)
		assert.Equal(t, metav1.ConditionFalse, c.Status)
		assert.Equal(t, string(gwv1.RouteReasonNoMatchingListenerHostname), c.Reason)
	})

	t.Run("sectionName mismatch, no hostnames -> NotAllowedByListeners", func(t *testing.T) {
		gw := gwWithListeners("prod", "gw", httpListener("web", ""))
		sn := gwv1.SectionName("nope")
		p := gwv1.ParentReference{Name: "gw", SectionName: &sn}
		r := routeWith("prod", "r", nil, []gwv1.HTTPRouteRule{backendRule("echo")})
		c := routeAcceptedCondition(gw, r, p, 1)
		assert.Equal(t, metav1.ConditionFalse, c.Status)
		assert.Equal(t, string(gwv1.RouteReasonNotAllowedByListeners), c.Reason)
	})
}

func TestRoutePartiallyInvalidCondition(t *testing.T) {
	t.Run("header match -> PartiallyInvalid", func(t *testing.T) {
		rule := backendRule("echo")
		rule.Matches[0].Headers = []gwv1.HTTPHeaderMatch{{Name: "X-Env", Value: "canary"}}
		r := routeWith("prod", "r", nil, []gwv1.HTTPRouteRule{rule})
		c, ok := routePartiallyInvalidCondition(r, 1)
		assert.True(t, ok)
		assert.Equal(t, metav1.ConditionTrue, c.Status)
		assert.Equal(t, string(gwv1.RouteReasonUnsupportedValue), c.Reason)
	})

	t.Run("path-only -> not partially invalid", func(t *testing.T) {
		r := routeWith("prod", "r", nil, []gwv1.HTTPRouteRule{backendRule("echo")})
		_, ok := routePartiallyInvalidCondition(r, 1)
		assert.False(t, ok)
	})
}

func TestRouteResolvedRefsCondition(t *testing.T) {
	gw := gwWithListeners("prod", "gw", httpListener("http", ""))
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "echo"}}

	cond := func(t *testing.T, r *gwv1.HTTPRoute, objs ...runtime.Object) metav1.Condition {
		task := newTestTaskWithObjs(t, gw, objs...)
		return task.routeResolvedRefsCondition(context.Background(), r, 1)
	}

	t.Run("existing same-ns Service -> ResolvedRefs", func(t *testing.T) {
		r := routeWith("prod", "r", nil, []gwv1.HTTPRouteRule{backendRule("echo")})
		c := cond(t, r, svc)
		assert.Equal(t, metav1.ConditionTrue, c.Status)
		assert.Equal(t, string(gwv1.RouteReasonResolvedRefs), c.Reason)
	})

	t.Run("missing Service -> BackendNotFound", func(t *testing.T) {
		r := routeWith("prod", "r", nil, []gwv1.HTTPRouteRule{backendRule("ghost")})
		c := cond(t, r)
		assert.Equal(t, metav1.ConditionFalse, c.Status)
		assert.Equal(t, string(gwv1.RouteReasonBackendNotFound), c.Reason)
	})

	t.Run("cross-namespace without grant -> RefNotPermitted", func(t *testing.T) {
		rule := backendRule("echo", func(ref *gwv1.BackendObjectReference) {
			ns := gwv1.Namespace("other")
			ref.Namespace = &ns
		})
		r := routeWith("prod", "r", nil, []gwv1.HTTPRouteRule{rule})
		c := cond(t, r, svc)
		assert.Equal(t, metav1.ConditionFalse, c.Status)
		assert.Equal(t, string(gwv1.RouteReasonRefNotPermitted), c.Reason)
	})

	t.Run("cross-namespace WITH ReferenceGrant -> ResolvedRefs", func(t *testing.T) {
		rule := backendRule("echo", func(ref *gwv1.BackendObjectReference) {
			ns := gwv1.Namespace("other")
			ref.Namespace = &ns
		})
		r := routeWith("prod", "r", nil, []gwv1.HTTPRouteRule{rule})
		svcOther := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "echo"}}
		grant := &gwv1beta1.ReferenceGrant{
			ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "grant"},
			Spec: gwv1beta1.ReferenceGrantSpec{
				From: []gwv1beta1.ReferenceGrantFrom{{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "prod"}},
				To:   []gwv1beta1.ReferenceGrantTo{{Group: "", Kind: "Service"}},
			},
		}
		c := cond(t, r, svcOther, grant)
		assert.Equal(t, metav1.ConditionTrue, c.Status)
		assert.Equal(t, string(gwv1.RouteReasonResolvedRefs), c.Reason)
	})

	t.Run("non-Service kind -> InvalidKind", func(t *testing.T) {
		rule := backendRule("echo", func(ref *gwv1.BackendObjectReference) {
			ref.Kind = ptr.To(gwv1.Kind("Secret"))
		})
		r := routeWith("prod", "r", nil, []gwv1.HTTPRouteRule{rule})
		c := cond(t, r, svc)
		assert.Equal(t, metav1.ConditionFalse, c.Status)
		assert.Equal(t, string(gwv1.RouteReasonInvalidKind), c.Reason)
	})
}

func TestRuleBackendPoliciesDiverge(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	route := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "r"}}
	twoBackends := gwv1.HTTPRouteRule{BackendRefs: []gwv1.HTTPBackendRef{
		{BackendRef: gwv1.BackendRef{BackendObjectReference: gwv1.BackendObjectReference{Name: "svc-a", Port: ptr.To(gwv1.PortNumber(80))}}},
		{BackendRef: gwv1.BackendRef{BackendObjectReference: gwv1.BackendObjectReference{Name: "svc-b", Port: ptr.To(gwv1.PortNumber(80))}}},
	}}

	t.Run("no policies -> no divergence", func(t *testing.T) {
		task := newTestTask(t, gw)
		d, err := task.ruleBackendPoliciesDiverge(context.Background(), route, twoBackends)
		assert.NoError(t, err)
		assert.False(t, d)
	})

	t.Run("health-check policy on only one of two backends -> divergence", func(t *testing.T) {
		hp := makeHCPolicy("prod", "hcp", "svc-a", "HTTP") // svc-b has none
		task := newTestTaskWithObjs(t, gw, hp)
		d, err := task.ruleBackendPoliciesDiverge(context.Background(), route, twoBackends)
		assert.NoError(t, err)
		assert.True(t, d)
	})

	t.Run("single backend never diverges", func(t *testing.T) {
		hp := makeHCPolicy("prod", "hcp", "svc-a", "HTTP")
		task := newTestTaskWithObjs(t, gw, hp)
		one := gwv1.HTTPRouteRule{BackendRefs: twoBackends.BackendRefs[:1]}
		d, err := task.ruleBackendPoliciesDiverge(context.Background(), route, one)
		assert.NoError(t, err)
		assert.False(t, d)
	})
}

func TestUpsertRouteParentStatus_PreservesOtherControllers(t *testing.T) {
	parent := gwv1.ParentReference{Name: "gw"}
	other := gwv1.RouteParentStatus{
		ParentRef:      gwv1.ParentReference{Name: "other-gw"},
		ControllerName: "example.com/other",
		Conditions:     []metav1.Condition{{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted"}},
	}
	parents := []gwv1.RouteParentStatus{other}

	upsertRouteParentStatus(&parents, parent, []metav1.Condition{
		routeCond("Accepted", metav1.ConditionTrue, "Accepted", "ok", 1),
	})

	assert.Len(t, parents, 2, "other controller entry preserved + ours appended")
	// Our entry exists with our controller name.
	var ours *gwv1.RouteParentStatus
	for i := range parents {
		if parents[i].ControllerName == albRouteController {
			ours = &parents[i]
		}
	}
	assert.NotNil(t, ours)
	assert.Equal(t, gwv1.ObjectName("gw"), ours.ParentRef.Name)
	// Other controller's entry untouched.
	assert.Equal(t, gwv1.GatewayController("example.com/other"), parents[0].ControllerName)
}

func TestRouteParentsEqual_IgnoresTime(t *testing.T) {
	mk := func(status metav1.ConditionStatus) []gwv1.RouteParentStatus {
		return []gwv1.RouteParentStatus{{
			ParentRef:      gwv1.ParentReference{Name: "gw"},
			ControllerName: albRouteController,
			Conditions:     []metav1.Condition{routeCond("Accepted", status, "Accepted", "ok", 1)},
		}}
	}
	a, b := mk(metav1.ConditionTrue), mk(metav1.ConditionTrue)
	assert.True(t, routeParentsEqual(a, b), "same status, different timestamps -> equal")
	assert.False(t, routeParentsEqual(mk(metav1.ConditionTrue), mk(metav1.ConditionFalse)), "different status -> not equal")
}
