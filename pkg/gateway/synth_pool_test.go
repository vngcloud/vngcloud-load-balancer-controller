package gateway

import "testing"

func TestSynthPoolName_Deterministic(t *testing.T) {
	a := SynthPoolName("12345678abcdef", 0, []BackendKey{{Namespace: "ns", Name: "a", Port: 80, Weight: 1}})
	b := SynthPoolName("12345678abcdef", 0, []BackendKey{{Namespace: "ns", Name: "a", Port: 80, Weight: 1}})
	if a != b {
		t.Fatalf("not deterministic: %s vs %s", a, b)
	}
	if len(a) > 50 {
		t.Errorf("name too long: %d", len(a))
	}
}

func TestSynthPoolName_OrderInsensitive(t *testing.T) {
	backendsA := []BackendKey{
		{Namespace: "ns", Name: "a", Port: 80, Weight: 1},
		{Namespace: "ns", Name: "b", Port: 80, Weight: 1},
	}
	backendsB := []BackendKey{
		{Namespace: "ns", Name: "b", Port: 80, Weight: 1},
		{Namespace: "ns", Name: "a", Port: 80, Weight: 1},
	}
	a := SynthPoolName("u", 0, backendsA)
	b := SynthPoolName("u", 0, backendsB)
	if a != b {
		t.Fatalf("expected order-insensitive: %s vs %s", a, b)
	}
}

func TestScaleWeights_Cap(t *testing.T) {
	out := ScaleWeights([]BackendWeight{{Weight: 1, Ready: 3}, {Weight: 99, Ready: 1}})
	if len(out) != 2 {
		t.Fatalf("want 2 entries, got %d", len(out))
	}
	for _, w := range out {
		if w < 1 || w > 100 {
			t.Errorf("weight out of range: %d", w)
		}
	}
}

func TestScaleWeights_FloorOne(t *testing.T) {
	out := ScaleWeights([]BackendWeight{{Weight: 1, Ready: 1000}, {Weight: 1000, Ready: 1}})
	for _, w := range out {
		if w < 1 {
			t.Errorf("weight floored below 1: %d", w)
		}
	}
}
