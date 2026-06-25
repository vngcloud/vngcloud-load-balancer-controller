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

func RouteToGateway() handler.EventHandler {
	enq := func(_ context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		r, ok := obj.(*gwv1.HTTPRoute)
		if !ok {
			return
		}
		for _, p := range r.Spec.ParentRefs {
			if p.Kind != nil && *p.Kind != "Gateway" {
				continue
			}
			ns := r.Namespace
			if p.Namespace != nil {
				ns = string(*p.Namespace)
			}
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: string(p.Name)}})
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
