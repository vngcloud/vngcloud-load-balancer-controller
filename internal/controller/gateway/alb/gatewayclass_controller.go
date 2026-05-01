// Package alb implements the vngcloud-alb GatewayClass reconcilers (GatewayClass,
// Gateway, HTTPRoute). The Gateway reconciler is the only writer to vngcloud; the
// HTTPRoute reconciler delegates to ALBGatewayUseCase.EnqueueParentGatewayForRoute.
package alb

import (
	"context"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	ctlshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses/status,verbs=update;patch
// +kubebuilder:rbac:groups=vks.vngcloud.vn,resources=loadbalancerconfigs,verbs=get;list;watch

// GatewayClassReconciler validates that referenced LoadBalancerConfig parameters
// exist and reports Accepted on the GatewayClass status.
type GatewayClassReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func NewGatewayClassReconciler(c client.Client, sch *runtime.Scheme) *GatewayClassReconciler {
	return &GatewayClassReconciler{Client: c, Scheme: sch}
}

func (r *GatewayClassReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gwv1.GatewayClass{}).
		Named("gatewayclass-alb").
		Complete(r)
}

func (r *GatewayClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx = contexts.NewContext(ctx).SetLogName("gwc/" + req.Name).GetContext()
	logger := contexts.NewContext(ctx).Log()

	gwc := &gwv1.GatewayClass{}
	if err := r.Get(ctx, req.NamespacedName, gwc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if string(gwc.Spec.ControllerName) != domain.ControllerNameALB {
		return ctrl.Result{}, nil
	}

	accepted := metav1.ConditionTrue
	reason, msg := string(gwv1.GatewayClassReasonAccepted), "GatewayClass accepted by vngcloud-alb controller"

	if gwc.Spec.ParametersRef != nil {
		ref := gwc.Spec.ParametersRef
		switch {
		case string(ref.Group) != "vks.vngcloud.vn" || string(ref.Kind) != "LoadBalancerConfig":
			accepted = metav1.ConditionFalse
			reason, msg = string(gwv1.GatewayClassReasonInvalidParameters),
				"parametersRef must point to vks.vngcloud.vn/LoadBalancerConfig"
		default:
			lbc := &vksv1alpha1.LoadBalancerConfig{}
			ns := ""
			if ref.Namespace != nil {
				ns = string(*ref.Namespace)
			}
			if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: string(ref.Name)}, lbc); err != nil {
				accepted = metav1.ConditionFalse
				reason, msg = string(gwv1.GatewayClassReasonInvalidParameters),
					"referenced LoadBalancerConfig not found: "+err.Error()
			}
		}
	}

	ctlshared.SetCondition(&gwc.Status.Conditions, string(gwv1.GatewayClassConditionStatusAccepted), accepted, reason, msg, gwc.Generation)
	if err := r.Status().Update(ctx, gwc); err != nil {
		logger.Errorf("status update failed: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}
	return ctrl.Result{}, nil
}
