package shared

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type RankedMatch struct {
	Match          gwv1.HTTPRouteMatch
	RouteCreated   metav1.Time
	RouteNamespace string
	RouteName      string
}

// ByMatchSpecificity orders RankedMatches per the Gateway-API spec's match
// precedence: Exact > Regex > Prefix paths, longer paths beat shorter,
// more headers beat fewer, more query params beat fewer, older route
// creation timestamp wins, then alphabetical "{namespace}/{name}" — the
// final tiebreaker the Gateway-API conformance spec requires when two
// routes were applied in the same metav1.Time-second window. Without it,
// stable-sort falls through to input order, which depends on the cache's
// non-deterministic map iteration and produces a Position-flip reconcile
// loop.
type ByMatchSpecificity []RankedMatch

func (s ByMatchSpecificity) Len() int      { return len(s) }
func (s ByMatchSpecificity) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s ByMatchSpecificity) Less(i, j int) bool {
	a, b := s[i], s[j]
	if r := pathRank(a.Match.Path) - pathRank(b.Match.Path); r != 0 {
		return r < 0
	}
	if pl, pr := pathLen(a.Match.Path), pathLen(b.Match.Path); pl != pr {
		return pl > pr
	}
	if hi, hj := len(a.Match.Headers), len(b.Match.Headers); hi != hj {
		return hi > hj
	}
	if qi, qj := len(a.Match.QueryParams), len(b.Match.QueryParams); qi != qj {
		return qi > qj
	}
	if !a.RouteCreated.Equal(&b.RouteCreated) {
		return a.RouteCreated.Before(&b.RouteCreated)
	}
	if a.RouteNamespace != b.RouteNamespace {
		return a.RouteNamespace < b.RouteNamespace
	}
	return a.RouteName < b.RouteName
}

func pathRank(p *gwv1.HTTPPathMatch) int {
	if p == nil || p.Type == nil {
		return 99
	}
	switch *p.Type {
	case gwv1.PathMatchExact:
		return 0
	case gwv1.PathMatchRegularExpression:
		return 1
	case gwv1.PathMatchPathPrefix:
		return 2
	}
	return 99
}

func pathLen(p *gwv1.HTTPPathMatch) int {
	if p == nil || p.Value == nil {
		return 0
	}
	return len(*p.Value)
}
