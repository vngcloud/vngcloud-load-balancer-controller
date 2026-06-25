package alb_gateway_uc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

func TestHasUnsupportedMatchDimension(t *testing.T) {
	tests := []struct {
		name    string
		matches []gwv1.HTTPRouteMatch
		want    bool
	}{
		{
			name:    "empty matches is supported",
			matches: nil,
			want:    false,
		},
		{
			name: "path-only is supported",
			matches: []gwv1.HTTPRouteMatch{{
				Path: &gwv1.HTTPPathMatch{
					Type:  ptrPathType(gwv1.PathMatchExact),
					Value: ptr.To("/foo"),
				},
			}},
			want: false,
		},
		{
			name: "header match is unsupported",
			matches: []gwv1.HTTPRouteMatch{{
				Headers: []gwv1.HTTPHeaderMatch{{Name: "X-Foo", Value: "bar"}},
			}},
			want: true,
		},
		{
			name: "queryParam match is unsupported",
			matches: []gwv1.HTTPRouteMatch{{
				QueryParams: []gwv1.HTTPQueryParamMatch{{Name: "q", Value: "v"}},
			}},
			want: true,
		},
		{
			name: "method match is unsupported",
			matches: []gwv1.HTTPRouteMatch{{
				Method: ptrMethod(gwv1.HTTPMethodGet),
			}},
			want: true,
		},
		{
			name: "path + header is unsupported",
			matches: []gwv1.HTTPRouteMatch{{
				Path:    &gwv1.HTTPPathMatch{Value: ptr.To("/")},
				Headers: []gwv1.HTTPHeaderMatch{{Name: "X-H", Value: "v"}},
			}},
			want: true,
		},
		{
			name: "multiple matches, one unsupported",
			matches: []gwv1.HTTPRouteMatch{
				{Path: &gwv1.HTTPPathMatch{Value: ptr.To("/ok")}},
				{Method: ptrMethod(gwv1.HTTPMethodPost)},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasUnsupportedMatchDimension(tt.matches))
		})
	}
}

func ptrPathType(pt gwv1.PathMatchType) *gwv1.PathMatchType { return &pt }
func ptrMethod(m gwv1.HTTPMethod) *gwv1.HTTPMethod          { return &m }

func TestIsWildcardHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"*.example.com", true},
		{"example.com", false},
		{"*", false},
		{"*.", false},
		{"*.a", true},
		{"foo.*.bar.com", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			assert.Equal(t, tt.want, isWildcardHost(tt.host))
		})
	}
}

func TestWildcardHostToRegex(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"*.example.com", `^[^.]+\.example\.com$`},
		{"*.foo.bar", `^[^.]+\.foo\.bar$`},
		{"*.a", `^[^.]+\.a$`},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			assert.Equal(t, tt.want, wildcardHostToRegex(tt.host))
		})
	}
}

func TestPolicyName(t *testing.T) {
	route := &gwv1.HTTPRoute{}
	route.UID = "abcdef12-dead-beef-0000-111122223333"

	t.Run("deterministic across calls", func(t *testing.T) {
		m := gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Value: ptr.To("/foo"), Type: ptrPathType(gwv1.PathMatchExact)}}
		n1 := policyName(route, 0, "example.com", m)
		n2 := policyName(route, 0, "example.com", m)
		assert.Equal(t, n1, n2)
	})
	t.Run("at most 50 chars", func(t *testing.T) {
		m := gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Value: ptr.To("/very/long/path/that/makes/the/name/exceed/fifty/chars")}}
		name := policyName(route, 99, "really.long.hostname.example.com", m)
		assert.LessOrEqual(t, len(name), 50)
	})
	t.Run("different ruleIdx produces different name", func(t *testing.T) {
		m := gwv1.HTTPRouteMatch{}
		n0 := policyName(route, 0, "", m)
		n1 := policyName(route, 1, "", m)
		assert.NotEqual(t, n0, n1)
	})
	t.Run("different host produces different name", func(t *testing.T) {
		m := gwv1.HTTPRouteMatch{}
		n0 := policyName(route, 0, "a.com", m)
		n1 := policyName(route, 0, "b.com", m)
		assert.NotEqual(t, n0, n1)
	})
}

func TestDedupPoliciesByName(t *testing.T) {
	t.Run("empty input returns empty", func(t *testing.T) {
		got := dedupPoliciesByName(nil)
		assert.Empty(t, got)
	})
	t.Run("no duplicates preserved and sorted", func(t *testing.T) {
		in := []v1alpha1.Policy{{Name: "b"}, {Name: "a"}}
		got := dedupPoliciesByName(in)
		assert.Equal(t, []v1alpha1.Policy{{Name: "a"}, {Name: "b"}}, got)
	})
	t.Run("duplicates dropped (first wins)", func(t *testing.T) {
		in := []v1alpha1.Policy{
			{Name: "x", Action: v2.PolicyActionREDIRECTTOPOOL},
			{Name: "y"},
			{Name: "x", Action: v2.PolicyActionREJECT}, // dup
		}
		got := dedupPoliciesByName(in)
		assert.Len(t, got, 2)
		found := false
		for _, p := range got {
			if p.Name == "x" {
				assert.Equal(t, v2.PolicyActionREDIRECTTOPOOL, p.Action)
				found = true
			}
		}
		assert.True(t, found)
	})
	t.Run("output sorted by name", func(t *testing.T) {
		in := []v1alpha1.Policy{{Name: "z"}, {Name: "m"}, {Name: "a"}}
		got := dedupPoliciesByName(in)
		assert.Equal(t, "a", got[0].Name)
		assert.Equal(t, "m", got[1].Name)
		assert.Equal(t, "z", got[2].Name)
	})
}

func TestBuildL7Rules(t *testing.T) {
	t.Run("empty host and nil path produces no rules", func(t *testing.T) {
		rules := buildL7Rules("", gwv1.HTTPRouteMatch{})
		assert.Empty(t, rules)
	})

	t.Run("literal host produces EQUALS hostname rule", func(t *testing.T) {
		rules := buildL7Rules("example.com", gwv1.HTTPRouteMatch{})
		assert.Len(t, rules, 1)
		assert.Equal(t, v2.PolicyRuleTypeHOSTNAME, rules[0].RuleType)
		assert.Equal(t, v2.PolicyCompareTypeEQUALS, rules[0].CompareType)
		assert.Equal(t, "example.com", rules[0].RuleValue)
	})

	t.Run("wildcard host produces REGEX hostname rule", func(t *testing.T) {
		rules := buildL7Rules("*.example.com", gwv1.HTTPRouteMatch{})
		assert.Len(t, rules, 1)
		assert.Equal(t, v2.PolicyRuleTypeHOSTNAME, rules[0].RuleType)
		assert.Equal(t, v2.PolicyCompareTypeREGEX, rules[0].CompareType)
		assert.Equal(t, `^[^.]+\.example\.com$`, rules[0].RuleValue)
	})

	t.Run("path exact produces EQUALS path rule", func(t *testing.T) {
		m := gwv1.HTTPRouteMatch{
			Path: &gwv1.HTTPPathMatch{
				Type:  ptrPathType(gwv1.PathMatchExact),
				Value: ptr.To("/exact"),
			},
		}
		rules := buildL7Rules("", m)
		assert.Len(t, rules, 1)
		assert.Equal(t, v2.PolicyRuleTypePATH, rules[0].RuleType)
		assert.Equal(t, v2.PolicyCompareTypeEQUALS, rules[0].CompareType)
		assert.Equal(t, "/exact", rules[0].RuleValue)
	})

	t.Run("path prefix produces STARTS_WITH path rule", func(t *testing.T) {
		m := gwv1.HTTPRouteMatch{
			Path: &gwv1.HTTPPathMatch{
				Type:  ptrPathType(gwv1.PathMatchPathPrefix),
				Value: ptr.To("/prefix"),
			},
		}
		rules := buildL7Rules("", m)
		assert.Len(t, rules, 1)
		assert.Equal(t, v2.PolicyCompareTypeSTARTSWITH, rules[0].CompareType)
	})

	t.Run("path regex produces REGEX path rule", func(t *testing.T) {
		m := gwv1.HTTPRouteMatch{
			Path: &gwv1.HTTPPathMatch{
				Type:  ptrPathType(gwv1.PathMatchRegularExpression),
				Value: ptr.To("^/api/.*"),
			},
		}
		rules := buildL7Rules("", m)
		assert.Len(t, rules, 1)
		assert.Equal(t, v2.PolicyCompareTypeREGEX, rules[0].CompareType)
	})

	t.Run("host + path produces two rules", func(t *testing.T) {
		m := gwv1.HTTPRouteMatch{
			Path: &gwv1.HTTPPathMatch{
				Type:  ptrPathType(gwv1.PathMatchExact),
				Value: ptr.To("/api"),
			},
		}
		rules := buildL7Rules("foo.com", m)
		assert.Len(t, rules, 2)
		assert.Equal(t, v2.PolicyRuleTypeHOSTNAME, rules[0].RuleType)
		assert.Equal(t, v2.PolicyRuleTypePATH, rules[1].RuleType)
	})

	t.Run("path with nil type defaults to STARTS_WITH", func(t *testing.T) {
		m := gwv1.HTTPRouteMatch{
			Path: &gwv1.HTTPPathMatch{Value: ptr.To("/any")},
		}
		rules := buildL7Rules("", m)
		assert.Len(t, rules, 1)
		assert.Equal(t, v2.PolicyCompareTypeSTARTSWITH, rules[0].CompareType)
	})
}

func TestApplyRequestRedirectFilter(t *testing.T) {
	t.Run("no filters returns false, no-op", func(t *testing.T) {
		p := &v1alpha1.Policy{Action: v2.PolicyActionREDIRECTTOPOOL}
		got := applyRequestRedirectFilter(p, nil)
		assert.False(t, got)
		assert.Equal(t, v2.PolicyActionREDIRECTTOPOOL, p.Action)
	})

	t.Run("non-redirect filter returns false", func(t *testing.T) {
		p := &v1alpha1.Policy{Action: v2.PolicyActionREDIRECTTOPOOL}
		filters := []gwv1.HTTPRouteFilter{{Type: gwv1.HTTPRouteFilterRequestHeaderModifier}}
		got := applyRequestRedirectFilter(p, filters)
		assert.False(t, got)
	})

	t.Run("redirect filter with nil RequestRedirect skipped", func(t *testing.T) {
		p := &v1alpha1.Policy{Action: v2.PolicyActionREDIRECTTOPOOL}
		filters := []gwv1.HTTPRouteFilter{{Type: gwv1.HTTPRouteFilterRequestRedirect, RequestRedirect: nil}}
		got := applyRequestRedirectFilter(p, filters)
		assert.False(t, got)
	})

	t.Run("redirect filter applied correctly", func(t *testing.T) {
		p := &v1alpha1.Policy{Action: v2.PolicyActionREDIRECTTOPOOL}
		host := gwv1.PreciseHostname("new.example.com")
		code := 301
		filters := []gwv1.HTTPRouteFilter{{
			Type: gwv1.HTTPRouteFilterRequestRedirect,
			RequestRedirect: &gwv1.HTTPRequestRedirectFilter{
				Scheme:     ptr.To("https"),
				Hostname:   &host,
				StatusCode: &code,
			},
		}}
		got := applyRequestRedirectFilter(p, filters)
		assert.True(t, got)
		assert.Equal(t, v2.PolicyActionREDIRECTTOURL, p.Action)
		assert.NotNil(t, p.RedirectUrl)
		assert.Equal(t, "https://new.example.com", *p.RedirectUrl)
		assert.Equal(t, int32(301), *p.RedirectHttpCode)
	})
}

func TestBuildRedirectURL(t *testing.T) {
	tests := []struct {
		name   string
		filter gwv1.HTTPRequestRedirectFilter
		want   string
	}{
		{
			name: "scheme+host",
			filter: gwv1.HTTPRequestRedirectFilter{
				Scheme:   ptr.To("https"),
				Hostname: ptrHostname("foo.com"),
			},
			want: "https://foo.com",
		},
		{
			name: "default scheme is https",
			filter: gwv1.HTTPRequestRedirectFilter{
				Hostname: ptrHostname("bar.com"),
			},
			want: "https://bar.com",
		},
		{
			name: "scheme+host+port",
			filter: gwv1.HTTPRequestRedirectFilter{
				Scheme:   ptr.To("http"),
				Hostname: ptrHostname("baz.com"),
				Port:     ptrPort(8080),
			},
			want: "http://baz.com:8080",
		},
		{
			name: "scheme+host+path",
			filter: gwv1.HTTPRequestRedirectFilter{
				Scheme:   ptr.To("https"),
				Hostname: ptrHostname("qux.com"),
				Path:     &gwv1.HTTPPathModifier{ReplaceFullPath: ptr.To("/new")},
			},
			want: "https://qux.com/new",
		},
		{
			name:   "empty filter",
			filter: gwv1.HTTPRequestRedirectFilter{},
			want:   "https://",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildRedirectURL(&tt.filter))
		})
	}
}

func ptrHostname(h string) *gwv1.PreciseHostname {
	v := gwv1.PreciseHostname(h)
	return &v
}

func ptrPort(p int) *gwv1.PortNumber {
	v := gwv1.PortNumber(p)
	return &v
}
