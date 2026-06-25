package alb

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

// newPredicateScheme returns a scheme with just the types albClassPredicate
// looks at.
func newPredicateScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := gwv1.Install(s); err != nil {
		t.Fatalf("install gateway-api scheme: %v", err)
	}
	return s
}

// gatewayWithClass builds a Gateway whose Spec.GatewayClassName is set.
func gatewayWithClass(name, gcName string) *gwv1.Gateway {
	return &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       gwv1.GatewaySpec{GatewayClassName: gwv1.ObjectName(gcName)},
	}
}

// gatewayClass builds a GatewayClass with the given controllerName.
func gatewayClass(name, controllerName string) *gwv1.GatewayClass {
	return &gwv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController(controllerName)},
	}
}

func TestAlbClassPredicate_MatchingControllerName_Accepts(t *testing.T) {
	gc := gatewayClass("alb-gc", consts.GatewayClassControllerNameALB)
	gw := gatewayWithClass("gw-1", "alb-gc")
	c := fake.NewClientBuilder().WithScheme(newPredicateScheme(t)).WithObjects(gc).Build()

	pred := albClassPredicate(c)
	if !pred.Create(event.CreateEvent{Object: gw}) {
		t.Errorf("expected predicate to accept Gateway under ALB GatewayClass")
	}
}

func TestAlbClassPredicate_NonMatchingControllerName_Rejects(t *testing.T) {
	gc := gatewayClass("other-gc", "gateway.example.io/other")
	gw := gatewayWithClass("gw-2", "other-gc")
	c := fake.NewClientBuilder().WithScheme(newPredicateScheme(t)).WithObjects(gc).Build()

	pred := albClassPredicate(c)
	if pred.Create(event.CreateEvent{Object: gw}) {
		t.Errorf("expected predicate to reject Gateway under non-ALB GatewayClass")
	}
}

func TestAlbClassPredicate_EmptyGatewayClassName_Rejects(t *testing.T) {
	gw := gatewayWithClass("gw-empty", "")
	c := fake.NewClientBuilder().WithScheme(newPredicateScheme(t)).Build()

	pred := albClassPredicate(c)
	if pred.Create(event.CreateEvent{Object: gw}) {
		t.Errorf("expected predicate to reject Gateway with empty GatewayClassName")
	}
}

func TestAlbClassPredicate_MissingGatewayClass_Rejects(t *testing.T) {
	// Gateway references a class that doesn't exist in the cluster.
	gw := gatewayWithClass("gw-orphan", "absent-gc")
	c := fake.NewClientBuilder().WithScheme(newPredicateScheme(t)).Build()

	pred := albClassPredicate(c)
	if pred.Create(event.CreateEvent{Object: gw}) {
		t.Errorf("expected predicate to reject Gateway whose GatewayClass is not found")
	}
}

func TestAlbClassPredicate_NonGatewayObject_Rejects(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newPredicateScheme(t)).Build()
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "stray", Namespace: "default"}}

	pred := albClassPredicate(c)
	if pred.Create(event.CreateEvent{Object: svc}) {
		t.Errorf("expected predicate to reject non-Gateway object")
	}
}

func TestAlbClassPredicate_AppliesToUpdateAndDelete(t *testing.T) {
	// Sanity check: NewPredicateFuncs wires the same matcher into all four
	// event types. We verify Update + Delete since reconcile loops triggered
	// by class swaps and finalizer-removal both go through these paths.
	gc := gatewayClass("alb-gc", consts.GatewayClassControllerNameALB)
	gw := gatewayWithClass("gw-1", "alb-gc")
	c := fake.NewClientBuilder().WithScheme(newPredicateScheme(t)).WithObjects(gc).Build()

	pred := albClassPredicate(c)
	if !pred.Update(event.UpdateEvent{ObjectOld: gw, ObjectNew: gw}) {
		t.Errorf("expected predicate to accept ALB Gateway on Update")
	}
	if !pred.Delete(event.DeleteEvent{Object: gw}) {
		t.Errorf("expected predicate to accept ALB Gateway on Delete")
	}
}

// --- conditionsEqualIgnoreTime ---

func TestConditionsEqualIgnoreTime(t *testing.T) {
	t1 := metav1.Now()
	t2 := metav1.NewTime(t1.Add(42)) // same content, different timestamp

	base := []metav1.Condition{
		{
			Type:               "Accepted",
			Status:             metav1.ConditionTrue,
			Reason:             "Accepted",
			Message:            "ok",
			ObservedGeneration: 3,
			LastTransitionTime: t1,
		},
	}

	cases := []struct {
		name string
		a, b []metav1.Condition
		want bool
	}{
		{
			name: "identical conditions",
			a:    base,
			b:    base,
			want: true,
		},
		{
			name: "same content, different LastTransitionTime",
			a:    base,
			b: []metav1.Condition{{
				Type: "Accepted", Status: metav1.ConditionTrue,
				Reason: "Accepted", Message: "ok",
				ObservedGeneration: 3, LastTransitionTime: t2,
			}},
			want: true,
		},
		{
			name: "different length",
			a:    base,
			b:    []metav1.Condition{},
			want: false,
		},
		{
			name: "different Status",
			a:    base,
			b: []metav1.Condition{{
				Type: "Accepted", Status: metav1.ConditionFalse,
				Reason: "Accepted", Message: "ok", ObservedGeneration: 3,
			}},
			want: false,
		},
		{
			name: "different Reason",
			a:    base,
			b: []metav1.Condition{{
				Type: "Accepted", Status: metav1.ConditionTrue,
				Reason: "Pending", Message: "ok", ObservedGeneration: 3,
			}},
			want: false,
		},
		{
			name: "different Message",
			a:    base,
			b: []metav1.Condition{{
				Type: "Accepted", Status: metav1.ConditionTrue,
				Reason: "Accepted", Message: "different", ObservedGeneration: 3,
			}},
			want: false,
		},
		{
			name: "different ObservedGeneration",
			a:    base,
			b: []metav1.Condition{{
				Type: "Accepted", Status: metav1.ConditionTrue,
				Reason: "Accepted", Message: "ok", ObservedGeneration: 4,
			}},
			want: false,
		},
		{
			name: "different condition Type (no overlap)",
			a:    base,
			b: []metav1.Condition{{
				Type: "Programmed", Status: metav1.ConditionTrue,
				Reason: "Accepted", Message: "ok", ObservedGeneration: 3,
			}},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := conditionsEqualIgnoreTime(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("conditionsEqualIgnoreTime: got %v, want %v", got, tc.want)
			}
		})
	}
}
