package alb_gateway_uc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
)

func TestBuildPolicies_HostnameAndPath(t *testing.T) {
	rule := gwv1.HTTPRouteRule{
		Matches: []gwv1.HTTPRouteMatch{{
			Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchPathPrefix), Value: ptr.To("/api")},
		}},
	}
	hosts := []gwv1.Hostname{"a.example.com"}
	policies := BuildPolicies("route-uid-12345678", 0, hosts, rule, "synth-pool", nil)
	if assert.Len(t, policies, 1) {
		p := policies[0]
		assert.Equal(t, loadbalancerv2.PolicyActionREDIRECTTOPOOL, p.Action)
		var sawHost, sawPath bool
		for _, r := range p.L7Rules {
			if r.RuleType == loadbalancerv2.PolicyRuleTypeHOSTNAME {
				sawHost = true
				assert.Equal(t, "a.example.com", r.RuleValue)
			}
			if r.RuleType == loadbalancerv2.PolicyRuleTypePATH {
				sawPath = true
				assert.Equal(t, loadbalancerv2.PolicyCompareTypeSTARTSWITH, r.CompareType)
				assert.Equal(t, "/api", r.RuleValue)
			}
		}
		assert.True(t, sawHost && sawPath)
		assert.NotNil(t, p.RedirectPoolName)
		assert.Equal(t, "synth-pool", *p.RedirectPoolName)
	}
}

func TestBuildPolicies_NoHostnames_OneMatch(t *testing.T) {
	rule := gwv1.HTTPRouteRule{
		Matches: []gwv1.HTTPRouteMatch{{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchExact), Value: ptr.To("/x")}}},
	}
	policies := BuildPolicies("uid", 0, nil, rule, "p", nil)
	assert.Len(t, policies, 1)
}

func TestBuildPolicies_RedirectFilter(t *testing.T) {
	redirect := gwv1.HTTPRequestRedirectFilter{
		Hostname:   ptr.To(gwv1.PreciseHostname("new.example.com")),
		StatusCode: ptr.To(301),
	}
	rule := gwv1.HTTPRouteRule{
		Matches: []gwv1.HTTPRouteMatch{{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchExact), Value: ptr.To("/")}}},
		Filters: []gwv1.HTTPRouteFilter{{Type: gwv1.HTTPRouteFilterRequestRedirect, RequestRedirect: &redirect}},
	}
	policies := BuildPolicies("uid", 0, nil, rule, "p", nil)
	assert.Len(t, policies, 1)
	assert.Equal(t, loadbalancerv2.PolicyActionREDIRECTTOURL, policies[0].Action)
	assert.NotNil(t, policies[0].RedirectUrl)
	assert.Equal(t, "https://new.example.com", *policies[0].RedirectUrl)
	assert.NotNil(t, policies[0].RedirectHttpCode)
	assert.EqualValues(t, 301, *policies[0].RedirectHttpCode)
}

func TestBuildPolicies_HostMatchCartesian(t *testing.T) {
	rule := gwv1.HTTPRouteRule{
		Matches: []gwv1.HTTPRouteMatch{
			{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchExact), Value: ptr.To("/a")}},
			{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchExact), Value: ptr.To("/b")}},
		},
	}
	hosts := []gwv1.Hostname{"x.com", "y.com"}
	policies := BuildPolicies("uid", 0, hosts, rule, "p", nil)
	assert.Len(t, policies, 4)
	names := map[string]bool{}
	for _, p := range policies {
		names[p.Name] = true
	}
	assert.Len(t, names, 4) // unique names
}

func TestBuildPolicies_WildcardHostnameUsesRegex(t *testing.T) {
	rule := gwv1.HTTPRouteRule{
		Matches: []gwv1.HTTPRouteMatch{{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchExact), Value: ptr.To("/")}}},
	}
	policies := BuildPolicies("uid", 0, []gwv1.Hostname{"*.example.com"}, rule, "p", nil)
	assert.Len(t, policies, 1)
	var sawHost bool
	for _, r := range policies[0].L7Rules {
		if r.RuleType == loadbalancerv2.PolicyRuleTypeHOSTNAME {
			sawHost = true
			assert.Equal(t, loadbalancerv2.PolicyCompareTypeREGEX, r.CompareType)
		}
	}
	assert.True(t, sawHost)
}
