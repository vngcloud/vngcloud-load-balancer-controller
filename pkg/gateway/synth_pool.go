package gateway

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
)

// BackendKey is the canonical identity of a backend in a route rule.
type BackendKey struct {
	Namespace string
	Name      string
	Port      int32
	Weight    int32
}

// SynthPoolName produces a deterministic pool name <= 50 chars.
// Format: vks-pool-<uid8>-<ruleIdx>-<hash5>.
// Stable under reorder of backends; changes when the backend set or weights change.
// All controller-managed vngcloud resources carry the "vks-" prefix so they can
// be identified at a glance in the dashboard / API.
func SynthPoolName(routeUID string, ruleIdx int, backends []BackendKey) string {
	uid := routeUID
	if len(uid) > 8 {
		uid = uid[:8]
	}
	sorted := append([]BackendKey(nil), backends...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Namespace != sorted[j].Namespace {
			return sorted[i].Namespace < sorted[j].Namespace
		}
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		if sorted[i].Port != sorted[j].Port {
			return sorted[i].Port < sorted[j].Port
		}
		return sorted[i].Weight < sorted[j].Weight
	})
	h := sha1.New()
	for _, b := range sorted {
		fmt.Fprintf(h, "%s/%s:%d:%d\n", b.Namespace, b.Name, b.Port, b.Weight)
	}
	sum := hex.EncodeToString(h.Sum(nil))[:5]
	return fmt.Sprintf("vks-pool-%s-%d-%s", uid, ruleIdx, sum)
}
