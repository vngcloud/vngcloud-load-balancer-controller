// Package nlb hosts the reconciler for Gateway-API Gateway objects under the
// vngcloud-nlb GatewayClass (Phase 2, L4). It is the thin sibling of the alb
// package: init gating, queue plumbing, and event filtering only. All
// Gateway → L4 LoadBalancerConfig translation lives in
// internal/usecase/gateway_uc/nlb_gateway_uc.
//
// Note: the TCPRoute/UDPRoute CRDs live in the Gateway-API *experimental*
// channel and are a deployment prerequisite. This controller is disabled by
// default (--disable-nlb-gateway-controller) so clusters without those CRDs
// don't fail to start a route watch.
package nlb

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
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
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	gwshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	gwhandlers "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared/eventhandlers"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/errs"
)

const nlbControllerName = "gateway-nlb"

// GatewayReconciler reconciles Gateway objects under the vngcloud-nlb class.
type GatewayReconciler struct {
	k8sClient client.Client
	scheme    *runtime.Scheme
	useCase   usecase.NLBGatewayUseCase

	initDone                atomic.Bool
	maxConcurrentReconciles int
}

func NewGatewayReconciler(
	k8sClient client.Client,
	scheme *runtime.Scheme,
	useCase usecase.NLBGatewayUseCase,
	maxConcurrentReconciles int,
) *GatewayReconciler {
	return &GatewayReconciler{
		k8sClient: k8sClient,
		scheme:    scheme,
		useCase:   useCase,
		maxConcurrentReconciles: func() int {
			if maxConcurrentReconciles > 0 {
				return maxConcurrentReconciles
			}
			return domain.DefaultMaxConcurrentReconciles
		}(),
	}
}

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/status,verbs=update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tcproutes;udproutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tcproutes/status;udproutes/status,verbs=update;patch
// +kubebuilder:rbac:groups=gateway.vks.vngcloud.vn,resources=vksgatewaypolicies;vksbackendpolicies;vkshealthcheckpolicies;vksroutepolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=vks.vngcloud.vn,resources=loadbalancerconfigs,verbs=get;list;watch;create;update;patch;delete

func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !r.initDone.Load() {
		ctrl.Log.Info("NLB Gateway init not done yet, requeueing...")
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	ctx = contexts.NewContext(ctx).
		SetLogName("nlb-gw/" + req.Namespace + "/" + req.Name).
		GetContext()
	logger := contexts.NewContext(ctx).Log()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	return errs.HandleReconcileError(r.useCase.EnsureNLBGatewayUseCase(ctx, req), logger)
}

func (r *GatewayReconciler) SetupWithManager(ctx context.Context, mgr manager.Manager) error {
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		log := ctrl.Log.WithName("init").WithName(nlbControllerName)
		log.Info("Running initialization...")

		const maxAttempts = 5
		const backoff = 2 * time.Second
		var lastErr error
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			lastErr = r.useCase.InitNLBGatewayUseCase(ctx)
			if lastErr == nil {
				log.Info("Initialization complete", "attempt", attempt)
				r.initDone.Store(true)
				return nil
			}
			log.Error(lastErr, "Initialization failed; retrying", "attempt", attempt, "max", maxAttempts)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
		log.Error(lastErr, "Fatal: initialization failed after retries")
		return lastErr
	})); err != nil {
		return err
	}

	// L4 route field indexes (TCP/UDP → parent Gateway, → backend Service).
	// Registered here (not by the ALB block) so the NLB path is self-contained.
	if err := gwshared.RegisterL4Indexes(ctx, mgr); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named(nlbControllerName).
		For(&gwv1.Gateway{}, builder.WithPredicates(nlbClassPredicate(r.k8sClient))).
		Watches(&gwv1a2.TCPRoute{}, gwhandlers.TCPRouteToGateway()).
		Watches(&gwv1a2.UDPRoute{}, gwhandlers.UDPRouteToGateway()).
		Watches(&gwv1alpha1.VKSGatewayPolicy{}, gwhandlers.VKSGatewayPolicyToGateway()).
		Watches(&gwv1alpha1.VKSBackendPolicy{}, gwhandlers.VKSBackendPolicyToL4Gateway(r.k8sClient)).
		Watches(&gwv1alpha1.VKSHealthCheckPolicy{}, gwhandlers.VKSHealthCheckPolicyToL4Gateway(r.k8sClient)).
		Watches(&v1alpha1.LoadBalancerConfig{}, lbcOwnerToGateway()).
		Watches(&corev1.Service{}, gwhandlers.ServiceToL4RouteParents(r.k8sClient)).
		Watches(&gwv1beta1.ReferenceGrant{}, gwhandlers.ReferenceGrantToGateways(r.k8sClient)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.maxConcurrentReconciles}).
		Complete(r)
}

// nlbClassPredicate keeps only Gateways bound to a GatewayClass whose
// ControllerName is the NLB one.
func nlbClassPredicate(k8sClient client.Client) predicate.Predicate {
	matches := func(o client.Object) bool {
		gw, ok := o.(*gwv1.Gateway)
		if !ok || gw.Spec.GatewayClassName == "" {
			return false
		}
		gc := &gwv1.GatewayClass{}
		if err := k8sClient.Get(context.Background(),
			client.ObjectKey{Name: string(gw.Spec.GatewayClassName)}, gc); err != nil {
			return false
		}
		return string(gc.Spec.ControllerName) == consts.GatewayClassControllerNameNLB
	}
	return predicate.NewPredicateFuncs(matches)
}

// lbcOwnerToGateway enqueues the Gateway that owns an LBC when the LBC changes
// (mirrors LBC.Status into Gateway.Status).
func lbcOwnerToGateway() handler.EventHandler {
	enq := func(obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		lbc, ok := obj.(*v1alpha1.LoadBalancerConfig)
		if !ok {
			return
		}
		if lbc.Labels[domain.LabelOwnerResourceKind] != domain.OwnerKindGateway {
			return
		}
		gwName := lbc.Labels[domain.LabelOwnerResourceName]
		if gwName == "" {
			return
		}
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: lbc.Namespace, Name: gwName}})
	}
	return handler.Funcs{
		CreateFunc: func(_ context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(e.Object, q)
		},
		UpdateFunc: func(_ context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(e.ObjectNew, q)
		},
		DeleteFunc: func(_ context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(e.Object, q)
		},
	}
}
