package alb

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// fakeQueue captures enqueued reconcile requests without needing a real queue.
type fakeQueue struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	items []reconcile.Request
}

func (q *fakeQueue) Add(item reconcile.Request) { q.items = append(q.items, item) }

// lbcWithLabels builds an LBC with the given owner labels.
func lbcWithLabels(ns, name string, labels map[string]string) *v1alpha1.LoadBalancerConfig {
	return &v1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    labels,
		},
	}
}

func TestLBCOwnerToGateway_CreateEnqueuesGateway(t *testing.T) {
	lbc := lbcWithLabels("mynamespace", "my-lbc", map[string]string{
		domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
		domain.LabelOwnerResourceName: "my-gateway",
	})
	h := lbcOwnerToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: lbc}, q)

	if len(q.items) != 1 {
		t.Fatalf("expected 1 enqueued item, got %d", len(q.items))
	}
	want := types.NamespacedName{Namespace: "mynamespace", Name: "my-gateway"}
	if q.items[0].NamespacedName != want {
		t.Errorf("enqueued request: got %v, want %v", q.items[0].NamespacedName, want)
	}
}

func TestLBCOwnerToGateway_UpdateEnqueuesGateway(t *testing.T) {
	lbc := lbcWithLabels("ns", "lbc-upd", map[string]string{
		domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
		domain.LabelOwnerResourceName: "gw-updated",
	})
	h := lbcOwnerToGateway()
	q := &fakeQueue{}
	// UpdateFunc uses ObjectNew
	h.Update(context.Background(), event.UpdateEvent{
		ObjectOld: lbcWithLabels("ns", "lbc-upd", map[string]string{}),
		ObjectNew: lbc,
	}, q)

	if len(q.items) != 1 {
		t.Fatalf("expected 1 enqueued item, got %d", len(q.items))
	}
	want := types.NamespacedName{Namespace: "ns", Name: "gw-updated"}
	if q.items[0].NamespacedName != want {
		t.Errorf("enqueued request: got %v, want %v", q.items[0].NamespacedName, want)
	}
}

func TestLBCOwnerToGateway_DeleteEnqueuesGateway(t *testing.T) {
	lbc := lbcWithLabels("del-ns", "lbc-del", map[string]string{
		domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
		domain.LabelOwnerResourceName: "gw-deleted",
	})
	h := lbcOwnerToGateway()
	q := &fakeQueue{}
	h.Delete(context.Background(), event.DeleteEvent{Object: lbc}, q)

	if len(q.items) != 1 {
		t.Fatalf("expected 1 enqueued item, got %d", len(q.items))
	}
	want := types.NamespacedName{Namespace: "del-ns", Name: "gw-deleted"}
	if q.items[0].NamespacedName != want {
		t.Errorf("enqueued request: got %v, want %v", q.items[0].NamespacedName, want)
	}
}

func TestLBCOwnerToGateway_MissingOwnerKindLabel_NoEnqueue(t *testing.T) {
	// LBC has owner-resource-name but NOT owner-resource-kind=Gateway
	lbc := lbcWithLabels("ns", "lbc-nokind", map[string]string{
		domain.LabelOwnerResourceName: "some-gw",
	})
	h := lbcOwnerToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: lbc}, q)

	if len(q.items) != 0 {
		t.Errorf("expected 0 enqueued items, got %d", len(q.items))
	}
}

func TestLBCOwnerToGateway_WrongKindLabel_NoEnqueue(t *testing.T) {
	lbc := lbcWithLabels("ns", "lbc-wrongkind", map[string]string{
		domain.LabelOwnerResourceKind: "Ingress", // wrong kind
		domain.LabelOwnerResourceName: "some-gw",
	})
	h := lbcOwnerToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: lbc}, q)

	if len(q.items) != 0 {
		t.Errorf("expected 0 enqueued items, got %d", len(q.items))
	}
}

func TestLBCOwnerToGateway_MissingOwnerNameLabel_NoEnqueue(t *testing.T) {
	// LBC has kind=Gateway but missing owner-resource-name
	lbc := lbcWithLabels("ns", "lbc-noname", map[string]string{
		domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
		// No LabelOwnerResourceName
	})
	h := lbcOwnerToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: lbc}, q)

	if len(q.items) != 0 {
		t.Errorf("expected 0 enqueued items, got %d", len(q.items))
	}
}

func TestLBCOwnerToGateway_WrongObjectType_NoEnqueue(t *testing.T) {
	// A non-LBC object (e.g. a Service) should be skipped
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-svc",
			Namespace: "ns",
			Labels: map[string]string{
				domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
				domain.LabelOwnerResourceName: "some-gw",
			},
		},
	}
	h := lbcOwnerToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: svc}, q)

	if len(q.items) != 0 {
		t.Errorf("expected 0 enqueued items for non-LBC object, got %d", len(q.items))
	}
}
