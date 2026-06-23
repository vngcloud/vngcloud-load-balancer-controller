package k8sbatch

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Batcher coalesces queued mutations against Kubernetes objects so a single
// reconcile performs at most one GET and one PATCH per object per
// sub-resource. Not safe for concurrent use; reconciles are single-goroutine
// per object key in controller-runtime.
type Batcher struct {
	client  client.Client
	pending map[objKey]*entry
}

// objKey identifies an object by GVK + namespace/name.
type objKey struct {
	gvk schema.GroupVersionKind
	nn  types.NamespacedName
}

// entry holds the queued mutators for one logical object plus a template
// pointer used to issue fresh GETs at flush time.
type entry struct {
	// template is the *T pointer captured from the first MutateSpec/
	// MutateStatus call for this objKey. DeepCopyObject on it must return
	// the same concrete type because the wrapped mutators below perform
	// an unchecked o.(T) type assertion on the passed client.Object.
	template client.Object

	// specMutators and statusMutators are closures that wrap a typed
	// func(*T) bool and perform an unchecked .(T) assertion on the
	// passed client.Object. Two MutateSpec/MutateStatus calls with the
	// same objKey but different concrete types would panic — but objKey
	// includes GVK, so this can only happen via API misuse.
	specMutators   []func(client.Object) bool
	statusMutators []func(client.Object) bool
}

// New returns a Batcher backed by c. The returned Batcher is not safe for
// concurrent use; reconciles are single-goroutine per object key in
// controller-runtime.
func New(c client.Client) *Batcher {
	return &Batcher{
		client:  c,
		pending: make(map[objKey]*entry),
	}
}

// Pending returns the number of distinct objects currently queued.
func (b *Batcher) Pending() int { return len(b.pending) }

// entryFor looks up or creates the queue entry for obj's identity. The
// first call captures obj as the template used for fresh GETs at flush
// time; later calls with the same key reuse the existing entry.
func (b *Batcher) entryFor(obj client.Object) *entry {
	key := b.keyOf(obj)
	if e, ok := b.pending[key]; ok {
		return e
	}
	e := &entry{template: obj}
	b.pending[key] = e
	return e
}

func (b *Batcher) keyOf(obj client.Object) objKey {
	gvk, err := apiutil.GVKForObject(obj, b.client.Scheme())
	if err != nil {
		// Should be impossible: scheme-registered types only.
		panic(fmt.Errorf("k8sbatch: GVK lookup failed for %T: %w", obj, err))
	}
	return objKey{
		gvk: gvk,
		nn: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		},
	}
}

// Flush drains the queue. For each distinct object, fetches fresh, applies
// queued mutators in order, issues at most one Spec and one Status patch
// with optimistic-lock retry-on-conflict. Best-effort across objects:
// returns errors.Join of per-object failures (nil on full success).
// Successful entries are removed; failed entries remain queued for a
// subsequent Flush.
func (b *Batcher) Flush(ctx context.Context) error {
	var errs []error
	for key, e := range b.pending {
		if err := b.flushObject(ctx, key, e); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", key.nn.Namespace, key.nn.Name, err))
			continue
		}
		delete(b.pending, key)
	}
	return errors.Join(errs...)
}

// flushObject runs the per-object pipeline: GET fresh, apply Spec mutators,
// patch Spec if changed, apply Status mutators against post-Spec state,
// patch Status if changed. Conflict on either patch restarts the entire
// pipeline (re-GET + re-apply mutators) via retry.RetryOnConflict.
func (b *Batcher) flushObject(ctx context.Context, key objKey, e *entry) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		fresh := e.template.DeepCopyObject().(client.Object)
		if err := b.client.Get(ctx, key.nn, fresh); err != nil {
			return err
		}

		// Spec patch
		oldSpec := fresh.DeepCopyObject().(client.Object)
		if applyMutators(fresh, e.specMutators) {
			logDiff(oldSpec, fresh, "spec")
			if err := b.client.Patch(ctx, fresh,
				client.MergeFromWithOptions(oldSpec, client.MergeFromWithOptimisticLock{})); err != nil {
				return err
			}
		}

		// Status patch. client.Patch above writes the API server's response
		// (including the bumped resourceVersion) back into fresh on success,
		// so this snapshot is the post-Spec-patch state — the optimistic
		// lock on the Status patch will use the correct resourceVersion.
		oldStatus := fresh.DeepCopyObject().(client.Object)
		if applyMutators(fresh, e.statusMutators) {
			logDiff(oldStatus, fresh, "status")
			if err := b.client.Status().Patch(ctx, fresh,
				client.MergeFromWithOptions(oldStatus, client.MergeFromWithOptimisticLock{})); err != nil {
				return err
			}
		}
		return nil
	})
}

// applyMutators runs each mutator against fresh in queue order — never
// short-circuits, so later mutators always observe earlier mutators'
// changes. Returns true if any mutator returned true (a later false does
// not undo an earlier true), signalling the caller should issue a patch.
func applyMutators(fresh client.Object, mutators []func(client.Object) bool) bool {
	changed := false
	for _, m := range mutators {
		if m(fresh) {
			changed = true
		}
	}
	return changed
}

// logDiff prints a debug-level diff of the patched sub-resource. No-op
// unless logrus debug level is enabled.
func logDiff(before, after client.Object, kind string) {
	if !logrus.IsLevelEnabled(logrus.DebugLevel) {
		return
	}
	diff := cmp.Diff(before, after)
	if diff == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "k8sbatch %s diff (%s/%s):\n%s\n",
		kind, before.GetNamespace(), before.GetName(), diff)
}
