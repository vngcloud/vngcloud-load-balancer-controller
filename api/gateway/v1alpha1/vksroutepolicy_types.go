package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vksroutepolicy,categories=gateway-api
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
// +kubebuilder:printcolumn:name="Targets",type=string,JSONPath=`.spec.targetRefs[*].name`
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type VKSRoutePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VKSRoutePolicySpec   `json:"spec,omitempty"`
	Status VKSRoutePolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type VKSRoutePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VKSRoutePolicy `json:"items"`
}

type VKSRoutePolicySpec struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	TargetRefs []LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`

	Actions  []VKSRuleAction `json:"actions,omitempty"`
	Position *int32          `json:"position,omitempty"`
}

type VKSRuleAction struct {
	// +kubebuilder:validation:Enum=Reject;Redirect
	Type     string             `json:"type"`
	Redirect *VKSRedirectAction `json:"redirect,omitempty"`
}

type VKSRedirectAction struct {
	URL             string `json:"url"`
	HTTPCode        *int32 `json:"httpCode,omitempty"`
	KeepQueryString *bool  `json:"keepQueryString,omitempty"`
}

type VKSRoutePolicyStatus struct {
	CommonStatus       `json:",inline"`
	CommonPolicyStatus `json:",inline"`
}

// Matches reports whether this policy targets the given PolicyTarget.
func (p *VKSRoutePolicy) Matches(t pkggw.PolicyTarget) bool {
	for _, ref := range p.Spec.TargetRefs {
		if pkggw.TargetRefMatchesWithSection(ref, p.Namespace, t) {
			return true
		}
	}
	return false
}

func init() {
	SchemeBuilder.Register(&VKSRoutePolicy{}, &VKSRoutePolicyList{})
}
