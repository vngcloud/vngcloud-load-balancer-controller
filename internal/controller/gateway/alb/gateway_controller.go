package alb

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/errs"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
	metricsutil "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/metrics/util"
)

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/status,verbs=update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/finalizers,verbs=update

// GatewayReconciler delegates all reconciliation logic to ALBGatewayUseCase. The
// reconciler itself only handles init gating, metrics increment, and timeout/requeue
// translation — keeping the reconciler thin matches the existing Ingress pattern.
type GatewayReconciler struct {
	Client            client.Client
	Scheme            *runtime.Scheme
	GatewayUseCase    usecase.ALBGatewayUseCase
	FinalizerManager  k8s.FinalizerManager
	EventRecorder     record.EventRecorder
	Logger            logr.Logger
	ReconcileCounters *metricsutil.ReconcileCounters
	MaxConcurrent     int

	initDone atomic.Bool
}

func NewGatewayReconciler(
	uc usecase.ALBGatewayUseCase,
	c client.Client,
	sch *runtime.Scheme,
	fm k8s.FinalizerManager,
	er record.EventRecorder,
	rc *metricsutil.ReconcileCounters,
	maxConcurrent int,
) *GatewayReconciler {
	if maxConcurrent <= 0 {
		maxConcurrent = domain.DefaultMaxConcurrentReconciles
	}
	return &GatewayReconciler{
		Client:            c,
		Scheme:            sch,
		GatewayUseCase:    uc,
		FinalizerManager:  fm,
		EventRecorder:     er,
		Logger:            ctrl.Log.WithName("controllers").WithName("alb-gateway"),
		ReconcileCounters: rc,
		MaxConcurrent:     maxConcurrent,
	}
}

func (r *GatewayReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gwv1.Gateway{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrent}).
		Named("gateway-alb").
		Complete(r)
}

func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !r.initDone.Load() {
		if err := r.GatewayUseCase.InitALBGatewayUseCase(ctx); err != nil {
			r.Logger.Error(err, "init failed; requeuing")
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		r.initDone.Store(true)
	}

	r.ReconcileCounters.IncrementGateway(req.NamespacedName)
	ctx = contexts.NewContext(ctx).SetLogName("gw/" + req.Namespace + "/" + req.Name).GetContext()
	logger := contexts.NewContext(ctx).Log()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	return errs.HandleReconcileError(r.GatewayUseCase.EnsureALBGatewayUseCase(ctx, req), logger)
}
