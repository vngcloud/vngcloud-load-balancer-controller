package consts

import (
	"time"
)

const (

	// High enough QPS to fit all expected use cases. QPS=0 is not set here, because
	// client code is overriding it.
	DefaultQPS = 1e6
	// High enough Burst to fit all expected use cases. Burst=0 is not set here, because
	// client code is overriding it.
	DefaultBurst = 1e6

	MaxRetries = 5

	// IngressKey picks a specific "class" for the Ingress.
	// The controller only processes Ingresses with this annotation either
	// unset, or set to either the configured value or the empty string.
	IngressKey = "kubernetes.io/ingress.class"
)

const (
	WaitLoadbalancerInitDelay   = 5 * time.Second
	WaitLoadbalancerFactor      = 1.2
	WaitLoadbalancerActiveSteps = 30
	WaitLoadbalancerDeleteSteps = 12
)

const (
	PROVIDER_NAME               = "vngcloud"
	ACTIVE_LOADBALANCER_STATUS  = "ACTIVE"
	CREATED_LOADBALANCER_STATUS = "CREATED"
	ERROR_LOADBALANCER_STATUS   = "ERROR"
)

const (
	FleetServiceNameLabel      = "fleet.vngcloud.vn/service-name"
	FleetServiceNamespaceLabel = "fleet.vngcloud.vn/service-namespace"
	FleetIDLabel               = "fleet.vngcloud.vn/fleet-id"
	FleetServiceIDLabel        = "fleet.vngcloud.vn/fleet-service-id"
	FleetClusterIDLabel        = "fleet.vngcloud.vn/cluster-id"

	ConfigClusterIdAnnotation = "fleet.vngcloud.vn/config-cluster-id"
)

const (
	GatewayClassControllerNameALB = "gateway.vks.vngcloud.vn/alb"
	GatewayClassControllerNameNLB = "gateway.vks.vngcloud.vn/nlb"
	GatewayClassNameALB           = "vngcloud-alb"
	GatewayClassNameNLB           = "vngcloud-nlb"
)
