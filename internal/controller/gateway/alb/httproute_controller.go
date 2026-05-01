package alb

import (
	"context"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/errs"
	metricsutil "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/metrics/util"
)

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/status,verbs=update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=referencegrants,verbs=get;list;watch

// HTTPRouteReconciler is intentionally thin: it does not write to vngcloud directly.
// Every HTTPRoute change enqueues the parent Gateway via the use-case so all LB
// mutations stay serialized through one writer.
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&gwv1.HTTPRoute{}).
		Named("httproute-alb").
		Complete(r)
}

func (r *HTTPRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.ReconcileCounters.IncrementHTTPRoute(req.NamespacedName)
	ctx = contexts.NewContext(ctx).SetLogName("httproute/" + req.Namespace + "/" + req.Name).GetContext()
	logger := contexts.NewContext(ctx).Log()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return errs.HandleReconcileError(r.GatewayUseCase.EnqueueParentGatewayForRoute(ctx, req), logger)
}
