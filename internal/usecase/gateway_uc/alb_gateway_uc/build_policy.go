package alb_gateway_uc

import (
	"fmt"

	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// BuildPolicies maps one HTTPRoute rule into one-or-more vngcloud policies, one per
// (hostname × match) cartesian. A nil hosts slice is treated as a single empty hostname.
// poolName is the synthetic pool that REDIRECT_TO_POOL targets when no overriding action.
// lrcs may be nil; when non-empty, AdditionalMatches and Actions extend / override.
func BuildPolicies(
	routeUID string,
	ruleIdx int,
	hosts []gwv1.Hostname,
	rule gwv1.HTTPRouteRule,
	poolName string,
	lrcs []*gatewayv1alpha1.ListenerRuleConfig,
) []vksv1alpha1.Policy {
	if len(hosts) == 0 {
		hosts = []gwv1.Hostname{""}
	}
	matches := rule.Matches
	if len(matches) == 0 {
		matches = []gwv1.HTTPRouteMatch{{}}
	}
	out := make([]vksv1alpha1.Policy, 0, len(hosts)*len(matches))

	redirect := findRedirectFilter(rule.Filters)
	uid := routeUID
	if len(uid) > 8 {
		uid = uid[:8]
	}

	for hi, h := range hosts {
		for mi, m := range matches {
			l7 := buildL7Rules(string(h), m, lrcs)
			p := vksv1alpha1.Policy{
				Name:    fmt.Sprintf("p_%s_%d_%d_%d", uid, ruleIdx, hi, mi),
				L7Rules: l7,
			}
			switch {
			case redirect != nil:
				p.Action = loadbalancerv2.PolicyActionREDIRECTTOURL
				if u := buildRedirectURL(*redirect); u != "" {
					p.RedirectUrl = ptr.To(u)
				}
				if redirect.StatusCode != nil {
					code := int32(*redirect.StatusCode)
					p.RedirectHttpCode = &code
				}
			default:
				p.Action = loadbalancerv2.PolicyActionREDIRECTTOPOOL
				p.RedirectPoolName = ptr.To(poolName)
			}
			out = append(out, p)
		}
	}
	return out
}

func buildL7Rules(host string, m gwv1.HTTPRouteMatch, lrcs []*gatewayv1alpha1.ListenerRuleConfig) []vksv1alpha1.L7Rule {
	var l7 []vksv1alpha1.L7Rule
	if cmp, val := pkggw.HostnameToL7Rule(host); cmp != "" {
		l7 = append(l7, vksv1alpha1.L7Rule{
			RuleType:    loadbalancerv2.PolicyRuleTypeHOSTNAME,
			CompareType: loadbalancerv2.PolicyCompareType(cmp),
			RuleValue:   val,
		})
	}
	if m.Path != nil && m.Path.Type != nil && m.Path.Value != nil {
		cmp, val := pkggw.PathToL7Rule(string(*m.Path.Type), *m.Path.Value)
		l7 = append(l7, vksv1alpha1.L7Rule{
			RuleType:    loadbalancerv2.PolicyRuleTypePATH,
			CompareType: loadbalancerv2.PolicyCompareType(cmp),
			RuleValue:   val,
		})
	}
	for _, lrc := range lrcs {
		for _, am := range lrc.Spec.AdditionalMatches {
			rt := mapAdditionalMatchType(am.Type)
			if rt == "" {
				// Match types not yet supported by vngcloud LB are silently dropped here.
				// The caller surfaces this as a Warning event / Programmed=False on the route.
				continue
			}
			l7 = append(l7, vksv1alpha1.L7Rule{
				RuleType:    rt,
				CompareType: loadbalancerv2.PolicyCompareType(am.Compare),
				RuleValue:   am.Value,
			})
		}
	}
	return l7
}

func findRedirectFilter(filters []gwv1.HTTPRouteFilter) *gwv1.HTTPRequestRedirectFilter {
	for i := range filters {
		if filters[i].Type == gwv1.HTTPRouteFilterRequestRedirect {
			return filters[i].RequestRedirect
		}
	}
	return nil
}

func buildRedirectURL(r gwv1.HTTPRequestRedirectFilter) string {
	scheme := "https"
	if r.Scheme != nil {
		scheme = *r.Scheme
	}
	host := ""
	if r.Hostname != nil {
		host = string(*r.Hostname)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// mapAdditionalMatchType maps a ListenerRuleConfig AdditionalMatch.Type onto a vngcloud
// PolicyRuleType. Phase 1: vngcloud LB has only HOST_NAME and PATH rule types, so all
// LRC-extension match types resolve to "" (skipped). When vngcloud adds Header/Query/
// Method/SourceIP support, this mapping table is the only thing that needs updating.
func mapAdditionalMatchType(t string) loadbalancerv2.PolicyRuleType {
	switch t {
	case "Header", "QueryParam", "Method", "SourceIP":
		return ""
	default:
		return ""
	}
}
