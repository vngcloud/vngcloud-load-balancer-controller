package alb_gateway_uc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
)

// makeNode creates a Node with a given providerID.
func makeNode(name, providerID string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{ProviderID: providerID},
	}
}

// --- NewALBGatewayUseCase tests ---

func TestNewALBGatewayUseCase(t *testing.T) {
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	uc := NewALBGatewayUseCase("cluster-1", mockK8s, mockVng, nil, fakeClient, nil)
	assert.NotNil(t, uc)
}

// --- InitALBGatewayUseCase tests ---

func TestInitALBGatewayUseCase_Idempotent(t *testing.T) {
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{
		k8sRepo:           mockK8s,
		vngcloudRepo:      mockVng,
		k8sClient:         fake.NewClientBuilder().WithScheme(newTestScheme()).Build(),
		clusterId:         "cluster-1",
		defaultZone:       "HCM03-1C",
		defaultNetworkId:  "net-1",
		defaultSubnetId:   "subnet-1",
		defaultSubnetCIDR: "10.0.0.0/24",
	}
	// Already initialised — should return nil without calling anything
	err := uc.InitALBGatewayUseCase(context.Background())
	assert.NoError(t, err)
}

func TestInitALBGatewayUseCase_ListNodeError(t *testing.T) {
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListNode(mock.Anything, mock.Anything).
		Return(errors.New("cache not ready"))

	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fake.NewClientBuilder().WithScheme(newTestScheme()).Build(),
	}
	err := uc.InitALBGatewayUseCase(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list nodes")
}

func TestInitALBGatewayUseCase_NoNodes(t *testing.T) {
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListNode(mock.Anything, mock.Anything).
		Return(nil) // leaves NodeList empty

	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fake.NewClientBuilder().WithScheme(newTestScheme()).Build(),
	}
	err := uc.InitALBGatewayUseCase(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no nodes")
}

func TestInitALBGatewayUseCase_GetServerNetworkInfoError(t *testing.T) {
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListNode(mock.Anything, mock.AnythingOfType("*v1.NodeList")).
		RunAndReturn(func(_ context.Context, list *corev1.NodeList, _ ...client.ListOption) error {
			list.Items = []corev1.Node{*makeNode("node-1", "vngcloud://ins-00000000-0000-0000-0000-000000000001")}
			return nil
		})

	mockVng.EXPECT().
		GetServerNetworkInfo(mock.Anything, "ins-00000000-0000-0000-0000-000000000001").
		Return(common.Zone(""), "", "", "", errors.New("cloud api error"))

	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fake.NewClientBuilder().WithScheme(newTestScheme()).Build(),
	}
	err := uc.InitALBGatewayUseCase(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "probe vngcloud network info")
}

func TestInitALBGatewayUseCase_Success(t *testing.T) {
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListNode(mock.Anything, mock.AnythingOfType("*v1.NodeList")).
		RunAndReturn(func(_ context.Context, list *corev1.NodeList, _ ...client.ListOption) error {
			list.Items = []corev1.Node{*makeNode("node-1", "vngcloud://ins-00000000-0000-0000-0000-000000000001")}
			return nil
		})

	mockVng.EXPECT().
		GetServerNetworkInfo(mock.Anything, "ins-00000000-0000-0000-0000-000000000001").
		Return(common.Zone("HCM03-1C"), "net-abc", "subnet-abc", "10.0.0.0/24", nil)

	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fake.NewClientBuilder().WithScheme(newTestScheme()).Build(),
		clusterId:    "cluster-1",
	}
	err := uc.InitALBGatewayUseCase(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, common.Zone("HCM03-1C"), uc.defaultZone)
	assert.Equal(t, "net-abc", uc.defaultNetworkId)
	assert.Equal(t, "subnet-abc", uc.defaultSubnetId)
	assert.Equal(t, "10.0.0.0/24", uc.defaultSubnetCIDR)
}

// TestInitALBGatewayUseCase_ClusterIdFromNodeLabel verifies the same
// fallback ingress_uc/service_uc do: when the controller wasn't given a
// --cluster-id, it picks the value off the first node carrying the
// vks.vngcloud.vn/cluster-id label.
func TestInitALBGatewayUseCase_ClusterIdFromNodeLabel(t *testing.T) {
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	node := makeNode("node-1", "vngcloud://ins-00000000-0000-0000-0000-000000000001")
	node.Labels = map[string]string{"vks.vngcloud.vn/cluster-id": "cluster-from-label"}

	mockK8s.EXPECT().
		ListNode(mock.Anything, mock.AnythingOfType("*v1.NodeList")).
		RunAndReturn(func(_ context.Context, list *corev1.NodeList, _ ...client.ListOption) error {
			list.Items = []corev1.Node{*node}
			return nil
		})

	mockVng.EXPECT().
		GetServerNetworkInfo(mock.Anything, "ins-00000000-0000-0000-0000-000000000001").
		Return(common.Zone("HCM03-1C"), "net-abc", "subnet-abc", "10.0.0.0/24", nil)

	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fake.NewClientBuilder().WithScheme(newTestScheme()).Build(),
		// clusterId left empty intentionally
	}
	err := uc.InitALBGatewayUseCase(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "cluster-from-label", uc.clusterId)
}

// TestInitALBGatewayUseCase_NoClusterIdAvailable: if --cluster-id wasn't
// passed AND no node carries the label, init must fail loudly so the user
// sees the misconfiguration instead of getting silent name collisions.
func TestInitALBGatewayUseCase_NoClusterIdAvailable(t *testing.T) {
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListNode(mock.Anything, mock.AnythingOfType("*v1.NodeList")).
		RunAndReturn(func(_ context.Context, list *corev1.NodeList, _ ...client.ListOption) error {
			list.Items = []corev1.Node{*makeNode("node-1", "vngcloud://ins-00000000-0000-0000-0000-000000000001")}
			return nil
		})

	mockVng.EXPECT().
		GetServerNetworkInfo(mock.Anything, "ins-00000000-0000-0000-0000-000000000001").
		Return(common.Zone("HCM03-1C"), "net-abc", "subnet-abc", "10.0.0.0/24", nil)

	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fake.NewClientBuilder().WithScheme(newTestScheme()).Build(),
	}
	err := uc.InitALBGatewayUseCase(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no clusterID found")
}

// --- fetchGatewayClass tests ---

func TestFetchGatewayClass_EmptyClassName(t *testing.T) {
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	uc := &albGatewayUseCase{k8sClient: fakeClient}
	gw := &gwv1.Gateway{} // empty class name
	gc, err := uc.fetchGatewayClass(context.Background(), gw)
	assert.Error(t, err)
	assert.Nil(t, gc)
}

func TestFetchGatewayClass_NotFound(t *testing.T) {
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	uc := &albGatewayUseCase{k8sClient: fakeClient}
	gw := &gwv1.Gateway{}
	gw.Spec.GatewayClassName = "nonexistent"
	gc, err := uc.fetchGatewayClass(context.Background(), gw)
	assert.NoError(t, err) // not-found returns nil, nil
	assert.Nil(t, gc)
}

func TestFetchGatewayClass_Found(t *testing.T) {
	s := newTestScheme()
	existingGC := &gwv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-alb"},
		Spec:       gwv1.GatewayClassSpec{ControllerName: consts.GatewayClassControllerNameALB},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existingGC).Build()
	uc := &albGatewayUseCase{k8sClient: fakeClient}
	gw := &gwv1.Gateway{}
	gw.Spec.GatewayClassName = "vngcloud-alb"
	gc, err := uc.fetchGatewayClass(context.Background(), gw)
	assert.NoError(t, err)
	assert.NotNil(t, gc)
	assert.Equal(t, gwv1.ObjectName("vngcloud-alb"), gwv1.ObjectName(gc.Name))
}

// --- EnsureALBGatewayUseCase tests ---

func TestEnsureALBGatewayUseCase_NotFound(t *testing.T) {
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "missing-gw"}}
	err := uc.EnsureALBGatewayUseCase(context.Background(), req)
	assert.NoError(t, err)
}

func TestEnsureALBGatewayUseCase_MismatchedClass(t *testing.T) {
	s := newTestScheme()
	// Gateway exists, but no GatewayClass of that name → fetchGatewayClass returns nil
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec:       gwv1.GatewaySpec{GatewayClassName: "other-class"},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "my-gw"}}
	err := uc.EnsureALBGatewayUseCase(context.Background(), req)
	// gc == nil → return nil
	assert.NoError(t, err)
}

func TestEnsureALBGatewayUseCase_WrongControllerName(t *testing.T) {
	s := newTestScheme()
	gc := &gwv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "nlb-class"},
		Spec:       gwv1.GatewayClassSpec{ControllerName: "gateway.vks.vngcloud.vn/nlb"},
	}
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec:       gwv1.GatewaySpec{GatewayClassName: "nlb-class"},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gc, gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "my-gw"}}
	err := uc.EnsureALBGatewayUseCase(context.Background(), req)
	assert.NoError(t, err) // not our class → ignore
}

func TestEnsureALBGatewayUseCase_DeletionPath(t *testing.T) {
	s := newTestScheme()
	gc := &gwv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-alb"},
		Spec:       gwv1.GatewayClassSpec{ControllerName: consts.GatewayClassControllerNameALB},
	}
	now := metav1.Now()
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "prod",
			Name:              "my-gw",
			Finalizers:        []string{domain.GatewayFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: gwv1.GatewaySpec{GatewayClassName: "vngcloud-alb"},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gc, gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	// handleDeletion → listOwnedLBCs (empty) → remove finalizer
	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient,
		finalizerManager: k8s.NewDefaultFinalizerManager(fakeClient, logr.Discard())}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "my-gw"}}
	err := uc.EnsureALBGatewayUseCase(context.Background(), req)
	assert.NoError(t, err)
}

// --- DeleteALBGatewayUseCase tests ---

func TestDeleteALBGatewayUseCase_NotFound(t *testing.T) {
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "gone-gw"}}
	err := uc.DeleteALBGatewayUseCase(context.Background(), req)
	assert.NoError(t, err)
}

func TestDeleteALBGatewayUseCase_ExistingGateway(t *testing.T) {
	s := newTestScheme()
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "prod",
			Name:       "existing-gw",
			Finalizers: []string{domain.GatewayFinalizer},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	// handleDeletion → listOwnedLBCs (empty) → remove finalizer
	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient,
		finalizerManager: k8s.NewDefaultFinalizerManager(fakeClient, logr.Discard())}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "existing-gw"}}
	err := uc.DeleteALBGatewayUseCase(context.Background(), req)
	assert.NoError(t, err)
}

// --- buildLoadBalancerName tests ---

// TestLoadBalancerName checks the LB name produced via NameHelper. The actual
// name shape (vks_<cluster>_<ns>_<name>_<hash>) is owned by NameHelper —
// this test verifies the Gateway use case feeds it the right inputs and the
// result is deterministic + length-bounded.
func TestLoadBalancerName(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod",
			Name:      "my-gateway",
			UID:       "abcd1234-dead-beef-0000-111122223333",
		},
	}
	task := newTestTask(t, gw)
	name := task.nameHelper.GetLoadBalancerDefaultName()
	assert.LessOrEqual(t, len(name), 50)
	// NameHelper.ValidateName replaces underscores with dashes (cloud's
	// loadBalancerName regex doesn't allow `_`), so we expect "vks-".
	assert.True(t, strings.HasPrefix(name, "vks-"), "expected vks- prefix in %q", name)
	// Deterministic
	assert.Equal(t, name, task.nameHelper.GetLoadBalancerDefaultName())
}

func TestLoadBalancerName_LongGatewayName(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod",
			Name:      "this-is-a-very-long-gateway-name-that-exceeds-limits",
			UID:       "abcd1234",
		},
	}
	task := newTestTask(t, gw)
	name := task.nameHelper.GetLoadBalancerDefaultName()
	assert.LessOrEqual(t, len(name), 50)
}

// --- resolveSubnetAndZone tests ---

func TestResolveSubnetAndZone_Defaults(t *testing.T) {
	gw := &gwv1.Gateway{}
	task := newTestTask(t, gw)
	// No policy → defaults
	subnet, network, zone, cidr, err := task.resolveSubnetAndZone(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "subnet-1", subnet)
	assert.Equal(t, "net-1", network)
	assert.Equal(t, "HCM03-1C", zone)
	assert.Equal(t, "10.0.0.0/24", cidr)
}

func TestResolveSubnetAndZone_PolicyOverridesSubnet(t *testing.T) {
	gw := &gwv1.Gateway{}
	task := newTestTask(t, gw)
	overrideSubnet := "subnet-override"
	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
	task.unscopedPolicy.Spec.LoadBalancerSpec = &gwv1alpha1.VKSLoadBalancerSpec{
		SubnetID: &overrideSubnet,
	}
	subnet, network, _, _, err := task.resolveSubnetAndZone(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "subnet-override", subnet)
	assert.Equal(t, "net-1", network) // network stays from default
}

// TestEnsureALBGatewayUseCase_GetError tests the path where fetchGatewayClass returns
// an error due to an empty gatewayClassName.
func TestEnsureALBGatewayUseCase_GetError(t *testing.T) {
	s := newTestScheme()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	// GatewayClass with empty name → fetchGatewayClass returns error.
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec:       gwv1.GatewaySpec{GatewayClassName: ""}, // empty → error
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "my-gw"}}
	err := uc.EnsureALBGatewayUseCase(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty gatewayClassName")
}

// TestDeleteALBGatewayUseCase_GetRealError tests that a genuine (non-NotFound) Get error is propagated.
func TestDeleteALBGatewayUseCase_GetRealError(t *testing.T) {
	// We can trigger a non-NotFound error by pointing Delete at a resource
	// that requires a scheme not registered. However fake client with correct scheme
	// only returns NotFound. So instead we test the meta.IsNoMatchError branch:
	// that is hit when the GVK is not registered in the scheme (no-match).
	// In practice with the correct scheme and a missing object we get NotFound.
	// This test exercises the path where the object exists but handleDeletion errors.
	s := newTestScheme()
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "prod",
			Name:       "del-gw",
			Finalizers: []string{domain.GatewayFinalizer},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	// deleteOwnedLBC → listOwnedLBCs fails
	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("list failed")).Once()

	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "del-gw"}}
	err := uc.DeleteALBGatewayUseCase(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list failed")
}

// TestEnsureALBGatewayUseCase_EnsureFinalizerThenRun tests the branch where
// ensureFinalizer succeeds and then task.run() is called. Without subnet info
// buildLoadBalancerConfig returns an error.
func TestEnsureALBGatewayUseCase_EnsureFinalizerThenRun(t *testing.T) {
	s := newTestScheme()
	gc := &gwv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-alb"},
		Spec:       gwv1.GatewayClassSpec{ControllerName: consts.GatewayClassControllerNameALB},
	}
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec:       gwv1.GatewaySpec{GatewayClassName: "vngcloud-alb"},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gc, gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient,
		finalizerManager: k8s.NewDefaultFinalizerManager(fakeClient, logr.Discard())}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "my-gw"}}
	// ensureFinalizer calls AddFinalizers which should succeed (fake client).
	// Then task.run() will be called. Without subnet info, buildLoadBalancerConfig
	// will fail with "could not resolve default subnet".
	// The list call is needed for buildLoadBalancerConfig → listOwnedLBCs.
	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	err := uc.EnsureALBGatewayUseCase(context.Background(), req)
	// Without subnet/zone set, buildLoadBalancerConfig fails.
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "could not resolve subnet/zone")
}
