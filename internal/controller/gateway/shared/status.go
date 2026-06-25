package shared

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func SetCondition(conds *[]metav1.Condition, c metav1.Condition) {
	for i, existing := range *conds {
		if existing.Type == c.Type {
			if existing.Status == c.Status &&
				existing.Reason == c.Reason &&
				existing.Message == c.Message {
				return
			}
			(*conds)[i] = c
			return
		}
	}
	*conds = append(*conds, c)
}

func EnsureAncestor(ancestors *[]gwv1alpha2.PolicyAncestorStatus, ref gwv1alpha2.ParentReference, controllerName string, conditions []metav1.Condition) {
	for i, a := range *ancestors {
		if string(a.ControllerName) == controllerName && parentRefEqual(a.AncestorRef, ref) {
			(*ancestors)[i].Conditions = conditions
			return
		}
	}
	*ancestors = append(*ancestors, gwv1alpha2.PolicyAncestorStatus{
		AncestorRef:    ref,
		ControllerName: gwv1alpha2.GatewayController(controllerName),
		Conditions:     conditions,
	})
}

func parentRefEqual(a, b gwv1alpha2.ParentReference) bool {
	return derefGroup(a.Group) == derefGroup(b.Group) &&
		derefKind(a.Kind) == derefKind(b.Kind) &&
		a.Name == b.Name &&
		derefNS(a.Namespace) == derefNS(b.Namespace) &&
		derefSection(a.SectionName) == derefSection(b.SectionName)
}

func derefGroup(p *gwv1alpha2.Group) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func derefKind(p *gwv1alpha2.Kind) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func derefNS(p *gwv1alpha2.Namespace) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func derefSection(p *gwv1alpha2.SectionName) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
