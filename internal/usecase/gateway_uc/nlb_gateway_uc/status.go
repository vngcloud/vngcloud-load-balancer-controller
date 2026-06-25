package nlb_gateway_uc

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

// writeGatewayStatus mirrors the ALB path: Accepted always True (we own the
// class), Programmed reflects LBC readiness, addresses come from LBC.Status.
func (t *nlbBuildTask) writeGatewayStatus(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig, translateErr error) error {
	cur := t.gw.DeepCopy()

	accepted := metav1.Condition{
		Type:               string(gwv1.GatewayConditionAccepted),
		Status:             metav1.ConditionTrue,
		Reason:             string(gwv1.GatewayReasonAccepted),
		Message:            "Gateway accepted by controller " + consts.GatewayClassControllerNameNLB,
		ObservedGeneration: t.gw.Generation,
		LastTransitionTime: metav1.Now(),
	}
	programmed := metav1.Condition{
		Type:               string(gwv1.GatewayConditionProgrammed),
		Status:             metav1.ConditionTrue,
		Reason:             string(gwv1.GatewayReasonProgrammed),
		Message:            "LoadBalancerConfig reconciled successfully",
		ObservedGeneration: t.gw.Generation,
		LastTransitionTime: metav1.Now(),
	}
	switch {
	case translateErr != nil:
		programmed.Status = metav1.ConditionFalse
		programmed.Reason = string(gwv1.GatewayReasonPending)
		programmed.Message = translateErr.Error()
	case lbc == nil:
		programmed.Status = metav1.ConditionFalse
		programmed.Reason = string(gwv1.GatewayReasonPending)
		programmed.Message = "LoadBalancerConfig not yet created"
	case !lbcReady(lbc):
		programmed.Status = metav1.ConditionFalse
		programmed.Reason = string(gwv1.GatewayReasonPending)
		programmed.Message = lbcStatusMessage(lbc)
	}

	shared.SetCondition(&cur.Status.Conditions, accepted)
	shared.SetCondition(&cur.Status.Conditions, programmed)
	cur.Status.Addresses = gatewayAddressesFromLBC(lbc)
	cur.Status.Listeners = t.listenerStatuses()

	if conditionsEqual(cur.Status.Conditions, t.gw.Status.Conditions) &&
		gatewayAddressesEqual(cur.Status.Addresses, t.gw.Status.Addresses) {
		return nil
	}
	if err := t.uc.k8sClient.Status().Patch(ctx, cur, client.MergeFrom(t.gw)); err != nil {
		return fmt.Errorf("patch Gateway %s/%s status: %w", t.gw.Namespace, t.gw.Name, err)
	}
	return nil
}

func (t *nlbBuildTask) listenerStatuses() []gwv1.ListenerStatus {
	out := make([]gwv1.ListenerStatus, 0, len(t.gw.Spec.Listeners))
	for i := range t.gw.Spec.Listeners {
		l := &t.gw.Spec.Listeners[i]
		_, _, _, ok := mapL4Protocol(l.Protocol)
		ls := gwv1.ListenerStatus{
			Name:           l.Name,
			SupportedKinds: supportedKindsForL4(l.Protocol),
		}
		status := metav1.ConditionTrue
		reason := string(gwv1.ListenerReasonAccepted)
		if !ok {
			status = metav1.ConditionFalse
			reason = string(gwv1.ListenerReasonUnsupportedProtocol)
		}
		shared.SetCondition(&ls.Conditions, metav1.Condition{
			Type:               string(gwv1.ListenerConditionAccepted),
			Status:             status,
			Reason:             reason,
			ObservedGeneration: t.gw.Generation,
			LastTransitionTime: metav1.Now(),
		})
		out = append(out, ls)
	}
	return out
}

func supportedKindsForL4(p gwv1.ProtocolType) []gwv1.RouteGroupKind {
	group := gwv1.Group(gwv1.GroupName)
	switch p {
	case gwv1.TCPProtocolType:
		return []gwv1.RouteGroupKind{{Group: &group, Kind: "TCPRoute"}}
	case gwv1.UDPProtocolType:
		return []gwv1.RouteGroupKind{{Group: &group, Kind: "UDPRoute"}}
	}
	return nil
}

// writeRouteStatuses writes Accepted/ResolvedRefs on each attached TCP/UDP route
// for our controllerName. Best-effort; idempotent (patches only on change).
func (t *nlbBuildTask) writeRouteStatuses(ctx context.Context) error {
	routes, err := t.listAttachedRoutes(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, r := range routes {
		if err := t.writeOneRouteStatus(ctx, r); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *nlbBuildTask) writeOneRouteStatus(ctx context.Context, r *l4Route) error {
	accepted := t.routeAttaches(r)
	resolved, resolvedReason, resolvedMsg := t.routeResolvedRefs(ctx, r)

	acceptedCond := metav1.Condition{
		Type:               string(gwv1.RouteConditionAccepted),
		Status:             metav1.ConditionTrue,
		Reason:             string(gwv1.RouteReasonAccepted),
		Message:            "Route accepted by NLB listener",
		ObservedGeneration: 0,
		LastTransitionTime: metav1.Now(),
	}
	if !accepted {
		acceptedCond.Status = metav1.ConditionFalse
		acceptedCond.Reason = string(gwv1.RouteReasonNotAllowedByListeners)
		acceptedCond.Message = "No NLB listener on the parent Gateway accepts this route"
	}
	resolvedCond := metav1.Condition{
		Type:               string(gwv1.RouteConditionResolvedRefs),
		Status:             resolved,
		Reason:             resolvedReason,
		Message:            resolvedMsg,
		ObservedGeneration: 0,
		LastTransitionTime: metav1.Now(),
	}

	switch r.kind {
	case "TCPRoute":
		obj := &gwv1a2.TCPRoute{}
		if err := t.uc.k8sClient.Get(ctx, types.NamespacedName{Namespace: r.namespace, Name: r.name}, obj); err != nil {
			return client.IgnoreNotFound(err)
		}
		cur := obj.DeepCopy()
		cur.Status.Parents = t.mergeRouteParents(r, obj.Status.Parents, obj.Generation, acceptedCond, resolvedCond)
		if routeParentsEqual(cur.Status.Parents, obj.Status.Parents) {
			return nil
		}
		return t.uc.k8sClient.Status().Patch(ctx, cur, client.MergeFrom(obj))
	case "UDPRoute":
		obj := &gwv1a2.UDPRoute{}
		if err := t.uc.k8sClient.Get(ctx, types.NamespacedName{Namespace: r.namespace, Name: r.name}, obj); err != nil {
			return client.IgnoreNotFound(err)
		}
		cur := obj.DeepCopy()
		cur.Status.Parents = t.mergeRouteParents(r, obj.Status.Parents, obj.Generation, acceptedCond, resolvedCond)
		if routeParentsEqual(cur.Status.Parents, obj.Status.Parents) {
			return nil
		}
		return t.uc.k8sClient.Status().Patch(ctx, cur, client.MergeFrom(obj))
	}
	return nil
}

// mergeRouteParents sets our controller's RouteParentStatus for every parentRef
// targeting this Gateway, preserving other controllers' entries (and our own
// entries for other gateways) verbatim.
func (t *nlbBuildTask) mergeRouteParents(r *l4Route, existing []gwv1.RouteParentStatus, gen int64, conds ...metav1.Condition) []gwv1.RouteParentStatus {
	for i := range conds {
		conds[i].ObservedGeneration = gen
	}
	out := make([]gwv1.RouteParentStatus, 0, len(existing)+1)
	for _, p := range existing {
		if string(p.ControllerName) == consts.GatewayClassControllerNameNLB && t.parentRefTargetsThisGW(r, p.ParentRef) {
			continue // replace our own stale entry for this Gateway
		}
		out = append(out, p)
	}
	for _, pr := range t.parentRefsTargetingGW(r) {
		ps := gwv1.RouteParentStatus{
			ParentRef:      pr,
			ControllerName: gwv1.GatewayController(consts.GatewayClassControllerNameNLB),
		}
		for _, c := range conds {
			shared.SetCondition(&ps.Conditions, c)
		}
		out = append(out, ps)
	}
	return out
}

// parentRefsTargetingGW returns the route's parentRefs that point at this Gateway.
func (t *nlbBuildTask) parentRefsTargetingGW(r *l4Route) []gwv1.ParentReference {
	out := make([]gwv1.ParentReference, 0, len(r.parentRefs))
	for i := range r.parentRefs {
		if t.parentRefTargetsThisGW(r, r.parentRefs[i]) {
			out = append(out, r.parentRefs[i])
		}
	}
	return out
}

func (t *nlbBuildTask) parentRefTargetsThisGW(r *l4Route, pr gwv1.ParentReference) bool {
	if pr.Kind != nil && *pr.Kind != "Gateway" {
		return false
	}
	ns := r.namespace
	if pr.Namespace != nil {
		ns = string(*pr.Namespace)
	}
	return ns == t.gw.Namespace && string(pr.Name) == t.gw.Name
}

// routeAttaches reports whether the route attaches to at least one supported
// listener on this Gateway.
func (t *nlbBuildTask) routeAttaches(r *l4Route) bool {
	for i := range t.gw.Spec.Listeners {
		l := &t.gw.Spec.Listeners[i]
		if _, _, _, ok := mapL4Protocol(l.Protocol); !ok {
			continue
		}
		if routeAttachesToListener(r, t.gw, l) {
			return true
		}
	}
	return false
}

// routeResolvedRefs validates the route's backendRefs (cross-ns ReferenceGrant
// + Service existence).
func (t *nlbBuildTask) routeResolvedRefs(ctx context.Context, r *l4Route) (metav1.ConditionStatus, string, string) {
	for i := range r.backendRefs {
		br := &r.backendRefs[i]
		ns := r.namespace
		if br.Namespace != nil {
			ns = string(*br.Namespace)
		}
		if ns != r.namespace {
			allowed, err := sharedUC.RefGrantAllowed(ctx, t.uc.k8sClient,
				sharedUC.Ref{Group: "", Kind: "Service", Namespace: ns, Name: string(br.Name)},
				sharedUC.Ref{Group: gwv1.GroupName, Kind: r.kind, Namespace: r.namespace, Name: r.name})
			if err != nil {
				return metav1.ConditionFalse, string(gwv1.RouteReasonRefNotPermitted), err.Error()
			}
			if !allowed {
				return metav1.ConditionFalse, string(gwv1.RouteReasonRefNotPermitted),
					fmt.Sprintf("cross-namespace backendRef %s/%s not permitted by any ReferenceGrant", ns, br.Name)
			}
		}
		var svc corev1.Service
		if err := t.uc.k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: string(br.Name)}, &svc); err != nil {
			if apierrors.IsNotFound(err) {
				return metav1.ConditionFalse, string(gwv1.RouteReasonBackendNotFound),
					fmt.Sprintf("backend Service %s/%s not found", ns, br.Name)
			}
			return metav1.ConditionFalse, string(gwv1.RouteReasonBackendNotFound), err.Error()
		}
	}
	return metav1.ConditionTrue, string(gwv1.RouteReasonResolvedRefs), "All backend references resolved"
}

// --- equality helpers (copied shape from the ALB status path) ---

func lbcReady(lbc *v1alpha1.LoadBalancerConfig) bool {
	for _, c := range lbc.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func lbcStatusMessage(lbc *v1alpha1.LoadBalancerConfig) string {
	if lbc.Status.LastReconcileMessage != "" {
		return "LBC: " + lbc.Status.LastReconcileMessage
	}
	return "LoadBalancerConfig not yet ready"
}

func gatewayAddressesFromLBC(lbc *v1alpha1.LoadBalancerConfig) []gwv1.GatewayStatusAddress {
	if lbc == nil || lbc.Status.Address == nil || *lbc.Status.Address == "" {
		return nil
	}
	at := gwv1.IPAddressType
	return []gwv1.GatewayStatusAddress{{Type: &at, Value: *lbc.Status.Address}}
}

func conditionsEqual(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}
	idx := make(map[string]metav1.Condition, len(b))
	for i := range b {
		idx[b[i].Type] = b[i]
	}
	for i := range a {
		other, ok := idx[a[i].Type]
		if !ok {
			return false
		}
		if a[i].Status != other.Status || a[i].Reason != other.Reason ||
			a[i].Message != other.Message || a[i].ObservedGeneration != other.ObservedGeneration {
			return false
		}
	}
	return true
}

func gatewayAddressesEqual(a, b []gwv1.GatewayStatusAddress) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

func routeParentsEqual(a, b []gwv1.RouteParentStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i].ControllerName) != string(b[i].ControllerName) {
			return false
		}
		if string(a[i].ParentRef.Name) != string(b[i].ParentRef.Name) {
			return false
		}
		if !conditionsEqual(a[i].Conditions, b[i].Conditions) {
			return false
		}
	}
	return true
}
