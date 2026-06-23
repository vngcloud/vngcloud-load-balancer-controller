package repository

import (
	"context"

	entityv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	global "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/glb/v1"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/inter"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	networkv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/network/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

type VngCloudRepository interface {
	// Metadata
	GetUserId() int

	// Load Balancer
	ListLoadBalancers(ctx context.Context, tags []string) (*entityv2.ListLoadBalancers, error)
	GetLoadBalancerByID(ctx context.Context, lbID string) (*entityv2.LoadBalancer, error)
	GetLoadBalancerByName(ctx context.Context, name string) (*entityv2.LoadBalancer, error)
	CreateLoadBalancer(ctx context.Context, lbOptions loadbalancerv2.ICreateLoadBalancerRequest) (*entityv2.LoadBalancer, error)
	DeleteLoadBalancer(ctx context.Context, lbID string) error
	ResizeLoadBalancer(ctx context.Context, lbID, packageID string) error
	WaitForLBActive(ctx context.Context, lbID string) (*entityv2.LoadBalancer, error)
	CreateInterLoadBalancer(ctx context.Context, lbOptions inter.ICreateLoadBalancerRequest) (*entityv2.LoadBalancer, error)

	// Load Balancer Package
	ListLoadBalancerPackageByZone(ctx context.Context, zone common.Zone) (*entityv2.ListLoadBalancerPackages, error)

	// Tags
	ListTags(ctx context.Context, resourceID string) (*entityv2.ListTags, error)
	// overwrites the tags of the resource
	CreateTags(ctx context.Context, resourceID string, tags map[string]string) error
	// adding or updating the tags to the resource
	UpdateTags(ctx context.Context, resourceID string, tags map[string]string) error

	// Security Group
	ListSecurityGroups(ctx context.Context) (*entityv2.ListSecgroups, error)
	// should check if server is not active yet
	UpdateSecGroupsOfServer(ctx context.Context, instanceID string, secgroups []string) (*entityv2.Server, error)
	GetSecurityGroup(ctx context.Context, secgroupID string) (*entityv2.Secgroup, error)
	DeleteSecurityGroup(ctx context.Context, secgroupID string) error
	CreateSecurityGroup(ctx context.Context, name string, description string) (*entityv2.Secgroup, error)

	CreateSecurityGroupRule(ctx context.Context, secgroupID string, opts networkv2.ICreateSecgroupRuleRequest) (*entityv2.SecgroupRule, error)
	DeleteSecurityGroupRule(ctx context.Context, secgroupID string, ruleID string) error
	ListSecurityGroupRules(ctx context.Context, secgroupID string) (*entityv2.ListSecgroupRules, error)

	// Subnet
	GetSubnetByID(ctx context.Context, networkID, subnetID string) (*entityv2.Subnet, error)

	// Server
	GetServerByID(ctx context.Context, serverID string) (*entityv2.Server, error)
	GetServerNetworkInfo(ctx context.Context, serverID string) (zoneID common.Zone, networkId, subnetID, subnetCIDR string, err error)
	WaitForServerActive(ctx context.Context, serverID string) error
	ListServerBySecgroupID(ctx context.Context, secgroupID string) (*entityv2.ListServers, error)

	// Pool
	CreatePool(ctx context.Context, lbID string, opt loadbalancerv2.ICreatePoolRequest) (*entityv2.Pool, error)
	ListPool(ctx context.Context, lbID string) (*entityv2.ListPools, error)
	UpdatePoolMembers(ctx context.Context, lbID, poolID string, members loadbalancerv2.IUpdatePoolMembersRequest) error
	GetPoolMembers(ctx context.Context, lbID, poolID string) (*entityv2.ListMembers, error)
	DeletePool(ctx context.Context, lbID, poolID string) error
	UpdatePool(ctx context.Context, lbID, poolID string, opt loadbalancerv2.IUpdatePoolRequest) error
	GetPoolHealthMonitorById(ctx context.Context, lbID, poolID string) (*entityv2.HealthMonitor, error)

	// Listener
	CreateListener(ctx context.Context, lbID string, opt loadbalancerv2.ICreateListenerRequest) (*entityv2.Listener, error)
	ListListenerOfLB(ctx context.Context, lbID string) (*entityv2.ListListeners, error)
	GetListenerById(ctx context.Context, lbID, listenerID string) (*entityv2.Listener, error)
	DeleteListener(ctx context.Context, lbID, listenerID string) error
	UpdateListener(ctx context.Context, lbID, listenerID string, opt loadbalancerv2.IUpdateListenerRequest) error

	// Policy
	CreatePolicy(ctx context.Context, lbID, listenerID string, opt loadbalancerv2.ICreatePolicyRequest) (*entityv2.Policy, error)
	ListPolicyOfListener(ctx context.Context, lbID, listenerID string) (*entityv2.ListPolicies, error)
	UpdatePolicy(ctx context.Context, lbID, listenerID, policyID string, opt loadbalancerv2.IUpdatePolicyRequest) error
	DeletePolicy(ctx context.Context, lbID, listenerID, policyID string) error
	ReorderPolicies(ctx context.Context, lbID, listenerID string, policyIDs []string) error

	// Certificate
	ListCertificates(ctx context.Context) (*entityv2.ListCertificates, error)
	GetCertificateByID(ctx context.Context, certID string) (*entityv2.Certificate, error)
	ImportCertificate(ctx context.Context, opt loadbalancerv2.ICreateCertificateRequest) (*entityv2.Certificate, error)
	DeleteCertificate(ctx context.Context, certID string) error

	// Global Load Balancer
	ListGlobalPackages(ctx context.Context) (*entityv2.ListGlobalPackages, error)
	ListGlobalLoadBalancers(ctx context.Context, tags []string) (*entityv2.ListGlobalLoadBalancers, error)
	GetGlobalLoadBalancerByID(ctx context.Context, glbID string) (*entityv2.GlobalLoadBalancer, error)
	GetGlobalLoadBalancerByName(ctx context.Context, glbID string) (*entityv2.GlobalLoadBalancer, error)
	CreateGlobalLoadBalancer(ctx context.Context, glbOptions global.ICreateGlobalLoadBalancerRequest) (*entityv2.GlobalLoadBalancer, error)
	DeleteGlobalLoadBalancer(ctx context.Context, glbID string) error
	WaitGlobalLoadBalancerActive(ctx context.Context, glbID string) (*entityv2.GlobalLoadBalancer, error)

	ListGlobalPools(ctx context.Context, glbID string) (*entityv2.ListGlobalPools, error)
	CreateGlobalPool(ctx context.Context, glbID string, opt global.ICreateGlobalPoolRequest) (*entityv2.GlobalPool, error)
	DeleteGlobalPool(ctx context.Context, glbID, poolID string) error
	UpdateGlobalPool(ctx context.Context, glbID, poolID string, opt global.IUpdateGlobalPoolRequest) error
	ListGlobalPoolMembers(ctx context.Context, glbID, poolID string) (*entityv2.ListGlobalPoolMembers, error)
	PatchGlobalPoolMembers(ctx context.Context, glbID, poolID string, opt global.IPatchGlobalPoolMembersRequest) error

	ListGlobalListeners(ctx context.Context, glbID string) (*entityv2.ListGlobalListeners, error)
	GetGlobalListener(ctx context.Context, glbID, listenerID string) (*entityv2.GlobalListener, error)
	CreateGlobalListener(ctx context.Context, glbID string, opt global.ICreateGlobalListenerRequest) (*entityv2.GlobalListener, error)
	DeleteGlobalListener(ctx context.Context, glbID, listenerID string) error
	UpdateGlobalListener(ctx context.Context, glbID, listenerID string, opt global.IUpdateGlobalListenerRequest) error
}

type K8sRepository interface {
	// Client returns the underlying controller-runtime client. Use cases
	// need it to construct k8sbatch.Batcher instances; the repo's typed
	// methods remain the preferred API for individual operations.
	Client() client.Client

	GetService(ctx context.Context, n types.NamespacedName) (*corev1.Service, error)
	UpdateServiceStatusAddress(ctx context.Context, n types.NamespacedName, address string) error

	ListNode(ctx context.Context, list *corev1.NodeList, opts ...client.ListOption) error
	// List(ctx context.Context, list ObjectList, opts ...ListOption) error

	GetLoadBalancerConfig(ctx context.Context, n types.NamespacedName) (*v1alpha1.LoadBalancerConfig, error)
	CreateLoadBalancerConfig(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig, opts ...client.CreateOption) error
	DeleteLoadBalancerConfig(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig) error
	PatchLoadBalancerConfig(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig, patch client.Patch, opts ...client.PatchOption) error
	UpdateLoadBalancerConfig(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig, opts ...client.UpdateOption) error
	PatchMutateStatusLoadBalancerConfig(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig, mutateFunc func(ctx context.Context, obj *v1alpha1.LoadBalancerConfig) bool) error
	ListLoadBalancerConfig(ctx context.Context, list *v1alpha1.LoadBalancerConfigList, opts ...client.ListOption) error

	GetNodeSecurityGroup(ctx context.Context, n types.NamespacedName) (*v1alpha1.NodeSecurityGroup, error)
	ListNodeSecurityGroup(ctx context.Context, list *v1alpha1.NodeSecurityGroupList, opts ...client.ListOption) error
	CreateNodeSecurityGroup(ctx context.Context, nsg *v1alpha1.NodeSecurityGroup, opts ...client.CreateOption) error
	DeleteNodeSecurityGroup(ctx context.Context, nsg *v1alpha1.NodeSecurityGroup) error
	PatchNodeSecurityGroup(ctx context.Context, nsg *v1alpha1.NodeSecurityGroup, patch client.Patch, opts ...client.PatchOption) error
	PatchMutateStatusNodeSecurityGroup(ctx context.Context, nsg *v1alpha1.NodeSecurityGroup, mutateFunc func(ctx context.Context, obj *v1alpha1.NodeSecurityGroup) bool) error

	GetIngress(ctx context.Context, n types.NamespacedName) (*networkingv1.Ingress, error)
	UpdateIngressStatusAddress(ctx context.Context, n types.NamespacedName, address string) error

	GetSecret(ctx context.Context, n types.NamespacedName) (*corev1.Secret, error)

	GetGlobalLoadBalancerConfig(ctx context.Context, n types.NamespacedName) (*v1alpha1.GlobalLoadBalancerConfig, error)
	ListGlobalLoadBalancerConfig(ctx context.Context, list *v1alpha1.GlobalLoadBalancerConfigList, opts ...client.ListOption) error
	CreateGlobalLoadBalancerConfig(ctx context.Context, glbc *v1alpha1.GlobalLoadBalancerConfig, opts ...client.CreateOption) error
	DeleteGlobalLoadBalancerConfig(ctx context.Context, glbc *v1alpha1.GlobalLoadBalancerConfig) error
	PatchGlobalLoadBalancerConfig(ctx context.Context, glbc *v1alpha1.GlobalLoadBalancerConfig, patch client.Patch, opts ...client.PatchOption) error
	PatchMutateStatusGlobalLoadBalancerConfig(ctx context.Context, glbc *v1alpha1.GlobalLoadBalancerConfig, mutateFunc func(ctx context.Context, obj *v1alpha1.GlobalLoadBalancerConfig) bool) error

	GetVngcloudGlobalLoadBalancer(ctx context.Context, n types.NamespacedName) (*v1alpha1.VngcloudGlobalLoadBalancer, error)
	PatchMutateStatusVngcloudGlobalLoadBalancer(ctx context.Context, vglb *v1alpha1.VngcloudGlobalLoadBalancer, mutateFunc func(ctx context.Context, obj *v1alpha1.VngcloudGlobalLoadBalancer) bool) error
}
