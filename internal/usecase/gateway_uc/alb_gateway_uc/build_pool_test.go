package alb_gateway_uc

import (
	"testing"

	"github.com/stretchr/testify/assert"

	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

func TestSynthesizeMembers_TwoBackends_90_10_Ratio(t *testing.T) {
	in := []BackendEndpoints{
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "a", Port: 80, Weight: 90}, Endpoints: []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}},
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "b", Port: 80, Weight: 10}, Endpoints: []string{"10.0.0.4"}},
	}
	members := SynthesizeMembers(in)
	var sumA, sumB int
	for _, m := range members {
		w := 0
		if m.Weight != nil {
			w = *m.Weight
		}
		if m.IP == "10.0.0.4" {
			sumB += w
		} else {
			sumA += w
		}
	}
	// Ratio must be ~9:1 within rounding tolerance.
	assert.True(t, sumA*10 >= sumB*85 && sumA*10 <= sumB*95, "want ~9:1 ratio, got %d:%d", sumA, sumB)
}

func TestSynthesizeMembers_AllSameWeight_AllOne(t *testing.T) {
	in := []BackendEndpoints{
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "a", Port: 80, Weight: 1}, Endpoints: []string{"10.0.0.1"}},
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "b", Port: 80, Weight: 1}, Endpoints: []string{"10.0.0.2"}},
	}
	members := SynthesizeMembers(in)
	assert.Len(t, members, 2)
	for _, m := range members {
		assert.NotNil(t, m.Weight)
		assert.GreaterOrEqual(t, *m.Weight, 1)
	}
}

func TestSynthesizeMembers_FloorsAt1(t *testing.T) {
	in := []BackendEndpoints{
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "a", Port: 80, Weight: 1}, Endpoints: []string{"10.0.0.1"}},
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "b", Port: 80, Weight: 99}, Endpoints: []string{"10.0.0.2"}},
	}
	members := SynthesizeMembers(in)
	for _, m := range members {
		assert.GreaterOrEqual(t, *m.Weight, 1)
	}
}

func TestSynthesizeMembers_SkipsZeroWeight(t *testing.T) {
	in := []BackendEndpoints{
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "a", Port: 80, Weight: 0}, Endpoints: []string{"10.0.0.1"}},
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "b", Port: 80, Weight: 1}, Endpoints: []string{"10.0.0.2"}},
	}
	members := SynthesizeMembers(in)
	assert.Len(t, members, 1)
	assert.Equal(t, "10.0.0.2", members[0].IP)
}

func TestSynthesizeMembers_Empty(t *testing.T) {
	assert.Nil(t, SynthesizeMembers(nil))
}
