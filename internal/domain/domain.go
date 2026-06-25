package domain

// TODO: refactor TargetType to use in LoadBalancer domain
type TargetType string

const (
	TargetTypeInstance TargetType = "instance"
	TargetTypeIP       TargetType = "ip"
)

// Finalizers
const (
	ServiceFinalizer    = "service.kubernetes.io/load-balancer-cleanup"
	IngressFinalizer    = "ingress.vngcloud.vn/resources"
	GlbFinalizer        = "glb.vngcloud.vn/resources"
	GlbcFinalizer       = "glbc.vngcloud.vn/resources"
	LbcFinalizer        = "lbc.vngcloud.vn/resources" // TODO: plan this finalizer name
	NsgFinalizer        = "nsg.vngcloud.vn/resources"
	VglbFinalizer       = "glb.vngcloud.vn/resources"
	ServiceGLBFinalizer = "glb.vks.vngcloud.vn/resources"
)

// Gateway API constants.
const (
	GatewayFinalizer   = "gateway.vks.vngcloud.vn/resources"
	HTTPRouteFinalizer = "gateway.vks.vngcloud.vn/route"

	OwnerKindGateway = "Gateway"

	// VKSResourceNamePrefix is the prefix every cloud-side resource the
	// VKS controllers create must start with — load balancers, listeners,
	// pools, policies, and member names. Matches the existing Ingress-style
	// vks_default_pool / vks_http_listener convention. The vngcloud LB API
	// limits names to [a-zA-Z0-9_.-]{5,50}.
	VKSResourceNamePrefix = "vks_"

	OwnerLabelGatewayUID = "gateway.vks.vngcloud.vn/owner-uid"
	OwnerLabelKind       = "vks.vngcloud.vn/owner-resource-kind"
)

// Annotations
const (
	SERVICE_ANNOTATION_PREFIX = "vks.vngcloud.vn"
	INGRESS_ANNOTATION_PREFIX = "vks.vngcloud.vn"
	VGLB_ANNOTATION_PREFIX    = "vks.vngcloud.vn"
	GLB_ANNOTATION_PREFIX     = "glb.vks.vngcloud.vn"
)

// CRD kind names (K8s strips TypeMeta from objects returned by Get; use these constants
// instead of obj.Kind when setting labels that must survive round-trips through the API server)
const (
	KindVngcloudGlobalLoadBalancer = "VngcloudGlobalLoadBalancer"
	KindService                    = "Service"
)

// Labels
const (
	LabelOwnerResourceKind = "vks.vngcloud.vn/owner-resource-kind"
	LabelOwnerResourceName = "vks.vngcloud.vn/owner-resource-name"
	LabelOwnerResourceUid  = "vks.vngcloud.vn/owner-resource-uid"

	// LabelNodeExcludeLB specifies that a node should not be used to create a Loadbalancer on
	// https://github.com/kubernetes/cloud-provider/blob/25867882d509131a6fdeaf812ceebfd0f19015dd/controllers/service/controller.go#L673
	LabelNodeExcludeLB = "node.kubernetes.io/exclude-from-external-load-balancers"
)

const (
	DEFAULT_LB_PREFIX_NAME = "vks" // "vks" is abbreviated of "cluster"

	DEFAULT_MEMBER_BACKUP_ROLE = false

	DEFAULT_PORTAL_NAME_LENGTH        = 50  // All the name must be less than 50 characters
	DEFAULT_PORTAL_DESCRIPTION_LENGTH = 255 // All the description must be less than 255 characters
	DEFAULT_MEMBER_WEIGHT             = 1
	DEFAULT_VLB_ID_PIECE_START_INDEX  = 8
	DEFAULT_VLB_ID_PIECE_LENGTH       = 8
	DEFAULT_HASH_NAME_LENGTH          = 5
	DEFAULT_NAME_DEFAULT_POOL         = "vks_default_pool"
	DEFAULT_HTTPS_LISTENER_NAME       = "vks_https_listener"
	DEFAULT_HTTP_LISTENER_NAME        = "vks_http_listener"

	VKS_CLUSTER_ID_PREFIX = "k8s-"
	VKS_CLUSTER_ID_LENGTH = 40

	// Tags
	VpcTagKey                = "vng.vpc.id"
	BillingTagKey            = "vng.billing.product"
	BillingTagValue          = "vks"
	ClusterTagKey            = "vng.vks.cluster.ids"
	ClusterTagValueSeparator = "/"

	DeprecatedClusterTagKey            = "vks-cluster-ids"
	DeprecatedClusterTagValueSeparator = "_"

	DefaultMaxConcurrentReconciles = 5
)
