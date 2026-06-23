package k8sbatch

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MutateStatus queues a mutator for the Status sub-resource of obj. The
// mutator is invoked at Flush time against a freshly GET'd copy of the
// object. Return false to indicate "nothing to change for this entry";
// if any queued mutator for this object returns true, a Status patch is
// issued.
func MutateStatus[T client.Object](b *Batcher, obj T, mutate func(obj T) bool) {
	e := b.entryFor(obj)
	e.statusMutators = append(e.statusMutators, func(o client.Object) bool {
		return mutate(o.(T))
	})
}

// MutateSpec queues a mutator for the Spec/metadata path of obj. Same
// flush semantics as MutateStatus.
func MutateSpec[T client.Object](b *Batcher, obj T, mutate func(obj T) bool) {
	e := b.entryFor(obj)
	e.specMutators = append(e.specMutators, func(o client.Object) bool {
		return mutate(o.(T))
	})
}
