package shared

import (
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// PolicyObj is the minimal interface a Direct policy must satisfy to participate in resolution.
type PolicyObj interface {
	GetName() string
	GetNamespace() string
	GetCreationTimestamp() metav1.Time
	Matches(t pkggw.PolicyTarget) bool
}

// ResolveDirectPolicy filters policies, sorts by (creationTimestamp, namespace/name) ascending,
// and returns the winner (oldest) plus the losers (Conflicted).
func ResolveDirectPolicy[P PolicyObj](in []P, target pkggw.PolicyTarget) (winner P, losers []P) {
	var matches []P
	for _, p := range in {
		if p.Matches(target) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		var zero P
		return zero, nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		ti := matches[i].GetCreationTimestamp()
		tj := matches[j].GetCreationTimestamp()
		if !ti.Equal(&tj) {
			return ti.Before(&tj)
		}
		ki := matches[i].GetNamespace() + "/" + matches[i].GetName()
		kj := matches[j].GetNamespace() + "/" + matches[j].GetName()
		return ki < kj
	})
	return matches[0], matches[1:]
}
