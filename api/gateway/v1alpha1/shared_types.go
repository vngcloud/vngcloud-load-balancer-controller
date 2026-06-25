package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// LocalPolicyTargetReference is re-exported for convenience.
// +kubebuilder:object:generate=false
type LocalPolicyTargetReference = gwv1alpha2.LocalPolicyTargetReference

// LocalPolicyTargetReferenceWithSectionName is re-exported for convenience.
// +kubebuilder:object:generate=false
type LocalPolicyTargetReferenceWithSectionName = gwv1alpha2.LocalPolicyTargetReferenceWithSectionName

// PolicyAncestorStatus is re-exported for convenience.
// +kubebuilder:object:generate=false
type PolicyAncestorStatus = gwv1alpha2.PolicyAncestorStatus

// CommonPolicyStatus is the embedded ancestor-status block carried by every VKS policy CRD.
type CommonPolicyStatus struct {
	// Ancestors records reconcile status per controller acting on this policy.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=16
	Ancestors []PolicyAncestorStatus `json:"ancestors,omitempty"`
}

// PolicyConditionType collects the standard reasons used across all four VKS policy CRDs.
const (
	PolicyConditionAccepted   = "Accepted"
	PolicyConditionProgrammed = "Programmed"

	PolicyReasonAccepted          = "Accepted"
	PolicyReasonConflicted        = "Conflicted"
	PolicyReasonInvalid           = "Invalid"
	PolicyReasonTargetNotFound    = "TargetNotFound"
	PolicyReasonNoReadyController = "NoReadyController"
	PolicyReasonProgrammed        = "Programmed"
	PolicyReasonPending           = "Pending"
)

// CommonStatus is the common subset embedded into each policy's Status.
type CommonStatus struct {
	Conditions         []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
}
