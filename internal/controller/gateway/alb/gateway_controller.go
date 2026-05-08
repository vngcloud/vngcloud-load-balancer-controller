package alb

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
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
	// Watch the LBCs we own so the Gateway re-reconciles when the LBC controller
	// asynchronously fills in Status.Address (vngcloud LB warm-up). Without this,
	// status.addresses on the Gateway never updates after its first reconcile.
	lbcToGateway := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		lbc, ok := obj.(*v1alpha1.LoadBalancerConfig)
		if !ok || lbc.Labels == nil {
			return nil
		}
		if lbc.Labels[domain.LabelOwnerResourceKind] != domain.KindGateway {
			return nil
		}
		name := lbc.Labels[domain.LabelOwnerResourceName]
		if name == "" {
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: lbc.Namespace, Name: name}}}
	})
	lbcPred := predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool { return true },
		DeleteFunc: func(_ event.DeleteEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldLbc, ok1 := e.ObjectOld.(*v1alpha1.LoadBalancerConfig)
			newLbc, ok2 := e.ObjectNew.(*v1alpha1.LoadBalancerConfig)
			if !ok1 || !ok2 {
				return false
			}
			return !equality.Semantic.DeepEqual(oldLbc.Status.Address, newLbc.Status.Address)
		},
		GenericFunc: func(_ event.GenericEvent) bool { return false },
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&gwv1.Gateway{}).
		Watches(&v1alpha1.LoadBalancerConfig{}, lbcToGateway, builder.WithPredicates(lbcPred)).
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

	gw := &gwv1.Gateway{}
	if err := r.Client.Get(ctx, req.NamespacedName, gw); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only manage finalizers for Gateways that belong to our class. Foreign Gateways
	// (different controllerName, or class missing) are ignored — adding our finalizer
	// to them would block deletion forever.
	gwc := &gwv1.GatewayClass{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: string(gw.Spec.GatewayClassName)}, gwc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if string(gwc.Spec.ControllerName) != domain.ControllerNameALB {
		return ctrl.Result{}, nil
	}

	if !gw.DeletionTimestamp.IsZero() {
		// Delete path: tear down resources, then remove finalizer.
		if err := r.GatewayUseCase.DeleteALBGatewayUseCase(ctx, req); err != nil {
			return errs.HandleReconcileError(err, logger)
		}
		if err := r.FinalizerManager.RemoveFinalizers(ctx, gw, domain.GatewayFinalizer); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if err := r.FinalizerManager.AddFinalizers(ctx, gw, domain.GatewayFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	return errs.HandleReconcileError(r.GatewayUseCase.EnsureALBGatewayUseCase(ctx, req), logger)
}
