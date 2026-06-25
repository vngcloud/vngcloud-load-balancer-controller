package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
)

// BackendKey is the canonical identity of a backend used in pool naming.
type BackendKey struct {
	Namespace string
	Name      string
	Port      int32
	Weight    int32
}

// BackendWeight is a (weight, ready-endpoints) pair fed to ScaleWeights.
type BackendWeight struct {
	Weight int32
	Ready  int32
}

// SynthPoolName returns a deterministic pool name <= 50 chars derived from the
// route UID, rule index, and backend set. Backend ordering is normalized.
// The name starts with the project-wide "vks_" prefix so every cloud-side
// resource the controller creates is greppable; mirrors domain.VKSResourceNamePrefix
// without depending on the internal domain package from this pure helper pkg.
func SynthPoolName(routeUID string, ruleIdx int, backends []BackendKey) string {
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
	h := sha256.New()
	for _, b := range sorted {
		_, _ = fmt.Fprintf(h, "%s/%s:%d=%d\n", b.Namespace, b.Name, b.Port, b.Weight)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	prefix := routeUID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	name := fmt.Sprintf("vks_gw_%s_%d_%s", prefix, ruleIdx, digest[:5])
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}

// ScaleWeights computes vngcloud member weights given (declared weight, ready endpoints)
// per backend. Member weights are floored at 1 and the largest member weight is capped at 100.
func ScaleWeights(in []BackendWeight) []int32 {
	if len(in) == 0 {
		return nil
	}
	out := make([]int32, len(in))
	var maxW big.Int
	for i, b := range in {
		n := b.Ready
		if n <= 0 {
			n = 1
		}
		mw := new(big.Int).SetInt64(int64(b.Weight))
		mw.Mul(mw, big.NewInt(100))
		mw.Quo(mw, big.NewInt(int64(n)))
		if mw.Sign() <= 0 {
			mw.SetInt64(1)
		}
		if mw.Cmp(&maxW) > 0 {
			maxW.Set(mw)
		}
		out[i] = int32(mw.Int64())
	}
	if maxW.Cmp(big.NewInt(100)) > 0 {
		scale := big.NewInt(100)
		for i := range out {
			v := new(big.Int).SetInt64(int64(out[i]))
			v.Mul(v, scale)
			v.Quo(v, &maxW)
			if v.Sign() <= 0 {
				v.SetInt64(1)
			}
			out[i] = int32(v.Int64())
		}
	}
	return out
}
