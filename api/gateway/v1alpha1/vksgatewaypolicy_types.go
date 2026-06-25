package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vksgwpolicy,categories=gateway-api
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
// +kubebuilder:printcolumn:name="Targets",type=string,JSONPath=`.spec.targetRefs[*].name`
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type VKSGatewayPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VKSGatewayPolicySpec   `json:"spec,omitempty"`
	Status VKSGatewayPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type VKSGatewayPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VKSGatewayPolicy `json:"items"`
}

type VKSGatewayPolicySpec struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	TargetRefs []LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`

	AllowedCIDRs      []string          `json:"allowedCidrs,omitempty"`
	InsertHeaders     map[string]string `json:"insertHeaders,omitempty"`
	TimeoutClient     *metav1.Duration  `json:"timeoutClient,omitempty"`
	TimeoutMember     *metav1.Duration  `json:"timeoutMember,omitempty"`
	TimeoutConnection *metav1.Duration  `json:"timeoutConnection,omitempty"`

	CertificateIDs      []string `json:"certificateIds,omitempty"`
	ClientCertificateID *string  `json:"clientCertificateId,omitempty"`

	LoadBalancerSpec *VKSLoadBalancerSpec `json:"loadBalancerSpec,omitempty"`
}

type VKSLoadBalancerSpec struct {
	// +kubebuilder:validation:Enum=Internet;Internal;InterVPC
	Scheme         *string           `json:"scheme,omitempty"`
	PackageID      *string           `json:"packageId,omitempty"`
	SubnetID       *string           `json:"subnetId,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	LoadBalancerID *string           `json:"loadBalancerId,omitempty"`

	// LoadBalancerName sets a custom name for the cloud LB at creation time.
	// Maps to LBC.Spec.LoadBalancerName — same as the Ingress
	// "load-balancer-name" annotation. Written only when the LBC is first
	// created (the cloud name is then mirrored back into Status); unset = a
	// controller-generated default name.
	LoadBalancerName *string `json:"loadBalancerName,omitempty"`

	// PreferZoneID pins the LB to a specific availability zone. The controller
	// resolves a subnet in that zone from a cluster node. Maps to the same
	// zone/subnet selection the Ingress "prefer-zone-id" annotation drives.
	// Lower precedence than LoadBalancerID (adoption) and SubnetID.
	PreferZoneID *string `json:"preferZoneId,omitempty"`

	// PrivateSubnetID overrides the LB's private subnet (used by the
	// InterVPC scheme). Maps to LBC.Spec.PrivateSubnetId — same field the
	// Ingress controller's "private-subnet-id" annotation writes.
	PrivateSubnetID *string `json:"privateSubnetId,omitempty"`

	// PrivateZoneID is the zone of the client subnet, required by the
	// InterVPC scheme since the client subnet lives in a different VPC.
	// Maps to LBC.Spec.PrivateZoneId — same as the Service controller's
	// "private-zone-id" annotation.
	PrivateZoneID *string `json:"privateZoneId,omitempty"`

	// EnableAutoscale toggles cloud-side autoscaling on the LB. Maps to
	// LBC.Spec.EnableAutoscale — same as the Ingress "enable-autoscale"
	// annotation. Unset = cloud default (off).
	EnableAutoscale *bool `json:"enableAutoscale,omitempty"`

	// IsPOC marks the LB as a POC tier. Maps to LBC.Spec.IsPoc — same as
	// the Ingress "is-poc" annotation. Unset = production tier.
	IsPOC *bool `json:"isPOC,omitempty"`
}

type VKSGatewayPolicyStatus struct {
	CommonStatus       `json:",inline"`
	CommonPolicyStatus `json:",inline"`
}

// Matches reports whether this policy targets the given PolicyTarget.
func (p *VKSGatewayPolicy) Matches(t pkggw.PolicyTarget) bool {
	for _, ref := range p.Spec.TargetRefs {
		if pkggw.TargetRefMatchesWithSection(ref, p.Namespace, t) {
			return true
		}
	}
	return false
}

func init() {
	SchemeBuilder.Register(&VKSGatewayPolicy{}, &VKSGatewayPolicyList{})
}
