package alb_gateway_uc

import (
	"context"

	"github.com/anngdinh/operator-helper/contexts"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// attachHTTPRoutes lists HTTPRoutes that target the Gateway, resolves their
// backends, and attaches synthesized pools and policies onto lbSpec. Pools
// are deduplicated across listeners by SynthPoolName.
//
// Backends that fail to resolve (missing Service, no endpoints, cross-NS
// without ReferenceGrant, unsupported group/kind) are logged and skipped at
// the rule level — surfacing partial-failure as Route ResolvedRefs=False is
// C9e's job.
func (uc *albGatewayUseCase) attachHTTPRoutes(ctx context.Context, gw *gwv1.Gateway, lbSpec *vksv1alpha1.LoadBalancerConfigSpec, acc *routeStatusAccumulator) error {
	logger := contexts.NewContext(ctx).Log()

	routeList := &gwv1.HTTPRouteList{}
	if err := uc.k8sRepo.ListHTTPRoute(ctx, routeList); err != nil {
		return err
	}
	attached := routesAttachedToGateway(routeList.Items, gw)
	if len(attached) == 0 {
		return nil
	}

	// Pre-register every matched route so even routes that fail to attach to
	// any listener (NoMatchingParent / NotAllowedByListeners) get a status
	// update.
	for _, ar := range attached {
		acc.initRoute(ar.Route, ar.Parents)
	}

	grantList := &gwv1beta1.ReferenceGrantList{}
	if err := uc.k8sRepo.ListReferenceGrant(ctx, grantList); err != nil {
		return err
	}

	tgcCache := map[string][]gatewayv1alpha1.TargetGroupConfig{}
	lrcCache := map[string][]*gatewayv1alpha1.ListenerRuleConfig{}

	getTGCs := func(ns string) []gatewayv1alpha1.TargetGroupConfig {
		if v, ok := tgcCache[ns]; ok {
			return v
		}
		list := &gatewayv1alpha1.TargetGroupConfigList{}
		if err := uc.k8sRepo.ListTargetGroupConfig(ctx, list, client.InNamespace(ns)); err != nil {
			logger.Warnf("ListTargetGroupConfig(%s) failed: %v", ns, err)
			tgcCache[ns] = nil
			return nil
		}
		tgcCache[ns] = list.Items
		return list.Items
	}
	getLRCs := func(ns string) []*gatewayv1alpha1.ListenerRuleConfig {
		if v, ok := lrcCache[ns]; ok {
			return v
		}
		list := &gatewayv1alpha1.ListenerRuleConfigList{}
		if err := uc.k8sRepo.ListListenerRuleConfig(ctx, list, client.InNamespace(ns)); err != nil {
			logger.Warnf("ListListenerRuleConfig(%s) failed: %v", ns, err)
			lrcCache[ns] = nil
			return nil
		}
		out := make([]*gatewayv1alpha1.ListenerRuleConfig, len(list.Items))
		for i := range list.Items {
			out[i] = &list.Items[i]
		}
		lrcCache[ns] = out
		return out
	}

	seenPool := map[string]struct{}{}

	for li := range lbSpec.Listeners {
		listener := &lbSpec.Listeners[li]
		gwListener := findGatewayListenerByName(gw.Spec.Listeners, listener.Name)
		if gwListener == nil {
			continue
		}

		for _, ar := range attached {
			if !routeAttachesToListener(gwListener, gw.Namespace, ar.Parents, ar.Route.Namespace) {
				continue
			}
			rep := acc.reports[string(ar.Route.UID)]
			for _, p := range ar.Parents {
				rep.markAttachedToListener(p)
			}

			tgcs := getTGCs(ar.Route.Namespace)
			lrcs := getLRCs(ar.Route.Namespace)

			for ruleIdx, rule := range ar.Route.Spec.Rules {
				ruleName := (*string)(nil)
				if rule.Name != nil {
					n := string(*rule.Name)
					ruleName = &n
				}

				resolved := make([]BackendEndpoints, 0, len(rule.BackendRefs))
				var props *gatewayv1alpha1.TargetGroupProperties
				for _, backend := range rule.BackendRefs {
					be, p, err := uc.resolveBackend(ctx, ar.Route.Namespace, ar.Route.Name, "HTTPRoute", ruleName, backend, grantList.Items, tgcs)
					if err != nil {
						logger.Warnf("backend %s/%s on route %s/%s rule %d skipped: %v",
							ar.Route.Namespace, backend.Name, ar.Route.Namespace, ar.Route.Name, ruleIdx, err)
						rep.recordBackendError(err, string(backend.Name))
						continue
					}
					if props == nil {
						props = p
					}
					resolved = append(resolved, *be)
				}
				if len(resolved) == 0 {
					continue
				}

				keys := make([]pkggw.BackendKey, len(resolved))
				for i, b := range resolved {
					keys[i] = b.Backend
				}
				poolName := pkggw.SynthPoolName(string(ar.Route.UID), ruleIdx, keys)

				if _, dup := seenPool[poolName]; !dup {
					seenPool[poolName] = struct{}{}
					lbSpec.Pools = append(lbSpec.Pools, buildPoolFromBackends(poolName, resolved, props))
				}

				policies := BuildPolicies(string(ar.Route.UID), ruleIdx, ar.Route.Spec.Hostnames, rule, poolName, lrcs)
				listener.Policies = append(listener.Policies, policies...)
			}
		}
	}
	return nil
}

// findGatewayListenerByName matches an LBC listener back to its source Gateway
// listener. The name passed in is the *vngcloud* listener name produced by
// build_listener.vngcloudListenerName, which may differ from the Gateway listener
// name (short Gateway names get a "gw-" prefix to clear vngcloud's 5-char floor).
// We compare against both forms so the lookup survives that munging.
func findGatewayListenerByName(listeners []gwv1.Listener, vngcloudName string) *gwv1.Listener {
	for i := range listeners {
		raw := string(listeners[i].Name)
		if raw == vngcloudName || vngcloudListenerName(raw) == vngcloudName {
			return &listeners[i]
		}
	}
	return nil
}
