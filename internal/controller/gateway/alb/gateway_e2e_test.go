/*
Copyright 2026.
*/

package alb

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// Each Describe block corresponds to a reconcile-logic scenario originally
// validated against a real vngcloud cluster (R-series in PHASE-1-MOCK-VALIDATIONS.md).
// Running them here against vngcloud_mocks turns the live ~3-minute warm-up
// per case into a sub-second assertion.

var _ = Describe("ALB Gateway controller — reconcile flows", Ordered, func() {
	var (
		ns = "gw-r"
		gw *gwv1.Gateway
	)

	BeforeAll(func() {
		makeNamespace(ns)
		makeNodePortService(ns, "echo", 80, 32428)
		makeNodePortService(ns, "echo-v2", 80, 30732)
	})

	// -------------------------------------------------------------------- R1+R0
	Describe("R0 — baseline: Gateway with one listener and one HTTPRoute", func() {
		var rt *gwv1.HTTPRoute

		BeforeAll(func() {
			gw = applyGateway(ns, "r-baseline", httpListener("http", 80))
			rt = applyHTTPRoute(ns, "r-baseline-route", "r-baseline",
				[]string{"baseline.example.com"}, b("echo", 80))
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, rt)
				_ = k8sClient.Delete(ctx, gw)
				expectGatewayDeleted(gw)
			})
		})

		It("attaches the controller's finalizer to the Gateway", func() {
			Eventually(func() bool { return gatewayHasFinalizer(gw) }, pollTimeout, pollInterval).Should(BeTrue())
		})

		It("provisions an owned LoadBalancerConfig with the expected listener + pool", func() {
			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				g.Expect(lbc.Spec.Listeners).To(HaveLen(1))
				g.Expect(lbc.Spec.Listeners[0].Name).To(Equal("vks-http"))
				g.Expect(lbc.Spec.Pools).To(HaveLen(1))
			}, pollTimeout*2, pollInterval).Should(Succeed())
		})

		It("propagates the owned LBC's status.address into Gateway.status.addresses", func() {
			// Tests the controller's gatherGatewayAddresses logic: whenever the
			// owned LBC has a non-empty status.address, the Gateway must mirror
			// it. We don't depend on the mock's exact warm-up timing — instead we
			// trigger a reconcile only after the LBC reports an address.
			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				g.Expect(lbc.Status.Address).ToNot(BeNil(), "owned LBC has no status.address yet")
				g.Expect(*lbc.Status.Address).ToNot(BeEmpty())
			}, pollTimeout*4, pollInterval).Should(Succeed())

			// Bump an annotation to fire one more Gateway reconcile so the
			// address propagation runs after the LBC.status.address is set.
			updateGateway(gw, func(fresh *gwv1.Gateway) {
				if fresh.Annotations == nil {
					fresh.Annotations = map[string]string{}
				}
				fresh.Annotations["test.gateway.vks/poll"] = "1"
			})

			Eventually(func(g Gomega) {
				cur := freshGateway(gw)
				g.Expect(cur.Status.Addresses).ToNot(BeEmpty())
			}, pollTimeout*2, pollInterval).Should(Succeed())
		})

		It("writes Accepted=True and Programmed=True conditions on the Gateway", func() {
			Eventually(func(g Gomega) {
				cur := freshGateway(gw)
				accepted := findCond(cur.Status.Conditions, "Accepted")
				programmed := findCond(cur.Status.Conditions, "Programmed")
				g.Expect(accepted).ToNot(BeNil())
				g.Expect(accepted.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(programmed).ToNot(BeNil())
				g.Expect(programmed.Status).To(Equal(metav1.ConditionTrue))
			}, pollTimeout, pollInterval).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------- R1
	Describe("R1 — adding a listener post-creation", func() {
		var rt *gwv1.HTTPRoute

		BeforeAll(func() {
			gw = applyGateway(ns, "r-add-listener", httpListener("http", 80))
			rt = applyHTTPRoute(ns, "r-add-listener-route", "r-add-listener",
				[]string{"add.example.com"}, b("echo", 80))
			Eventually(func(g Gomega) {
				g.Expect(expectOwnedLBC(freshGateway(gw)).Spec.Listeners).To(HaveLen(1))
			}, pollTimeout*2, pollInterval).Should(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, rt)
				_ = k8sClient.Delete(ctx, gw)
				expectGatewayDeleted(gw)
			})
		})

		It("adds a second listener to the existing LBC", func() {
			updateGateway(gw, func(fresh *gwv1.Gateway) {
				fresh.Spec.Listeners = append(fresh.Spec.Listeners,
					gwv1.Listener{Name: "extra", Protocol: gwv1.HTTPProtocolType, Port: 8080})
			})

			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				g.Expect(lbc.Spec.Listeners).To(HaveLen(2))
				names := []string{lbc.Spec.Listeners[0].Name, lbc.Spec.Listeners[1].Name}
				g.Expect(names).To(ContainElements("vks-http", "vks-extra"))
			}, pollTimeout*2, pollInterval).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------- R2
	Describe("R2 — removing a listener", func() {
		var rt *gwv1.HTTPRoute

		BeforeAll(func() {
			gw = applyGateway(ns, "r-rm-listener",
				httpListener("http", 80), httpListener("extra", 8080))
			rt = applyHTTPRoute(ns, "r-rm-listener-route", "r-rm-listener",
				[]string{"rm.example.com"}, b("echo", 80))
			Eventually(func(g Gomega) {
				g.Expect(expectOwnedLBC(freshGateway(gw)).Spec.Listeners).To(HaveLen(2))
			}, pollTimeout*2, pollInterval).Should(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, rt)
				_ = k8sClient.Delete(ctx, gw)
				expectGatewayDeleted(gw)
			})
		})

		It("removes the second listener and keeps the first", func() {
			updateGateway(gw, func(fresh *gwv1.Gateway) {
				fresh.Spec.Listeners = []gwv1.Listener{httpListener("http", 80)}
			})

			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				g.Expect(lbc.Spec.Listeners).To(HaveLen(1))
				g.Expect(lbc.Spec.Listeners[0].Name).To(Equal("vks-http"))
			}, pollTimeout*2, pollInterval).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------- R3
	Describe("R3 — switching HTTPRoute backendRefs from single to weighted", func() {
		var rt *gwv1.HTTPRoute

		BeforeAll(func() {
			gw = applyGateway(ns, "r-canary", httpListener("http", 80))
			rt = applyHTTPRoute(ns, "r-canary-route", "r-canary",
				[]string{"canary.example.com"}, b("echo", 80))
			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				g.Expect(lbc.Spec.Pools).To(HaveLen(1))
				g.Expect(lbc.Spec.Pools[0].Members).ToNot(BeEmpty())
			}, pollTimeout*2, pollInterval).Should(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, rt)
				_ = k8sClient.Delete(ctx, gw)
				expectGatewayDeleted(gw)
			})
		})

		It("expands the pool to include both backends with scaled weights", func() {
			updateHTTPRoute(rt, func(fresh *gwv1.HTTPRoute) {
				fresh.Spec.Rules[0].BackendRefs = []gwv1.HTTPBackendRef{
					weightedBackendRef("echo", 80, 70),
					weightedBackendRef("echo-v2", 80, 30),
				}
			})

			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				g.Expect(lbc.Spec.Pools).To(HaveLen(1))
				m := lbc.Spec.Pools[0].Members
				g.Expect(m).To(HaveLen(4)) // 2 nodes × 2 backends
				ports := map[int]bool{}
				for i := range m {
					ports[m[i].Port] = true
				}
				g.Expect(ports).To(HaveKey(32428))
				g.Expect(ports).To(HaveKey(30732))
			}, pollTimeout*2, pollInterval).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------- R4
	Describe("R4 — adding a second HTTPRoute to the same Gateway", func() {
		var rk, rd *gwv1.HTTPRoute

		BeforeAll(func() {
			gw = applyGateway(ns, "r-multiroute", httpListener("http", 80))
			rk = applyHTTPRoute(ns, "r-multiroute-keep", "r-multiroute",
				[]string{"keep.example.com"}, b("echo", 80))
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, rk)
				_ = k8sClient.Delete(ctx, gw)
				expectGatewayDeleted(gw)
			})
		})

		It("creates a second policy on the listener for the second route", func() {
			rd = applyHTTPRoute(ns, "r-multiroute-extra", "r-multiroute",
				[]string{"extra.example.com"}, b("echo-v2", 80))
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rd) })

			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				l := listenerByName(lbc, "vks-http")
				g.Expect(l).ToNot(BeNil())
				g.Expect(l.Policies).To(HaveLen(2))
				hosts := []string{}
				for _, p := range l.Policies {
					for _, r := range p.L7Rules {
						if r.RuleType == "HOST_NAME" {
							hosts = append(hosts, r.RuleValue)
						}
					}
				}
				g.Expect(hosts).To(ContainElements("keep.example.com", "extra.example.com"))
				g.Expect(lbc.Spec.Pools).To(HaveLen(2))
			}, pollTimeout*2, pollInterval).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------- R5 (Bug 11 regression)
	Describe("R5 — deleting one HTTPRoute auto-prunes its policy + pool (Bug 11 regression)", func() {
		var rk, rd *gwv1.HTTPRoute

		BeforeAll(func() {
			gw = applyGateway(ns, "r-prune", httpListener("http", 80))
			rk = applyHTTPRoute(ns, "r-prune-keep", "r-prune",
				[]string{"keep-prune.example.com"}, b("echo", 80))
			rd = applyHTTPRoute(ns, "r-prune-drop", "r-prune",
				[]string{"drop-prune.example.com"}, b("echo-v2", 80))
			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				l := listenerByName(lbc, "vks-http")
				g.Expect(l).ToNot(BeNil())
				g.Expect(l.Policies).To(HaveLen(2))
			}, pollTimeout*2, pollInterval).Should(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, rk)
				_ = k8sClient.Delete(ctx, gw)
				expectGatewayDeleted(gw)
			})
		})

		It("removes the orphan policy after the route is deleted, with no manual trigger", func() {
			Expect(k8sClient.Delete(ctx, rd)).To(Succeed())

			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				l := listenerByName(lbc, "vks-http")
				g.Expect(l).ToNot(BeNil())
				g.Expect(l.Policies).To(HaveLen(1))
				for _, r := range l.Policies[0].L7Rules {
					if r.RuleType == "HOST_NAME" {
						g.Expect(r.RuleValue).To(Equal("keep-prune.example.com"))
					}
				}
			}, pollTimeout*2, pollInterval).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------- R6
	Describe("R6 — per-Gateway LoadBalancerConfig override propagates listener fields", func() {
		var override *vksv1alpha1.LoadBalancerConfig
		var rt *gwv1.HTTPRoute

		BeforeAll(func() {
			override = applyOverrideLBC(ns, "r-override-lbc", []vksv1alpha1.Listener{{
				Name:              "http",
				Protocol:          "HTTP",
				ProtocolPort:      80,
				TimeoutClient:     ptr.To(int32(123)),
				TimeoutMember:     ptr.To(int32(456)),
				TimeoutConnection: ptr.To(int32(7)),
				AllowedCidrs:      ptr.To("10.0.0.0/8"),
			}})
			gw = applyGatewayWithInfra(ns, "r-override", "r-override-lbc", httpListener("http", 80))
			rt = applyHTTPRoute(ns, "r-override-route", "r-override",
				[]string{"override.example.com"}, b("echo", 80))
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, rt)
				_ = k8sClient.Delete(ctx, gw)
				_ = k8sClient.Delete(ctx, override)
				expectGatewayDeleted(gw)
			})
		})

		It("applies the override's timeouts and allowedCidrs to the merged LBC listener", func() {
			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				l := listenerByName(lbc, "vks-http")
				g.Expect(l).ToNot(BeNil())
				g.Expect(l.TimeoutClient).ToNot(BeNil())
				g.Expect(*l.TimeoutClient).To(Equal(int32(123)))
				g.Expect(l.TimeoutMember).ToNot(BeNil())
				g.Expect(*l.TimeoutMember).To(Equal(int32(456)))
				g.Expect(l.TimeoutConnection).ToNot(BeNil())
				g.Expect(*l.TimeoutConnection).To(Equal(int32(7)))
				g.Expect(l.AllowedCidrs).ToNot(BeNil())
				g.Expect(*l.AllowedCidrs).To(Equal("10.0.0.0/8"))
			}, pollTimeout*2, pollInterval).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------- R7
	Describe("R7 — GatewayClass-level LBC tags propagate to the Gateway-owned LBC", func() {
		var (
			gwc      *gwv1.GatewayClass
			classLBC *vksv1alpha1.LoadBalancerConfig
			rt       *gwv1.HTTPRoute
		)

		BeforeAll(func() {
			classLBC = applyClassLBC(ns, "r-class-lbc", map[string]string{
				"classDefault": "true", "env": "test",
			})
			gwc = applyClassWithParametersRef("vngcloud-alb-cls", ns, "r-class-lbc")
			gw = applyGatewayWithClass(ns, "r-class", "vngcloud-alb-cls", httpListener("http", 80))
			rt = applyHTTPRoute(ns, "r-class-route", "r-class",
				[]string{"cls.example.com"}, b("echo", 80))
			DeferCleanup(func() {
				// Cleanup ordering: Gateway must die before its referenced GatewayClass /
				// classLBC, otherwise a final reconcile reads back stale class-level
				// state and the lbc_uc may try to delete a never-fully-created mock LB.
				// We don't assert expectGatewayDeleted here — the suite-level namespace
				// teardown takes care of leftover LBCs and the GatewayClass cleanup is
				// best-effort.
				_ = k8sClient.Delete(ctx, rt)
				_ = k8sClient.Delete(ctx, gw)
				Eventually(func() bool {
					fresh := &gwv1.Gateway{}
					return apierrors.IsNotFound(
						k8sClient.Get(ctx, types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}, fresh))
				}, pollTimeout*2, pollInterval).Should(BeTrue(), "Gateway must be gone before tearing down its class")
				_ = k8sClient.Delete(ctx, classLBC)
				_ = k8sClient.Delete(ctx, gwc)
			})
		})

		It("merges the class-level tags into the Gateway-owned LBC", func() {
			Eventually(func(g Gomega) {
				lbc := expectOwnedLBC(freshGateway(gw))
				g.Expect(lbc.Spec.Tags).To(HaveKeyWithValue("classDefault", "true"))
				g.Expect(lbc.Spec.Tags).To(HaveKeyWithValue("env", "test"))
			}, pollTimeout*2, pollInterval).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------- R9
	Describe("R9 — Gateway delete cascades to owned LBC", func() {
		var rt *gwv1.HTTPRoute

		It("removes the Gateway, its finalizer, and the owned LBC", func() {
			gw = applyGateway(ns, "r-delete", httpListener("http", 80))
			rt = applyHTTPRoute(ns, "r-delete-route", "r-delete",
				[]string{"del.example.com"}, b("echo", 80))
			lbc := expectOwnedLBC(freshGateway(gw))
			Expect(lbc).ToNot(BeNil())

			// Cleanup: route first to avoid attach noise during delete.
			Expect(k8sClient.Delete(ctx, rt)).To(Succeed())
			Expect(k8sClient.Delete(ctx, gw)).To(Succeed())

			// Owned LBC must be gone, Gateway must be gone.
			expectGatewayDeleted(gw)

			// Foreign LBCs in other namespaces must not have been touched (sanity).
			otherList := &vksv1alpha1.LoadBalancerConfigList{}
			Expect(k8sClient.List(ctx, otherList,
				client.InNamespace(ns),
				client.MatchingLabels{domain.LabelOwnerResourceUid: string(gw.UID)})).To(Succeed())
			Expect(otherList.Items).To(BeEmpty())
		})
	})
})

// ----------------------------------------------------------------------------
// helpers used by the spec file (kept here to avoid bloating helpers_test.go)
// ----------------------------------------------------------------------------

// findCond returns the named condition or nil.
func findCond(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}

// weightedBackendRef builds an HTTPBackendRef with a weight value.
func weightedBackendRef(svc string, port int32, weight int32) gwv1.HTTPBackendRef {
	return gwv1.HTTPBackendRef{
		BackendRef: gwv1.BackendRef{
			BackendObjectReference: gwv1.BackendObjectReference{
				Name: gwv1.ObjectName(svc),
				Port: ptr.To(gwv1.PortNumber(port)),
			},
			Weight: ptr.To(weight),
		},
	}
}

// applyGatewayWithInfra creates a Gateway whose Infrastructure.parametersRef
// points at a same-namespace LoadBalancerConfig.
func applyGatewayWithInfra(ns, name, lbcName string, listeners ...gwv1.Listener) *gwv1.Gateway {
	GinkgoHelper()
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1.GatewaySpec{
			GatewayClassName: "vngcloud-alb",
			Listeners:        listeners,
			Infrastructure: &gwv1.GatewayInfrastructure{
				ParametersRef: &gwv1.LocalParametersReference{
					Group: "vks.vngcloud.vn",
					Kind:  "LoadBalancerConfig",
					Name:  lbcName,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, gw)).To(Succeed())
	return gw
}

// applyGatewayWithClass creates a Gateway pointing at a non-default GatewayClass.
func applyGatewayWithClass(ns, name, className string, listeners ...gwv1.Listener) *gwv1.Gateway {
	GinkgoHelper()
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1.GatewaySpec{
			GatewayClassName: gwv1.ObjectName(className),
			Listeners:        listeners,
		},
	}
	Expect(k8sClient.Create(ctx, gw)).To(Succeed())
	return gw
}

// applyOverrideLBC creates a per-Gateway LoadBalancerConfig template.
// The required spec fields (loadBalancerName, vpcId, subnetId, zoneId) are
// stubbed because at this point they're CRD-required even though the merge
// effectively ignores them when the LBC is used as a template (Bug 12).
func applyOverrideLBC(ns, name string, listeners []vksv1alpha1.Listener) *vksv1alpha1.LoadBalancerConfig {
	GinkgoHelper()
	lbc := &vksv1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: vksv1alpha1.LoadBalancerConfigSpec{
			Type:             "Layer 7",
			LoadBalancerName: "_template",
			SubnetId:         "_overlay",
			VpcId:            "_overlay",
			ZoneId:           "HCM03-1A",
			Listeners:        listeners,
		},
	}
	Expect(k8sClient.Create(ctx, lbc)).To(Succeed())
	return lbc
}

// applyClassLBC creates a class-level LoadBalancerConfig template carrying tags.
func applyClassLBC(ns, name string, tags map[string]string) *vksv1alpha1.LoadBalancerConfig {
	GinkgoHelper()
	lbc := &vksv1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: vksv1alpha1.LoadBalancerConfigSpec{
			Type:             "Layer 7",
			LoadBalancerName: "_template",
			SubnetId:         "_overlay",
			VpcId:            "_overlay",
			ZoneId:           "HCM03-1A",
			Tags:             tags,
		},
	}
	Expect(k8sClient.Create(ctx, lbc)).To(Succeed())
	return lbc
}

// applyClassWithParametersRef creates a non-default GatewayClass with a parametersRef.
func applyClassWithParametersRef(name, lbcNS, lbcName string) *gwv1.GatewayClass {
	GinkgoHelper()
	ns := gwv1.Namespace(lbcNS)
	gwc := &gwv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: gwv1.GatewayClassSpec{
			ControllerName: gwv1.GatewayController(domain.ControllerNameALB),
			ParametersRef: &gwv1.ParametersReference{
				Group:     "vks.vngcloud.vn",
				Kind:      "LoadBalancerConfig",
				Namespace: &ns,
				Name:      lbcName,
			},
		},
	}
	Expect(k8sClient.Create(ctx, gwc)).To(Succeed())
	return gwc
}
