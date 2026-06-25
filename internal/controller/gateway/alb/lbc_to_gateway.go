package alb

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// lbcOwnerToGateway enqueues the Gateway that owns a LoadBalancerConfig when
// the LBC changes. The Gateway use case mirrors LBC.Status into Gateway.Status
// (Phase F); this watch is what triggers that mirroring.
//
// Match criterion: LBC has the labels we set during creation
//
//	vks.vngcloud.vn/owner-resource-kind=Gateway
//	vks.vngcloud.vn/owner-resource-name=<gw-name>
//
// We use the labels (not ownerReferences) because the existing pattern in
// ingress_uc explicitly omits ownerReferences so that users can keep the LBC
// alive after deleting the Ingress. We follow the same convention here.
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
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: lbc.Namespace,
			Name:      gwName,
		}})
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
