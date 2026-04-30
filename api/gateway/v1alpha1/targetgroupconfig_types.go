/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// TargetReference points at a backend Service in the same namespace.
type TargetReference struct {
	// Group of the target. Defaults to "" (core).
	// +optional
	Group *string `json:"group,omitempty"`
	// Kind of the target. Defaults to "Service".
	// +optional
	Kind *string `json:"kind,omitempty"`
	// Name of the target.
	// +required
	Name string `json:"name"`
}

// TargetGroupProperties holds vngcloud-specific pool / health-check options.
type TargetGroupProperties struct {
	// TargetType: "instance" (NodePort-routed) or "ip" (pod-direct, requires Cilium native routing).
	// +kubebuilder:validation:Enum=instance;ip
	// +optional
	TargetType *string `json:"targetType,omitempty"`

	// PoolAlgorithm: ROUND_ROBIN, LEAST_CONNECTIONS, SOURCE_IP, etc. (vngcloud values).
	// +optional
	PoolAlgorithm *string `json:"poolAlgorithm,omitempty"`

	// EnableStickySession enables session persistence on the pool.
	// +optional
	EnableStickySession *bool `json:"enableStickySession,omitempty"`

	// EnableTLSEncryption: speak HTTPS to backend (pool TLS).
	// +optional
	EnableTLSEncryption *bool `json:"enableTLSEncryption,omitempty"`

	// EnableProxyProtocol: enables PROXY-protocol for L4/L7 backend.
	// +optional
	EnableProxyProtocol *bool `json:"enableProxyProtocol,omitempty"`

	// HealthCheck configures the pool's health monitor.
	// +optional
	HealthCheck *vksv1alpha1.PoolHealthMonitor `json:"healthCheck,omitempty"`

	// TargetNodeLabels restricts which nodes are pool members (instance-mode only).
	// +optional
	TargetNodeLabels map[string]string `json:"targetNodeLabels,omitempty"`

	// ManageDFPMembers controls whether the controller manages "default-forwarding-pool" members.
	// +optional
	ManageDFPMembers *bool `json:"manageDFPMembers,omitempty"`
}

// RouteIdentifier scopes a per-route override.
type RouteIdentifier struct {
	// Group of the route (e.g., "gateway.networking.k8s.io").
	// +required
	Group string `json:"group"`
	// Kind of the route (e.g., "HTTPRoute", "TCPRoute").
	// +required
	Kind string `json:"kind"`
	// Namespace of the route. Defaults to the TGC's namespace.
	// +optional
	Namespace *string `json:"namespace,omitempty"`
	// Name of the route.
	// +required
	Name string `json:"name"`
	// RuleName matches the route's spec.rules[].name when set.
	// +optional
	RuleName *string `json:"ruleName,omitempty"`
}

// RouteSpecificConfig overrides DefaultConfig for a specific route (and optional rule).
type RouteSpecificConfig struct {
	// RouteIdentifier scopes this override.
	// +required
	RouteIdentifier RouteIdentifier `json:"routeIdentifier"`
	// Config to apply for the matched route / rule.
	// +required
	Config TargetGroupProperties `json:"config"`
}

// TargetGroupConfigSpec defines the desired state of TargetGroupConfig.
type TargetGroupConfigSpec struct {
	// TargetReference points at the backend Service this configuration applies to.
	// +required
	TargetReference TargetReference `json:"targetReference"`

	// DefaultConfig applies when this TGC is selected via targetReference and no
	// routeConfiguration overrides match the active route.
	// +required
	DefaultConfig TargetGroupProperties `json:"defaultConfig"`

	// RouteConfigurations carries per-route overrides selected by route GVK + name (and
	// optional rule name). Highest specificity wins (route+rule > route > default).
	// +optional
	RouteConfigurations []RouteSpecificConfig `json:"routeConfigurations,omitempty"`
}

// TargetGroupConfigStatus defines the observed state of TargetGroupConfig.
type TargetGroupConfigStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tgc

// TargetGroupConfig is the Schema for the targetgroupconfigs API.
type TargetGroupConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TargetGroupConfigSpec   `json:"spec,omitempty"`
	Status TargetGroupConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TargetGroupConfigList contains a list of TargetGroupConfig.
type TargetGroupConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TargetGroupConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TargetGroupConfig{}, &TargetGroupConfigList{})
}
