package lbc_uc

import (
	"context"
	"sync"

	"github.com/anngdinh/operator-helper/contexts"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/config"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8sbatch"
)

type lbcUseCase struct {
	cfg          *config.Config
	k8sRepo      repository.K8sRepository
	vngcloudRepo repository.VngCloudRepository

	// lbLocks holds a mutex per LoadBalancer ID to prevent concurrent modifications
	// when multiple LBCs share the same LoadBalancer
	lbLocks sync.Map // map[string]*sync.Mutex
}

func NewLoadBalancerConfigUseCase(
	cfg *config.Config,
	k8sRepo repository.K8sRepository,
	vngcloudRepo repository.VngCloudRepository,
) usecase.LoadBalancerConfigUseCase {
	return &lbcUseCase{
		cfg:          cfg,
		k8sRepo:      k8sRepo,
		vngcloudRepo: vngcloudRepo,
	}
}

func (uc *lbcUseCase) InitLoadBalancerConfigUseCase(ctx context.Context) error {
	return nil
}

// getLBLock returns a mutex for the given LoadBalancer ID, creating one if necessary.
// This ensures only one LBC can modify a LoadBalancer at a time.
func (uc *lbcUseCase) getLBLock(lbID string) *sync.Mutex {
	if lbID == "" {
		return nil
	}
	lock, _ := uc.lbLocks.LoadOrStore(lbID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// getLoadBalancerID returns the LoadBalancer ID from spec or status
func (uc *lbcUseCase) getLoadBalancerID(lbConfig *v1alpha1.LoadBalancerConfig) string {
	if lbConfig.Spec.LoadBalancerId != nil && *lbConfig.Spec.LoadBalancerId != "" {
		return *lbConfig.Spec.LoadBalancerId
	}
	if lbConfig.Status.LoadBalancerId != nil && *lbConfig.Status.LoadBalancerId != "" {
		return *lbConfig.Status.LoadBalancerId
	}
	return ""
}

func (uc *lbcUseCase) EnsureLoadBalancerConfigUseCase(ctx context.Context, req ctrl.Request) error {
	lbConfig, err := uc.k8sRepo.GetLoadBalancerConfig(ctx, req.NamespacedName)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	// Acquire lock by LoadBalancer ID to prevent concurrent modifications
	if lock := uc.getLBLock(uc.getLoadBalancerID(lbConfig)); lock != nil {
		lock.Lock()
		defer lock.Unlock()
	}

	// Perform the actual reconciliation
	err = uc.ensure(ctx, lbConfig)

	// Update reconciliation tracking fields and conditions
	now := metav1.Now()
	message := "Successfully reconciled"
	conditionStatus := metav1.ConditionTrue
	conditionReason := v1alpha1.LBCReasonReconcileSuccess
	if err != nil {
		message = err.Error()
		conditionStatus = metav1.ConditionFalse
		conditionReason = v1alpha1.LBCReasonReconcileFailed
	}

	// !IMPORTANT!: The tests will fail without this
	statusErr := uc.k8sRepo.PatchMutateStatusLoadBalancerConfig(ctx, lbConfig, func(ctx context.Context, obj *v1alpha1.LoadBalancerConfig) bool {
		changed := false

		// Update ObservedGeneration and LastReconcileMessage
		if obj.Status.ObservedGeneration == nil || *obj.Status.ObservedGeneration != lbConfig.Generation ||
			obj.Status.LastReconcileMessage != message {
			obj.Status.ObservedGeneration = &lbConfig.Generation
			obj.Status.LastReconcileMessage = message
			obj.Status.LastReconcileTime = &now
			changed = true
		}

		// Update Ready condition using Kubernetes standard pattern
		newCondition := metav1.Condition{
			Type:               v1alpha1.LBCConditionTypeReady,
			Status:             conditionStatus,
			ObservedGeneration: lbConfig.Generation,
			LastTransitionTime: now,
			Reason:             conditionReason,
			Message:            message,
		}
		if meta.SetStatusCondition(&obj.Status.Conditions, newCondition) {
			changed = true
		}

		return changed
	})
	if statusErr != nil {
		logger := contexts.NewContext(ctx).Log()
		logger.Warnf("Failed to update reconciliation tracking fields: %v", statusErr)
	}

	return err
}

func (uc *lbcUseCase) ensure(ctx context.Context, lbConfig *v1alpha1.LoadBalancerConfig) error {
	logger := contexts.NewContext(ctx).Log()
	task := &defaultModelDeployTask{
		logger:       logger,
		cfg:          uc.cfg,
		lbConfig:     lbConfig,
		vngcloudRepo: uc.vngcloudRepo,
		k8sRepo:      uc.k8sRepo,
		batcher:      k8sbatch.New(uc.k8sRepo.Client()),
	}

	deployErr := task.deploy(ctx)
	flushErr := task.batcher.Flush(ctx)
	if deployErr != nil {
		// Deploy is the primary error; surface flush failures as a log so
		// callers' requeue logic still sees the original cause. Partial
		// status writes that did flush successfully are still persisted.
		if flushErr != nil {
			logger.Warnf("flush of batched status updates failed: %v", flushErr)
		}
		return deployErr
	}
	return flushErr
}

func (uc *lbcUseCase) DeleteLoadBalancerConfigUseCase(ctx context.Context, req ctrl.Request) error {
	lbConfig, err := uc.k8sRepo.GetLoadBalancerConfig(ctx, req.NamespacedName)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	// Acquire lock by LoadBalancer ID to prevent concurrent modifications
	if lock := uc.getLBLock(uc.getLoadBalancerID(lbConfig)); lock != nil {
		lock.Lock()
		defer lock.Unlock()
	}

	logger := contexts.NewContext(ctx).Log()
	task := &defaultModelDeployTask{
		logger:       logger,
		cfg:          uc.cfg,
		lbConfig:     lbConfig,
		vngcloudRepo: uc.vngcloudRepo,
		k8sRepo:      uc.k8sRepo,
		batcher:      k8sbatch.New(uc.k8sRepo.Client()),
	}

	deleteErr := task.delete(ctx)
	flushErr := task.batcher.Flush(ctx)
	if deleteErr != nil {
		if flushErr != nil {
			logger.Warnf("flush of batched status updates failed: %v", flushErr)
		}
		return deleteErr
	}
	return flushErr
}
