package shared

import (
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// MatchSpecificity returns a sortable specificity score for a Gateway-API HTTPRouteMatch.
// Higher is more specific. Encoding (high → low):
//
//	bit 24..31 : path-type weight (Exact=4, RegularExpression=3, PathPrefix=2, ImplementationSpecific/none=1)
//	bit 16..23 : path length (capped at 255)
//	bit  8..15 : header-match count (capped at 255)
//	bit  0..7  : query-param-match count (capped at 255)
func MatchSpecificity(m gwv1.HTTPRouteMatch) uint64 {
	var s uint64
	if m.Path != nil {
		switch t := m.Path.Type; {
		case t != nil && *t == gwv1.PathMatchExact:
			s |= 4 << 24
		case t != nil && *t == gwv1.PathMatchRegularExpression:
			s |= 3 << 24
		case t != nil && *t == gwv1.PathMatchPathPrefix:
			s |= 2 << 24
		default:
			s |= 1 << 24
		}
		if m.Path.Value != nil {
			n := uint64(len(*m.Path.Value))
			if n > 255 {
				n = 255
			}
			s |= n << 16
		}
	}
	s |= uint64(capByte(len(m.Headers))) << 8
	s |= uint64(capByte(len(m.QueryParams)))
	return s
}

func capByte(n int) int {
	if n > 255 {
		return 255
	}
	return n
}
