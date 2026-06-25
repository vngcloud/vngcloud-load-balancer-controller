package alb_gateway_uc

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

// albRouteController is this controller's name, used to scope the
// RouteParentStatus entries we own (other controllers' entries are preserved).
var albRouteController = gwv1.GatewayController(consts.GatewayClassControllerNameALB)

// writeRouteStatuses computes and persists per-parent status (Accepted,
// ResolvedRefs, PartiallyInvalid) on every HTTPRoute attached to this Gateway.
//
// Route attachment + backend resolution don't depend on cloud provisioning, so
// this runs every reconcile regardless of LB readiness. It is idempotent — the
// status is only patched when it actually changes — so the status write does
// not feed back into a reconcile loop through the Gateway's HTTPRoute watch.
func (t *defaultGatewayBuildTask) writeRouteStatuses(ctx context.Context) error {
	routes, err := t.listAttachedHTTPRoutes(ctx)
	if err != nil {
		return err
	}
	for i := range routes {
		if err := t.writeOneRouteStatus(ctx, &routes[i]); err != nil {
			// Best-effort per route — one route's status failure must not block
			// the others or mask the reconcile result.
			t.logger.Warnf("write status for HTTPRoute %s/%s: %v", routes[i].Namespace, routes[i].Name, err)
		}
	}
	return nil
}

func (t *defaultGatewayBuildTask) writeOneRouteStatus(ctx context.Context, route *gwv1.HTTPRoute) error {
	parents := parentRefsForGateway(route, t.gw)
	if len(parents) == 0 {
		return nil
	}
	cur := route.DeepCopy()
	for pi := range parents {
		conds := t.routeParentConditions(ctx, route, parents[pi])
		upsertRouteParentStatus(&cur.Status.Parents, parents[pi], conds)
	}
	if routeParentsEqual(cur.Status.Parents, route.Status.Parents) {
		return nil
	}
	if err := t.uc.k8sClient.Status().Patch(ctx, cur, client.MergeFrom(route)); err != nil {
		return fmt.Errorf("patch HTTPRoute status: %w", err)
	}
	return nil
}

// routeParentConditions builds the Accepted / ResolvedRefs (+ PartiallyInvalid)
// conditions for one (route, parentRef) pair.
func (t *defaultGatewayBuildTask) routeParentConditions(ctx context.Context, route *gwv1.HTTPRoute, parent gwv1.ParentReference) []metav1.Condition {
	gen := route.Generation
	conds := []metav1.Condition{
		routeAcceptedCondition(t.gw, route, parent, gen),
		t.routeResolvedRefsCondition(ctx, route, gen),
	}
	if c, ok := routePartiallyInvalidCondition(route, gen); ok {
		conds = append(conds, c)
	}
	return conds
}

// routeAcceptedCondition reports whether the route attaches to at least one
// supported listener on the Gateway for the given parentRef.
func routeAcceptedCondition(gw *gwv1.Gateway, route *gwv1.HTTPRoute, parent gwv1.ParentReference, gen int64) metav1.Condition {
	for i := range gw.Spec.Listeners {
		l := &gw.Spec.Listeners[i]
		if _, ok := mapListenerProtocol(l.Protocol); !ok {
			continue
		}
		if listenerAcceptsRoute(l, route, &parent) {
			return routeCond(string(gwv1.RouteConditionAccepted), metav1.ConditionTrue,
				string(gwv1.RouteReasonAccepted), "Route accepted by listener", gen)
		}
	}
	// In the list, namespace already passed; the remaining reason is a
	// hostname/sectionName mismatch on every listener.
	reason := gwv1.RouteReasonNotAllowedByListeners
	if len(route.Spec.Hostnames) > 0 {
		reason = gwv1.RouteReasonNoMatchingListenerHostname
	}
	return routeCond(string(gwv1.RouteConditionAccepted), metav1.ConditionFalse,
		string(reason), "No listener accepts this route", gen)
}

// routeResolvedRefsCondition checks each backendRef resolves to an existing
// same-namespace Service. Cross-namespace refs are reported RefNotPermitted
// (ReferenceGrant evaluation is not wired in Phase 1, matching the build path,
// which drops them).
func (t *defaultGatewayBuildTask) routeResolvedRefsCondition(ctx context.Context, route *gwv1.HTTPRoute, gen int64) metav1.Condition {
	ok := func(reason gwv1.RouteConditionReason, msg string, status metav1.ConditionStatus) metav1.Condition {
		return routeCond(string(gwv1.RouteConditionResolvedRefs), status, string(reason), msg, gen)
	}
	for ri := range route.Spec.Rules {
		for bi := range route.Spec.Rules[ri].BackendRefs {
			br := &route.Spec.Rules[ri].BackendRefs[bi].BackendRef
			group := ""
			if br.Group != nil {
				group = string(*br.Group)
			}
			kind := "Service"
			if br.Kind != nil {
				kind = string(*br.Kind)
			}
			if group != "" || kind != "Service" {
				return ok(gwv1.RouteReasonInvalidKind, fmt.Sprintf("backendRef %s/%s unsupported (Service only)", group, kind), metav1.ConditionFalse)
			}
			ns := route.Namespace
			if br.Namespace != nil {
				ns = string(*br.Namespace)
			}
			if ns != route.Namespace {
				allowed, err := sharedUC.RefGrantAllowed(ctx, t.uc.k8sClient,
					sharedUC.Ref{Group: "", Kind: "Service", Namespace: ns, Name: string(br.Name)},
					sharedUC.Ref{Group: gwv1.GroupName, Kind: "HTTPRoute", Namespace: route.Namespace, Name: route.Name})
				if err != nil || !allowed {
					return ok(gwv1.RouteReasonRefNotPermitted, fmt.Sprintf("cross-namespace backendRef %s/%s not permitted by any ReferenceGrant", ns, br.Name), metav1.ConditionFalse)
				}
			}
			var svc corev1.Service
			if err := t.uc.k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: string(br.Name)}, &svc); err != nil {
				if apierrors.IsNotFound(err) {
					return ok(gwv1.RouteReasonBackendNotFound, fmt.Sprintf("backend Service %s/%s not found", ns, br.Name), metav1.ConditionFalse)
				}
				return ok(gwv1.RouteReasonBackendNotFound, fmt.Sprintf("backend Service %s/%s: %v", ns, br.Name, err), metav1.ConditionFalse)
			}
		}
	}
	// Divergent per-backend policies in a rule fail closed (the rule is dropped
	// from the LBC); surface it so the user sees why traffic isn't served.
	for ri := range route.Spec.Rules {
		if diverge, err := t.ruleBackendPoliciesDiverge(ctx, route, route.Spec.Rules[ri]); err == nil && diverge {
			return ok(reasonBackendConfigMismatch, "a rule's backends carry divergent VKSBackend/VKSHealthCheck policies", metav1.ConditionFalse)
		}
	}
	return ok(gwv1.RouteReasonResolvedRefs, "All backend references resolved", metav1.ConditionTrue)
}

// reasonBackendConfigMismatch is a vngcloud-specific ResolvedRefs reason: a
// rule's backends merge into one pool but carry conflicting per-backend policy.
const reasonBackendConfigMismatch gwv1.RouteConditionReason = "BackendConfigMismatch"

// routePartiallyInvalidCondition flags routes that have at least one rule the
// controller silently drops (match dimensions VNGCloud LB can't express).
func routePartiallyInvalidCondition(route *gwv1.HTTPRoute, gen int64) (metav1.Condition, bool) {
	for ri := range route.Spec.Rules {
		if hasUnsupportedMatchDimension(route.Spec.Rules[ri].Matches) {
			return routeCond(string(gwv1.RouteConditionPartiallyInvalid), metav1.ConditionTrue,
				string(gwv1.RouteReasonUnsupportedValue),
				"One or more rules use match dimensions (header/queryParam/method) VNGCloud LB does not support and were dropped", gen), true
		}
	}
	return metav1.Condition{}, false
}

func routeCond(condType string, status metav1.ConditionStatus, reason, msg string, gen int64) metav1.Condition {
	return metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: gen,
		LastTransitionTime: metav1.Now(),
	}
}

// upsertRouteParentStatus sets conds on the RouteParentStatus entry this
// controller owns for parentRef, creating it if absent and preserving entries
// owned by other controllers / other parents.
func upsertRouteParentStatus(parents *[]gwv1.RouteParentStatus, parentRef gwv1.ParentReference, conds []metav1.Condition) {
	for i := range *parents {
		if (*parents)[i].ControllerName == albRouteController && parentRefEqual((*parents)[i].ParentRef, parentRef) {
			for _, c := range conds {
				shared.SetCondition(&(*parents)[i].Conditions, c)
			}
			return
		}
	}
	entry := gwv1.RouteParentStatus{ParentRef: parentRef, ControllerName: albRouteController}
	for _, c := range conds {
		shared.SetCondition(&entry.Conditions, c)
	}
	*parents = append(*parents, entry)
}

func parentRefEqual(a, b gwv1.ParentReference) bool {
	return a.Name == b.Name &&
		ptrStrEqual((*string)(a.Namespace), (*string)(b.Namespace)) &&
		ptrStrEqual((*string)(a.SectionName), (*string)(b.SectionName)) &&
		portEqual(a.Port, b.Port)
}

func ptrStrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func portEqual(a, b *gwv1.PortNumber) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// routeParentsEqual compares two parent-status lists ignoring condition
// timestamps, so an unchanged status is not re-patched (avoids a reconcile loop).
func routeParentsEqual(a, b []gwv1.RouteParentStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		var match *gwv1.RouteParentStatus
		for j := range b {
			if b[j].ControllerName == a[i].ControllerName && parentRefEqual(b[j].ParentRef, a[i].ParentRef) {
				match = &b[j]
				break
			}
		}
		if match == nil || !conditionsEqual(a[i].Conditions, match.Conditions) {
			return false
		}
	}
	return true
}
