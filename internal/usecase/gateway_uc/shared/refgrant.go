package shared

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// Ref identifies one side of a cross-namespace reference.
type Ref struct {
	Group, Kind, Namespace, Name string
}

// RefGrantAllowed reports whether `from` is permitted to reference `to`.
func RefGrantAllowed(ctx context.Context, c client.Client, to, from Ref) (bool, error) {
	if to.Namespace == from.Namespace {
		return true, nil
	}
	var grants gwv1beta1.ReferenceGrantList
	if err := c.List(ctx, &grants, client.InNamespace(to.Namespace)); err != nil {
		return false, err
	}
	for _, g := range grants.Items {
		if !grantFromMatches(g.Spec.From, from) {
			continue
		}
		if !grantToMatches(g.Spec.To, to) {
			continue
		}
		return true, nil
	}
	return false, nil
}

func grantFromMatches(froms []gwv1beta1.ReferenceGrantFrom, f Ref) bool {
	for _, x := range froms {
		if string(x.Group) == f.Group && string(x.Kind) == f.Kind && string(x.Namespace) == f.Namespace {
			return true
		}
	}
	return false
}

func grantToMatches(tos []gwv1beta1.ReferenceGrantTo, t Ref) bool {
	for _, x := range tos {
		if string(x.Group) != t.Group || string(x.Kind) != t.Kind {
			continue
		}
		if x.Name == nil || string(*x.Name) == t.Name {
			return true
		}
	}
	return false
}
