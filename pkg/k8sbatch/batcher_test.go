package k8sbatch_test

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8sbatch"
)

var _ = Describe("Batcher", func() {
	Describe("New", func() {
		It("returns a non-nil Batcher with Pending() == 0", func() {
			b := k8sbatch.New(k8sClient)
			Expect(b).NotTo(BeNil())
			Expect(b.Pending()).To(Equal(0))
		})
	})
})

var _ = Describe("Batcher Flush — single status mutator", func() {
	var ns string

	BeforeEach(func() {
		ns = fmt.Sprintf("k8sbatch-%d-%d", GinkgoRandomSeed(), CurrentSpecReport().LeafNodeLocation.LineNumber)
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
	})

	It("issues one Status patch when a mutator returns true", func() {
		lbc := newTestLBC(ns, "lbc-1")
		Expect(k8sClient.Create(ctx, lbc)).To(Succeed())

		b := k8sbatch.New(k8sClient)
		k8sbatch.MutateStatus(b, lbc, func(o *v1alpha1.LoadBalancerConfig) bool {
			o.Status.LastReconcileMessage = "patched-by-batcher"
			return true
		})

		Expect(b.Flush(ctx)).To(Succeed())
		Expect(b.Pending()).To(Equal(0))

		got := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbc), got)).To(Succeed())
		Expect(got.Status.LastReconcileMessage).To(Equal("patched-by-batcher"))
	})

	It("does not patch when the mutator returns false", func() {
		lbc := newTestLBC(ns, "lbc-noop")
		Expect(k8sClient.Create(ctx, lbc)).To(Succeed())

		// Capture the original ResourceVersion. If no patch happens, RV stays.
		original := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbc), original)).To(Succeed())
		originalRV := original.ResourceVersion

		b := k8sbatch.New(k8sClient)
		k8sbatch.MutateStatus(b, lbc, func(o *v1alpha1.LoadBalancerConfig) bool {
			return false // no change
		})

		Expect(b.Flush(ctx)).To(Succeed())
		Expect(b.Pending()).To(Equal(0))

		got := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbc), got)).To(Succeed())
		Expect(got.ResourceVersion).To(Equal(originalRV), "expected no patch and therefore unchanged ResourceVersion")
	})
})

var _ = Describe("Batcher Flush — multiple mutators on one object", func() {
	var ns string

	BeforeEach(func() {
		ns = fmt.Sprintf("k8sbatch-multi-%d", GinkgoRandomSeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
	})

	It("applies status mutators in queue order and issues one patch", func() {
		lbc := newTestLBC(ns, "lbc-order")
		Expect(k8sClient.Create(ctx, lbc)).To(Succeed())

		b := k8sbatch.New(k8sClient)
		// Mutator A sets the message to "first".
		k8sbatch.MutateStatus(b, lbc, func(o *v1alpha1.LoadBalancerConfig) bool {
			o.Status.LastReconcileMessage = "first"
			return true
		})
		// Mutator B asserts A ran, then overwrites with "second".
		k8sbatch.MutateStatus(b, lbc, func(o *v1alpha1.LoadBalancerConfig) bool {
			Expect(o.Status.LastReconcileMessage).To(Equal("first"),
				"mutator B should observe mutator A's change")
			o.Status.LastReconcileMessage = "second"
			return true
		})

		Expect(b.Flush(ctx)).To(Succeed())

		got := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbc), got)).To(Succeed())
		Expect(got.Status.LastReconcileMessage).To(Equal("second"))
	})
})

var _ = Describe("Batcher Flush — NotFound on GET", func() {
	var ns string

	BeforeEach(func() {
		ns = fmt.Sprintf("k8sbatch-nf-%d", GinkgoRandomSeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
	})

	It("returns the NotFound error and leaves the entry queued", func() {
		// Note: never created in the API server.
		ghost := newTestLBC(ns, "does-not-exist")

		b := k8sbatch.New(k8sClient)
		k8sbatch.MutateStatus(b, ghost, func(o *v1alpha1.LoadBalancerConfig) bool {
			o.Status.LastReconcileMessage = "should-never-land"
			return true
		})
		Expect(b.Pending()).To(Equal(1))

		err := b.Flush(ctx)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"expected the joined error to unwrap to a NotFound; got: %v", err)
		Expect(b.Pending()).To(Equal(1), "failed entry should remain queued")
	})
})

var _ = Describe("Batcher Flush — Spec mutator", func() {
	var ns string

	BeforeEach(func() {
		ns = fmt.Sprintf("k8sbatch-spec-%d-%d",
			GinkgoRandomSeed(),
			CurrentSpecReport().LeafNodeLocation.LineNumber)
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
	})

	It("issues one Spec patch and no Status patch when only Spec is queued", func() {
		nsg := newTestNSG(ns, "nsg-spec")
		Expect(k8sClient.Create(ctx, nsg)).To(Succeed())

		b := k8sbatch.New(k8sClient)
		k8sbatch.MutateSpec(b, nsg, func(o *v1alpha1.NodeSecurityGroup) bool {
			o.Spec.AttachSecurityGroups = []string{"sg-foo", "sg-bar"}
			return true
		})
		Expect(b.Flush(ctx)).To(Succeed())
		Expect(b.Pending()).To(Equal(0))

		got := &v1alpha1.NodeSecurityGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nsg), got)).To(Succeed())
		Expect(got.Spec.AttachSecurityGroups).To(Equal([]string{"sg-foo", "sg-bar"}))
	})

	It("patches Spec first then Status against the post-Spec state", func() {
		const sg = "sg-1"
		nsg := newTestNSG(ns, "nsg-both")
		Expect(k8sClient.Create(ctx, nsg)).To(Succeed())

		b := k8sbatch.New(k8sClient)

		// Spec mutator changes AttachSecurityGroups.
		k8sbatch.MutateSpec(b, nsg, func(o *v1alpha1.NodeSecurityGroup) bool {
			o.Spec.AttachSecurityGroups = []string{sg}
			return true
		})
		// Status mutator runs AFTER Spec; observes the post-Spec state.
		k8sbatch.MutateStatus(b, nsg, func(o *v1alpha1.NodeSecurityGroup) bool {
			Expect(o.Spec.AttachSecurityGroups).To(Equal([]string{sg}),
				"status mutator should run against post-Spec state")
			// Echo a summary of the Spec change into Status to verify ordering.
			o.Status.LastReconcileMessage = "applied:" + sg
			return true
		})

		Expect(b.Flush(ctx)).To(Succeed())

		got := &v1alpha1.NodeSecurityGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nsg), got)).To(Succeed())
		Expect(got.Spec.AttachSecurityGroups).To(Equal([]string{sg}))
		Expect(got.Status.LastReconcileMessage).To(Equal("applied:" + sg))
	})

	It("skips Status when Spec patch fails and keeps both queued", func() {
		nsg := newTestNSG(ns, "nsg-fail")
		Expect(k8sClient.Create(ctx, nsg)).To(Succeed())

		// Construct a batcher whose Spec patch always fails. retry.RetryOnConflict
		// only retries on Conflict, so a non-conflict error returns immediately.
		failingErr := errors.New("forced spec patch failure")
		wrapped := &failingPatchClient{
			Client:    k8sClient,
			err:       failingErr,
			remaining: 1000, // effectively forever
		}
		b := k8sbatch.New(wrapped)

		statusCalled := false
		k8sbatch.MutateSpec(b, nsg, func(o *v1alpha1.NodeSecurityGroup) bool {
			o.Spec.AttachSecurityGroups = []string{"sg-new"}
			return true
		})
		k8sbatch.MutateStatus(b, nsg, func(o *v1alpha1.NodeSecurityGroup) bool {
			statusCalled = true
			return true
		})

		err := b.Flush(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("forced spec patch failure"))
		Expect(statusCalled).To(BeFalse(), "status mutator must not run when spec patch fails")
		Expect(b.Pending()).To(Equal(1), "failed entry should remain queued")

		// Sanity check: the live object's Spec is unchanged (zero-value, since
		// newTestNSG doesn't set AttachSecurityGroups).
		got := &v1alpha1.NodeSecurityGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nsg), got)).To(Succeed())
		Expect(got.Spec.AttachSecurityGroups).To(BeEmpty())
	})
})

var _ = Describe("Batcher Flush — best-effort across objects", func() {
	var ns string

	BeforeEach(func() {
		ns = fmt.Sprintf("k8sbatch-multi-obj-%d", GinkgoRandomSeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
	})

	It("attempts every object even if one fails and returns joined errors", func() {
		// Real, existing object — patch should succeed.
		ok := newTestLBC(ns, "lbc-ok")
		Expect(k8sClient.Create(ctx, ok)).To(Succeed())

		// Ghost object — GET will return NotFound.
		ghost := newTestLBC(ns, "lbc-ghost")

		b := k8sbatch.New(k8sClient)
		k8sbatch.MutateStatus(b, ok, func(o *v1alpha1.LoadBalancerConfig) bool {
			o.Status.LastReconcileMessage = "msg-ok"
			return true
		})
		k8sbatch.MutateStatus(b, ghost, func(o *v1alpha1.LoadBalancerConfig) bool {
			o.Status.LastReconcileMessage = "msg-ghost"
			return true
		})

		err := b.Flush(ctx)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"joined error should unwrap to NotFound; got: %v", err)

		// Successful object cleared; failed object remains queued.
		Expect(b.Pending()).To(Equal(1))

		// Successful patch landed.
		got := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ok), got)).To(Succeed())
		Expect(got.Status.LastReconcileMessage).To(Equal("msg-ok"))
	})
})

var _ = Describe("Batcher Flush — mid-reconcile flushes", func() {
	var ns string

	BeforeEach(func() {
		ns = fmt.Sprintf("k8sbatch-midflush-%d", GinkgoRandomSeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
	})

	It("re-GETs on the second Flush so mutators see the current cluster state", func() {
		lbc := newTestLBC(ns, "lbc-mid")
		Expect(k8sClient.Create(ctx, lbc)).To(Succeed())

		b := k8sbatch.New(k8sClient)

		// First flush: set message to "first".
		k8sbatch.MutateStatus(b, lbc, func(o *v1alpha1.LoadBalancerConfig) bool {
			o.Status.LastReconcileMessage = "first"
			return true
		})
		Expect(b.Flush(ctx)).To(Succeed())
		Expect(b.Pending()).To(Equal(0))

		// Out-of-band: someone else patches the status.
		external := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbc), external)).To(Succeed())
		externalCopy := external.DeepCopy()
		external.Status.LastReconcileMessage = "external"
		Expect(k8sClient.Status().Patch(ctx, external,
			client.MergeFrom(externalCopy))).To(Succeed())

		// Second flush: mutator must observe "external" (proving fresh GET).
		k8sbatch.MutateStatus(b, lbc, func(o *v1alpha1.LoadBalancerConfig) bool {
			Expect(o.Status.LastReconcileMessage).To(Equal("external"),
				"second Flush should re-GET, not reuse first Flush's cached state")
			o.Status.LastReconcileMessage = "second"
			return true
		})
		Expect(b.Flush(ctx)).To(Succeed())

		got := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbc), got)).To(Succeed())
		Expect(got.Status.LastReconcileMessage).To(Equal("second"))
	})
})

var _ = Describe("Batcher Flush — real API server conflict retry", func() {
	var ns string

	BeforeEach(func() {
		ns = fmt.Sprintf("k8sbatch-conflict-%d", GinkgoRandomSeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
	})

	It("re-GETs and re-applies the mutator after a conflict", func() {
		lbc := newTestLBC(ns, "lbc-conflict")
		Expect(k8sClient.Create(ctx, lbc)).To(Succeed())

		invocations := 0
		mutator := func(o *v1alpha1.LoadBalancerConfig) bool {
			invocations++
			if invocations == 1 {
				// Out-of-band patch: change the same object so the upcoming
				// optimistic-lock PATCH will 409.
				external := &v1alpha1.LoadBalancerConfig{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbc), external)).To(Succeed())
				orig := external.DeepCopy()
				external.Status.LastReconcileMessage = "out-of-band"
				Expect(k8sClient.Status().Patch(ctx, external,
					client.MergeFrom(orig))).To(Succeed())
			}
			// On both invocations, set the desired final value.
			o.Status.LastReconcileMessage = "final"
			return true
		}

		b := k8sbatch.New(k8sClient)
		k8sbatch.MutateStatus(b, lbc, mutator)
		Expect(b.Flush(ctx)).To(Succeed())
		Expect(invocations).To(BeNumerically(">=", 2),
			"mutator should be re-invoked after the first patch's conflict")

		got := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbc), got)).To(Succeed())
		Expect(got.Status.LastReconcileMessage).To(Equal("final"))
	})
})

var _ = Describe("Batcher — identity coalescing", func() {
	var ns string

	BeforeEach(func() {
		ns = fmt.Sprintf("k8sbatch-coalesce-%d", GinkgoRandomSeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
	})

	It("treats two distinct *T pointers with the same identity as one entry", func() {
		lbcA := newTestLBC(ns, "lbc-coalesce")
		Expect(k8sClient.Create(ctx, lbcA)).To(Succeed())

		// Different in-memory pointer, same Name/Namespace/GVK.
		lbcB := newTestLBC(ns, "lbc-coalesce")
		Expect(lbcA).NotTo(BeIdenticalTo(lbcB))

		b := k8sbatch.New(k8sClient)
		k8sbatch.MutateStatus(b, lbcA, func(o *v1alpha1.LoadBalancerConfig) bool {
			o.Status.LastReconcileMessage = "from-A"
			return true
		})
		k8sbatch.MutateStatus(b, lbcB, func(o *v1alpha1.LoadBalancerConfig) bool {
			Expect(o.Status.LastReconcileMessage).To(Equal("from-A"),
				"both mutators should run against the same fresh GET")
			o.Status.LastReconcileMessage = "from-B"
			return true
		})
		Expect(b.Pending()).To(Equal(1), "should coalesce by identity, not pointer")

		Expect(b.Flush(ctx)).To(Succeed())

		got := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbcA), got)).To(Succeed())
		Expect(got.Status.LastReconcileMessage).To(Equal("from-B"))
	})
})

var _ = Describe("Batcher — multiple types", func() {
	var ns string

	BeforeEach(func() {
		ns = fmt.Sprintf("k8sbatch-multitype-%d", GinkgoRandomSeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
	})

	It("flushes mutations against different CRD types in one Flush", func() {
		lbc := newTestLBC(ns, "lbc-mt")
		nsg := newTestNSG(ns, "nsg-mt")
		Expect(k8sClient.Create(ctx, lbc)).To(Succeed())
		Expect(k8sClient.Create(ctx, nsg)).To(Succeed())

		b := k8sbatch.New(k8sClient)
		k8sbatch.MutateStatus(b, lbc, func(o *v1alpha1.LoadBalancerConfig) bool {
			o.Status.LastReconcileMessage = "lbc-mt-msg"
			return true
		})
		k8sbatch.MutateStatus(b, nsg, func(o *v1alpha1.NodeSecurityGroup) bool {
			o.Status.LastReconcileMessage = "nsg-mt-msg"
			return true
		})
		Expect(b.Pending()).To(Equal(2))

		Expect(b.Flush(ctx)).To(Succeed())
		Expect(b.Pending()).To(Equal(0))

		gotLBC := &v1alpha1.LoadBalancerConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(lbc), gotLBC)).To(Succeed())
		Expect(gotLBC.Status.LastReconcileMessage).To(Equal("lbc-mt-msg"))

		gotNSG := &v1alpha1.NodeSecurityGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nsg), gotNSG)).To(Succeed())
		Expect(gotNSG.Status.LastReconcileMessage).To(Equal("nsg-mt-msg"))
	})
})

// failingPatchClient wraps a client.Client and returns the configured
// error from the next n calls to Patch (non-status). Status patches and
// Get/Create/Delete pass through unchanged. Used in tests to force
// deterministic Spec-patch failures.
type failingPatchClient struct {
	client.Client
	err       error
	remaining int
}

func (f *failingPatchClient) Patch(
	ctx context.Context,
	obj client.Object,
	patch client.Patch,
	opts ...client.PatchOption,
) error {
	if f.remaining > 0 {
		f.remaining--
		return f.err
	}
	return f.Client.Patch(ctx, obj, patch, opts...)
}
