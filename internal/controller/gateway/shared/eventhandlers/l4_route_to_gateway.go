package eventhandlers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

const gatewayKind = "Gateway"

// enqueueParentGateways adds a reconcile request for every parentRef of kind
// Gateway on the route. Shared by the TCP/UDP route handlers.
func enqueueParentGateways(routeNS string, refs []gwv1.ParentReference, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	for _, p := range refs {
		if p.Kind != nil && *p.Kind != gatewayKind {
			continue
		}
		ns := routeNS
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: string(p.Name)}})
	}
}

// TCPRouteToGateway enqueues the parent Gateway(s) of a changed TCPRoute.
func TCPRouteToGateway() handler.EventHandler {
	enq := func(_ context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		if r, ok := obj.(*gwv1a2.TCPRoute); ok {
			enqueueParentGateways(r.Namespace, r.Spec.ParentRefs, q)
		}
	}
	return objToRequests(enq)
}

// UDPRouteToGateway enqueues the parent Gateway(s) of a changed UDPRoute.
func UDPRouteToGateway() handler.EventHandler {
	enq := func(_ context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		if r, ok := obj.(*gwv1a2.UDPRoute); ok {
			enqueueParentGateways(r.Namespace, r.Spec.ParentRefs, q)
		}
	}
	return objToRequests(enq)
}

// ServiceToL4RouteParents enqueues the parent Gateways of any TCPRoute/UDPRoute
// whose backendRefs name a changed Service (endpoint churn → re-resolve pool
// members). Mirrors ServiceToRouteParents for the L4 path.
func ServiceToL4RouteParents(c client.Client) handler.EventHandler {
	enq := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		svc, ok := obj.(*corev1.Service)
		if !ok {
			return
		}
		key := svc.Namespace + "/" + svc.Name

		var tcp gwv1a2.TCPRouteList
		if err := c.List(ctx, &tcp,
			client.InNamespace(svc.Namespace),
			client.MatchingFieldsSelector{Selector: fields.OneTermEqualSelector(shared.IndexTCPRouteByService, key)},
		); err == nil {
			for i := range tcp.Items {
				enqueueParentGateways(tcp.Items[i].Namespace, tcp.Items[i].Spec.ParentRefs, q)
			}
		}

		var udp gwv1a2.UDPRouteList
		if err := c.List(ctx, &udp,
			client.InNamespace(svc.Namespace),
			client.MatchingFieldsSelector{Selector: fields.OneTermEqualSelector(shared.IndexUDPRouteByService, key)},
		); err == nil {
			for i := range udp.Items {
				enqueueParentGateways(udp.Items[i].Namespace, udp.Items[i].Spec.ParentRefs, q)
			}
		}
	}
	return objToRequests(enq)
}

// VKSBackendPolicyToL4Gateway enqueues parent Gateways of any TCP/UDP route
// whose backendRefs name a Service the policy targets. L4 sibling of
// VKSBackendPolicyToGateway (which uses HTTPRoute indexes).
func VKSBackendPolicyToL4Gateway(c client.Client) handler.EventHandler {
	return l4PolicyByServiceHandler(c, func(obj client.Object) (string, []string) {
		p, ok := obj.(*vksv1.VKSBackendPolicy)
		if !ok {
			return "", nil
		}
		return p.Namespace, targetNames(p.Spec.TargetRefs)
	})
}

// VKSHealthCheckPolicyToL4Gateway is the L4 sibling for health-check policies.
func VKSHealthCheckPolicyToL4Gateway(c client.Client) handler.EventHandler {
	return l4PolicyByServiceHandler(c, func(obj client.Object) (string, []string) {
		p, ok := obj.(*vksv1.VKSHealthCheckPolicy)
		if !ok {
			return "", nil
		}
		return p.Namespace, targetNames(p.Spec.TargetRefs)
	})
}

func targetNames(refs []vksv1.LocalPolicyTargetReference) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, string(r.Name))
	}
	return out
}

func l4PolicyByServiceHandler(c client.Client, extract func(client.Object) (string, []string)) handler.EventHandler {
	enq := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		ns, names := extract(obj)
		if ns == "" {
			return
		}
		for _, n := range names {
			key := ns + "/" + n
			var tcp gwv1a2.TCPRouteList
			if err := c.List(ctx, &tcp, client.InNamespace(ns),
				client.MatchingFieldsSelector{Selector: fields.OneTermEqualSelector(shared.IndexTCPRouteByService, key)}); err == nil {
				for i := range tcp.Items {
					enqueueParentGateways(tcp.Items[i].Namespace, tcp.Items[i].Spec.ParentRefs, q)
				}
			}
			var udp gwv1a2.UDPRouteList
			if err := c.List(ctx, &udp, client.InNamespace(ns),
				client.MatchingFieldsSelector{Selector: fields.OneTermEqualSelector(shared.IndexUDPRouteByService, key)}); err == nil {
				for i := range udp.Items {
					enqueueParentGateways(udp.Items[i].Namespace, udp.Items[i].Spec.ParentRefs, q)
				}
			}
		}
	}
	return objToRequests(enq)
}

// objToRequests wraps an enqueue function as a CRUD-symmetric EventHandler.
func objToRequests(enq func(context.Context, client.Object, workqueue.TypedRateLimitingInterface[reconcile.Request])) handler.EventHandler {
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
