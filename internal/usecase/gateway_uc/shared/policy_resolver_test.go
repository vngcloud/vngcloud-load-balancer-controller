package shared

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

type fakePolicy struct {
	name      string
	namespace string
	section   string
	targetKey string
	created   time.Time
}

func (p *fakePolicy) GetName() string                   { return p.name }
func (p *fakePolicy) GetNamespace() string              { return p.namespace }
func (p *fakePolicy) GetCreationTimestamp() metav1.Time { return metav1.NewTime(p.created) }
func (p *fakePolicy) Matches(target pkggw.PolicyTarget) bool {
	return p.namespace == target.Namespace &&
		p.targetKey == target.Name &&
		p.section == target.SectionName
}

func TestResolveDirectPolicy_OldestWins(t *testing.T) {
	older := &fakePolicy{name: "a", namespace: "ns", targetKey: "gw1", created: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	newer := &fakePolicy{name: "b", namespace: "ns", targetKey: "gw1", created: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	in := []*fakePolicy{newer, older}
	target := pkggw.PolicyTarget{Namespace: "ns", Name: "gw1"}
	win, lose := ResolveDirectPolicy(in, target)
	if win == nil || win.GetName() != "a" {
		t.Fatalf("want winner=a; got %#v", win)
	}
	if len(lose) != 1 || lose[0].GetName() != "b" {
		t.Fatalf("want losers=[b]; got %#v", lose)
	}
}

func TestResolveDirectPolicy_NoMatch(t *testing.T) {
	p := &fakePolicy{name: "a", namespace: "ns", targetKey: "other", created: time.Now()}
	win, lose := ResolveDirectPolicy([]*fakePolicy{p}, pkggw.PolicyTarget{Namespace: "ns", Name: "gw1"})
	if win != nil {
		t.Fatal("expected no winner")
	}
	if len(lose) != 0 {
		t.Fatal("expected no losers")
	}
}

func TestResolveDirectPolicy_NameTiebreak(t *testing.T) {
	same := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &fakePolicy{name: "alpha", namespace: "ns", targetKey: "gw1", created: same}
	b := &fakePolicy{name: "beta", namespace: "ns", targetKey: "gw1", created: same}
	win, _ := ResolveDirectPolicy([]*fakePolicy{b, a}, pkggw.PolicyTarget{Namespace: "ns", Name: "gw1"})
	if win.GetName() != "alpha" {
		t.Fatalf("want alpha; got %s", win.GetName())
	}
}
