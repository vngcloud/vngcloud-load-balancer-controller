package alb

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase"
	metricsutil "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/metrics/util"
)

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/status,verbs=update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=referencegrants,verbs=get;list;watch

// HTTPRouteReconciler exists only to bump the per-route reconcile metric.
// All routing-relevant behavior lives on the Gateway controller, which
// directly Watches HTTPRoutes (see gateway_controller.go SetupWithManager).
//
// Earlier revisions of this reconciler PATCHed a route-revision annotation
// on the parent Gateway to force a reconcile; that produced one API write
// per route per startup, which was wasted I/O. The direct Watches replaced
// it with an in-memory workqueue add.
type HTTPRouteReconciler struct {
	Client            client.Client
	Scheme            *runtime.Scheme
	GatewayUseCase    usecase.ALBGatewayUseCase
	ReconcileCounters *metricsutil.ReconcileCounters
}

func NewHTTPRouteReconciler(
	uc usecase.ALBGatewayUseCase,
	c client.Client,
	sch *runtime.Scheme,
	rc *metricsutil.ReconcileCounters,
) *HTTPRouteReconciler {
	return &HTTPRouteReconciler{Client: c, Scheme: sch, GatewayUseCase: uc, ReconcileCounters: rc}
}

func (r *HTTPRouteReconciler) SetupWithManager(mgr manager.Manager) error {
	// GenerationChangedPredicate filters out status-only updates so we
	// don't increment the metric on our own status writes.
	return ctrl.NewControllerManagedBy(mgr).
		For(&gwv1.HTTPRoute{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("httproute-alb").
		Complete(r)
}

func (r *HTTPRouteReconciler) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.ReconcileCounters.IncrementHTTPRoute(req.NamespacedName)
	return ctrl.Result{}, nil
}
