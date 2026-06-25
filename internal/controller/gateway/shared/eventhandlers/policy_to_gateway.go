package eventhandlers

import (
	"context"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// VKSGatewayPolicyToGateway enqueues the Gateways named in the policy's
// targetRefs. The Gateway use case re-resolves the policy's effective
// LB-level + listener-level fields on every reconcile, so a policy edit
// only needs to reach the Gateway controller's queue to take effect.
func VKSGatewayPolicyToGateway() handler.EventHandler {
	enq := func(_ context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		p, ok := obj.(*vksv1.VKSGatewayPolicy)
		if !ok {
			return
		}
		for _, t := range p.Spec.TargetRefs {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: p.Namespace, Name: string(t.Name)}})
		}
	}
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, e.Object, q)
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, e.ObjectNew, q)
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, e.Object, q)
		},
	}
}

// VKSBackendPolicyToGateway and VKSHealthCheckPolicyToGateway both target
// Services. To enqueue affected Gateways: list HTTPRoutes whose backendRefs
// reference the Service, then enqueue the parent Gateway of each. Reuses the
// IndexHTTPRouteByService field index registered by shared.RegisterIndexes.
func VKSBackendPolicyToGateway(c client.Client) handler.EventHandler {
	return policyByServiceTargetHandler[*vksv1.VKSBackendPolicy](c, func(obj client.Object) []vksv1.LocalPolicyTargetReference {
		p := obj.(*vksv1.VKSBackendPolicy)
		return p.Spec.TargetRefs
	}, func(obj client.Object) string {
		return obj.(*vksv1.VKSBackendPolicy).Namespace
	})
}

func VKSHealthCheckPolicyToGateway(c client.Client) handler.EventHandler {
	return policyByServiceTargetHandler[*vksv1.VKSHealthCheckPolicy](c, func(obj client.Object) []vksv1.LocalPolicyTargetReference {
		p := obj.(*vksv1.VKSHealthCheckPolicy)
		return p.Spec.TargetRefs
	}, func(obj client.Object) string {
		return obj.(*vksv1.VKSHealthCheckPolicy).Namespace
	})
}

// VKSRoutePolicyToGateway targets HTTPRoutes — enqueue parent Gateways of
// each named route.
func VKSRoutePolicyToGateway(c client.Client) handler.EventHandler {
	enq := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		p, ok := obj.(*vksv1.VKSRoutePolicy)
		if !ok {
			return
		}
		for _, t := range p.Spec.TargetRefs {
			var route gwv1.HTTPRoute
			if err := c.Get(ctx, types.NamespacedName{Namespace: p.Namespace, Name: string(t.Name)}, &route); err != nil {
				continue
			}
			enqueueRouteParents(&route, q)
		}
	}
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, e.Object, q)
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, e.ObjectNew, q)
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, e.Object, q)
		},
	}
}

// policyByServiceTargetHandler builds a handler.EventHandler that, given a
// policy whose targetRefs name Services, lists HTTPRoutes referencing those
// Services and enqueues their parent Gateways.
func policyByServiceTargetHandler[P client.Object](c client.Client, refs func(client.Object) []vksv1.LocalPolicyTargetReference, ns func(client.Object) string) handler.EventHandler {
	enq := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		policyNS := ns(obj)
		for _, t := range refs(obj) {
			var routes gwv1.HTTPRouteList
			sel := fields.OneTermEqualSelector(shared.IndexHTTPRouteByService, policyNS+"/"+string(t.Name))
			if err := c.List(ctx, &routes,
				client.InNamespace(policyNS),
				client.MatchingFieldsSelector{Selector: sel},
			); err != nil {
				continue
			}
			for i := range routes.Items {
				enqueueRouteParents(&routes.Items[i], q)
			}
		}
	}
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, e.Object, q)
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, e.ObjectNew, q)
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, e.Object, q)
		},
	}
}

func enqueueRouteParents(r *gwv1.HTTPRoute, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	for _, p := range r.Spec.ParentRefs {
		if p.Kind != nil && *p.Kind != gatewayKind {
			continue
		}
		ns := r.Namespace
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: string(p.Name)}})
	}
}
