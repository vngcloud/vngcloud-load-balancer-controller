package alb_gateway_uc

import (
	"context"
	"fmt"

	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// applyRoutePolicyToPolicies overlays VKSRoutePolicy fields onto the policies
// generated for a single (route, rule) pair. Two scopes are checked: the
// rule's own sectionName, then the route as a whole (no sectionName); both
// overlays apply, with rule-scoped winning on per-field conflict.
//
// Phase 1 honors:
//   - Actions[Reject|Redirect]   → vngcloud Action override (REJECT / REDIRECT_TO_URL)
//   - Position                   → policy position override
//
// FixedResponse and AdditionalMatches are not in the trimmed VKSRoutePolicy
// schema (vngcloud LB doesn't support them); see design spec §1.6.
func (t *defaultGatewayBuildTask) applyRoutePolicyToPolicies(ctx context.Context, policies []v1alpha1.Policy, route *gwv1.HTTPRoute, ruleName string) ([]v1alpha1.Policy, error) {
	overlays, err := t.resolveRoutePolicies(ctx, route, ruleName)
	if err != nil {
		return policies, err
	}
	if len(overlays) == 0 {
		return policies, nil
	}
	for i := range policies {
		for _, p := range overlays {
			applyOneRouteOverlay(&policies[i], p)
		}
	}
	return policies, nil
}

// applyRoutePolicyToRankedPolicies is the rankedPolicy-aware variant. Same
// overlay rules; preserves the rank metadata so the caller can sort + assign
// Position values afterwards. When a VKSRoutePolicy supplies Position, it's
// recorded on rankedPolicy.userPosition so sortAndAssignPositions honors
// the user's value verbatim instead of overwriting with the auto-assigned one.
func (t *defaultGatewayBuildTask) applyRoutePolicyToRankedPolicies(ctx context.Context, items []rankedPolicy, route *gwv1.HTTPRoute, ruleName string) ([]rankedPolicy, error) {
	overlays, err := t.resolveRoutePolicies(ctx, route, ruleName)
	if err != nil {
		return items, err
	}
	if len(overlays) == 0 {
		return items, nil
	}
	for i := range items {
		for _, p := range overlays {
			applyOneRouteOverlay(&items[i].policy, p)
			if p.Spec.Position != nil {
				v := *p.Spec.Position
				items[i].userPosition = &v
			}
		}
	}
	return items, nil
}

func applyOneRouteOverlay(p *v1alpha1.Policy, rp *gwv1alpha1.VKSRoutePolicy) {
	if rp.Spec.Position != nil {
		v := *rp.Spec.Position
		p.Position = &v
	}
	if len(rp.Spec.Actions) == 0 {
		return
	}
	// One action wins (the first one). VKSRoutePolicy.Actions[0] supersedes
	// the rule's default REDIRECT_TO_POOL.
	a := rp.Spec.Actions[0]
	switch a.Type {
	case "Reject":
		p.Action = v2.PolicyActionREJECT
		p.RedirectPoolName = nil
		p.RedirectUrl = nil
		p.RedirectHttpCode = nil
	case "Redirect":
		if a.Redirect == nil {
			return
		}
		p.Action = v2.PolicyActionREDIRECTTOURL
		p.RedirectPoolName = nil
		p.RedirectUrl = ptr.To(a.Redirect.URL)
		if a.Redirect.HTTPCode != nil {
			code := *a.Redirect.HTTPCode
			p.RedirectHttpCode = &code
		}
		if a.Redirect.KeepQueryString != nil {
			v := *a.Redirect.KeepQueryString
			p.KeepQueryString = &v
		}
	}
}

// resolveRoutePolicies returns the (rule-scoped, route-scoped) winners for
// a given (route, rule). Order matters: rule-scoped is applied last so it
// overrides route-scoped on per-field conflicts.
func (t *defaultGatewayBuildTask) resolveRoutePolicies(ctx context.Context, route *gwv1.HTTPRoute, ruleName string) ([]*gwv1alpha1.VKSRoutePolicy, error) {
	var list gwv1alpha1.VKSRoutePolicyList
	if err := t.uc.k8sClient.List(ctx, &list, client.InNamespace(route.Namespace)); err != nil {
		return nil, fmt.Errorf("list VKSRoutePolicy in %s: %w", route.Namespace, err)
	}
	cands := make([]*gwv1alpha1.VKSRoutePolicy, 0, len(list.Items))
	for i := range list.Items {
		cands = append(cands, &list.Items[i])
	}

	out := make([]*gwv1alpha1.VKSRoutePolicy, 0, 2)
	// Route-scoped (no sectionName) — applied first so rule-scoped wins.
	if win, _ := sharedUC.ResolveDirectPolicy(cands, pkggw.PolicyTarget{
		Group: "gateway.networking.k8s.io", Kind: "HTTPRoute",
		Namespace: route.Namespace, Name: route.Name,
	}); win != nil {
		out = append(out, win)
	}
	// Rule-scoped (sectionName=ruleName).
	if ruleName != "" {
		if win, _ := sharedUC.ResolveDirectPolicy(cands, pkggw.PolicyTarget{
			Group: "gateway.networking.k8s.io", Kind: "HTTPRoute",
			Namespace: route.Namespace, Name: route.Name, SectionName: ruleName,
		}); win != nil {
			out = append(out, win)
		}
	}
	return out, nil
}
