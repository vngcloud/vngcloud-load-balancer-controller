package shared_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
)

func mkGrant(ns string, fromGroup, fromKind, fromNS, toGroup, toKind, toName string) *gwv1beta1.ReferenceGrant {
	rgFrom := gwv1beta1.ReferenceGrantFrom{
		Group: gwv1beta1.Group(fromGroup), Kind: gwv1beta1.Kind(fromKind), Namespace: gwv1beta1.Namespace(fromNS),
	}
	rgTo := gwv1beta1.ReferenceGrantTo{Group: gwv1beta1.Group(toGroup), Kind: gwv1beta1.Kind(toKind)}
	if toName != "" {
		n := gwv1beta1.ObjectName(toName)
		rgTo.Name = &n
	}
	return &gwv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rg"},
		Spec:       gwv1beta1.ReferenceGrantSpec{From: []gwv1beta1.ReferenceGrantFrom{rgFrom}, To: []gwv1beta1.ReferenceGrantTo{rgTo}},
	}
}

func TestRefGrantAllowed_SameNamespace(t *testing.T) {
	p := shared.RefRequest{
		FromGroup: "gateway.networking.k8s.io", FromKind: "HTTPRoute", FromNS: "ns1",
		ToGroup: "", ToKind: "Service", ToNS: "ns1", ToName: "svc",
	}
	assert.True(t, shared.RefGrantAllowed(p, nil))
}

func TestRefGrantAllowed_CrossNamespace_GrantPresent(t *testing.T) {
	grants := []*gwv1beta1.ReferenceGrant{
		mkGrant("ns2", "gateway.networking.k8s.io", "HTTPRoute", "ns1", "", "Service", ""),
	}
	p := shared.RefRequest{
		FromGroup: "gateway.networking.k8s.io", FromKind: "HTTPRoute", FromNS: "ns1",
		ToGroup: "", ToKind: "Service", ToNS: "ns2", ToName: "svc",
	}
	assert.True(t, shared.RefGrantAllowed(p, grants))
}

func TestRefGrantAllowed_CrossNamespace_NoGrant(t *testing.T) {
	p := shared.RefRequest{
		FromGroup: "gateway.networking.k8s.io", FromKind: "HTTPRoute", FromNS: "ns1",
		ToGroup: "", ToKind: "Service", ToNS: "ns2", ToName: "svc",
	}
	assert.False(t, shared.RefGrantAllowed(p, nil))
}

func TestRefGrantAllowed_NameSpecificMismatch(t *testing.T) {
	grants := []*gwv1beta1.ReferenceGrant{
		mkGrant("ns2", "gateway.networking.k8s.io", "HTTPRoute", "ns1", "", "Service", "other"),
	}
	p := shared.RefRequest{
		FromGroup: "gateway.networking.k8s.io", FromKind: "HTTPRoute", FromNS: "ns1",
		ToGroup: "", ToKind: "Service", ToNS: "ns2", ToName: "svc",
	}
	assert.False(t, shared.RefGrantAllowed(p, grants))
}
