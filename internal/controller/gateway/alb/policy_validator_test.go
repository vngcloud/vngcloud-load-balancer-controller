package alb

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

var _ = Describe("VKS policy validators", func() {

	gwPolicyTargeting := func(name, gwName string) *gwv1alpha1.VKSGatewayPolicy {
		return &gwv1alpha1.VKSGatewayPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
			Spec: gwv1alpha1.VKSGatewayPolicySpec{
				TargetRefs: []gwv1alpha1.LocalPolicyTargetReferenceWithSectionName{{
					LocalPolicyTargetReference: gwv1alpha2.LocalPolicyTargetReference{
						Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: gwv1alpha2.ObjectName(gwName),
					},
				}},
			},
		}
	}

	acceptedReason := func(p *gwv1alpha1.VKSGatewayPolicy) string {
		for _, c := range p.Status.Conditions {
			if c.Type == gwv1alpha1.PolicyConditionAccepted {
				return c.Reason
			}
		}
		return ""
	}

	It("reports the newer of two policies targeting the same Gateway as Conflicted", func() {
		gc := newALBGatewayClass("alb-gc-pol")
		Expect(k8sClient.Create(ctx, gc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })
		Eventually(func(g Gomega) {
			got := &gwv1.GatewayClass{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-pol"}, got)).To(Succeed())
			g.Expect(got.Status.Conditions).NotTo(BeEmpty())
		}, albTimeout, albInterval).Should(Succeed())

		gw := newGateway("alb-gw-pol", testNS, "alb-gc-pol")
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

		older := gwPolicyTargeting("pol-a-older", "alb-gw-pol") // created first + lex-first -> winner
		Expect(k8sClient.Create(ctx, older)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, older) })
		newer := gwPolicyTargeting("pol-b-newer", "alb-gw-pol")
		Expect(k8sClient.Create(ctx, newer)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, newer) })

		Eventually(func(g Gomega) {
			a := &gwv1alpha1.VKSGatewayPolicy{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "pol-a-older", Namespace: testNS}, a)).To(Succeed())
			b := &gwv1alpha1.VKSGatewayPolicy{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "pol-b-newer", Namespace: testNS}, b)).To(Succeed())
			g.Expect(acceptedReason(a)).To(Equal(gwv1alpha1.PolicyReasonAccepted))
			g.Expect(acceptedReason(b)).To(Equal(gwv1alpha1.PolicyReasonConflicted))
			g.Expect(b.Status.Ancestors).NotTo(BeEmpty())
		}, albTimeout, albInterval).Should(Succeed())
	})

	It("reports TargetNotFound when the target Gateway is absent", func() {
		pol := gwPolicyTargeting("pol-orphan", "no-such-gw")
		Expect(k8sClient.Create(ctx, pol)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pol) })

		Eventually(func(g Gomega) {
			got := &gwv1alpha1.VKSGatewayPolicy{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "pol-orphan", Namespace: testNS}, got)).To(Succeed())
			g.Expect(acceptedReason(got)).To(Equal(gwv1alpha1.PolicyReasonTargetNotFound))
		}, albTimeout, albInterval).Should(Succeed())
	})
})
