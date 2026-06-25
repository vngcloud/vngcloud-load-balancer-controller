package shared

import (
	"sort"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestSortMatches_ExactBeforePrefix(t *testing.T) {
	pathExact := gwv1.PathMatchExact
	pathPrefix := gwv1.PathMatchPathPrefix
	items := []RankedMatch{
		{Match: gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: &pathPrefix, Value: ptr("/api")}}},
		{Match: gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: &pathExact, Value: ptr("/api/v1")}}},
	}
	sort.Stable(ByMatchSpecificity(items))
	if items[0].Match.Path.Type == nil || *items[0].Match.Path.Type != pathExact {
		t.Fatalf("expected exact first; got %#v", items[0])
	}
}

func TestSortMatches_TimestampTiebreak(t *testing.T) {
	older := metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := metav1.NewTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	items := []RankedMatch{
		{RouteCreated: newer},
		{RouteCreated: older},
	}
	sort.Stable(ByMatchSpecificity(items))
	if !items[0].RouteCreated.Equal(&older) {
		t.Fatalf("expected older first")
	}
}

func ptr[T any](v T) *T { return &v }
