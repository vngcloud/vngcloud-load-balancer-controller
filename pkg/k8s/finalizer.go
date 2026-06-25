package k8s

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type FinalizerManager interface {
	AddFinalizers(ctx context.Context, object client.Object, finalizers ...string) error
	RemoveFinalizers(ctx context.Context, object client.Object, finalizers ...string) error
}

func NewDefaultFinalizerManager(k8sClient client.Client, log logr.Logger) FinalizerManager {
	return &defaultFinalizerManager{
		k8sClient: k8sClient,
		log:       log,
	}
}

type defaultFinalizerManager struct {
	k8sClient client.Client

	log logr.Logger
}

func (m *defaultFinalizerManager) AddFinalizers(ctx context.Context, obj client.Object, finalizers ...string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := m.k8sClient.Get(ctx, NamespacedName(obj), obj); err != nil {
			return err
		}

		oldObj := obj.DeepCopyObject().(client.Object)
		needsUpdate := false
		for _, finalizer := range finalizers {
			if !HasFinalizer(obj, finalizer) {
				controllerutil.AddFinalizer(obj, finalizer)
				needsUpdate = true
			}
		}
		if !needsUpdate {
			return nil
		}
		return m.k8sClient.Patch(ctx, obj, client.MergeFromWithOptions(oldObj, client.MergeFromWithOptimisticLock{}))
	})
}

func (m *defaultFinalizerManager) RemoveFinalizers(ctx context.Context, obj client.Object, finalizers ...string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := m.k8sClient.Get(ctx, NamespacedName(obj), obj); err != nil {
			// Object already gone — finalizer removal is moot, goal is met.
			return client.IgnoreNotFound(err)
		}

		oldObj := obj.DeepCopyObject().(client.Object)
		needsUpdate := false
		for _, finalizer := range finalizers {
			if HasFinalizer(obj, finalizer) {
				controllerutil.RemoveFinalizer(obj, finalizer)
				needsUpdate = true
			}
		}
		if !needsUpdate {
			return nil
		}
		err := m.k8sClient.Patch(ctx, obj, client.MergeFromWithOptions(oldObj, client.MergeFromWithOptimisticLock{}))
		if err != nil {
			// Object deleted between Get and Patch — goal is already met.
			// Conflicts are retried by RetryOnConflict (re-Gets a fresh copy).
			return client.IgnoreNotFound(err)
		}
		// return nil

		// Wait for the finalizers to be removed, with a timeout
		timeout := time.After(3 * time.Second)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		// First immediate check
		for {
			// Check immediately before waiting
			if err := m.k8sClient.Get(ctx, NamespacedName(obj), obj); err != nil {
				return client.IgnoreNotFound(err)
			}

			// Check if finalizers are removed
			stillHasFinalizers := false
			for _, finalizer := range finalizers {
				if HasFinalizer(obj, finalizer) {
					stillHasFinalizers = true
					break
				}
			}

			if !stillHasFinalizers {
				// log.Info("Finalizers removed successfully", "object", obj.GetName())
				return nil
			}

			// Wait for the next tick or timeout
			select {
			case <-timeout:
				// log.Warn("Timeout while waiting for finalizers to be removed", "object", obj.GetName())
				return errors.New("timeout while waiting for finalizers to be removed")
			case <-ticker.C:
				// Next iteration after the ticker interval
			}
		}
	})
}

// HasFinalizer tests whether k8s object has specified finalizer
func HasFinalizer(obj metav1.Object, finalizer string) bool {
	f := obj.GetFinalizers()
	for _, e := range f {
		if e == finalizer {
			return true
		}
	}
	return false
}
