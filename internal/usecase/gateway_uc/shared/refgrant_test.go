package shared

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = gwv1beta1.Install(s)
	return s
}

func TestRefGrantAllowed_SameNamespace(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	ok, err := RefGrantAllowed(context.Background(), c,
		Ref{Group: "", Kind: "Service", Namespace: "ns", Name: "svc"},
		Ref{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "ns", Name: "r"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("same-namespace must always be allowed")
	}
}

func TestRefGrantAllowed_GrantPresent(t *testing.T) {
	grant := &gwv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "backend-ns"},
		Spec: gwv1beta1.ReferenceGrantSpec{
			From: []gwv1beta1.ReferenceGrantFrom{{
				Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "route-ns",
			}},
			To: []gwv1beta1.ReferenceGrantTo{{Group: "", Kind: "Service"}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(grant).Build()
	ok, _ := RefGrantAllowed(context.Background(), c,
		Ref{Group: "", Kind: "Service", Namespace: "backend-ns", Name: "svc"},
		Ref{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "route-ns", Name: "r"},
	)
	if !ok {
		t.Fatal("grant should allow")
	}
}

func TestRefGrantAllowed_NoGrant(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	ok, _ := RefGrantAllowed(context.Background(), c,
		Ref{Group: "", Kind: "Service", Namespace: "backend-ns", Name: "svc"},
		Ref{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "route-ns", Name: "r"},
	)
	if ok {
		t.Fatal("no grant: should deny")
	}
}
