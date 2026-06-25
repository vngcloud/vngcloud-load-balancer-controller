package alb_gateway_uc

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

const testLoadBalancerID = "lb-existing"

// newTaskAndMocks builds a task exposing its repository mocks so tests can set
// expectations on the VNGCloud / K8s repos (needed for subnet/zone resolution).
func newTaskAndMocks(t *testing.T, gw *gwv1.Gateway) (*defaultGatewayBuildTask, *repository.MockK8sRepository, *repository.MockVngCloudRepository) {
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{
		k8sRepo:           mockK8s,
		vngcloudRepo:      mockVng,
		k8sClient:         fake.NewClientBuilder().WithScheme(newTestScheme()).Build(),
		defaultZone:       "HCM03-1C",
		defaultNetworkId:  "net-1",
		defaultSubnetId:   "subnet-1",
		defaultSubnetCIDR: "10.0.0.0/24",
		clusterId:         "cluster-1",
	}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("cluster-1", "gateway", gw.Namespace, gw.Name),
	}
	return task, mockK8s, mockVng
}

func gwPolicyWithLBSpec(lb *gwv1alpha1.VKSLoadBalancerSpec) *gwv1alpha1.VKSGatewayPolicy {
	return &gwv1alpha1.VKSGatewayPolicy{Spec: gwv1alpha1.VKSGatewayPolicySpec{LoadBalancerSpec: lb}}
}

// --- resolveSubnetAndZone: adoption (load-balancer-id) ---

func TestResolveSubnetAndZone_AdoptByLoadBalancerID(t *testing.T) {
	task, _, mockVng := newTaskAndMocks(t, &gwv1.Gateway{})
	lbID := testLoadBalancerID
	task.unscopedPolicy = gwPolicyWithLBSpec(&gwv1alpha1.VKSLoadBalancerSpec{LoadBalancerID: &lbID})
	// Adopting an LB whose backend subnet differs from the cluster default →
	// mirror that subnet's zone/cidr so the LBC is coherent with the real LB.
	mockVng.EXPECT().GetLoadBalancerByID(mock.Anything, testLoadBalancerID).
		Return(&entity.LoadBalancer{BackendSubnetID: "subnet-other"}, nil)
	mockVng.EXPECT().GetSubnetByID(mock.Anything, "net-1", "subnet-other").
		Return(&entity.Subnet{Id: "subnet-other", ZoneID: "HCM03-2B", Cidr: "10.1.0.0/24"}, nil)

	subnet, network, zone, cidr, err := task.resolveSubnetAndZone(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "subnet-other", subnet)
	assert.Equal(t, "net-1", network)
	assert.Equal(t, "HCM03-2B", zone)
	assert.Equal(t, "10.1.0.0/24", cidr)
}

func TestResolveSubnetAndZone_AdoptLBOnDefaultSubnet(t *testing.T) {
	task, _, mockVng := newTaskAndMocks(t, &gwv1.Gateway{})
	lbID := testLoadBalancerID
	task.unscopedPolicy = gwPolicyWithLBSpec(&gwv1alpha1.VKSLoadBalancerSpec{LoadBalancerID: &lbID})
	// LB already on the cluster default subnet → short-circuit to defaults,
	// no GetSubnetByID call (asserting it's NOT called via mockery strictness).
	mockVng.EXPECT().GetLoadBalancerByID(mock.Anything, testLoadBalancerID).
		Return(&entity.LoadBalancer{BackendSubnetID: "subnet-1"}, nil)

	subnet, network, zone, _, err := task.resolveSubnetAndZone(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "subnet-1", subnet)
	assert.Equal(t, "net-1", network)
	assert.Equal(t, "HCM03-1C", zone)
}

func TestResolveSubnetAndZone_AdoptLBLookupError(t *testing.T) {
	task, _, mockVng := newTaskAndMocks(t, &gwv1.Gateway{})
	lbID := "missing"
	task.unscopedPolicy = gwPolicyWithLBSpec(&gwv1alpha1.VKSLoadBalancerSpec{LoadBalancerID: &lbID})
	mockVng.EXPECT().GetLoadBalancerByID(mock.Anything, "missing").
		Return(nil, errors.New("not found"))
	_, _, _, _, err := task.resolveSubnetAndZone(context.Background())
	assert.Error(t, err)
}

// --- resolveSubnetAndZone: prefer-zone-id ---

func TestResolveSubnetAndZone_PreferZone(t *testing.T) {
	task, mockK8s, mockVng := newTaskAndMocks(t, &gwv1.Gateway{})
	zone := "HCM03-2B"
	task.unscopedPolicy = gwPolicyWithLBSpec(&gwv1alpha1.VKSLoadBalancerSpec{PreferZoneID: &zone})
	mockK8s.EXPECT().ListNode(mock.Anything, mock.AnythingOfType("*v1.NodeList")).
		RunAndReturn(func(_ context.Context, list *corev1.NodeList, _ ...client.ListOption) error {
			list.Items = []corev1.Node{*makeNode("node-1", "vngcloud://ins-00000000-0000-0000-0000-000000000099")}
			return nil
		})
	mockVng.EXPECT().GetServerNetworkInfo(mock.Anything, "ins-00000000-0000-0000-0000-000000000099").
		Return(common.Zone("HCM03-2B"), "net-2", "subnet-2", "10.2.0.0/24", nil)

	subnet, network, z, cidr, err := task.resolveSubnetAndZone(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "subnet-2", subnet)
	assert.Equal(t, "net-2", network)
	assert.Equal(t, "HCM03-2B", z)
	assert.Equal(t, "10.2.0.0/24", cidr)
}

func TestResolveSubnetAndZone_PreferZoneEqualsDefault(t *testing.T) {
	task, _, _ := newTaskAndMocks(t, &gwv1.Gateway{})
	zone := "HCM03-1C" // == default zone → short-circuit, no node listing
	task.unscopedPolicy = gwPolicyWithLBSpec(&gwv1alpha1.VKSLoadBalancerSpec{PreferZoneID: &zone})
	subnet, _, z, _, err := task.resolveSubnetAndZone(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "subnet-1", subnet)
	assert.Equal(t, "HCM03-1C", z)
}

func TestResolveSubnetAndZone_PreferZoneNoMatch(t *testing.T) {
	task, mockK8s, mockVng := newTaskAndMocks(t, &gwv1.Gateway{})
	zone := "HCM99-9Z"
	task.unscopedPolicy = gwPolicyWithLBSpec(&gwv1alpha1.VKSLoadBalancerSpec{PreferZoneID: &zone})
	mockK8s.EXPECT().ListNode(mock.Anything, mock.AnythingOfType("*v1.NodeList")).
		RunAndReturn(func(_ context.Context, list *corev1.NodeList, _ ...client.ListOption) error {
			list.Items = []corev1.Node{*makeNode("node-1", "vngcloud://ins-00000000-0000-0000-0000-000000000099")}
			return nil
		})
	mockVng.EXPECT().GetServerNetworkInfo(mock.Anything, "ins-00000000-0000-0000-0000-000000000099").
		Return(common.Zone("HCM03-1C"), "net-1", "subnet-1", "10.0.0.0/24", nil)
	_, _, _, _, err := task.resolveSubnetAndZone(context.Background())
	assert.Error(t, err)
}

// --- resolveLoadBalancerName ---

func TestResolveLoadBalancerName(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)

	// No policy name → controller default.
	assert.Equal(t, task.nameHelper.GetLoadBalancerDefaultName(), task.resolveLoadBalancerName())

	// Policy name wins.
	custom := "my-custom-lb"
	task.unscopedPolicy = gwPolicyWithLBSpec(&gwv1alpha1.VKSLoadBalancerSpec{LoadBalancerName: &custom})
	assert.Equal(t, "my-custom-lb", task.resolveLoadBalancerName())
}

// newFakeClientWithHTTPRouteIndex builds a fake client that has the HTTPRoute parent-gateway
// field index registered — required by listAttachedHTTPRoutes.
func newFakeClientWithHTTPRouteIndex(objs ...client.Object) client.Client {
	s := newTestScheme()
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&gwv1.Gateway{}).
		WithIndex(&gwv1.HTTPRoute{}, shared.IndexHTTPRouteByParentGateway, func(obj client.Object) []string {
			r := obj.(*gwv1.HTTPRoute)
			var keys []string
			for _, p := range r.Spec.ParentRefs {
				ns := r.Namespace
				if p.Namespace != nil {
					ns = string(*p.Namespace)
				}
				if p.Kind == nil || *p.Kind == "Gateway" {
					keys = append(keys, ns+"/"+string(p.Name))
				}
			}
			return keys
		}).
		Build()
}

// newFullTask builds a task with a real fake client containing the gateway policy objs.
func newFullTask(t *testing.T, gw *gwv1.Gateway, objs ...client.Object) (*defaultGatewayBuildTask, *repository.MockK8sRepository) {
	s := newTestScheme()
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&gwv1.Gateway{})
	for _, o := range objs {
		builder = builder.WithObjects(o)
	}
	fakeClient := builder.Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{
		k8sRepo:           mockK8s,
		vngcloudRepo:      mockVng,
		k8sClient:         fakeClient,
		defaultZone:       "HCM03-1C",
		defaultNetworkId:  "net-1",
		defaultSubnetId:   "subnet-1",
		defaultSubnetCIDR: "10.0.0.0/24",
		clusterId:         "cluster-1",
	}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("cluster-1", "gateway", gw.Namespace, gw.Name),
	}
	return task, mockK8s
}

func TestResolveGatewayPolicies_NoPolicies(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw", UID: "uid-1"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}
	task, _ := newFullTask(t, gw)
	err := task.resolveGatewayPolicies(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, task.unscopedPolicy)
	assert.Empty(t, task.listenerPolicies)
}

func TestResolveGatewayPolicies_WithUnscopedPolicy(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod",
			Name:      "my-gw",
			UID:       "uid-1",
		},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	// VKSGatewayPolicy targeting this Gateway (no sectionName = unscoped)
	policy := &gwv1alpha1.VKSGatewayPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw-policy"},
		Spec: gwv1alpha1.VKSGatewayPolicySpec{
			TargetRefs: []gwv1alpha1.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gwv1alpha1.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "my-gw",
				},
			}},
		},
	}

	task, _ := newFullTask(t, gw, policy)
	err := task.resolveGatewayPolicies(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, task.unscopedPolicy)
}

func TestApplyLoadBalancerSpec_NilPolicy(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)
	task.unscopedPolicy = nil
	lbc := &v1alpha1.LoadBalancerConfig{}
	task.applyLoadBalancerSpec(lbc)
	// No-op
	assert.Nil(t, lbc.Spec.Scheme)
	assert.Nil(t, lbc.Spec.PackageId)
}

func TestApplyLoadBalancerSpec_NilLoadBalancerSpec(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)
	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{} // no LoadBalancerSpec
	lbc := &v1alpha1.LoadBalancerConfig{}
	task.applyLoadBalancerSpec(lbc)
	assert.Nil(t, lbc.Spec.Scheme)
}

func TestApplyLoadBalancerSpec_WithFields(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)

	scheme := "Internet"
	pkg := "pkg-1"
	lbID := "lb-existing"
	subID := "subnet-private"
	zoneID := "HCM03-1A"
	autoscale := true
	isPoc := false
	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{
		Spec: gwv1alpha1.VKSGatewayPolicySpec{
			LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{
				Scheme:          &scheme,
				PackageID:       &pkg,
				LoadBalancerID:  &lbID,
				PrivateSubnetID: &subID,
				PrivateZoneID:   &zoneID,
				EnableAutoscale: &autoscale,
				IsPOC:           &isPoc,
				Tags:            map[string]string{"env": "prod"},
			},
		},
	}
	lbc := &v1alpha1.LoadBalancerConfig{}
	task.applyLoadBalancerSpec(lbc)
	assert.NotNil(t, lbc.Spec.Scheme)
	assert.NotNil(t, lbc.Spec.PackageId)
	assert.Equal(t, "pkg-1", *lbc.Spec.PackageId)
	assert.NotNil(t, lbc.Spec.LoadBalancerId)
	assert.NotNil(t, lbc.Spec.PrivateSubnetId)
	assert.Equal(t, "subnet-private", *lbc.Spec.PrivateSubnetId)
	if assert.NotNil(t, lbc.Spec.PrivateZoneId) {
		assert.Equal(t, "HCM03-1A", string(*lbc.Spec.PrivateZoneId))
	}
	assert.NotNil(t, lbc.Spec.EnableAutoscale)
	assert.True(t, *lbc.Spec.EnableAutoscale)
	assert.NotNil(t, lbc.Spec.IsPoc)
	assert.False(t, *lbc.Spec.IsPoc)
	assert.Equal(t, map[string]string{"env": "prod"}, lbc.Spec.Tags)
}

// TestApplyLoadBalancerSpec_RemovesStaleFields locks in the fix for the
// sticky-field bug: when the user removes a field from VKSGatewayPolicy
// (or removes the policy entirely), applyLoadBalancerSpec must clear the
// corresponding LBC.Spec field — otherwise the previous value persists
// forever, diverging from the Ingress controller's reassign-every-reconcile
// behavior.
func TestApplyLoadBalancerSpec_RemovesStaleFields(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}

	// Pre-populate the LBC with values from a previous reconcile.
	scheme := v2.LoadBalancerScheme("Internet")
	pkg := "pkg-old"
	lbID := "lb-old"
	subID := "subnet-old"
	autoscale := true
	isPoc := true
	lbc := &v1alpha1.LoadBalancerConfig{
		Spec: v1alpha1.LoadBalancerConfigSpec{
			Scheme:          &scheme,
			PackageId:       &pkg,
			LoadBalancerId:  &lbID,
			PrivateSubnetId: &subID,
			EnableAutoscale: &autoscale,
			IsPoc:           &isPoc,
			Tags:            map[string]string{"keep-me": "no"},
		},
	}

	t.Run("policy entirely removed clears all stale fields", func(t *testing.T) {
		task := newTestTask(t, gw)
		task.unscopedPolicy = nil
		got := lbc.DeepCopy()
		task.applyLoadBalancerSpec(got)
		assert.Nil(t, got.Spec.Scheme)
		assert.Nil(t, got.Spec.PackageId)
		assert.Nil(t, got.Spec.LoadBalancerId)
		assert.Nil(t, got.Spec.PrivateSubnetId)
		assert.Nil(t, got.Spec.EnableAutoscale)
		assert.Nil(t, got.Spec.IsPoc)
		assert.Nil(t, got.Spec.Tags)
	})

	t.Run("partial policy clears only the unset fields", func(t *testing.T) {
		task := newTestTask(t, gw)
		newScheme := "Internal"
		task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{
			Spec: gwv1alpha1.VKSGatewayPolicySpec{
				LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{
					Scheme: &newScheme,
				},
			},
		}
		got := lbc.DeepCopy()
		task.applyLoadBalancerSpec(got)
		// Scheme overwritten with the new value.
		assert.NotNil(t, got.Spec.Scheme)
		assert.Equal(t, v2.LoadBalancerScheme("Internal"), *got.Spec.Scheme)
		// All other previously-set fields are cleared.
		assert.Nil(t, got.Spec.PackageId)
		assert.Nil(t, got.Spec.LoadBalancerId)
		assert.Nil(t, got.Spec.PrivateSubnetId)
		assert.Nil(t, got.Spec.EnableAutoscale)
		assert.Nil(t, got.Spec.IsPoc)
		assert.Nil(t, got.Spec.Tags)
	})

	t.Run("Tags are replaced, not merged", func(t *testing.T) {
		task := newTestTask(t, gw)
		task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{
			Spec: gwv1alpha1.VKSGatewayPolicySpec{
				LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{
					Tags: map[string]string{"env": "prod"},
				},
			},
		}
		got := &v1alpha1.LoadBalancerConfig{
			Spec: v1alpha1.LoadBalancerConfigSpec{
				Tags: map[string]string{"old-key": "old-val"},
			},
		}
		task.applyLoadBalancerSpec(got)
		assert.Equal(t, map[string]string{"env": "prod"}, got.Spec.Tags,
			"old-key must be evicted when the policy doesn't list it")
	})
}

// TestBuildLoadBalancerConfig_CreatePath tests the happy path where no LBC exists yet.
func TestBuildLoadBalancerConfig_CreatePath(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod",
			Name:      "my-gw",
			UID:       types.UID("gw-uid-1"),
		},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	// The Gateway itself is in the fake client for status patch; index required for HTTPRoute listing.
	fakeClient := newFakeClientWithHTTPRouteIndex(gw)
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	// listOwnedLBCs → empty (new gateway)
	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	// CreateLoadBalancerConfig called
	mockK8s.EXPECT().
		CreateLoadBalancerConfig(mock.Anything, mock.Anything).
		Return(nil).Once()

	uc := &albGatewayUseCase{
		k8sRepo:           mockK8s,
		vngcloudRepo:      mockVng,
		k8sClient:         fakeClient,
		defaultZone:       "HCM03-1C",
		defaultNetworkId:  "net-1",
		defaultSubnetId:   "subnet-1",
		defaultSubnetCIDR: "10.0.0.0/24",
		clusterId:         "cluster-1",
	}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("cluster-1", "gateway", gw.Namespace, gw.Name),
	}

	err := task.buildLoadBalancerConfig(context.Background())
	assert.NoError(t, err)
}

// TestBuildLoadBalancerConfig_PatchPath tests the happy path where the LBC already exists.
func TestBuildLoadBalancerConfig_PatchPath(t *testing.T) {
	gwUID := types.UID("gw-uid-2")
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw", UID: gwUID},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	existingLBC := &v1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod",
			Name:      "my-gw-lbc",
			Labels: map[string]string{
				domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
				domain.LabelOwnerResourceUid:  string(gwUID),
			},
		},
		Spec: v1alpha1.LoadBalancerConfigSpec{VpcId: "net-old"},
	}

	// Index required for HTTPRoute listing.
	fakeClient := newFakeClientWithHTTPRouteIndex(gw)
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	// listOwnedLBCs returns one existing LBC
	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, list *v1alpha1.LoadBalancerConfigList, _ ...client.ListOption) error {
			list.Items = []v1alpha1.LoadBalancerConfig{*existingLBC}
			return nil
		}).Once()

	// Spec changed (VpcId) → PatchLoadBalancerConfig called
	mockK8s.EXPECT().
		PatchLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	uc := &albGatewayUseCase{
		k8sRepo:           mockK8s,
		vngcloudRepo:      mockVng,
		k8sClient:         fakeClient,
		defaultZone:       "HCM03-1C",
		defaultNetworkId:  "net-1",
		defaultSubnetId:   "subnet-1",
		defaultSubnetCIDR: "10.0.0.0/24",
		clusterId:         "cluster-1",
	}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("cluster-1", "gateway", gw.Namespace, gw.Name),
	}

	err := task.buildLoadBalancerConfig(context.Background())
	assert.NoError(t, err)
}

// TestBuildLoadBalancerConfig_MultipleOwnedLBCsError ensures >1 LBC returns error.
func TestBuildLoadBalancerConfig_MultipleOwnedLBCsError(t *testing.T) {
	gwUID := types.UID("gw-uid-3")
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw", UID: gwUID},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, list *v1alpha1.LoadBalancerConfigList, _ ...client.ListOption) error {
			list.Items = []v1alpha1.LoadBalancerConfig{
				{ObjectMeta: metav1.ObjectMeta{Name: "lbc-1"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "lbc-2"}},
			}
			return nil
		}).Once()

	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	task := &defaultGatewayBuildTask{
		uc: uc, gw: gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("cluster-1", "gateway", gw.Namespace, gw.Name),
	}
	err := task.buildLoadBalancerConfig(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple LBCs")
}

// TestRun exercises the top-level task.run() path.
func TestRun_NoSubnet(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw-run", UID: "uid-run"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}
	fakeClient := newFakeClientWithHTTPRouteIndex(gw)
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fakeClient,
		// No subnet/zone → error in buildLoadBalancerConfig
	}
	task := &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("cluster-1", "gateway", gw.Namespace, gw.Name),
	}
	err := task.run(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "could not resolve subnet/zone")
}

// TestBuildLoadBalancerConfig_MissingSubnet tests error when no subnet available.
func TestBuildLoadBalancerConfig_MissingSubnet(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw", UID: "uid"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fakeClient,
		// No defaults set — subnet/network/zone all empty
	}
	task := &defaultGatewayBuildTask{
		uc: uc, gw: gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("cluster-1", "gateway", gw.Namespace, gw.Name),
	}
	err := task.buildLoadBalancerConfig(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "could not resolve subnet/zone")
}
