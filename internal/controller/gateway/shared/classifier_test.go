package shared_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

func TestALBAllowsListener(t *testing.T) {
	cases := []struct {
		proto gwv1.ProtocolType
		want  bool
	}{
		{gwv1.HTTPProtocolType, true},
		{gwv1.HTTPSProtocolType, true},
		{gwv1.TLSProtocolType, true},
		{gwv1.TCPProtocolType, false},
		{gwv1.UDPProtocolType, false},
	}
	for _, c := range cases {
		t.Run(string(c.proto), func(t *testing.T) {
			assert.Equal(t, c.want, shared.ALBAllowsListener(c.proto))
		})
	}
}

func TestValidateListenerSet_DupPort(t *testing.T) {
	listeners := []gwv1.Listener{
		{Name: "a", Protocol: gwv1.HTTPProtocolType, Port: 80},
		{Name: "b", Protocol: gwv1.HTTPProtocolType, Port: 80},
	}
	res := shared.ValidateListenersForALB(listeners)
	assert.Equal(t, shared.ListenerInvalidReasonNone, res["a"])
	assert.Equal(t, shared.ListenerInvalidReasonDupPort, res["b"])
}

func TestValidateListenerSet_UnsupportedProtocol(t *testing.T) {
	listeners := []gwv1.Listener{{Name: "a", Protocol: gwv1.TCPProtocolType, Port: 9000}}
	res := shared.ValidateListenersForALB(listeners)
	assert.Equal(t, shared.ListenerInvalidReasonUnsupportedProtocol, res["a"])
}
