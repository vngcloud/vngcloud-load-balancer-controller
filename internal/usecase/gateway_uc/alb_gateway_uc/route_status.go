package alb_gateway_uc

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/anngdinh/operator-helper/contexts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// Reason strings for the conditions on RouteParentStatus. Mirrors the upstream
// gwv1 constants where applicable; we vendor them as plain strings to avoid a
// separate import dependency in this file's tests.
const (
	reasonAccepted              = string(gwv1.RouteReasonAccepted)
	reasonNoMatchingParent      = string(gwv1.RouteReasonNoMatchingParent)
	reasonNotAllowedByListeners = string(gwv1.RouteReasonNotAllowedByListeners)
	reasonResolvedRefs          = string(gwv1.RouteReasonResolvedRefs)
	reasonBackendNotFound       = string(gwv1.RouteReasonBackendNotFound)
	reasonRefNotPermitted       = string(gwv1.RouteReasonRefNotPermitted)
	reasonInvalidKind           = string(gwv1.RouteReasonInvalidKind)
)

// routeReport accumulates per-(route, parentRef) status information during a
// Gateway reconcile. Each entry maps to one row in the route's Status.Parents
// for our controllerName.
type routeReport struct {
	route   *gwv1.HTTPRoute
	parents map[parentRefKey]*parentReport
}

type parentReport struct {
	parentRef gwv1.ParentReference

	// Accepted: did the route attach to ≥1 listener on this parent gateway?
	accepted        bool
	acceptedReason  string
	acceptedMessage string

	// ResolvedRefs: did all backendRefs resolve? First failure wins.
	resolvedRefs        bool
	resolvedRefsReason  string
	resolvedRefsMessage string
}

// parentRefKey uniquely identifies a parent within a route.Status.Parents list.
// Mirrors AWS LBC's keying so we preserve other-controller entries unchanged
// while updating our own.
type parentRefKey struct {
	group       string
	kind        string
	namespace   string
	name        string
	sectionName string
	port        int32
}

func keyFromParentRef(p gwv1.ParentReference, defaultNS string) parentRefKey {
	g := string(gwv1.GroupName)
	if p.Group != nil && *p.Group != "" {
		g = string(*p.Group)
	}
	k := "Gateway"
	if p.Kind != nil && *p.Kind != "" {
		k = string(*p.Kind)
	}
	ns := defaultNS
	if p.Namespace != nil {
		ns = string(*p.Namespace)
	}
	sn := ""
	if p.SectionName != nil {
		sn = string(*p.SectionName)
	}
	port := int32(0)
	if p.Port != nil {
		port = int32(*p.Port)
	}
	return parentRefKey{group: g, kind: k, namespace: ns, name: string(p.Name), sectionName: sn, port: port}
}

// routeStatusAccumulator collects routeReports during attachHTTPRoutes, keyed
// by route UID so we can flush a single status patch per route after deploy.
type routeStatusAccumulator struct {
	reports map[string]*routeReport
}

func newRouteStatusAccumulator() *routeStatusAccumulator {
	return &routeStatusAccumulator{reports: map[string]*routeReport{}}
}

// initRoute registers a route with its matched parents (those targeting our
// gateway). Idempotent — repeated calls keep the first report.
func (a *routeStatusAccumulator) initRoute(route *gwv1.HTTPRoute, matched []gwv1.ParentReference) *routeReport {
	if r, ok := a.reports[string(route.UID)]; ok {
		return r
	}
	r := &routeReport{route: route, parents: map[parentRefKey]*parentReport{}}
	for _, p := range matched {
		key := keyFromParentRef(p, route.Namespace)
		r.parents[key] = &parentReport{
			parentRef: p,
			// Default: not attached, no matching parent. Overridden as we
			// discover the route attaches to a listener.
			accepted:        false,
			acceptedReason:  reasonNoMatchingParent,
			acceptedMessage: "no listener on parent Gateway accepts this route",
			// Default: refs resolve. Flipped on first backend failure.
			resolvedRefs:       true,
			resolvedRefsReason: reasonResolvedRefs,
		}
	}
	a.reports[string(route.UID)] = r
	return r
}

func (r *routeReport) markAttachedToListener(p gwv1.ParentReference) {
	pr, ok := r.parents[keyFromParentRef(p, r.route.Namespace)]
	if !ok {
		return
	}
	pr.accepted = true
	pr.acceptedReason = reasonAccepted
	pr.acceptedMessage = "Route accepted by parent Gateway"
}

// recordBackendError classifies a resolveBackend error onto every parent
// report (a route's backend errors are not parent-specific — same backend
// would fail under any parent that accepts the route).
func (r *routeReport) recordBackendError(err error, refName string) {
	if err == nil {
		return
	}
	reason, msg := classifyBackendError(err, refName)
	for _, pr := range r.parents {
		// First failure wins.
		if !pr.resolvedRefs {
			continue
		}
		pr.resolvedRefs = false
		pr.resolvedRefsReason = reason
		pr.resolvedRefsMessage = msg
	}
}

func classifyBackendError(err error, refName string) (reason, msg string) {
	switch {
	case errors.Is(err, utils.ErrNotFound):
		return reasonBackendNotFound, fmt.Sprintf("backend Service %q not found", refName)
	case errors.Is(err, errRefNotPermitted):
		return reasonRefNotPermitted, err.Error()
	case errors.Is(err, errInvalidBackendKind), errors.Is(err, errBackendMissingPort):
		return reasonInvalidKind, err.Error()
	default:
		// Generic resolution failures (zero endpoints, transient list errors,
		// etc.) — not strictly "RefNotResolved" reasons in the spec but it's
		// the closest fit. Use BackendNotFound as a catch-all so users see
		// *something* is wrong with the ref.
		return reasonBackendNotFound, err.Error()
	}
}

// writeHTTPRouteStatuses patches each accumulated route's Status.Parents,
// preserving entries owned by other controllers (matched by parentRefKey not
// in our matched set) and writing one entry per parentRef we control.
//
// Best-effort: per-route patch failures are logged and skipped — they don't
// fail the gateway reconcile because LB state is already consistent.
func (uc *albGatewayUseCase) writeHTTPRouteStatuses(ctx context.Context, acc *routeStatusAccumulator) {
	if acc == nil || len(acc.reports) == 0 {
		return
	}
	logger := contexts.NewContext(ctx).Log()

	for _, rep := range acc.reports {
		if err := uc.k8sRepo.PatchMutateStatusHTTPRoute(ctx, rep.route, func(_ context.Context, fresh *gwv1.HTTPRoute) bool {
			return mergeRouteParents(fresh, rep)
		}); err != nil {
			logger.Warnf("patch HTTPRoute status %s/%s: %v", rep.route.Namespace, rep.route.Name, err)
		}
	}
}

// mergeRouteParents builds the new Status.Parents by walking Spec.ParentRefs
// in order. For each spec parentRef:
//   - if it's one of ours (in rep.parents), write our two conditions,
//   - otherwise, copy the existing status entry unchanged (so other
//     controllers' entries are preserved).
//
// Entries in the existing Status.Parents whose parentRef is no longer in
// Spec.ParentRefs get dropped. Returns true when the new Parents list differs
// from the existing one.
//
// LastTransitionTime is inherited from the prior matching condition when the
// Status (True/False) doesn't change — this prevents a status-write loop where
// every gateway reconcile would otherwise bump the timestamp, fire the route
// reconciler, bump the gateway's route-revision annotation, and fire another
// gateway reconcile.
func mergeRouteParents(fresh *gwv1.HTTPRoute, rep *routeReport) bool {
	gen := fresh.Generation
	existing := map[parentRefKey]gwv1.RouteParentStatus{}
	for _, ps := range fresh.Status.Parents {
		existing[keyFromParentRef(ps.ParentRef, fresh.Namespace)] = ps
	}

	out := make([]gwv1.RouteParentStatus, 0, len(fresh.Spec.ParentRefs))
	for _, pref := range fresh.Spec.ParentRefs {
		key := keyFromParentRef(pref, fresh.Namespace)
		if pr, mine := rep.parents[key]; mine {
			out = append(out, buildOurRouteParentStatus(pref, pr, gen, existing[key]))
			continue
		}
		if ps, ok := existing[key]; ok {
			out = append(out, ps)
		}
	}

	if reflect.DeepEqual(fresh.Status.Parents, out) {
		return false
	}
	fresh.Status.Parents = out
	return true
}

func buildOurRouteParentStatus(pref gwv1.ParentReference, pr *parentReport, gen int64, prior gwv1.RouteParentStatus) gwv1.RouteParentStatus {
	conds := []metav1.Condition{
		newRouteCondition(string(gwv1.RouteConditionAccepted), pr.accepted, pr.acceptedReason, pr.acceptedMessage, gen, prior.Conditions),
		newRouteCondition(string(gwv1.RouteConditionResolvedRefs), pr.resolvedRefs, pr.resolvedRefsReason, pr.resolvedRefsMessage, gen, prior.Conditions),
	}
	return gwv1.RouteParentStatus{
		ParentRef:      pref,
		ControllerName: gwv1.GatewayController(domain.ControllerNameALB),
		Conditions:     conds,
	}
}

// newRouteCondition builds a metav1.Condition. When a prior condition with the
// same Type and Status exists, its LastTransitionTime is inherited — this is
// the standard meta.SetCondition semantic and is what prevents identity
// patches from creating reconcile churn.
func newRouteCondition(t string, ok bool, reason, msg string, gen int64, priorConds []metav1.Condition) metav1.Condition {
	st := metav1.ConditionFalse
	if ok {
		st = metav1.ConditionTrue
	}
	transition := metav1.Now()
	for i := range priorConds {
		if priorConds[i].Type == t && priorConds[i].Status == st && !priorConds[i].LastTransitionTime.IsZero() {
			transition = priorConds[i].LastTransitionTime
			break
		}
	}
	return metav1.Condition{
		Type:               t,
		Status:             st,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: gen,
		LastTransitionTime: transition,
	}
}
