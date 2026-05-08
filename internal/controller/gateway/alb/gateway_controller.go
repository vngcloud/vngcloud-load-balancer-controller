package alb

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
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
	"k8s.io/client-go/util/workqueue"
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
	// Watch Services so a Service created (or deleted, or port-edited) AFTER
	// an HTTPRoute that referenced it triggers a Gateway reconcile. Without
	// this, route.status.parents[].conditions[ResolvedRefs] stays stale at
	// BackendNotFound and the LB pool never picks up the now-resolvable
	// backend. Map fn fan-outs to every vngcloud-alb Gateway whose attached
	// HTTPRoutes reference this Service.
	serviceToGateways := handler.EnqueueRequestsFromMapFunc(r.mapServiceToGateways)
	servicePred := predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool { return true },
		DeleteFunc: func(_ event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldS, ok1 := e.ObjectOld.(*corev1.Service)
			newS, ok2 := e.ObjectNew.(*corev1.Service)
			if !ok1 || !ok2 {
				return false
			}
			return !equality.Semantic.DeepEqual(oldS.Spec, newS.Spec)
		},
		GenericFunc: func(_ event.GenericEvent) bool { return false },
	}
	// Watch HTTPRoutes directly. On Update we map BOTH old and new parentRefs
	// so a route that changes parent (gw-A → gw-B) reconciles A (to prune
	// orphan pools) and B (to attach the new route). Replaces the older
	// pattern of bumping a route-revision annotation on the gateway, which
	// produced needless PATCH writes — every event flows through the
	// in-memory workqueue (deduped by key).
	routeHandler := handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			for _, req := range r.mapHTTPRouteToGateways(ctx, e.Object) {
				q.Add(req)
			}
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if e.ObjectOld.GetGeneration() == e.ObjectNew.GetGeneration() {
				return
			}
			for _, req := range r.mapHTTPRouteToGateways(ctx, e.ObjectOld) {
				q.Add(req)
			}
			for _, req := range r.mapHTTPRouteToGateways(ctx, e.ObjectNew) {
				q.Add(req)
			}
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			for _, req := range r.mapHTTPRouteToGateways(ctx, e.Object) {
				q.Add(req)
			}
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&gwv1.Gateway{}).
		Watches(&v1alpha1.LoadBalancerConfig{}, lbcToGateway, builder.WithPredicates(lbcPred)).
		Watches(&corev1.Service{}, serviceToGateways, builder.WithPredicates(servicePred)).
		Watches(&gwv1.HTTPRoute{}, routeHandler).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrent}).
		Named("gateway-alb").
		Complete(r)
}

// mapHTTPRouteToGateways extracts parentRefs from an HTTPRoute object and
// returns reconcile requests for each parent Gateway whose GatewayClass is
// vngcloud-alb. Used by the HTTPRoute Watches handler to enqueue parent
// gateways without needing the older annotation-bump pattern.
func (r *GatewayReconciler) mapHTTPRouteToGateways(ctx context.Context, obj client.Object) []reconcile.Request {
	rt, ok := obj.(*gwv1.HTTPRoute)
	if !ok {
		return nil
	}
	seen := map[types.NamespacedName]struct{}{}
	out := []reconcile.Request{}
	for _, p := range rt.Spec.ParentRefs {
		if !isGatewayParentRef(p) {
			continue
		}
		ns := rt.Namespace
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		key := types.NamespacedName{Namespace: ns, Name: string(p.Name)}
		if _, dup := seen[key]; dup {
			continue
		}
		gw := &gwv1.Gateway{}
		if err := r.Client.Get(ctx, key, gw); err != nil {
			continue
		}
		gwc := &gwv1.GatewayClass{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: string(gw.Spec.GatewayClassName)}, gwc); err != nil {
			continue
		}
		if string(gwc.Spec.ControllerName) != domain.ControllerNameALB {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, reconcile.Request{NamespacedName: key})
	}
	return out
}

// mapServiceToGateways returns reconcile requests for every vngcloud-alb
// Gateway whose attached HTTPRoutes reference the changed Service as a
// backendRef. The function tolerates list/get errors silently (returning
// fewer requests is safer than panicking inside an event handler).
func (r *GatewayReconciler) mapServiceToGateways(ctx context.Context, obj client.Object) []reconcile.Request {
	svc, ok := obj.(*corev1.Service)
	if !ok {
		return nil
	}
	routes := &gwv1.HTTPRouteList{}
	if err := r.Client.List(ctx, routes); err != nil {
		return nil
	}
	seen := map[types.NamespacedName]struct{}{}
	out := []reconcile.Request{}
	for i := range routes.Items {
		rt := &routes.Items[i]
		if !routeReferencesService(rt, svc.Namespace, svc.Name) {
			continue
		}
		for _, p := range rt.Spec.ParentRefs {
			if !isGatewayParentRef(p) {
				continue
			}
			ns := rt.Namespace
			if p.Namespace != nil {
				ns = string(*p.Namespace)
			}
			key := types.NamespacedName{Namespace: ns, Name: string(p.Name)}
			if _, dup := seen[key]; dup {
				continue
			}
			gw := &gwv1.Gateway{}
			if err := r.Client.Get(ctx, key, gw); err != nil {
				continue
			}
			gwc := &gwv1.GatewayClass{}
			if err := r.Client.Get(ctx, types.NamespacedName{Name: string(gw.Spec.GatewayClassName)}, gwc); err != nil {
				continue
			}
			if string(gwc.Spec.ControllerName) != domain.ControllerNameALB {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, reconcile.Request{NamespacedName: key})
		}
	}
	return out
}

// routeReferencesService reports whether any backendRef in route names the
// given Service (group=core, kind=Service or unset). Cross-namespace refs
// are honored: a backendRef with explicit Namespace points to that Service
// regardless of the route's own namespace.
func routeReferencesService(route *gwv1.HTTPRoute, svcNS, svcName string) bool {
	for _, rule := range route.Spec.Rules {
		for _, backend := range rule.BackendRefs {
			ref := backend.BackendObjectReference
			if ref.Group != nil && *ref.Group != "" {
				continue
			}
			if ref.Kind != nil && *ref.Kind != "" && *ref.Kind != "Service" {
				continue
			}
			ns := route.Namespace
			if ref.Namespace != nil {
				ns = string(*ref.Namespace)
			}
			if ns == svcNS && string(ref.Name) == svcName {
				return true
			}
		}
	}
	return false
}

// isGatewayParentRef returns true when the parentRef points at a Gateway
// (group=gateway.networking.k8s.io, kind=Gateway). Both Group and Kind are
// optional; the Gateway-API default is Gateway when unset.
func isGatewayParentRef(p gwv1.ParentReference) bool {
	if p.Group != nil && *p.Group != "" && string(*p.Group) != gwv1.GroupName {
		return false
	}
	if p.Kind != nil && *p.Kind != "" && string(*p.Kind) != "Gateway" {
		return false
	}
	return true
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
