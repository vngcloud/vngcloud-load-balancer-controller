package alb_gateway_uc

import (
	"context"
	"strconv"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	ctlshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// AnnotationRouteRevision lives on Gateway objects. The HTTPRoute reconciler
// bumps it (via EnqueueParentGatewayForRoute) so controller-runtime fires a
// Gateway reconcile event without needing a separate channel source. The
// value is opaque (a UnixNano timestamp) — callers only compare for change.
const AnnotationRouteRevision = "gateway.vks.vngcloud.vn/route-revision"

// markAcceptedAndProgrammed writes the standard gateway-api positive
// conditions after a successful reconcile. It does not touch addresses or
// per-listener status — those need data the lbc_uc deploy path produces
// asynchronously and are deferred to a follow-up.
func (uc *albGatewayUseCase) markAcceptedAndProgrammed(ctx context.Context, gw *gwv1.Gateway) error {
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
		return true
	})
}

// EnqueueParentGatewayForRoute fires a synthetic re-reconcile of every
// vngcloud-alb Gateway named in the HTTPRoute's parentRefs by writing the
// AnnotationRouteRevision annotation with a fresh tick. Foreign Gateways
// (different controllerName) are skipped so we don't produce noise on
// resources we don't own.
//
// Best-effort: per-Gateway failures are logged and skipped; a missing
// HTTPRoute is treated as "already cleaned up" and returns nil.
func (uc *albGatewayUseCase) EnqueueParentGatewayForRoute(ctx context.Context, routeRef ctrl.Request) error {
	logger := contexts.NewContext(ctx).Log()

	rt, err := uc.k8sRepo.GetHTTPRoute(ctx, routeRef.NamespacedName)
	if err != nil {
		return client.IgnoreNotFound(err)
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
