package alb_gateway_uc

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
)

func buildUCWithGW(t *testing.T, gw *gwv1.Gateway) (*albGatewayUseCase, *repository.MockK8sRepository) {
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{
		k8sRepo:          mockK8s,
		vngcloudRepo:     mockVng,
		k8sClient:        fakeClient,
		finalizerManager: k8s.NewDefaultFinalizerManager(fakeClient, logr.Discard()),
	}
	return uc, mockK8s
}

func TestEnsureFinalizer_Idempotent(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "prod",
			Name:       "my-gw",
			Finalizers: []string{domain.GatewayFinalizer},
		},
	}
	uc, _ := buildUCWithGW(t, gw)
	// Already has finalizer → no-op
	err := uc.ensureFinalizer(context.Background(), gw)
	assert.NoError(t, err)
	assert.Contains(t, gw.Finalizers, domain.GatewayFinalizer)
}

func TestEnsureFinalizer_AddsFinalizer(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod",
			Name:      "my-gw",
		},
	}
	uc, _ := buildUCWithGW(t, gw)
	err := uc.ensureFinalizer(context.Background(), gw)
	assert.NoError(t, err)
	assert.Contains(t, gw.Finalizers, domain.GatewayFinalizer)
}

func TestHandleDeletion_NoOwnedLBCs(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "prod",
			Name:              "my-gw",
			Finalizers:        []string{domain.GatewayFinalizer},
			DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
		},
	}
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	// No owned LBCs
	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	uc := &albGatewayUseCase{
		k8sRepo:          mockK8s,
		vngcloudRepo:     mockVng,
		k8sClient:        fakeClient,
		finalizerManager: k8s.NewDefaultFinalizerManager(fakeClient, logr.Discard()),
	}
	err := uc.handleDeletion(context.Background(), gw)
	assert.NoError(t, err)
	// Finalizer should be removed
	assert.NotContains(t, gw.Finalizers, domain.GatewayFinalizer)
}

func TestHandleDeletion_DeletesOwnedLBC(t *testing.T) {
	gwUID := types.UID("test-uid-123")
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "prod",
			Name:              "my-gw",
			UID:               gwUID,
			Finalizers:        []string{domain.GatewayFinalizer},
			DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
		},
	}
	lbc := &v1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod",
			Name:      "my-gw-lbc",
			Labels: map[string]string{
				domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
				domain.LabelOwnerResourceUid:  string(gwUID),
			},
		},
	}

	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	// ListLoadBalancerConfig returns our LBC
	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, list *v1alpha1.LoadBalancerConfigList, opts ...client.ListOption) error {
			list.Items = []v1alpha1.LoadBalancerConfig{*lbc}
			return nil
		})

	// DeleteLoadBalancerConfig is called for the LBC
	mockK8s.EXPECT().
		DeleteLoadBalancerConfig(mock.Anything, mock.Anything).
		Return(nil)

	uc := &albGatewayUseCase{
		k8sRepo:          mockK8s,
		vngcloudRepo:     mockVng,
		k8sClient:        fakeClient,
		finalizerManager: k8s.NewDefaultFinalizerManager(fakeClient, logr.Discard()),
	}
	err := uc.handleDeletion(context.Background(), gw)
	assert.NoError(t, err)
}

func TestHandleDeletion_GatewayAlreadyGone(t *testing.T) {
	// Reconciler races deletion: the Gateway no longer exists in the apiserver
	// by the time we try to drop the finalizer, so RemoveFinalizers' re-Get
	// returns NotFound. Cleanup is already complete — handleDeletion must not
	// error/re-queue.
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "prod",
			Name:              "gone-gw",
			Finalizers:        []string{domain.GatewayFinalizer},
			DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
		},
	}
	s := newTestScheme()
	// Build the client WITHOUT the Gateway → the re-Get returns NotFound.
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	uc := &albGatewayUseCase{
		k8sRepo:          mockK8s,
		vngcloudRepo:     mockVng,
		k8sClient:        fakeClient,
		finalizerManager: k8s.NewDefaultFinalizerManager(fakeClient, logr.Discard()),
	}
	err := uc.handleDeletion(context.Background(), gw)
	assert.NoError(t, err)
}

func TestDeleteOwnedLBC_AlreadyGone(t *testing.T) {
	// deleteOwnedLBC should silently ignore IsNotFound errors.
	gwUID := types.UID("uid-gone")
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw", UID: gwUID},
	}
	lbc := &v1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "lbc-gone"},
	}
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(gw).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, list *v1alpha1.LoadBalancerConfigList, _ ...client.ListOption) error {
			list.Items = []v1alpha1.LoadBalancerConfig{*lbc}
			return nil
		})

	// DeleteLoadBalancerConfig returns not-found — should be swallowed
	mockK8s.EXPECT().
		DeleteLoadBalancerConfig(mock.Anything, mock.Anything).
		Return(apierrors.NewNotFound(v1alpha1.GroupVersion.WithResource("loadbalancerconfigs").GroupResource(), "lbc-gone"))

	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	err := uc.deleteOwnedLBC(context.Background(), gw)
	assert.NoError(t, err)
}

func TestListOwnedLBCs(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod",
			Name:      "my-gw",
			UID:       "gw-uid-1",
		},
	}
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)

	ownedLBC := &v1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod",
			Name:      "owned-lbc",
			Labels: map[string]string{
				domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
				domain.LabelOwnerResourceUid:  "gw-uid-1",
			},
		},
	}

	mockK8s.EXPECT().
		ListLoadBalancerConfig(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, list *v1alpha1.LoadBalancerConfigList, opts ...client.ListOption) error {
			list.Items = []v1alpha1.LoadBalancerConfig{*ownedLBC}
			return nil
		})

	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fakeClient,
	}

	lbcs, err := uc.listOwnedLBCs(context.Background(), gw)
	assert.NoError(t, err)
	assert.Len(t, lbcs, 1)
	assert.Equal(t, "owned-lbc", lbcs[0].Name)
}
