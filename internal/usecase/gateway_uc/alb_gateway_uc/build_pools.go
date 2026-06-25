package alb_gateway_uc

import (
	"context"
	"fmt"
	"sort"

	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// synthesizePool builds one v1alpha1.Pool from one HTTPRoute rule. Members
// resolve to pod IPs ("ip" target type) or node IPs + nodePort ("instance"
// target type) per the backend's VKSBackendPolicy.TargetType, defaulting to
// "instance" — matches the Ingress controller's default and works on overlay
// CNIs where pod IPs aren't routable from the cloud LB. Weights are computed
// via pkggw.ScaleWeights so that a 1:99 traffic split with an uneven
// readyEndpoints count still lands in the API's accepted [1..100] range.
// Pool name is deterministic via pkggw.SynthPoolName.
//
// Same-namespace backendRefs only. Cross-namespace via ReferenceGrant lands
// in a separate follow-up.
func (t *defaultGatewayBuildTask) synthesizePool(ctx context.Context, route *gwv1.HTTPRoute, ruleIdx int, rule gwv1.HTTPRouteRule) (*v1alpha1.Pool, error) {
	if len(rule.BackendRefs) == 0 {
		return nil, fmt.Errorf("rule %d on HTTPRoute %s/%s has no backendRefs", ruleIdx, route.Namespace, route.Name)
	}

	keys := make([]pkggw.BackendKey, 0, len(rule.BackendRefs))
	weights := make([]pkggw.BackendWeight, 0, len(rule.BackendRefs))
	memberAddrs := make([][]utils.EndpointAddress, 0, len(rule.BackendRefs))

	for i := range rule.BackendRefs {
		br := &rule.BackendRefs[i].BackendRef
		ns := route.Namespace
		if br.Namespace != nil {
			ns = string(*br.Namespace)
		}
		if ns != route.Namespace {
			// Cross-namespace backendRef: include it only when a ReferenceGrant
			// in the backend's namespace permits HTTPRoutes from the route's
			// namespace. Otherwise drop it (the route's ResolvedRefs status
			// reports RefNotPermitted).
			allowed, err := sharedUC.RefGrantAllowed(ctx, t.uc.k8sClient,
				sharedUC.Ref{Group: "", Kind: "Service", Namespace: ns, Name: string(br.Name)},
				sharedUC.Ref{Group: gwv1.GroupName, Kind: "HTTPRoute", Namespace: route.Namespace, Name: route.Name})
			if err != nil {
				return nil, fmt.Errorf("check ReferenceGrant for backendRef %s/%s: %w", ns, br.Name, err)
			}
			if !allowed {
				t.logger.Warnf("backendRef %s/%s on HTTPRoute %s/%s is cross-namespace and not permitted by any ReferenceGrant; skipping",
					ns, br.Name, route.Namespace, route.Name)
				continue
			}
		}
		port := int32(0)
		if br.Port != nil {
			port = int32(*br.Port)
		}
		weight := int32(1)
		if br.Weight != nil {
			weight = *br.Weight
		}
		// Skip explicit-zero-weight backends (Gateway-API spec convention for
		// "kept in spec but no traffic").
		if weight == 0 {
			continue
		}

		targetType, err := t.resolveTargetType(ctx, ns, string(br.Name))
		if err != nil {
			return nil, err
		}
		nodeLabels, err := t.resolveTargetNodeLabels(ctx, ns, string(br.Name))
		if err != nil {
			return nil, err
		}
		svcKey := types.NamespacedName{Namespace: ns, Name: string(br.Name)}
		resolveOpts := []utils.EndpointResolveOption{
			utils.WithNodeSelector(labels.SelectorFromSet(labels.Set(nodeLabels))),
		}
		var addrs []utils.EndpointAddress
		switch targetType {
		case domain.TargetTypeInstance:
			addrs, err = t.uc.endpointResolver.ResolveNodePortEndpoints(ctx, svcKey, intstr.FromInt(int(port)), resolveOpts...)
		default: // domain.TargetTypeIP
			addrs, err = t.uc.endpointResolver.ResolvePodEndpoints(ctx, svcKey, intstr.FromInt(int(port)), resolveOpts...)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve endpoints for backendRef %s/%s (mode=%s): %w", ns, br.Name, targetType, err)
		}

		ready := int32(len(addrs))
		if ready == 0 {
			// 0 ready endpoints would set ScaleWeights's denominator to 1
			// (its own internal floor); leaving it at 0 here is fine and lets
			// the pool still be created, members come back when pods become
			// Ready. Avoids reconcile churn during rollouts.
			ready = 0
		}
		keys = append(keys, pkggw.BackendKey{Namespace: ns, Name: string(br.Name), Port: port, Weight: weight})
		weights = append(weights, pkggw.BackendWeight{Weight: weight, Ready: ready})
		memberAddrs = append(memberAddrs, addrs)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("rule %d on HTTPRoute %s/%s: all backends were dropped (cross-ns or zero-weight)",
			ruleIdx, route.Namespace, route.Name)
	}

	scaled := pkggw.ScaleWeights(weights)

	// Build Members. Each backend's resolved addresses share the backend's
	// scaled weight. Ports come from the resolved EndpointAddress (covers
	// named-port resolution by EndpointResolver). Member names get the
	// project-wide "vks_" prefix so every cloud-side resource is greppable;
	// truncated to 50 chars to satisfy the cloud's name limit.
	members := make([]v1alpha1.PoolMember, 0)
	for i, addrs := range memberAddrs {
		w := int(scaled[i])
		for _, a := range addrs {
			members = append(members, v1alpha1.PoolMember{
				Name:        memberName(a.Name),
				IP:          a.IP,
				Port:        a.Port,
				MonitorPort: a.Port,
				Weight:      &w,
			})
		}
	}
	// Stable order so DeepEqual on Spec is meaningful between reconciles.
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].IP != members[j].IP {
			return members[i].IP < members[j].IP
		}
		return members[i].Port < members[j].Port
	})

	pool := &v1alpha1.Pool{
		Name:     pkggw.SynthPoolName(string(route.UID), ruleIdx, keys),
		Protocol: v2.PoolProtocolHTTP,
		Members:  members,
		HealthMonitor: v1alpha1.PoolHealthMonitor{
			Protocol: v2.HealthCheckProtocolTCP,
		},
	}

	// Apply VKSBackendPolicy / VKSHealthCheckPolicy overlays for the first
	// backend. When a rule has multiple backendRefs targeting different
	// Services, this picks the policy attached to the first one — Phase F's
	// status work will surface a "conflicting per-backend policies" warning
	// when other backendRefs would have produced a different overlay.
	first := keys[0]
	if err := t.applyBackendPolicyToPool(ctx, pool, first.Namespace, first.Name); err != nil {
		return nil, err
	}
	if err := t.applyHealthCheckPolicyToPool(ctx, pool, first.Namespace, first.Name); err != nil {
		return nil, err
	}
	return pool, nil
}
