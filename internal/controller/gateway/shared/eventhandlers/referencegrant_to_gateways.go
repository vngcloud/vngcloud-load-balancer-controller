package eventhandlers

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ReferenceGrantToGateways enqueues every Gateway when a ReferenceGrant
// changes. A grant in the backend's namespace can newly permit (or revoke) a
// cross-namespace backendRef, so the owning Gateway must re-resolve its pools.
// Mapping a grant to exactly the affected Gateways would need a cross-namespace
// reverse index; enqueuing all Gateways is correct and bounded (non-ALB
// Gateways no-op in the use case).
func ReferenceGrantToGateways(c client.Client) handler.EventHandler {
	enq := func(ctx context.Context, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		var gws gwv1.GatewayList
		if err := c.List(ctx, &gws); err != nil {
			return
		}
		for i := range gws.Items {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: gws.Items[i].Namespace, Name: gws.Items[i].Name,
			}})
		}
	}
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, _ event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, q)
		},
		UpdateFunc: func(ctx context.Context, _ event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, q)
		},
		DeleteFunc: func(ctx context.Context, _ event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enq(ctx, q)
		},
	}
}
