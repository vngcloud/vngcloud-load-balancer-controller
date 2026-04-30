package shared

import (
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// RefRequest captures a cross-namespace reference query against the ReferenceGrant set.
type RefRequest struct {
	FromGroup, FromKind, FromNS string
	ToGroup, ToKind, ToNS, ToName string
}

// RefGrantAllowed returns true if the given reference is permitted. Same-namespace
// references are always allowed; cross-namespace requires an applicable ReferenceGrant.
func RefGrantAllowed(r RefRequest, grants []*gwv1beta1.ReferenceGrant) bool {
	if r.FromNS == r.ToNS {
		return true
	}
	for _, g := range grants {
		if g.Namespace != r.ToNS {
			continue
		}
		if !grantMatchesFrom(g, r) {
			continue
		}
		if grantMatchesTo(g, r) {
			return true
		}
	}
	return false
}

func grantMatchesFrom(g *gwv1beta1.ReferenceGrant, r RefRequest) bool {
	for _, f := range g.Spec.From {
		if string(f.Group) == r.FromGroup && string(f.Kind) == r.FromKind && string(f.Namespace) == r.FromNS {
			return true
		}
	}
	return false
}

func grantMatchesTo(g *gwv1beta1.ReferenceGrant, r RefRequest) bool {
	for _, to := range g.Spec.To {
		if string(to.Group) != r.ToGroup || string(to.Kind) != r.ToKind {
			continue
		}
		if to.Name == nil {
			return true
		}
		if string(*to.Name) == r.ToName {
			return true
		}
	}
	return false
}
