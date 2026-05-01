package gateway_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

func TestSynthPoolName_Deterministic(t *testing.T) {
	backends := []gateway.BackendKey{
		{Namespace: "ns1", Name: "svc-a", Port: 80, Weight: 90},
		{Namespace: "ns1", Name: "svc-b", Port: 80, Weight: 10},
	}
	n1 := gateway.SynthPoolName("12345678-aaaa", 0, backends)
	n2 := gateway.SynthPoolName("12345678-aaaa", 0, backends)
	assert.Equal(t, n1, n2)
	assert.True(t, strings.HasPrefix(n1, "vks-pool-12345678-0-"))
	assert.LessOrEqual(t, len(n1), 50)
}

func TestSynthPoolName_StableUnderReorder(t *testing.T) {
	a := []gateway.BackendKey{
		{Namespace: "n", Name: "a", Port: 80, Weight: 1},
		{Namespace: "n", Name: "b", Port: 80, Weight: 2},
	}
	b := []gateway.BackendKey{
		{Namespace: "n", Name: "b", Port: 80, Weight: 2},
		{Namespace: "n", Name: "a", Port: 80, Weight: 1},
	}
	assert.Equal(t, gateway.SynthPoolName("uid", 0, a), gateway.SynthPoolName("uid", 0, b))
}

func TestSynthPoolName_ChangesOnWeightChange(t *testing.T) {
	a := []gateway.BackendKey{{Namespace: "n", Name: "a", Port: 80, Weight: 1}}
	b := []gateway.BackendKey{{Namespace: "n", Name: "a", Port: 80, Weight: 2}}
	assert.NotEqual(t, gateway.SynthPoolName("uid", 0, a), gateway.SynthPoolName("uid", 0, b))
}
