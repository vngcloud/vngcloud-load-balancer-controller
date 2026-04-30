/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"slices"

	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// Condition types for LoadBalancerConfig
const (
	// LBCConditionTypeReady indicates the LoadBalancerConfig is ready and fully reconciled
	LBCConditionTypeReady = "Ready"
)

// Condition reasons for LoadBalancerConfig
const (
	LBCReasonReconcileSuccess = "ReconcileSuccess"
	LBCReasonReconcileFailed  = "ReconcileFailed"
)

// InsertHeader defines a header to be inserted in requests
type InsertHeader struct {
	// HeaderName is the name of the header to insert
	// +required
	HeaderName string `json:"headerName"`

	// HeaderValue is the value of the header to insert
	// +required
	HeaderValue string `json:"headerValue"`
}

// L7Rule defines a Layer 7 rule for policies
type L7Rule struct {
	// CompareType is how to compare the rule value
	// +required
	CompareType loadbalancerv2.PolicyCompareType `json:"compareType"`

	// RuleValue is the value to compare against
	// +required
	RuleValue string `json:"ruleValue"`

	// RuleType is the type of rule
	// +required
	RuleType loadbalancerv2.PolicyRuleType `json:"ruleType"`
}

// Policy defines a policy configuration for application load balancer listeners
type Policy struct {
	// Name is the name of the policy
	// +required
	Name string `json:"name"`

	// Description is an optional description for the policy
	// +optional
	Description *string `json:"description,omitempty"`

	// RedirectPoolName is the name of the pool to redirect to
	// +optional
	RedirectPoolName *string `json:"redirectPoolName,omitempty"`

	// Action defines the action to take
	// +required
	Action loadbalancerv2.PolicyAction `json:"action"`

	// RedirectUrl is the URL to redirect to (for REDIRECT_TO_URL action)
	// +optional
	RedirectUrl *string `json:"redirectUrl,omitempty"`

	// RedirectHttpCode is the HTTP code to use for redirect
	// +optional
	RedirectHttpCode *int32 `json:"redirectHttpCode,omitempty"`

	// KeepQueryString determines if query string should be kept on redirect
	// +optional
	KeepQueryString *bool `json:"keepQueryString,omitempty"`

	// Position is the position/priority of the policy
	// +optional
	// +kubebuilder:validation:Minimum=1
	Position *int32 `json:"position,omitempty"`

	// L7Rules is the list of L7 rules for this policy
	// +optional
	// +listType=atomic
	L7Rules []L7Rule `json:"l7Rules,omitempty"`
}

// Pool defines a pool configuration for the load balancer
type Pool struct {
	// Name is the name of the pool
	// +required
	Name string `json:"name"`

	// Protocol is the protocol for the pool
	// +required
	Protocol loadbalancerv2.PoolProtocol `json:"protocol"`

	// Description is an optional description for the pool
	// +optional
	Description *string `json:"description,omitempty"`

	// Algorithm is the load balancing algorithm for the pool
	// +optional
	Algorithm *loadbalancerv2.PoolAlgorithm `json:"algorithm,omitempty"`

	// Stickiness enables sticky sessions for the pool
	// +optional
	Stickiness *bool `json:"stickiness,omitempty"`

	// TLSEncryption enables TLS encryption for the pool
	// +optional
	TLSEncryption *bool `json:"tlsEncryption,omitempty"`

	// HealthMonitor defines the health monitor configuration for the pool
	// +optional
	HealthMonitor PoolHealthMonitor `json:"healthMonitor,omitempty"`

	// Members is the list of members in the pool
	// +optional
	// +listType=atomic
	Members []PoolMember `json:"members,omitempty"`
}

type PoolHealthMonitor struct {
	// Protocol is the protocol used for health checks
	// +required
	Protocol loadbalancerv2.HealthCheckProtocol `json:"protocol"`

	// HealthyThreshold is the number of consecutive successful checks before marking healthy
	// +optional
	HealthyThreshold *int `json:"healthyThreshold"`

	// UnhealthyThreshold is the number of consecutive failed checks before marking unhealthy
	// +optional
	UnhealthyThreshold *int `json:"unhealthyThreshold"`

	// Interval is the time in seconds between each health check
	// +optional
	Interval *int `json:"interval"`

	// Timeout is the maximum time in seconds to wait for a response
	// +optional
	Timeout *int `json:"timeout"`

	// HealthCheckMethod specifies how the health check request is made (e.g., GET, TCP)
	// +optional
	HealthCheckMethod *loadbalancerv2.HealthCheckMethod `json:"healthCheckMethod,omitempty"`

	// HttpVersion defines which HTTP version to use for HTTP-based health checks
	// +optional
	HttpVersion *loadbalancerv2.HealthCheckHttpVersion `json:"httpVersion,omitempty"`

	// HealthCheckPath is the path used for HTTP health checks
	// +optional
	HealthCheckPath *string `json:"healthCheckPath,omitempty"`

	// DomainName is the hostname sent in the HTTP Host header
	// +optional
	DomainName *string `json:"domainName,omitempty"`

	// SuccessCode specifies which HTTP codes indicate a healthy response
	// +optional
	SuccessCode *string `json:"successCode,omitempty"`
}

// PoolMember defines a member of a load balancer pool
type PoolMember struct {
	// IP is the IP address of the pool member
	// +required
	IP string `json:"ip"`

	// Port is the port number of the pool member
	// +required
	Port int `json:"port"`

	// MonitorPort is the monitor port of the pool member
	// +required
	MonitorPort int `json:"monitorPort,omitempty"`

	// Name is an optional name for the pool member
	// +optional
	Name string `json:"name,omitempty"`

	// Weight is the weight of the pool member
	// +optional
	Weight *int `json:"weight,omitempty"`

	// Backup indicates if the member is a backup member
	// +optional
	Backup *bool `json:"backup,omitempty"`
}

// Equal compares two PoolMember for equality
func (a PoolMember) Equal(b PoolMember) bool {
	return a.IP == b.IP && a.Port == b.Port && a.MonitorPort == b.MonitorPort && a.Name == b.Name
}

// Listener defines a listener configuration for the load balancer
type Listener struct {
	// Name is the name of the listener
	// +required
	Name string `json:"name,omitempty"`

	// Description is an optional description for the listener
	// +optional
	Description *string `json:"description,omitempty"`

	// Protocol is the protocol for the listener
	// +required
	Protocol loadbalancerv2.ListenerProtocol `json:"protocol"`

	// ProtocolPort is the port number for the listener
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ProtocolPort int32 `json:"protocolPort"`

	// DefaultPoolName is the name of the default pool
	// +optional
	DefaultPoolName *string `json:"defaultPoolName,omitempty"`

	// TimeoutClient is the client timeout in seconds
	// +optional
	TimeoutClient *int32 `json:"timeoutClient,omitempty"`

	// TimeoutMember is the member timeout in seconds
	// +optional
	TimeoutMember *int32 `json:"timeoutMember,omitempty"`

	// TimeoutConnection is the connection timeout in seconds
	// +optional
	TimeoutConnection *int32 `json:"timeoutConnection,omitempty"`

	// AllowedCidrs defines the allowed CIDR blocks
	// +optional
	AllowedCidrs *string `json:"allowedCidrs,omitempty"`

	// InsertHeaders defines headers to insert into requests
	// +optional
	// +listType=atomic
	InsertHeaders []InsertHeader `json:"insertHeaders,omitempty"`

	// Policies is the list of policies for this listener (for L7)
	// +optional
	// +listType=map
	// +listMapKey=name
	Policies []Policy `json:"policies,omitempty"`

	// // AutoReorderPolicies enables automatic reordering of policies
	// // +optional
	// AutoReorderPolicies *bool `json:"autoReorderPolicies,omitempty"`

	// CertificateDefault is the default certificate for the listener (for L7)
	// +optional
	CertificateDefault *ListenerCertificate `json:"certificateDefault,omitempty"`

	// CertificateAuthorities is a list of certificate authorities for mutual TLS (for L7)
	// +optional
	// +listType=atomic
	CertificateAuthorities []ListenerCertificate `json:"certificateAuthorities,omitempty"`

	// ClientCertificateId is the client certificate Id for mutual TLS
	// +optional
	ClientCertificateId *string `json:"clientCertificateId,omitempty"`

	// SSLPolicy selects the listener's TLS protocol/cipher policy (vngcloud-defined).
	// Only honored on HTTPS / TLS-terminate listeners.
	// +optional
	SSLPolicy *string `json:"sslPolicy,omitempty"`

	// ALPNPolicy controls ALPN advertisement (e.g., "HTTP2Optional", "HTTP1Only").
	// Only honored on HTTPS / TLS-terminate listeners.
	// +optional
	ALPNPolicy *string `json:"alpnPolicy,omitempty"`
}

// ListenerCertificate defines a certificate associated with a listener, can be referenced by ID, name, or secret name. Must provide at least one.
type ListenerCertificate struct {
	// Id is the ID of the certificate
	// +optional
	Id *string `json:"id,omitempty"`

	// Name is the name of the certificate
	// +optional
	Name *string `json:"name,omitempty"`

	// SecretName is the name of the Kubernetes secret containing the certificate
	// +optional
	SecretName *string `json:"secretName,omitempty"`
}

// LoadBalancerConfigSpec defines the desired state of LoadBalancerConfig
type LoadBalancerConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// Type defines the type of load balancer (network or application)
	// +required
	Type loadbalancerv2.LoadBalancerType `json:"type"`

	// ClusterId is the ID of the cluster where the load balancer will be deployed. It helps in organizing resources.
	// +optional
	ClusterId *string `json:"clusterId,omitempty"`

	// General fields for all load balancers
	// LoadBalancerName is the name of the load balancer (only set via annotation)
	// +required
	LoadBalancerName string `json:"loadBalancerName,omitempty"`

	// SubnetId is the subnet id for the load balancer
	// +required
	SubnetId string `json:"subnetId,omitempty"`

	// VpcId is the VPC id for the load balancer
	// +required
	VpcId string `json:"vpcId,omitempty"`

	// BackendSubnetId is deprecated, use PrivateSubnetId instead
	// +optional
	BackendSubnetId *string `json:"backendSubnetId,omitempty"`

	// PrivateSubnetId is the private subnet id for the load balancer (for INTERVPC scheme)
	// This is the subnet where the LB's private interface resides
	// +optional
	PrivateSubnetId *string `json:"privateSubnetId,omitempty"`

	// PrivateZoneId is the zone of the client subnet for InterVPC load balancer
	// This is required for InterVPC scheme since the client subnet is in a different VPC
	// +optional
	PrivateZoneId *common.Zone `json:"privateZoneId,omitempty"`

	// ZoneId must be the zone where the load balancer will be created
	// +required
	ZoneId common.Zone `json:"zoneId,omitempty"`

	// PackageId is the size/package of the load balancer
	// +optional
	PackageId *string `json:"packageId,omitempty"`

	// Scheme defines if the load balancer is internal or external
	// +optional
	// +kubebuilder:validation:Enum=Internal;Internet;InterVPC
	Scheme *loadbalancerv2.LoadBalancerScheme `json:"scheme,omitempty"`

	// LoadBalancerId is managed by the controller
	// +optional
	LoadBalancerId *string `json:"loadBalancerId,omitempty"`

	// EnableAutoscale enables autoscaling for the load balancer
	// +optional
	EnableAutoscale *bool `json:"enableAutoscale,omitempty"`

	// Tags are key-value pairs for load balancer tagging
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// IsPOC indicates if this is a proof of concept deployment
	// +optional
	IsPoc *bool `json:"isPoc,omitempty"`

	// Listeners defines the array of listeners for the load balancer
	// +optional
	// +listType=map
	// +listMapKey=name
	Listeners []Listener `json:"listeners,omitempty"`

	// Pools defines the array of pools for the load balancer
	// +optional
	// +listType=map
	// +listMapKey=name
	Pools []Pool `json:"pools,omitempty"`

	// CreateCertificates defines certificates to be created
	// +optional
	// +listType=map
	// +listMapKey=secretName
	CreateCertificates []CreateCertificate `json:"createCertificates,omitempty"`

	// MergingMode controls how this LBC merges with another LBC when both
	// GatewayClass.parametersRef and Gateway.spec.infrastructure.parametersRef are set.
	// Only honored when this object is referenced by GatewayClass.parametersRef.
	// Defaults to PreferGateway.
	// +optional
	// +kubebuilder:validation:Enum=PreferGateway;PreferGatewayClass
	MergingMode *MergingMode `json:"mergingMode,omitempty"`
}

// MergingMode controls how a GatewayClass-level LoadBalancerConfig merges with a
// Gateway-level one. Only honored when this LBC is referenced by GatewayClass.parametersRef.
type MergingMode string

const (
	MergingModePreferGateway      MergingMode = "PreferGateway"
	MergingModePreferGatewayClass MergingMode = "PreferGatewayClass"
)

type CreateCertificate struct {
	// SecretName is the name of the Kubernetes secret containing the certificate
	// +required
	SecretName string `json:"secretName,omitempty"`
}

// LoadBalancerConfigStatus defines the observed state of LoadBalancerConfig.
type LoadBalancerConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ObservedGeneration reflects the generation of the most recently observed spec
	// +optional
	ObservedGeneration *int64 `json:"observedGeneration,omitempty"`

	// LastReconcileTime is the timestamp of the last reconciliation attempt
	// +optional
	LastReconcileTime *metav1.Time `json:"lastReconcileTime,omitempty"`

	// LastReconcileMessage contains a message from the last reconciliation
	// +optional
	LastReconcileMessage string `json:"lastReconcileMessage,omitempty"`

	// Address is the DNS name or IP address assigned to the load balancer
	// +optional
	Address *string `json:"address,omitempty"`

	// // UpdatedAt is the timestamp when the load balancer was last updated
	// // +optional
	// UpdatedAt *metav1.Time `json:"updatedAt,omitempty"`

	// LoadBalancerId is the actual ID of the load balancer in VNG Cloud
	// +optional
	LoadBalancerId *string `json:"loadBalancerId,omitempty"`

	// CreatedTags is the map of tags created on the load balancer
	// +optional
	CreatedTags map[string]string `json:"createdTags,omitempty"`

	// CreatedListeners is the list of created listener IDs
	// +optional
	// +listType=map
	// +listMapKey=name
	CreatedPools []CreatedPool `json:"createdPools,omitempty"`

	// CreatedListeners is the list of created listener IDs
	// +optional
	// +listType=map
	// +listMapKey=id
	CreatedListeners []CreatedListener `json:"createdListeners,omitempty"`

	// CreatedCertificates is the list of created certificate IDs
	// +optional
	// +listType=map
	// +listMapKey=id
	CreatedCertificates []CreatedCertificate `json:"createdCertificates,omitempty"`

	// // ManageDFPMembers indicates if the controller should manage DFP members
	// // +optional
	// ManageDFPMembers *bool `json:"manageDFPMembers,omitempty"`

	// Conditions represent the current state of the LoadBalancerConfig resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Ready": the resource is fully functional and ready to serve traffic
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type CreatedPool struct {
	// Id is the ID of the created pool
	// +required
	Id string `json:"id,omitempty"`

	// Name is the name of the created pool
	// +required
	Name string `json:"name,omitempty"`

	// CreatedMembers is the list of created member IDs
	// +optional
	// +listType=atomic
	CreatedMembers []PoolMember `json:"createdMembers,omitempty"`
}

// Equal compares two CreatedPool for equality (order-independent for members)
func (a CreatedPool) Equal(b CreatedPool) bool {
	if a.Id != b.Id || a.Name != b.Name {
		return false
	}
	if len(a.CreatedMembers) != len(b.CreatedMembers) {
		return false
	}
	for _, memberA := range a.CreatedMembers {
		if !slices.ContainsFunc(b.CreatedMembers, memberA.Equal) {
			return false
		}
	}
	return true
}

type CreatedListener struct {
	// Id is the ID of the created listener
	// +required
	Id string `json:"id,omitempty"`

	// Port is the port number of the created listener
	// +required
	Port int `json:"port,omitempty"`

	// CreatedPolicies is the list of created policy IDs
	// +optional
	// +listType=map
	// +listMapKey=id
	CreatedPolicies []CreatedPolicy `json:"createdPolicies,omitempty"`
}

// Equal compares two CreatedListener for equality (order-independent for policies)
func (a CreatedListener) Equal(b CreatedListener) bool {
	if a.Id != b.Id || a.Port != b.Port {
		return false
	}
	if len(a.CreatedPolicies) != len(b.CreatedPolicies) {
		return false
	}
	for _, policyA := range a.CreatedPolicies {
		if !slices.ContainsFunc(b.CreatedPolicies, policyA.Equal) {
			return false
		}
	}
	return true
}

type CreatedPolicy struct {
	// Id is the ID of the created policy
	// +required
	Id string `json:"id,omitempty"`
}

// Equal compares two CreatedPolicy for equality
func (a CreatedPolicy) Equal(b CreatedPolicy) bool {
	return a.Id == b.Id
}

type CreatedCertificate struct {
	// SecretName is the name of the Kubernetes secret for the certificate
	// +required
	SecretName string `json:"secretName,omitempty"`

	// ResourceVersion is the resource version of the secret at creation time
	// +required
	ResourceVersion string `json:"resourceVersion,omitempty"`

	// Id is the ID of the created certificate
	// +required
	Id string `json:"id,omitempty"`

	// CertificateName is the name of the created certificate
	// +optional
	CertificateName *string `json:"certificateName,omitempty"`
}

// Equal compares two CreatedCertificate for equality
func (a CreatedCertificate) Equal(b CreatedCertificate) bool {
	if a.Id != b.Id || a.SecretName != b.SecretName || a.ResourceVersion != b.ResourceVersion {
		return false
	}
	// Compare optional CertificateName
	if (a.CertificateName == nil) != (b.CertificateName == nil) {
		return false
	}
	if a.CertificateName != nil && *a.CertificateName != *b.CertificateName {
		return false
	}
	return true
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=lbc
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="LoadBalancer-Id",type="string",JSONPath=".status.loadBalancerId"
// +kubebuilder:printcolumn:name="Address",type="string",JSONPath=".status.address"
// +kubebuilder:printcolumn:name="Zone",type="string",JSONPath=".spec.zoneId"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LoadBalancerConfig is the Schema for the loadbalancerconfigs API
type LoadBalancerConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of LoadBalancerConfig
	// +required
	Spec LoadBalancerConfigSpec `json:"spec"`

	// status defines the observed state of LoadBalancerConfig
	// +optional
	Status LoadBalancerConfigStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// LoadBalancerConfigList contains a list of LoadBalancerConfig
type LoadBalancerConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LoadBalancerConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LoadBalancerConfig{}, &LoadBalancerConfigList{})
}
