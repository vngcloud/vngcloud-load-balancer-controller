package alb_gateway_uc

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"

	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// buildPoolsAndPolicies walks attached HTTPRoutes and produces
//   - a flat []Pool (deduped by pool name)
//   - the slice of (listenerName → []Policy) ready to fold into LBC.Listeners.
//
// Per the design spec §1.6, the vngcloud LB API supports only HOST_NAME and
// PATH at the L7 rule level. HTTPRoute matches that use header / queryParam /
// method are dropped here with a warning; Phase F status work surfaces this
// as Accepted=False on the affected route's parent status.
//
// Phase E (initial): no VKSRoutePolicy overlay, no VKSBackendPolicy /
// VKSHealthCheckPolicy on pools. Those land in a follow-up commit.
func (t *defaultGatewayBuildTask) buildPoolsAndPolicies(ctx context.Context) ([]v1alpha1.Pool, map[string][]v1alpha1.Policy, error) {
	routes, err := t.listAttachedHTTPRoutes(ctx)
	if err != nil {
		return nil, nil, err
	}

	pools := make([]v1alpha1.Pool, 0)
	seenPool := make(map[string]struct{})
	appendPool := func(p *v1alpha1.Pool) {
		if _, ok := seenPool[p.Name]; ok {
			return
		}
		seenPool[p.Name] = struct{}{}
		pools = append(pools, *p)
	}

	listenerPolicies := make(map[string][]v1alpha1.Policy)
	listenerRanked := make(map[string][]rankedPolicy)

	for ri := range routes {
		route := &routes[ri]
		parentRefs := parentRefsForGateway(route, t.gw)
		if len(parentRefs) == 0 {
			continue
		}

		for ruleIdx, rule := range route.Spec.Rules {
			if hasUnsupportedMatchDimension(rule.Matches) {
				t.logger.Warnf("HTTPRoute %s/%s rule %d uses match dimensions VNGCloud LB doesn't support "+
					"(headers/queryParams/method); skipping rule. Route status reports PartiallyInvalid.",
					route.Namespace, route.Name, ruleIdx)
				continue
			}

			// A rule's backends merge into one synthetic pool with a single
			// overlay; divergent per-backend policies fail the rule closed
			// (route status reports BackendConfigMismatch).
			if diverge, err := t.ruleBackendPoliciesDiverge(ctx, route, rule); err != nil {
				return nil, nil, err
			} else if diverge {
				t.logger.Warnf("HTTPRoute %s/%s rule %d: backends carry divergent VKSBackend/VKSHealthCheck policies; skipping rule",
					route.Namespace, route.Name, ruleIdx)
				continue
			}

			// A rule with no backendRefs is valid Gateway-API when it carries
			// a RequestRedirect filter (or, in future phases, FixedResponse-
			// equivalent — not supported by VNGCloud, see §1.6). For those
			// filter-only rules we don't synthesize a pool; the policy will
			// emit REDIRECT_TO_URL straight from the filter.
			poolName := ""
			if len(rule.BackendRefs) > 0 {
				pool, err := t.synthesizePool(ctx, route, ruleIdx, rule)
				if err != nil {
					t.logger.Warnf("HTTPRoute %s/%s rule %d: %v; skipping rule",
						route.Namespace, route.Name, ruleIdx, err)
					continue
				}
				appendPool(pool)
				poolName = pool.Name
			} else if !hasRedirectFilter(rule.Filters) {
				t.logger.Warnf("HTTPRoute %s/%s rule %d has no backendRefs and no RequestRedirect filter; skipping",
					route.Namespace, route.Name, ruleIdx)
				continue
			}

			ruleName := ""
			if rule.Name != nil {
				ruleName = string(*rule.Name)
			}

			for _, parent := range parentRefs {
				for li := range t.gw.Spec.Listeners {
					l := &t.gw.Spec.Listeners[li]
					if l.Protocol != gwv1.HTTPProtocolType && l.Protocol != gwv1.HTTPSProtocolType {
						continue
					}
					if !listenerAcceptsRoute(l, route, &parent) {
						continue
					}
					hostnames := matchingRouteHostnames(l, route)
					ranked := t.buildListenerPolicies(route, ruleIdx, rule, hostnames, poolName)
					ranked, err := t.applyRoutePolicyToRankedPolicies(ctx, ranked, route, ruleName)
					if err != nil {
						return nil, nil, err
					}
					listenerRanked[string(l.Name)] = append(listenerRanked[string(l.Name)], ranked...)
				}
			}
		}
	}

	// Sort each listener's policies by Gateway-API match specificity
	// (Exact > Regex > Prefix; longer paths first; more headers; older
	// route timestamp tiebreak) and assign sequential Positions starting
	// at 1 so the cloud LB evaluates the most specific match first.
	// Mirrors how GCP gke-gateway / AWS LBC order rules on a URL map.
	// VKSRoutePolicy.Position is honored verbatim if the user set it.
	for k, items := range listenerRanked {
		listenerPolicies[k] = sortAndAssignPositions(items)
	}
	sort.SliceStable(pools, func(i, j int) bool { return pools[i].Name < pools[j].Name })

	return pools, listenerPolicies, nil
}

// hasUnsupportedMatchDimension returns true if any HTTPRouteMatch uses a
// dimension VNGCloud LB doesn't translate (header / queryParam / method).
// This is checked at the rule level — one such match invalidates the whole
// rule, since rejecting per-match would silently drop traffic.
func hasUnsupportedMatchDimension(matches []gwv1.HTTPRouteMatch) bool {
	for _, m := range matches {
		if len(m.Headers) > 0 || len(m.QueryParams) > 0 || m.Method != nil {
			return true
		}
	}
	return false
}

// hasRedirectFilter reports whether any filter is a RequestRedirect — used to
// keep filter-only HTTPRoute rules (no backendRefs) alive so they can emit a
// REDIRECT_TO_URL policy.
func hasRedirectFilter(filters []gwv1.HTTPRouteFilter) bool {
	for _, f := range filters {
		if f.Type == gwv1.HTTPRouteFilterRequestRedirect && f.RequestRedirect != nil {
			return true
		}
	}
	return false
}

// rankedPolicy pairs a generated v1alpha1.Policy with the metadata
// sortAndAssignPositions needs to order it on the listener (more-specific
// match first). userPosition is non-nil when a VKSRoutePolicy.Position
// applied via overlay; sortAndAssignPositions honors it verbatim instead
// of overwriting with the auto-assigned value.
type rankedPolicy struct {
	policy       v1alpha1.Policy
	rank         shared.RankedMatch
	userPosition *int32
}

// buildListenerPolicies emits one rankedPolicy per (hostname × match)
// combo. With no hostnames, one policy per match (no HOST_NAME rule).
// Action is REDIRECT_TO_POOL when poolName is non-empty; if poolName is
// empty (filter-only rule) the action stays REDIRECT_TO_POOL until the
// RequestRedirect filter — guaranteed present by the caller in that case —
// flips it to REDIRECT_TO_URL.
//
// policyName is keyed on (route.UID, ruleIdx, host, match) so multiple
// HTTPRoutes attached to the same Gateway don't collide on a single name.
func (t *defaultGatewayBuildTask) buildListenerPolicies(route *gwv1.HTTPRoute, ruleIdx int, rule gwv1.HTTPRouteRule, hostnames []string, poolName string) []rankedPolicy {
	matches := rule.Matches
	if len(matches) == 0 {
		// Default-empty match → match all on this listener.
		matches = []gwv1.HTTPRouteMatch{{}}
	}
	hostList := hostnames
	if len(hostList) == 0 {
		hostList = []string{""}
	}

	out := make([]rankedPolicy, 0, len(matches)*len(hostList))
	for _, host := range hostList {
		for _, m := range matches {
			rules := buildL7Rules(host, m)
			policy := v1alpha1.Policy{
				Name:    policyName(route, ruleIdx, host, m),
				Action:  v2.PolicyActionREDIRECTTOPOOL,
				L7Rules: rules,
			}
			if poolName != "" {
				policy.RedirectPoolName = ptr.To(poolName)
			}
			if applyRequestRedirectFilter(&policy, rule.Filters) {
				policy.RedirectPoolName = nil
			}
			out = append(out, rankedPolicy{
				policy: policy,
				rank: shared.RankedMatch{
					Match:          m,
					RouteCreated:   route.CreationTimestamp,
					RouteNamespace: route.Namespace,
					RouteName:      route.Name,
				},
			})
		}
	}
	return out
}

// sortAndAssignPositions orders the listener's policies by Gateway-API
// match specificity and assigns sequential Position values 1..N so the
// cloud LB evaluates the most-specific match first. User-set positions
// from VKSRoutePolicy.Position are honored verbatim (not renumbered).
// Sorting is stable so policies that compare equal stay in their original
// relative order — important for deterministic Spec equality across
// reconciles.
func sortAndAssignPositions(items []rankedPolicy) []v1alpha1.Policy {
	// Dedup by name first so two routes that hash to the same policy name
	// don't emit a duplicate (defence in depth — names should already be
	// unique because they include route.UID).
	seen := make(map[string]struct{}, len(items))
	deduped := make([]rankedPolicy, 0, len(items))
	for i := range items {
		if _, ok := seen[items[i].policy.Name]; ok {
			continue
		}
		seen[items[i].policy.Name] = struct{}{}
		deduped = append(deduped, items[i])
	}

	// Stable sort by match specificity. Lower index → higher priority on
	// the cloud LB.
	ranks := make(shared.ByMatchSpecificity, 0, len(deduped))
	for _, rp := range deduped {
		ranks = append(ranks, rp.rank)
	}
	indices := make([]int, len(deduped))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return ranks.Less(indices[i], indices[j])
	})

	out := make([]v1alpha1.Policy, 0, len(deduped))
	for newPos, oldIdx := range indices {
		rp := deduped[oldIdx]
		if rp.userPosition != nil {
			rp.policy.Position = rp.userPosition
		} else {
			pos := int32(newPos + 1) // 1-based
			rp.policy.Position = &pos
		}
		out = append(out, rp.policy)
	}
	return out
}

// buildL7Rules emits a HOST_NAME rule (literal → EQUAL_TO, wildcard → REGEX)
// when host is non-empty, and a PATH rule from the match's path. Other match
// dimensions (header / queryParam / method) are guaranteed empty by
// hasUnsupportedMatchDimension having returned false earlier.
func buildL7Rules(host string, m gwv1.HTTPRouteMatch) []v1alpha1.L7Rule {
	rules := make([]v1alpha1.L7Rule, 0, 2)
	if host != "" {
		compare := v2.PolicyCompareTypeEQUALS
		value := host
		if isWildcardHost(host) {
			compare = v2.PolicyCompareTypeREGEX
			value = wildcardHostToRegex(host)
		}
		rules = append(rules, v1alpha1.L7Rule{
			RuleType:    v2.PolicyRuleTypeHOSTNAME,
			CompareType: compare,
			RuleValue:   value,
		})
	}
	if m.Path != nil && m.Path.Value != nil && *m.Path.Value != "" {
		compare := v2.PolicyCompareTypeSTARTSWITH
		if m.Path.Type != nil {
			switch *m.Path.Type {
			case gwv1.PathMatchExact:
				compare = v2.PolicyCompareTypeEQUALS
			case gwv1.PathMatchRegularExpression:
				compare = v2.PolicyCompareTypeREGEX
			case gwv1.PathMatchPathPrefix:
				compare = v2.PolicyCompareTypeSTARTSWITH
			}
		}
		rules = append(rules, v1alpha1.L7Rule{
			RuleType:    v2.PolicyRuleTypePATH,
			CompareType: compare,
			RuleValue:   *m.Path.Value,
		})
	}
	return rules
}

// applyRequestRedirectFilter handles the Gateway-API RequestRedirect filter.
// Returns true when the filter was applied (caller drops RedirectPoolName).
func applyRequestRedirectFilter(p *v1alpha1.Policy, filters []gwv1.HTTPRouteFilter) bool {
	for _, f := range filters {
		if f.Type != gwv1.HTTPRouteFilterRequestRedirect || f.RequestRedirect == nil {
			continue
		}
		r := f.RequestRedirect
		p.Action = v2.PolicyActionREDIRECTTOURL
		p.RedirectUrl = ptr.To(buildRedirectURL(r))
		if r.StatusCode != nil {
			code := int32(*r.StatusCode)
			p.RedirectHttpCode = &code
		}
		return true
	}
	return false
}

func buildRedirectURL(r *gwv1.HTTPRequestRedirectFilter) string {
	scheme := "https"
	if r.Scheme != nil {
		scheme = *r.Scheme
	}
	host := ""
	if r.Hostname != nil {
		host = string(*r.Hostname)
	}
	url := scheme + "://" + host
	if r.Port != nil {
		url = fmt.Sprintf("%s:%d", url, *r.Port)
	}
	if r.Path != nil && r.Path.ReplaceFullPath != nil {
		url += *r.Path.ReplaceFullPath
	}
	return url
}

// policyName generates a deterministic LBC.Policy name keyed by route UID,
// rule index, hostname, and a hash of the match. Mirrors the synth-pool
// approach so two reconciles converge on the same policy name.
func policyName(route *gwv1.HTTPRoute, ruleIdx int, host string, m gwv1.HTTPRouteMatch) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(host))
	if m.Path != nil {
		if m.Path.Value != nil {
			_, _ = h.Write([]byte(*m.Path.Value))
		}
		if m.Path.Type != nil {
			_, _ = h.Write([]byte(*m.Path.Type))
		}
	}
	uid := string(route.UID)
	if len(uid) > 8 {
		uid = uid[:8]
	}
	name := fmt.Sprintf("%sgw_%s_%d_%x", domain.VKSResourceNamePrefix, uid, ruleIdx, h.Sum32())
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}

// dedupPoliciesByName drops second-and-later occurrences of any policy with
// a duplicate name. Sorts by name afterwards so the listener's policy slice
// is comparable across reconciles.
func dedupPoliciesByName(in []v1alpha1.Policy) []v1alpha1.Policy {
	seen := make(map[string]struct{}, len(in))
	out := make([]v1alpha1.Policy, 0, len(in))
	for i := range in {
		if _, ok := seen[in[i].Name]; ok {
			continue
		}
		seen[in[i].Name] = struct{}{}
		out = append(out, in[i])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func isWildcardHost(h string) bool {
	return len(h) > 2 && h[0] == '*' && h[1] == '.'
}

func wildcardHostToRegex(h string) string {
	// "*.example.com" → "^[^.]+\.example\.com$" — pkg/gateway has the same
	// shape; we re-implement here to keep the LBC.L7Rule.RuleValue self-
	// contained without an extra import dance.
	regexed := make([]byte, 0, len(h)+8)
	regexed = append(regexed, '^', '[', '^', '.', ']', '+', '\\', '.')
	for i := 2; i < len(h); i++ {
		c := h[i]
		switch c {
		case '.', '+', '?', '^', '$', '|', '(', ')', '{', '}', '[', ']', '\\':
			regexed = append(regexed, '\\', c)
		default:
			regexed = append(regexed, c)
		}
	}
	regexed = append(regexed, '$')
	return string(regexed)
}
