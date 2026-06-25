package shared

import (
	"testing"

	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestProtocolAllowedForALB(t *testing.T) {
	cases := map[gwv1.ProtocolType]bool{
		gwv1.HTTPProtocolType:  true,
		gwv1.HTTPSProtocolType: true,
		gwv1.TLSProtocolType:   true,
		gwv1.TCPProtocolType:   false,
		gwv1.UDPProtocolType:   false,
	}
	for p, want := range cases {
		if got := ProtocolAllowedForALB(p); got != want {
			t.Errorf("ProtocolAllowedForALB(%q) = %v; want %v", p, got, want)
		}
	}
}

func TestMixedProtocols(t *testing.T) {
	listeners := []gwv1.Listener{
		{Protocol: gwv1.HTTPProtocolType},
		{Protocol: gwv1.TCPProtocolType},
	}
	if !HasMixedProtocols(listeners, "alb") {
		t.Fatal("expected mixed=true")
	}
	pure := []gwv1.Listener{{Protocol: gwv1.HTTPProtocolType}, {Protocol: gwv1.HTTPSProtocolType}}
	if HasMixedProtocols(pure, "alb") {
		t.Fatal("expected mixed=false")
	}
}
