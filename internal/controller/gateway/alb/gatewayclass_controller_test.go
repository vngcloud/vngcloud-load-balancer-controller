package alb

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ = Describe("GatewayClass reconciler", func() {
	It("accepts a GatewayClass with our controllerName and no parametersRef", func() {
		const name = "vngcloud-alb-accept-test"
		gc := &gwv1.GatewayClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController("gateway.vks.vngcloud.vn/alb")},
		}
		Expect(k8sClient.Create(ctx, gc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

		Eventually(func(g Gomega) {
			out := &gwv1.GatewayClass{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, out)).To(Succeed())
			var accepted *metav1.Condition
			for i := range out.Status.Conditions {
				if out.Status.Conditions[i].Type == string(gwv1.GatewayClassConditionStatusAccepted) {
					accepted = &out.Status.Conditions[i]
				}
			}
			g.Expect(accepted).NotTo(BeNil(), "Accepted condition not yet present")
			g.Expect(accepted.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(accepted.Reason).To(Equal(string(gwv1.GatewayClassReasonAccepted)))
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())
	})

	It("ignores GatewayClasses with a different controllerName", func() {
		const name = "vngcloud-alb-ignore-test"
		gc := &gwv1.GatewayClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController("other.example.com/foo")},
		}
		Expect(k8sClient.Create(ctx, gc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

		// The CRD itself defaults status.conditions to {Accepted=Unknown, reason=Pending}.
		// Our reconciler must not overwrite that for foreign controllerNames — verify the
		// default condition stays untouched (status=Unknown / reason=Pending) over a
		// reconcile window.
		Consistently(func(g Gomega) {
			out := &gwv1.GatewayClass{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, out)).To(Succeed())
			var accepted *metav1.Condition
			for i := range out.Status.Conditions {
				if out.Status.Conditions[i].Type == string(gwv1.GatewayClassConditionStatusAccepted) {
					accepted = &out.Status.Conditions[i]
				}
			}
			g.Expect(accepted).NotTo(BeNil())
			g.Expect(accepted.Status).To(Equal(metav1.ConditionUnknown),
				"foreign GatewayClass Accepted status should stay Unknown; got %v (reason=%s)",
				accepted.Status, accepted.Reason)
			g.Expect(accepted.Reason).To(Equal(string(gwv1.GatewayClassReasonPending)))
		}, 3*time.Second, 200*time.Millisecond).Should(Succeed())
	})
})
