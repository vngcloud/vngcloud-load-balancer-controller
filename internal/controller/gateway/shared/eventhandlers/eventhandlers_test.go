package eventhandlers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

// fakeQueue captures enqueued requests without needing a real work queue.
type fakeQueue struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	items []reconcile.Request
}

func (q *fakeQueue) Add(item reconcile.Request) { q.items = append(q.items, item) }

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = gwv1.Install(s)
	_ = vksv1.AddToScheme(s)
	return s
}

// routeWithParents constructs an HTTPRoute that declares the given parent Gateway names.
func routeWithParents(routeNS string, parents ...types.NamespacedName) *gwv1.HTTPRoute {
	r := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: routeNS},
	}
	for _, p := range parents {
		ref := gwv1.ParentReference{Name: gwv1.ObjectName(p.Name)}
		if p.Namespace != "" && p.Namespace != routeNS {
			ns := gwv1.Namespace(p.Namespace)
			ref.Namespace = &ns
		}
		r.Spec.ParentRefs = append(r.Spec.ParentRefs, ref)
	}
	return r
}

// ---- RouteToGateway ---------------------------------------------------------

func TestRouteToGateway_CreateEnqueuesParents(t *testing.T) {
	route := routeWithParents("ns",
		types.NamespacedName{Namespace: "ns", Name: "gw-a"},
		types.NamespacedName{Namespace: "other", Name: "gw-b"},
	)
	h := RouteToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: route}, q)
	assertRequests(t, q.items, []types.NamespacedName{
		{Namespace: "ns", Name: "gw-a"},
		{Namespace: "other", Name: "gw-b"},
	})
}

func TestRouteToGateway_UpdateEnqueuesNewObject(t *testing.T) {
	oldRoute := routeWithParents("ns", types.NamespacedName{Namespace: "ns", Name: "gw-old"})
	newRoute := routeWithParents("ns", types.NamespacedName{Namespace: "ns", Name: "gw-new"})
	h := RouteToGateway()
	q := &fakeQueue{}
	h.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRoute, ObjectNew: newRoute}, q)
	assertRequests(t, q.items, []types.NamespacedName{
		{Namespace: "ns", Name: "gw-new"},
	})
}

func TestRouteToGateway_DeleteEnqueuesParents(t *testing.T) {
	route := routeWithParents("ns", types.NamespacedName{Namespace: "ns", Name: "gw-a"})
	h := RouteToGateway()
	q := &fakeQueue{}
	h.Delete(context.Background(), event.DeleteEvent{Object: route}, q)
	assertRequests(t, q.items, []types.NamespacedName{
		{Namespace: "ns", Name: "gw-a"},
	})
}

func TestRouteToGateway_NonGatewayKindSkipped(t *testing.T) {
	knd := gwv1.Kind("Service")
	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "ns"},
	}
	route.Spec.ParentRefs = []gwv1.ParentReference{
		{Kind: &knd, Name: "svc-x"},
	}
	h := RouteToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: route}, q)
	if len(q.items) != 0 {
		t.Errorf("expected 0 enqueued items, got %d", len(q.items))
	}
}

func TestRouteToGateway_NonRouteObjectSkipped(t *testing.T) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"}}
	h := RouteToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: svc}, q)
	if len(q.items) != 0 {
		t.Errorf("expected 0 enqueued items, got %d", len(q.items))
	}
}

// ---- VKSGatewayPolicyToGateway ----------------------------------------------

func policyWithTargets(ns string, names ...string) *vksv1.VKSGatewayPolicy {
	refs := make([]vksv1.LocalPolicyTargetReferenceWithSectionName, len(names))
	for i, name := range names {
		refs[i] = vksv1.LocalPolicyTargetReferenceWithSectionName{
			LocalPolicyTargetReference: vksv1.LocalPolicyTargetReference{Name: gwv1.ObjectName(name)},
		}
	}
	return &vksv1.VKSGatewayPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "pol", Namespace: ns},
		Spec:       vksv1.VKSGatewayPolicySpec{TargetRefs: refs},
	}
}

func TestVKSGatewayPolicyToGateway_CreateEnqueues(t *testing.T) {
	pol := policyWithTargets("ns", "gw-a", "gw-b")
	h := VKSGatewayPolicyToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: pol}, q)
	assertRequests(t, q.items, []types.NamespacedName{
		{Namespace: "ns", Name: "gw-a"},
		{Namespace: "ns", Name: "gw-b"},
	})
}

func TestVKSGatewayPolicyToGateway_UpdateEnqueues(t *testing.T) {
	oldPol := policyWithTargets("ns", "gw-old")
	newPol := policyWithTargets("ns", "gw-new")
	h := VKSGatewayPolicyToGateway()
	q := &fakeQueue{}
	h.Update(context.Background(), event.UpdateEvent{ObjectOld: oldPol, ObjectNew: newPol}, q)
	assertRequests(t, q.items, []types.NamespacedName{
		{Namespace: "ns", Name: "gw-new"},
	})
}

func TestVKSGatewayPolicyToGateway_DeleteEnqueues(t *testing.T) {
	pol := policyWithTargets("ns", "gw-a")
	h := VKSGatewayPolicyToGateway()
	q := &fakeQueue{}
	h.Delete(context.Background(), event.DeleteEvent{Object: pol}, q)
	assertRequests(t, q.items, []types.NamespacedName{
		{Namespace: "ns", Name: "gw-a"},
	})
}

func TestVKSGatewayPolicyToGateway_NonPolicySkipped(t *testing.T) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"}}
	h := VKSGatewayPolicyToGateway()
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: svc}, q)
	if len(q.items) != 0 {
		t.Errorf("expected 0 items, got %d", len(q.items))
	}
}

// routeWithBackendAndParents builds an HTTPRoute in ns that refers to svcName as a backend
// and declares the given gateway names as parents.
func routeWithBackendAndParents(ns, svcName string, gatewayNames ...string) *gwv1.HTTPRoute {
	r := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: ns},
		Spec: gwv1.HTTPRouteSpec{
			Rules: []gwv1.HTTPRouteRule{
				{BackendRefs: []gwv1.HTTPBackendRef{
					{BackendRef: gwv1.BackendRef{BackendObjectReference: gwv1.BackendObjectReference{Name: gwv1.ObjectName(svcName)}}},
				}},
			},
		},
	}
	for _, gw := range gatewayNames {
		r.Spec.ParentRefs = append(r.Spec.ParentRefs, gwv1.ParentReference{Name: gwv1.ObjectName(gw)})
	}
	return r
}

// routeWithBackendAndNonGWParent builds an HTTPRoute with a non-Gateway parentRef.
func routeWithBackendAndNonGWParent(ns, svcName string) *gwv1.HTTPRoute {
	knd := gwv1.Kind("Service")
	r := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: ns},
		Spec: gwv1.HTTPRouteSpec{
			Rules: []gwv1.HTTPRouteRule{
				{BackendRefs: []gwv1.HTTPBackendRef{
					{BackendRef: gwv1.BackendRef{BackendObjectReference: gwv1.BackendObjectReference{Name: gwv1.ObjectName(svcName)}}},
				}},
			},
		},
	}
	r.Spec.ParentRefs = []gwv1.ParentReference{{Kind: &knd, Name: "svc-parent"}}
	return r
}

// ---- ServiceToRouteParents --------------------------------------------------

func TestServiceToRouteParents_CreateEnqueuesGateways(t *testing.T) {
	scheme := testScheme()
	route := routeWithBackendAndParents("ns", "svc", "gw-a")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		WithIndex(&gwv1.HTTPRoute{}, shared.IndexHTTPRouteByService, shared.IndexHTTPRouteByServiceFunc).
		Build()

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"}}
	h := ServiceToRouteParents(c)
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: svc}, q)
	assertRequests(t, q.items, []types.NamespacedName{
		{Namespace: "ns", Name: "gw-a"},
	})
}

func TestServiceToRouteParents_UpdateEnqueuesGateways(t *testing.T) {
	scheme := testScheme()
	route := routeWithBackendAndParents("ns", "svc", "gw-x")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		WithIndex(&gwv1.HTTPRoute{}, shared.IndexHTTPRouteByService, shared.IndexHTTPRouteByServiceFunc).
		Build()

	oldSvc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"}}
	newSvc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"}}
	h := ServiceToRouteParents(c)
	q := &fakeQueue{}
	h.Update(context.Background(), event.UpdateEvent{ObjectOld: oldSvc, ObjectNew: newSvc}, q)
	assertRequests(t, q.items, []types.NamespacedName{
		{Namespace: "ns", Name: "gw-x"},
	})
}

func TestServiceToRouteParents_DeleteEnqueuesGateways(t *testing.T) {
	scheme := testScheme()
	route := routeWithBackendAndParents("ns", "svc", "gw-del")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		WithIndex(&gwv1.HTTPRoute{}, shared.IndexHTTPRouteByService, shared.IndexHTTPRouteByServiceFunc).
		Build()

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"}}
	h := ServiceToRouteParents(c)
	q := &fakeQueue{}
	h.Delete(context.Background(), event.DeleteEvent{Object: svc}, q)
	assertRequests(t, q.items, []types.NamespacedName{
		{Namespace: "ns", Name: "gw-del"},
	})
}

func TestServiceToRouteParents_NonServiceSkipped(t *testing.T) {
	scheme := testScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	route := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "ns"}}
	h := ServiceToRouteParents(c)
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: route}, q)
	if len(q.items) != 0 {
		t.Errorf("expected 0 items, got %d", len(q.items))
	}
}

func TestServiceToRouteParents_SkipsNonGatewayParents(t *testing.T) {
	scheme := testScheme()
	route := routeWithBackendAndNonGWParent("ns", "svc")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		WithIndex(&gwv1.HTTPRoute{}, shared.IndexHTTPRouteByService, shared.IndexHTTPRouteByServiceFunc).
		Build()

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"}}
	h := ServiceToRouteParents(c)
	q := &fakeQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: svc}, q)
	if len(q.items) != 0 {
		t.Errorf("expected 0 items (non-gateway parent skipped), got %d", len(q.items))
	}
}

// ---- helpers ----------------------------------------------------------------

func assertRequests(t *testing.T, got []reconcile.Request, want []types.NamespacedName) {
	t.Helper()
	wantSet := make(map[types.NamespacedName]bool, len(want))
	for _, nn := range want {
		wantSet[nn] = true
	}
	gotSet := make(map[types.NamespacedName]bool, len(got))
	for _, r := range got {
		gotSet[r.NamespacedName] = true
	}
	for nn := range wantSet {
		if !gotSet[nn] {
			t.Errorf("expected enqueued request %v not found; got %v", nn, got)
		}
	}
	for nn := range gotSet {
		if !wantSet[nn] {
			t.Errorf("unexpected enqueued request %v", nn)
		}
	}
}
