package alb

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

// +kubebuilder:rbac.groups=gateway.networking.k8s.io,resources=gatewayclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses/status,verbs=patch;update

// GatewayClassReconciler accepts every GatewayClass whose ControllerName is
// the ALB one. It writes the Accepted condition only — no other side effects.
type GatewayClassReconciler struct {
	k8sClient client.Client
	scheme    *runtime.Scheme
}

func NewGatewayClassReconciler(k8sClient client.Client, scheme *runtime.Scheme) *GatewayClassReconciler {
	return &GatewayClassReconciler{k8sClient: k8sClient, scheme: scheme}
}

// Reconcile sets Accepted=True on the GatewayClass.
func (r *GatewayClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	gc := &gwv1.GatewayClass{}
	if err := r.k8sClient.Get(ctx, req.NamespacedName, gc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if string(gc.Spec.ControllerName) != consts.GatewayClassControllerNameALB {
		return ctrl.Result{}, nil
	}

	cur := gc.DeepCopy()
	accepted := metav1.Condition{
		Type:               string(gwv1.GatewayClassConditionStatusAccepted),
		Status:             metav1.ConditionTrue,
		Reason:             string(gwv1.GatewayClassReasonAccepted),
		Message:            "GatewayClass accepted by " + consts.GatewayClassControllerNameALB,
		ObservedGeneration: gc.Generation,
		LastTransitionTime: metav1.Now(),
	}
	shared.SetCondition(&cur.Status.Conditions, accepted)

	if conditionsEqualIgnoreTime(cur.Status.Conditions, gc.Status.Conditions) {
		return ctrl.Result{}, nil
	}
	if err := r.k8sClient.Status().Patch(ctx, cur, client.MergeFrom(gc)); err != nil {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}
	return ctrl.Result{}, nil
}

func (r *GatewayClassReconciler) SetupWithManager(_ context.Context, mgr manager.Manager) error {
	matches := func(o client.Object) bool {
		gc, ok := o.(*gwv1.GatewayClass)
		return ok && string(gc.Spec.ControllerName) == consts.GatewayClassControllerNameALB
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("gatewayclass-alb").
		For(&gwv1.GatewayClass{}, builder.WithPredicates(predicate.NewPredicateFuncs(matches))).
		Complete(r)
}

func conditionsEqualIgnoreTime(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}
	idx := make(map[string]metav1.Condition, len(b))
	for i := range b {
		idx[b[i].Type] = b[i]
	}
	for i := range a {
		other, ok := idx[a[i].Type]
		if !ok {
			return false
		}
		if a[i].Status != other.Status || a[i].Reason != other.Reason ||
			a[i].Message != other.Message || a[i].ObservedGeneration != other.ObservedGeneration {
			return false
		}
	}
	return true
}
