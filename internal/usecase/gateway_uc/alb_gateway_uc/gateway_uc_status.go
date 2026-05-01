package alb_gateway_uc

import (
	"context"
	"strconv"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	ctlshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// AnnotationRouteRevision lives on Gateway objects. The HTTPRoute reconciler
// bumps it (via EnqueueParentGatewayForRoute) so controller-runtime fires a
// Gateway reconcile event without needing a separate channel source. The
// value is opaque (a UnixNano timestamp) — callers only compare for change.
const AnnotationRouteRevision = "gateway.vks.vngcloud.vn/route-revision"

// markAcceptedAndProgrammed writes the standard gateway-api positive
// conditions after a successful reconcile. Addresses are populated from
// the most-recently-deployed LoadBalancerConfig (the LBC reconciler fills
// in status.address asynchronously once vngcloud assigns one).
func (uc *albGatewayUseCase) markAcceptedAndProgrammed(ctx context.Context, gw *gwv1.Gateway) error {
	addrs := uc.gatherGatewayAddresses(ctx, gw)
	return uc.k8sRepo.PatchMutateStatusGateway(ctx, gw, func(_ context.Context, fresh *gwv1.Gateway) bool {
		ctlshared.SetCondition(&fresh.Status.Conditions,
			string(gwv1.GatewayConditionAccepted), metav1.ConditionTrue,
			string(gwv1.GatewayReasonAccepted),
			"Gateway accepted by vngcloud-alb",
			fresh.Generation,
		)
		ctlshared.SetCondition(&fresh.Status.Conditions,
			string(gwv1.GatewayConditionProgrammed), metav1.ConditionTrue,
			string(gwv1.GatewayReasonProgrammed),
			"LoadBalancerConfig deployed",
			fresh.Generation,
		)
		fresh.Status.Addresses = addrs
		return true
	})
}

// gatherGatewayAddresses reads the owned LoadBalancerConfig(s) and returns
// the Gateway-API status address list. Returns nil when no LBC is reachable
// or none has produced an address yet — the caller leaves the field empty
// (Programmed=True still indicates "config deployed", regardless of address
// availability — vngcloud LB warm-up is async).
func (uc *albGatewayUseCase) gatherGatewayAddresses(ctx context.Context, gw *gwv1.Gateway) []gwv1.GatewayStatusAddress {
	logger := contexts.NewContext(ctx).Log()

	lbcList := &v1alpha1.LoadBalancerConfigList{}
	if err := uc.k8sRepo.ListLoadBalancerConfig(ctx, lbcList,
		client.InNamespace(gw.Namespace),
		client.MatchingLabels{
			domain.LabelOwnerResourceName: gw.Name,
			domain.LabelOwnerResourceKind: gw.Kind,
			domain.LabelOwnerResourceUid:  string(gw.UID),
		}); err != nil {
		logger.Warnf("listing LBCs for address propagation: %v", err)
		return nil
	}

	out := make([]gwv1.GatewayStatusAddress, 0, len(lbcList.Items))
	seen := map[string]struct{}{}
	for i := range lbcList.Items {
		addr := lbcList.Items[i].Status.Address
		if addr == nil || *addr == "" {
			continue
		}
		if _, dup := seen[*addr]; dup {
			continue
		}
		seen[*addr] = struct{}{}
		t := gwv1.IPAddressType
		out = append(out, gwv1.GatewayStatusAddress{Type: &t, Value: *addr})
	}
	return out
}

// enqueueAllALBGatewaysInNamespace bumps the route-revision annotation on every
// vngcloud-alb Gateway in `ns` so each reconciles and prunes any pool/policy
// it owned that referenced a route now gone. Used when an HTTPRoute is deleted
// — at that point we no longer have the route's parentRefs, so we fan out.
func (uc *albGatewayUseCase) enqueueAllALBGatewaysInNamespace(ctx context.Context, ns string) error {
	logger := contexts.NewContext(ctx).Log()

	gws := &gwv1.GatewayList{}
	if err := uc.k8sRepo.ListGateway(ctx, gws, client.InNamespace(ns)); err != nil {
		return err
	}
	tick := strconv.FormatInt(time.Now().UnixNano(), 10)
	for i := range gws.Items {
		gw := &gws.Items[i]
		gwc, gcerr := uc.k8sRepo.GetGatewayClass(ctx, string(gw.Spec.GatewayClassName))
		if gcerr != nil || string(gwc.Spec.ControllerName) != domain.ControllerNameALB {
			continue
		}
		if perr := uc.k8sRepo.PatchMutateGateway(ctx, gw, func(_ context.Context, fresh *gwv1.Gateway) bool {
			if fresh.Annotations == nil {
				fresh.Annotations = map[string]string{}
			}
			if fresh.Annotations[AnnotationRouteRevision] == tick {
				return false
			}
			fresh.Annotations[AnnotationRouteRevision] = tick
			return true
		}); perr != nil {
			logger.Warnf("failed to bump %s on gateway %s/%s after route delete: %v", AnnotationRouteRevision, gw.Namespace, gw.Name, perr)
		}
	}
	return nil
}

// EnqueueParentGatewayForRoute fires a synthetic re-reconcile of every
// vngcloud-alb Gateway named in the HTTPRoute's parentRefs by writing the
// AnnotationRouteRevision annotation with a fresh tick. Foreign Gateways
// (different controllerName) are skipped so we don't produce noise on
// resources we don't own.
//
// Best-effort: per-Gateway failures are logged and skipped. When the
// HTTPRoute itself is gone (deleted), we fan out to every vngcloud-alb
// Gateway in the route's namespace so each has a chance to prune the
// orphaned policies/pools belonging to the deleted route. This handles
// the "delete HTTPRoute, sibling routes remain" scenario.
func (uc *albGatewayUseCase) EnqueueParentGatewayForRoute(ctx context.Context, routeRef ctrl.Request) error {
	logger := contexts.NewContext(ctx).Log()

	rt, err := uc.k8sRepo.GetHTTPRoute(ctx, routeRef.NamespacedName)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		// Route deleted — fan out to every vngcloud-alb Gateway in the namespace.
		return uc.enqueueAllALBGatewaysInNamespace(ctx, routeRef.Namespace)
	}

	tick := strconv.FormatInt(time.Now().UnixNano(), 10)
	seen := map[types.NamespacedName]struct{}{}

	for _, p := range rt.Spec.ParentRefs {
		if p.Group != nil && *p.Group != "" && string(*p.Group) != gwv1.GroupName {
			continue
		}
		if p.Kind != nil && *p.Kind != "" && string(*p.Kind) != "Gateway" {
			continue
		}
		ns := rt.Namespace
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		key := types.NamespacedName{Namespace: ns, Name: string(p.Name)}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		gw, gerr := uc.k8sRepo.GetGateway(ctx, key)
		if gerr != nil {
			continue
		}
		gwc, gcerr := uc.k8sRepo.GetGatewayClass(ctx, string(gw.Spec.GatewayClassName))
		if gcerr != nil {
			continue
		}
		if string(gwc.Spec.ControllerName) != domain.ControllerNameALB {
			continue
		}

		if perr := uc.k8sRepo.PatchMutateGateway(ctx, gw, func(_ context.Context, fresh *gwv1.Gateway) bool {
			if fresh.Annotations == nil {
				fresh.Annotations = map[string]string{}
			}
			if fresh.Annotations[AnnotationRouteRevision] == tick {
				return false
			}
			fresh.Annotations[AnnotationRouteRevision] = tick
			return true
		}); perr != nil {
			logger.Warnf("failed to bump %s on gateway %s/%s: %v", AnnotationRouteRevision, key.Namespace, key.Name, perr)
		}
	}
	return nil
}
