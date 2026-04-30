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
// Format: gw_<uid8>_<ruleIdx>_<hash5>.
// Stable under reorder of backends; changes when the backend set or weights change.
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
	return fmt.Sprintf("gw_%s_%d_%s", uid, ruleIdx, sum)
}
