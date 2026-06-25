package gateway

import (
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// PolicyTarget identifies a candidate object a Direct policy may attach to.
type PolicyTarget struct {
	Group       string
	Kind        string
	Namespace   string
	Name        string
	SectionName string
}

// TargetRefMatches reports whether a LocalPolicyTargetReference matches the given target.
// The policy and the target must be in the same namespace (Direct attachment).
func TargetRefMatches(ref gwv1alpha2.LocalPolicyTargetReference, policyNamespace string, t PolicyTarget) bool {
	if string(ref.Group) != t.Group {
		return false
	}
	if string(ref.Kind) != t.Kind {
		return false
	}
	if string(ref.Name) != t.Name {
		return false
	}
	return policyNamespace == t.Namespace
}

// TargetRefMatchesWithSection compares a LocalPolicyTargetReferenceWithSectionName against a target.
// SectionName matches when both are empty, both equal, or the target's section is empty.
func TargetRefMatchesWithSection(
	ref gwv1alpha2.LocalPolicyTargetReferenceWithSectionName,
	policyNamespace string,
	t PolicyTarget,
) bool {
	base := gwv1alpha2.LocalPolicyTargetReference{Group: ref.Group, Kind: ref.Kind, Name: ref.Name}
	if !TargetRefMatches(base, policyNamespace, t) {
		return false
	}
	refSection := ""
	if ref.SectionName != nil {
		refSection = string(*ref.SectionName)
	}
	return refSection == t.SectionName
}
