package alb_gateway_uc

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

func TestClassifyBackendError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"NotFound wraps utils.ErrNotFound", fmt.Errorf("%w: services \"svc\" not found", utils.ErrNotFound), reasonBackendNotFound},
		{"InvalidKind", fmt.Errorf("%w: /ConfigMap", errInvalidBackendKind), reasonInvalidKind},
		{"MissingPort treated as InvalidKind", fmt.Errorf("%w: backendRef \"x\"", errBackendMissingPort), reasonInvalidKind},
		{"RefNotPermitted", fmt.Errorf("%w: cross-NS", errRefNotPermitted), reasonRefNotPermitted},
		{"unknown error falls back to BackendNotFound", errors.New("transient list error"), reasonBackendNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := classifyBackendError(tc.err, "svc")
			assert.Equal(t, tc.wantReason, r)
		})
	}
}

func TestRouteReport_FirstBackendErrorWins(t *testing.T) {
	rep := &routeReport{
		route:   &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "ns"}},
		parents: map[parentRefKey]*parentReport{},
	}
	pref := parentRef("", "", "", "gw", "", nil)
	rep.parents[keyFromParentRef(pref, "ns")] = &parentReport{
		parentRef: pref, resolvedRefs: true, resolvedRefsReason: reasonResolvedRefs,
	}

	rep.recordBackendError(fmt.Errorf("%w: missing", utils.ErrNotFound), "svc-a")
	rep.recordBackendError(fmt.Errorf("%w: cross-NS", errRefNotPermitted), "svc-b")

	pr := rep.parents[keyFromParentRef(pref, "ns")]
	assert.False(t, pr.resolvedRefs)
	assert.Equal(t, reasonBackendNotFound, pr.resolvedRefsReason)
}

func TestMergeRouteParents_PreservesOtherControllers(t *testing.T) {
	other := gwv1.RouteParentStatus{
		ParentRef:      parentRef("", "", "", "foreign-gw", "", nil),
		ControllerName: "example.com/other-controller",
		Conditions: []metav1.Condition{
			{Type: string(gwv1.RouteConditionAccepted), Status: metav1.ConditionTrue, Reason: "Accepted"},
		},
	}
	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Generation: 5},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{
				ParentRefs: []gwv1.ParentReference{
					parentRef("", "", "", "foreign-gw", "", nil),
					parentRef("", "", "", "our-gw", "", nil),
				},
			},
		},
		Status: gwv1.HTTPRouteStatus{RouteStatus: gwv1.RouteStatus{Parents: []gwv1.RouteParentStatus{other}}},
	}
	ourPref := parentRef("", "", "", "our-gw", "", nil)
	rep := &routeReport{
		route: route,
		parents: map[parentRefKey]*parentReport{
			keyFromParentRef(ourPref, "ns"): {
				parentRef:           ourPref,
				accepted:            true,
				acceptedReason:      reasonAccepted,
				resolvedRefs:        false,
				resolvedRefsReason:  reasonBackendNotFound,
				resolvedRefsMessage: "backend Service \"missing-svc\" not found",
			},
		},
	}

	changed := mergeRouteParents(route, rep)
	assert.True(t, changed)
	assert.Len(t, route.Status.Parents, 2)
	assert.Equal(t, other.ControllerName, route.Status.Parents[0].ControllerName)
	assert.Equal(t, gwv1.GatewayController(domain.ControllerNameALB), route.Status.Parents[1].ControllerName)

	got := route.Status.Parents[1]
	assert.Equal(t, "Accepted", got.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, got.Conditions[0].Status)
	assert.Equal(t, "ResolvedRefs", got.Conditions[1].Type)
	assert.Equal(t, metav1.ConditionFalse, got.Conditions[1].Status)
	assert.Equal(t, reasonBackendNotFound, got.Conditions[1].Reason)
	assert.Equal(t, int64(5), got.Conditions[1].ObservedGeneration)
}

func TestMergeRouteParents_DropsStaleParents(t *testing.T) {
	stale := gwv1.RouteParentStatus{
		ParentRef:      parentRef("", "", "", "removed-gw", "", nil),
		ControllerName: domain.ControllerNameALB,
	}
	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns"},
		Spec:       gwv1.HTTPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{parentRef("", "", "", "our-gw", "", nil)}}},
		Status:     gwv1.HTTPRouteStatus{RouteStatus: gwv1.RouteStatus{Parents: []gwv1.RouteParentStatus{stale}}},
	}
	pref := parentRef("", "", "", "our-gw", "", nil)
	rep := &routeReport{
		route:   route,
		parents: map[parentRefKey]*parentReport{keyFromParentRef(pref, "ns"): {parentRef: pref, accepted: true, acceptedReason: reasonAccepted, resolvedRefs: true, resolvedRefsReason: reasonResolvedRefs}},
	}

	mergeRouteParents(route, rep)
	if assert.Len(t, route.Status.Parents, 1) {
		assert.Equal(t, gwv1.ObjectName("our-gw"), route.Status.Parents[0].ParentRef.Name)
	}
}

func TestRouteReport_DefaultsToNoMatchingParent(t *testing.T) {
	acc := newRouteStatusAccumulator()
	route := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", UID: "u1"}}
	pref := parentRef("", "", "", "gw", "", nil)
	rep := acc.initRoute(route, []gwv1.ParentReference{pref})

	pr := rep.parents[keyFromParentRef(pref, "ns")]
	assert.False(t, pr.accepted)
	assert.Equal(t, reasonNoMatchingParent, pr.acceptedReason)

	rep.markAttachedToListener(pref)
	assert.True(t, pr.accepted)
	assert.Equal(t, reasonAccepted, pr.acceptedReason)
}
