package gateway

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func TestTargetRefMatches(t *testing.T) {
	grp := gwv1alpha2.Group("gateway.networking.k8s.io")
	tref := gwv1alpha2.LocalPolicyTargetReference{
		Group: grp,
		Kind:  gwv1alpha2.Kind("Gateway"),
		Name:  gwv1alpha2.ObjectName("prod-gw"),
	}
	ok := TargetRefMatches(tref, "ns", PolicyTarget{
		Group:     "gateway.networking.k8s.io",
		Kind:      "Gateway",
		Name:      "prod-gw",
		Namespace: "ns",
	})
	if !ok {
		t.Fatal("expected match")
	}
}

func TestTargetRefMatches_DifferentName(t *testing.T) {
	tref := gwv1alpha2.LocalPolicyTargetReference{
		Group: "gateway.networking.k8s.io",
		Kind:  "Gateway",
		Name:  "prod-gw",
	}
	if TargetRefMatches(tref, "ns", PolicyTarget{
		Group: "gateway.networking.k8s.io", Kind: "Gateway",
		Name: "staging-gw", Namespace: "ns",
	}) {
		t.Fatal("unexpected match")
	}
	_ = metav1.Now()
}

func sectionPtr(s string) *gwv1alpha2.SectionName {
	v := gwv1alpha2.SectionName(s)
	return &v
}

func TestTargetRefMatchesWithSection(t *testing.T) {
	cases := []struct {
		name          string
		refSection    *gwv1alpha2.SectionName
		targetSection string
		wantMatch     bool
	}{
		{
			name:          "both empty sections match",
			refSection:    nil,
			targetSection: "",
			wantMatch:     true,
		},
		{
			name:          "ref section matches target section",
			refSection:    sectionPtr("listener-a"),
			targetSection: "listener-a",
			wantMatch:     true,
		},
		{
			name:          "ref section does not match different target section",
			refSection:    sectionPtr("listener-a"),
			targetSection: "listener-b",
			wantMatch:     false,
		},
		{
			name:          "ref section set but target section empty — no match",
			refSection:    sectionPtr("listener-a"),
			targetSection: "",
			wantMatch:     false,
		},
		{
			name:          "ref section nil but target section set — no match",
			refSection:    nil,
			targetSection: "listener-a",
			wantMatch:     false,
		},
	}

	base := gwv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gwv1alpha2.LocalPolicyTargetReference{
			Group: "gateway.networking.k8s.io",
			Kind:  "Gateway",
			Name:  "prod-gw",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := base
			ref.SectionName = tc.refSection
			got := TargetRefMatchesWithSection(ref, "ns", PolicyTarget{
				Group:       "gateway.networking.k8s.io",
				Kind:        "Gateway",
				Name:        "prod-gw",
				Namespace:   "ns",
				SectionName: tc.targetSection,
			})
			if got != tc.wantMatch {
				t.Errorf("TargetRefMatchesWithSection = %v; want %v", got, tc.wantMatch)
			}
		})
	}
}

func TestTargetRefMatchesWithSection_BaseMismatch(t *testing.T) {
	// When the base (group/kind/name/namespace) doesn't match, section is irrelevant.
	ref := gwv1alpha2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gwv1alpha2.LocalPolicyTargetReference{
			Group: "gateway.networking.k8s.io",
			Kind:  "Gateway",
			Name:  "other-gw",
		},
		SectionName: sectionPtr("listener-a"),
	}
	got := TargetRefMatchesWithSection(ref, "ns", PolicyTarget{
		Group: "gateway.networking.k8s.io", Kind: "Gateway",
		Name: "prod-gw", Namespace: "ns", SectionName: "listener-a",
	})
	if got {
		t.Fatal("base mismatch should not match")
	}
}
