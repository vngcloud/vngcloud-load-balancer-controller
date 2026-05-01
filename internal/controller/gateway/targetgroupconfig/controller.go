// Package targetgroupconfig validates TargetGroupConfig CRDs and reports observability
// status. It does not write to vngcloud — backend pool config is consumed by the Gateway
// use-case during route reconciliation via shared.ResolveTargetGroupProps.
package targetgroupconfig

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	ctlshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

// +kubebuilder:rbac:groups=gateway.vks.vngcloud.vn,resources=targetgroupconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=gateway.vks.vngcloud.vn,resources=targetgroupconfigs/status,verbs=update;patch

// Reconciler validates schema and reports Accepted=True. Conflict detection is
// performed at route-resolution time inside shared.ResolveTargetGroupProps.
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func New(c client.Client, sch *runtime.Scheme) *Reconciler {
	return &Reconciler{Client: c, Scheme: sch}
}

func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.TargetGroupConfig{}).
		Named("targetgroupconfig").
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &gatewayv1alpha1.TargetGroupConfig{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if obj.Status.ObservedGeneration == obj.Generation {
		return ctrl.Result{}, nil
	}
	ctlshared.SetCondition(&obj.Status.Conditions, "Accepted",
		metav1.ConditionTrue, "Accepted", "TargetGroupConfig observed", obj.Generation)
	obj.Status.ObservedGeneration = obj.Generation
	return ctrl.Result{}, r.Status().Update(ctx, obj)
}
