package alb

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

const (
	albTimeout  = time.Second * 30
	albInterval = time.Millisecond * 250
	testNS      = "default"
)

// newALBGatewayClass creates a GatewayClass that the ALB controller claims.
func newALBGatewayClass(name string) *gwv1.GatewayClass {
	return &gwv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: gwv1.GatewayClassSpec{
			ControllerName: consts.GatewayClassControllerNameALB,
		},
	}
}

// newGateway creates a Gateway under the given GatewayClass with one HTTP listener.
func newGateway(name, ns, gwClassName string) *gwv1.Gateway {
	return &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: gwv1.GatewaySpec{
			GatewayClassName: gwv1.ObjectName(gwClassName),
			Listeners: []gwv1.Listener{
				{
					Name:     "http",
					Port:     80,
					Protocol: gwv1.HTTPProtocolType,
				},
			},
		},
	}
}

// newNodePortSvc creates a minimal NodePort Service for testing.
func newNodePortSvc(name, ns string, port int32, nodePort int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{
					Name:     "http",
					Port:     port,
					Protocol: corev1.ProtocolTCP,
					NodePort: nodePort,
				},
			},
			Selector: map[string]string{"app": name},
		},
	}
}

// newHTTPRoute creates a simple HTTPRoute pointing at a backend service and a
// parent Gateway.
// nolint:unparam
func newHTTPRoute(name, ns, gwName, svcName string, svcPort int32) *gwv1.HTTPRoute {
	port := gwv1.PortNumber(svcPort)
	return &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{
				ParentRefs: []gwv1.ParentReference{
					{
						Name: gwv1.ObjectName(gwName),
					},
				},
			},
			Rules: []gwv1.HTTPRouteRule{
				{
					BackendRefs: []gwv1.HTTPBackendRef{
						{
							BackendRef: gwv1.BackendRef{
								BackendObjectReference: gwv1.BackendObjectReference{
									Name: gwv1.ObjectName(svcName),
									Port: &port,
								},
							},
						},
					},
				},
			},
		},
	}
}

// findLBCByGatewayOwnerLabels lists LBCs owned by the given Gateway.
func findLBCsByGatewayOwnerLabels(gwName, ns string) (*v1alpha1.LoadBalancerConfigList, error) {
	lbcList := &v1alpha1.LoadBalancerConfigList{}
	err := k8sClient.List(ctx, lbcList,
		client.InNamespace(ns),
		client.MatchingLabels{
			domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
			domain.LabelOwnerResourceName: gwName,
		},
	)
	return lbcList, err
}

var _ = Describe("ALB Gateway Controller", func() {

	Context("GatewayClass reconciliation", func() {

		It("should set Accepted=True on a GatewayClass with our controllerName", func() {
			gc := newALBGatewayClass("alb-gc-accept")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-accept"}, got)).To(Succeed())
				var accepted *metav1.Condition
				for i := range got.Status.Conditions {
					if got.Status.Conditions[i].Type == string(gwv1.GatewayClassConditionStatusAccepted) {
						accepted = &got.Status.Conditions[i]
						break
					}
				}
				g.Expect(accepted).NotTo(BeNil(), "expected Accepted condition")
				g.Expect(accepted.Status).To(Equal(metav1.ConditionTrue))
			}, albTimeout, albInterval).Should(Succeed())
		})

		It("should not write Accepted=True condition on a GatewayClass with a different controllerName", func() {
			gc := &gwv1.GatewayClass{
				ObjectMeta: metav1.ObjectMeta{Name: "other-gc"},
				Spec: gwv1.GatewayClassSpec{
					ControllerName: "gateway.example.io/other",
				},
			}
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			// Give the reconciler time to potentially (wrongly) act on this object.
			// The API server may pre-populate a "Pending" condition; what we verify is
			// that our controller never writes Accepted=True.
			Consistently(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "other-gc"}, got)).To(Succeed())
				for _, c := range got.Status.Conditions {
					if c.Type == string(gwv1.GatewayClassConditionStatusAccepted) {
						g.Expect(c.Status).NotTo(Equal(metav1.ConditionTrue),
							"expected our ALB controller NOT to set Accepted=True for a non-ALB GatewayClass")
					}
				}
			}, 3*time.Second, albInterval).Should(Succeed())
		})

	})

	Context("Gateway → LBC translation", func() {

		It("should create an LBC owned by the Gateway", func() {
			// Create the GatewayClass first
			gc := newALBGatewayClass("alb-gc-lbc")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			// Wait for GatewayClass to be accepted so the predicate is satisfied
			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-lbc"}, got)).To(Succeed())
				g.Expect(got.Status.Conditions).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			// Create a NodePort service
			svc := newNodePortSvc("alb-svc-lbc", testNS, 80, 30180)
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

			// Create a Gateway
			gw := newGateway("alb-gw-lbc", testNS, "alb-gc-lbc")
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

			// Create an HTTPRoute parented by the Gateway
			route := newHTTPRoute("alb-route-lbc", testNS, "alb-gw-lbc", "alb-svc-lbc", 80)
			Expect(k8sClient.Create(ctx, route)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

			// Eventually: an LBC should be created with Gateway owner labels
			Eventually(func(g Gomega) {
				lbcList, err := findLBCsByGatewayOwnerLabels("alb-gw-lbc", testNS)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(lbcList.Items).NotTo(BeEmpty(), "expected LBC to be created for Gateway")

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Labels[domain.LabelOwnerResourceKind]).To(Equal(domain.OwnerKindGateway))
				g.Expect(lbc.Labels[domain.LabelOwnerResourceName]).To(Equal("alb-gw-lbc"))
			}, albTimeout, albInterval).Should(Succeed())
		})

		It("should write HTTPRoute Accepted + ResolvedRefs status on its parent", func() {
			gc := newALBGatewayClass("alb-gc-rstatus")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })
			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-rstatus"}, got)).To(Succeed())
				g.Expect(got.Status.Conditions).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			svc := newNodePortSvc("alb-svc-rstatus", testNS, 80, 30182)
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

			gw := newGateway("alb-gw-rstatus", testNS, "alb-gc-rstatus")
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

			route := newHTTPRoute("alb-route-rstatus", testNS, "alb-gw-rstatus", "alb-svc-rstatus", 80)
			Expect(k8sClient.Create(ctx, route)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

			findCond := func(conds []metav1.Condition, t string) *metav1.Condition {
				for i := range conds {
					if conds[i].Type == t {
						return &conds[i]
					}
				}
				return nil
			}
			Eventually(func(g Gomega) {
				got := &gwv1.HTTPRoute{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-route-rstatus", Namespace: testNS}, got)).To(Succeed())
				var parent *gwv1.RouteParentStatus
				for i := range got.Status.Parents {
					if string(got.Status.Parents[i].ControllerName) == consts.GatewayClassControllerNameALB {
						parent = &got.Status.Parents[i]
						break
					}
				}
				g.Expect(parent).NotTo(BeNil(), "expected our controller's parent status entry")
				accepted := findCond(parent.Conditions, string(gwv1.RouteConditionAccepted))
				g.Expect(accepted).NotTo(BeNil())
				g.Expect(accepted.Status).To(Equal(metav1.ConditionTrue))
				resolved := findCond(parent.Conditions, string(gwv1.RouteConditionResolvedRefs))
				g.Expect(resolved).NotTo(BeNil())
				g.Expect(resolved.Status).To(Equal(metav1.ConditionTrue))
			}, albTimeout, albInterval).Should(Succeed())
		})

		It("should set Gateway.Status Accepted condition", func() {
			// Create GatewayClass
			gc := newALBGatewayClass("alb-gc-status")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			// Wait for GatewayClass to be accepted
			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-status"}, got)).To(Succeed())
				g.Expect(got.Status.Conditions).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			svc := newNodePortSvc("alb-svc-status", testNS, 80, 30181)
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

			gw := newGateway("alb-gw-status", testNS, "alb-gc-status")
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

			// Eventually: Gateway should have Accepted condition from the use case
			Eventually(func(g Gomega) {
				got := &gwv1.Gateway{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gw-status", Namespace: testNS}, got)).To(Succeed())

				var accepted *metav1.Condition
				for i := range got.Status.Conditions {
					if got.Status.Conditions[i].Type == string(gwv1.GatewayConditionAccepted) {
						accepted = &got.Status.Conditions[i]
						break
					}
				}
				g.Expect(accepted).NotTo(BeNil(), "expected Accepted condition on Gateway")
				g.Expect(accepted.Status).To(Equal(metav1.ConditionTrue))
			}, albTimeout, albInterval).Should(Succeed())
		})

		It("should not create LBC for a Gateway with a non-ALB class", func() {
			// Create a non-ALB GatewayClass
			otherGC := &gwv1.GatewayClass{
				ObjectMeta: metav1.ObjectMeta{Name: "other-gc-no-lbc"},
				Spec: gwv1.GatewayClassSpec{
					ControllerName: "gateway.example.io/other",
				},
			}
			Expect(k8sClient.Create(ctx, otherGC)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, otherGC) })

			gw := newGateway("alb-gw-wrong-class", testNS, "other-gc-no-lbc")
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

			// Consistently: no LBC should be created
			Consistently(func(g Gomega) {
				lbcList, err := findLBCsByGatewayOwnerLabels("alb-gw-wrong-class", testNS)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(lbcList.Items).To(BeEmpty(),
					"expected no LBC for Gateway with non-ALB class")
			}, 4*time.Second, albInterval).Should(Succeed())
		})

	})

	Context("Gateway deletion", func() {

		It("should garbage-collect the LBC when the Gateway is deleted", func() {
			// Create GatewayClass
			gc := newALBGatewayClass("alb-gc-del")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			// Wait for GatewayClass to be accepted
			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-del"}, got)).To(Succeed())
				g.Expect(got.Status.Conditions).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			svc := newNodePortSvc("alb-svc-del", testNS, 80, 30182)
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

			gw := newGateway("alb-gw-del", testNS, "alb-gc-del")
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())

			// Wait until LBC is created
			Eventually(func(g Gomega) {
				lbcList, err := findLBCsByGatewayOwnerLabels("alb-gw-del", testNS)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(lbcList.Items).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			// Delete the Gateway
			Expect(k8sClient.Delete(ctx, gw)).To(Succeed())

			// Eventually: LBC should be gone (no items with those owner labels, all deleted)
			Eventually(func(g Gomega) {
				lbcList := &v1alpha1.LoadBalancerConfigList{}
				err := k8sClient.List(ctx, lbcList,
					client.InNamespace(testNS),
					client.MatchingLabels{
						domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
						domain.LabelOwnerResourceName: "alb-gw-del",
					},
				)
				g.Expect(err).NotTo(HaveOccurred())

				// Filter to non-deleted items
				var live []v1alpha1.LoadBalancerConfig
				for _, item := range lbcList.Items {
					if item.DeletionTimestamp.IsZero() {
						live = append(live, item)
					}
				}
				g.Expect(live).To(BeEmpty(), "expected LBC owned by Gateway to be garbage-collected")
			}, albTimeout*2, albInterval).Should(Succeed())

			// Also verify Gateway itself is gone
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gw-del", Namespace: testNS}, &gwv1.Gateway{})
				return apierrors.IsNotFound(err)
			}, albTimeout, albInterval).Should(BeTrue(), "expected Gateway to be fully deleted")
		})

	})

})
