package alb_gateway_uc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

func TestLbcReady(t *testing.T) {
	t.Run("no conditions → not ready", func(t *testing.T) {
		assert.False(t, lbcReady(&v1alpha1.LoadBalancerConfig{}))
	})
	t.Run("Ready=True → ready", func(t *testing.T) {
		lbc := &v1alpha1.LoadBalancerConfig{}
		lbc.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		assert.True(t, lbcReady(lbc))
	})
	t.Run("Ready=False → not ready", func(t *testing.T) {
		lbc := &v1alpha1.LoadBalancerConfig{}
		lbc.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse}}
		assert.False(t, lbcReady(lbc))
	})
	t.Run("other condition → not ready", func(t *testing.T) {
		lbc := &v1alpha1.LoadBalancerConfig{}
		lbc.Status.Conditions = []metav1.Condition{{Type: "Accepted", Status: metav1.ConditionTrue}}
		assert.False(t, lbcReady(lbc))
	})
}

func TestLbcStatusMessage(t *testing.T) {
	t.Run("empty message returns default", func(t *testing.T) {
		lbc := &v1alpha1.LoadBalancerConfig{}
		assert.Equal(t, "LoadBalancerConfig not yet ready", lbcStatusMessage(lbc))
	})
	t.Run("non-empty message prefixed with LBC:", func(t *testing.T) {
		lbc := &v1alpha1.LoadBalancerConfig{}
		lbc.Status.LastReconcileMessage = "some error"
		assert.Equal(t, "LBC: some error", lbcStatusMessage(lbc))
	})
}

func TestGatewayAddressesFromLBC(t *testing.T) {
	t.Run("nil lbc returns nil", func(t *testing.T) {
		assert.Nil(t, gatewayAddressesFromLBC(nil))
	})
	t.Run("nil Address returns nil", func(t *testing.T) {
		assert.Nil(t, gatewayAddressesFromLBC(&v1alpha1.LoadBalancerConfig{}))
	})
	t.Run("empty Address string returns nil", func(t *testing.T) {
		lbc := &v1alpha1.LoadBalancerConfig{}
		lbc.Status.Address = ptr.To("")
		assert.Nil(t, gatewayAddressesFromLBC(lbc))
	})
	t.Run("valid address returns single IP address", func(t *testing.T) {
		lbc := &v1alpha1.LoadBalancerConfig{}
		lbc.Status.Address = ptr.To("10.1.2.3")
		addrs := gatewayAddressesFromLBC(lbc)
		assert.Len(t, addrs, 1)
		assert.Equal(t, "10.1.2.3", addrs[0].Value)
		assert.Equal(t, gwv1.IPAddressType, *addrs[0].Type)
	})
}

func TestConditionsEqual(t *testing.T) {
	t.Run("empty slices equal", func(t *testing.T) {
		assert.True(t, conditionsEqual(nil, nil))
	})
	t.Run("different lengths not equal", func(t *testing.T) {
		a := []metav1.Condition{{Type: "A", Status: metav1.ConditionTrue, Reason: "R", Message: "M"}}
		assert.False(t, conditionsEqual(a, nil))
	})
	t.Run("same conditions equal (ignoring LastTransitionTime)", func(t *testing.T) {
		t1 := metav1.Now()
		t2 := metav1.NewTime(t1.Add(-1e9))
		a := []metav1.Condition{{
			Type: "Programmed", Status: metav1.ConditionTrue,
			Reason: "Programmed", Message: "ok",
			ObservedGeneration: 1,
			LastTransitionTime: t1,
		}}
		b := []metav1.Condition{{
			Type: "Programmed", Status: metav1.ConditionTrue,
			Reason: "Programmed", Message: "ok",
			ObservedGeneration: 1,
			LastTransitionTime: t2,
		}}
		assert.True(t, conditionsEqual(a, b))
	})
	t.Run("different status not equal", func(t *testing.T) {
		a := []metav1.Condition{{Type: "P", Status: metav1.ConditionTrue, Reason: "R"}}
		b := []metav1.Condition{{Type: "P", Status: metav1.ConditionFalse, Reason: "R"}}
		assert.False(t, conditionsEqual(a, b))
	})
	t.Run("different reason not equal", func(t *testing.T) {
		a := []metav1.Condition{{Type: "P", Status: metav1.ConditionTrue, Reason: "R1"}}
		b := []metav1.Condition{{Type: "P", Status: metav1.ConditionTrue, Reason: "R2"}}
		assert.False(t, conditionsEqual(a, b))
	})
	t.Run("different message not equal", func(t *testing.T) {
		a := []metav1.Condition{{Type: "P", Status: metav1.ConditionTrue, Reason: "R", Message: "M1"}}
		b := []metav1.Condition{{Type: "P", Status: metav1.ConditionTrue, Reason: "R", Message: "M2"}}
		assert.False(t, conditionsEqual(a, b))
	})
	t.Run("type missing in b not equal", func(t *testing.T) {
		a := []metav1.Condition{{Type: "A", Status: metav1.ConditionTrue, Reason: "R"}}
		b := []metav1.Condition{{Type: "B", Status: metav1.ConditionTrue, Reason: "R"}}
		assert.False(t, conditionsEqual(a, b))
	})
}

func TestGatewayAddressesEqual(t *testing.T) {
	ipType := gwv1.IPAddressType
	t.Run("empty equal", func(t *testing.T) {
		assert.True(t, gatewayAddressesEqual(nil, nil))
	})
	t.Run("same address equal", func(t *testing.T) {
		a := []gwv1.GatewayStatusAddress{{Type: &ipType, Value: "1.2.3.4"}}
		b := []gwv1.GatewayStatusAddress{{Type: &ipType, Value: "1.2.3.4"}}
		assert.True(t, gatewayAddressesEqual(a, b))
	})
	t.Run("different value not equal", func(t *testing.T) {
		a := []gwv1.GatewayStatusAddress{{Type: &ipType, Value: "1.2.3.4"}}
		b := []gwv1.GatewayStatusAddress{{Type: &ipType, Value: "5.6.7.8"}}
		assert.False(t, gatewayAddressesEqual(a, b))
	})
	t.Run("different length not equal", func(t *testing.T) {
		a := []gwv1.GatewayStatusAddress{{Value: "1.2.3.4"}}
		assert.False(t, gatewayAddressesEqual(a, nil))
	})
	t.Run("nil type vs non-nil type handled", func(t *testing.T) {
		a := []gwv1.GatewayStatusAddress{{Type: nil, Value: "1.2.3.4"}}
		b := []gwv1.GatewayStatusAddress{{Type: &ipType, Value: "1.2.3.4"}}
		assert.False(t, gatewayAddressesEqual(a, b))
	})
}

func TestListenerStatusesEqual(t *testing.T) {
	t.Run("empty equal", func(t *testing.T) {
		assert.True(t, listenerStatusesEqual(nil, nil))
	})
	t.Run("different length not equal", func(t *testing.T) {
		a := []gwv1.ListenerStatus{{Name: "http"}}
		assert.False(t, listenerStatusesEqual(a, nil))
	})
	t.Run("same name + conditions equal", func(t *testing.T) {
		cond := metav1.Condition{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted"}
		a := []gwv1.ListenerStatus{{Name: "http", Conditions: []metav1.Condition{cond}}}
		b := []gwv1.ListenerStatus{{Name: "http", Conditions: []metav1.Condition{cond}}}
		assert.True(t, listenerStatusesEqual(a, b))
	})
	t.Run("name in a missing from b not equal", func(t *testing.T) {
		a := []gwv1.ListenerStatus{{Name: "http"}}
		b := []gwv1.ListenerStatus{{Name: "https"}}
		assert.False(t, listenerStatusesEqual(a, b))
	})
}

func TestSupportedKindsForProtocol(t *testing.T) {
	t.Run("HTTP returns HTTPRoute", func(t *testing.T) {
		kinds := supportedKindsForProtocol(gwv1.HTTPProtocolType)
		assert.Len(t, kinds, 1)
		assert.Equal(t, gwv1.Kind("HTTPRoute"), kinds[0].Kind)
	})
	t.Run("HTTPS returns HTTPRoute", func(t *testing.T) {
		kinds := supportedKindsForProtocol(gwv1.HTTPSProtocolType)
		assert.Len(t, kinds, 1)
		assert.Equal(t, gwv1.Kind("HTTPRoute"), kinds[0].Kind)
	})
	t.Run("TCP returns nil", func(t *testing.T) {
		kinds := supportedKindsForProtocol(gwv1.TCPProtocolType)
		assert.Nil(t, kinds)
	})
}

func TestListenerAcceptedStatus(t *testing.T) {
	t.Run("HTTP → True", func(t *testing.T) {
		l := &gwv1.Listener{Protocol: gwv1.HTTPProtocolType}
		assert.Equal(t, metav1.ConditionTrue, listenerAcceptedStatus(l))
	})
	t.Run("HTTPS → True", func(t *testing.T) {
		l := &gwv1.Listener{Protocol: gwv1.HTTPSProtocolType}
		assert.Equal(t, metav1.ConditionTrue, listenerAcceptedStatus(l))
	})
	t.Run("TCP → False", func(t *testing.T) {
		l := &gwv1.Listener{Protocol: gwv1.TCPProtocolType}
		assert.Equal(t, metav1.ConditionFalse, listenerAcceptedStatus(l))
	})
}

func TestListenerAcceptedReason(t *testing.T) {
	t.Run("HTTP → Accepted", func(t *testing.T) {
		l := &gwv1.Listener{Protocol: gwv1.HTTPProtocolType}
		assert.Equal(t, string(gwv1.ListenerReasonAccepted), listenerAcceptedReason(l))
	})
	t.Run("TLS → UnsupportedProtocol", func(t *testing.T) {
		l := &gwv1.Listener{Protocol: gwv1.TLSProtocolType}
		assert.Equal(t, string(gwv1.ListenerReasonUnsupportedProtocol), listenerAcceptedReason(l))
	})
}
