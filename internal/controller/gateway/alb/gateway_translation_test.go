package alb

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// These specs cover the live-cluster smoke scenarios validated against a real
// vngcloud LB on KUBECONFIG=~/vks-test-gatewayapi.yaml: VKSBackendPolicy update
// propagation, unsupported-match drop, cross-namespace backend drop,
// multi-listener HTTP+HTTPS with TLS Secret, and policy ordering by match
// specificity. Each scenario is reduced to "spec equality on the generated
// LBC", since envtest can't reach the cloud LB itself.

// fetchOwnedLBC returns the (single) LBC owned by the named Gateway, or nil if
// none yet exists. Used inside Eventually blocks.
// nolint:unparam
func fetchOwnedLBC(gwName, ns string) *v1alpha1.LoadBalancerConfig {
	lbcList, err := findLBCsByGatewayOwnerLabels(gwName, ns)
	if err != nil || len(lbcList.Items) == 0 {
		return nil
	}
	return &lbcList.Items[0]
}

// pathPrefixMatch builds an HTTPRouteMatch on PathPrefix(value).
func pathPrefixMatch(value string) gwv1.HTTPRouteMatch {
	t := gwv1.PathMatchPathPrefix
	return gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: &t, Value: ptr.To(value)}}
}

// pathExactMatch builds an HTTPRouteMatch on Exact(value).
func pathExactMatch(value string) gwv1.HTTPRouteMatch {
	t := gwv1.PathMatchExact
	return gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: &t, Value: ptr.To(value)}}
}

// hasL7Rule reports whether the policy has an L7Rule with the given (type,
// compare, value).
// nolint:unparam
func hasL7Rule(p v1alpha1.Policy, ruleType v2.PolicyRuleType, cmp v2.PolicyCompareType, value string) bool {
	for _, r := range p.L7Rules {
		if r.RuleType == ruleType && r.CompareType == cmp && r.RuleValue == value {
			return true
		}
	}
	return false
}

var _ = Describe("ALB Gateway Translation (live-cluster smoke parity)", func() {

	Context("VKSBackendPolicy", func() {

		It("propagates poolAlgorithm changes to LBC.Pool.Algorithm", func() {
			gc := newALBGatewayClass("alb-gc-bp-prop")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-bp-prop"}, got)).To(Succeed())
				g.Expect(got.Status.Conditions).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			svc := newNodePortSvc("alb-svc-bp", testNS, 80, 30190)
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

			gw := newGateway("alb-gw-bp", testNS, "alb-gc-bp-prop")
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

			route := newHTTPRoute("alb-route-bp", testNS, "alb-gw-bp", "alb-svc-bp", 80)
			Expect(k8sClient.Create(ctx, route)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

			// LBC is created; algorithm is unset until a VKSBackendPolicy lands.
			Eventually(func(g Gomega) {
				lbc := fetchOwnedLBC("alb-gw-bp", testNS)
				g.Expect(lbc).NotTo(BeNil())
				g.Expect(lbc.Spec.Pools).NotTo(BeEmpty())
				g.Expect(lbc.Spec.Pools[0].Algorithm).To(BeNil())
			}, albTimeout, albInterval).Should(Succeed())

			// Apply VKSBackendPolicy with SOURCE_IP. Reconcile should pick it
			// up via the BackendPolicy event handler and overlay it onto the
			// pool — no manual annotate or restart.
			alg := "SOURCE_IP"
			bp := &gwv1alpha1.VKSBackendPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "bp-source-ip", Namespace: testNS},
				Spec: gwv1alpha1.VKSBackendPolicySpec{
					TargetRefs: []gwv1alpha2.LocalPolicyTargetReference{{
						Group: "", Kind: "Service", Name: "alb-svc-bp",
					}},
					PoolAlgorithm: &alg,
				},
			}
			Expect(k8sClient.Create(ctx, bp)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, bp) })

			Eventually(func(g Gomega) {
				lbc := fetchOwnedLBC("alb-gw-bp", testNS)
				g.Expect(lbc).NotTo(BeNil())
				g.Expect(lbc.Spec.Pools).NotTo(BeEmpty())
				g.Expect(lbc.Spec.Pools[0].Algorithm).NotTo(BeNil())
				g.Expect(string(*lbc.Spec.Pools[0].Algorithm)).To(Equal("SOURCE_IP"))
			}, albTimeout, albInterval).Should(Succeed())
		})

	})

	Context("Unsupported-match handling", func() {

		It("drops rules using header matches and keeps path-only rules", func() {
			gc := newALBGatewayClass("alb-gc-unsupp")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-unsupp"}, got)).To(Succeed())
				g.Expect(got.Status.Conditions).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			svc := newNodePortSvc("alb-svc-unsupp", testNS, 80, 30191)
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

			gw := newGateway("alb-gw-unsupp", testNS, "alb-gc-unsupp")
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

			port := gwv1.PortNumber(80)
			rt := &gwv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "alb-route-unsupp", Namespace: testNS},
				Spec: gwv1.HTTPRouteSpec{
					CommonRouteSpec: gwv1.CommonRouteSpec{
						ParentRefs: []gwv1.ParentReference{{Name: "alb-gw-unsupp"}},
					},
					Rules: []gwv1.HTTPRouteRule{
						// Rule 0: path + Header — entire rule must be skipped.
						{
							Matches: []gwv1.HTTPRouteMatch{{
								Path:    pathPrefixMatch("/dropped").Path,
								Headers: []gwv1.HTTPHeaderMatch{{Name: "X-Foo", Value: "bar"}},
							}},
							BackendRefs: []gwv1.HTTPBackendRef{{BackendRef: gwv1.BackendRef{
								BackendObjectReference: gwv1.BackendObjectReference{Name: "alb-svc-unsupp", Port: &port},
							}}},
						},
						// Rule 1: path-only — must produce a policy.
						{
							Matches: []gwv1.HTTPRouteMatch{pathPrefixMatch("/ok")},
							BackendRefs: []gwv1.HTTPBackendRef{{BackendRef: gwv1.BackendRef{
								BackendObjectReference: gwv1.BackendObjectReference{Name: "alb-svc-unsupp", Port: &port},
							}}},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, rt)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rt) })

			Eventually(func(g Gomega) {
				lbc := fetchOwnedLBC("alb-gw-unsupp", testNS)
				g.Expect(lbc).NotTo(BeNil())
				g.Expect(lbc.Spec.Listeners).To(HaveLen(1))
				policies := lbc.Spec.Listeners[0].Policies
				// Exactly one policy survives — the dropped rule contributes nothing.
				g.Expect(policies).To(HaveLen(1))
				g.Expect(hasL7Rule(policies[0], v2.PolicyRuleTypePATH, v2.PolicyCompareTypeSTARTSWITH, "/ok")).To(BeTrue())
				// And the dropped path is not present anywhere.
				for _, p := range policies {
					g.Expect(hasL7Rule(p, v2.PolicyRuleTypePATH, v2.PolicyCompareTypeSTARTSWITH, "/dropped")).To(BeFalse())
				}
			}, albTimeout, albInterval).Should(Succeed())
		})

	})

	Context("Cross-namespace backendRef", func() {

		It("skips the rule when the only backendRef points outside the route's namespace", func() {
			gc := newALBGatewayClass("alb-gc-xns")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-xns"}, got)).To(Succeed())
				g.Expect(got.Status.Conditions).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			otherNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "alb-xns-other"}}
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, otherNS))).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, otherNS) })

			svc := newNodePortSvc("alb-svc-xns", "alb-xns-other", 80, 30192)
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

			gw := newGateway("alb-gw-xns", testNS, "alb-gc-xns")
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

			port := gwv1.PortNumber(80)
			otherNSObj := gwv1.Namespace("alb-xns-other")
			rt := &gwv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "alb-route-xns", Namespace: testNS},
				Spec: gwv1.HTTPRouteSpec{
					CommonRouteSpec: gwv1.CommonRouteSpec{
						ParentRefs: []gwv1.ParentReference{{Name: "alb-gw-xns"}},
					},
					Rules: []gwv1.HTTPRouteRule{{
						Matches: []gwv1.HTTPRouteMatch{pathPrefixMatch("/")},
						BackendRefs: []gwv1.HTTPBackendRef{{BackendRef: gwv1.BackendRef{
							BackendObjectReference: gwv1.BackendObjectReference{
								Name: "alb-svc-xns", Port: &port, Namespace: &otherNSObj,
							},
						}}},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, rt)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rt) })

			// LBC is created (Gateway accepted) but with no pools/policies —
			// the only rule was dropped because its backend is cross-ns and
			// ReferenceGrant evaluation lands in a follow-up.
			Eventually(func(g Gomega) {
				lbc := fetchOwnedLBC("alb-gw-xns", testNS)
				g.Expect(lbc).NotTo(BeNil())
				g.Expect(lbc.Spec.Pools).To(BeEmpty())
				g.Expect(lbc.Spec.Listeners).To(HaveLen(1))
				g.Expect(lbc.Spec.Listeners[0].Policies).To(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())
		})

	})

	Context("Multi-listener Gateway with TLS Secret", func() {

		It("emits two listeners — HTTP and HTTPS with the secret's name in CertificateDefault", func() {
			gc := newALBGatewayClass("alb-gc-ml")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-ml"}, got)).To(Succeed())
				g.Expect(got.Status.Conditions).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			svc := newNodePortSvc("alb-svc-ml", testNS, 80, 30193)
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

			tlsSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "alb-tls-ml", Namespace: testNS},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("dummy-cert"),
					corev1.TLSPrivateKeyKey: []byte("dummy-key"),
				},
			}
			Expect(k8sClient.Create(ctx, tlsSecret)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, tlsSecret) })

			tlsKind := gwv1.Kind("Secret")
			gw := &gwv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{Name: "alb-gw-ml", Namespace: testNS},
				Spec: gwv1.GatewaySpec{
					GatewayClassName: gwv1.ObjectName("alb-gc-ml"),
					Listeners: []gwv1.Listener{
						{Name: "http", Port: 80, Protocol: gwv1.HTTPProtocolType},
						{
							Name: "https", Port: 443, Protocol: gwv1.HTTPSProtocolType,
							TLS: &gwv1.GatewayTLSConfig{
								Mode: ptr.To(gwv1.TLSModeTerminate),
								CertificateRefs: []gwv1.SecretObjectReference{{
									Kind: &tlsKind, Name: gwv1.ObjectName("alb-tls-ml"),
								}},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

			rt := newHTTPRoute("alb-route-ml", testNS, "alb-gw-ml", "alb-svc-ml", 80)
			Expect(k8sClient.Create(ctx, rt)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rt) })

			Eventually(func(g Gomega) {
				lbc := fetchOwnedLBC("alb-gw-ml", testNS)
				g.Expect(lbc).NotTo(BeNil())
				g.Expect(lbc.Spec.Listeners).To(HaveLen(2))

				var http, https *v1alpha1.Listener
				for i := range lbc.Spec.Listeners {
					switch lbc.Spec.Listeners[i].Protocol {
					case v2.ListenerProtocolHTTP:
						http = &lbc.Spec.Listeners[i]
					case v2.ListenerProtocolHTTPS:
						https = &lbc.Spec.Listeners[i]
					}
				}
				g.Expect(http).NotTo(BeNil())
				g.Expect(http.ProtocolPort).To(Equal(int32(80)))
				g.Expect(http.CertificateDefault).To(BeNil())

				g.Expect(https).NotTo(BeNil())
				g.Expect(https.ProtocolPort).To(Equal(int32(443)))
				g.Expect(https.CertificateDefault).NotTo(BeNil())
				g.Expect(https.CertificateDefault.SecretName).NotTo(BeNil())
				g.Expect(*https.CertificateDefault.SecretName).To(Equal("alb-tls-ml"))

				// And the LBC controller should be told to import the Secret.
				var found bool
				for _, c := range lbc.Spec.CreateCertificates {
					if c.SecretName == "alb-tls-ml" {
						found = true
						break
					}
				}
				g.Expect(found).To(BeTrue(), "expected alb-tls-ml in CreateCertificates")
			}, albTimeout, albInterval).Should(Succeed())
		})

	})

	Context("Policy ordering by match specificity", func() {

		It("orders Exact match before PathPrefix on the same listener", func() {
			gc := newALBGatewayClass("alb-gc-ord")
			Expect(k8sClient.Create(ctx, gc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

			Eventually(func(g Gomega) {
				got := &gwv1.GatewayClass{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "alb-gc-ord"}, got)).To(Succeed())
				g.Expect(got.Status.Conditions).NotTo(BeEmpty())
			}, albTimeout, albInterval).Should(Succeed())

			svc := newNodePortSvc("alb-svc-ord-v1", testNS, 80, 30194)
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

			svc2 := newNodePortSvc("alb-svc-ord-v2", testNS, 80, 30195)
			Expect(k8sClient.Create(ctx, svc2)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc2) })

			gw := newGateway("alb-gw-ord", testNS, "alb-gc-ord")
			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

			port := gwv1.PortNumber(80)
			// Route A: PathPrefix /api → v1 (less specific)
			routeA := &gwv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "route-prefix", Namespace: testNS},
				Spec: gwv1.HTTPRouteSpec{
					CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "alb-gw-ord"}}},
					Hostnames:       []gwv1.Hostname{"ord.example.com"},
					Rules: []gwv1.HTTPRouteRule{{
						Matches: []gwv1.HTTPRouteMatch{pathPrefixMatch("/api")},
						BackendRefs: []gwv1.HTTPBackendRef{{BackendRef: gwv1.BackendRef{
							BackendObjectReference: gwv1.BackendObjectReference{Name: "alb-svc-ord-v1", Port: &port},
						}}},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, routeA)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, routeA) })

			// Route B: Exact /api/v1 → v2 (more specific — must come first).
			routeB := &gwv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "route-exact", Namespace: testNS},
				Spec: gwv1.HTTPRouteSpec{
					CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "alb-gw-ord"}}},
					Hostnames:       []gwv1.Hostname{"ord.example.com"},
					Rules: []gwv1.HTTPRouteRule{{
						Matches: []gwv1.HTTPRouteMatch{pathExactMatch("/api/v1")},
						BackendRefs: []gwv1.HTTPBackendRef{{BackendRef: gwv1.BackendRef{
							BackendObjectReference: gwv1.BackendObjectReference{Name: "alb-svc-ord-v2", Port: &port},
						}}},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, routeB)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, routeB) })

			Eventually(func(g Gomega) {
				lbc := fetchOwnedLBC("alb-gw-ord", testNS)
				g.Expect(lbc).NotTo(BeNil())
				g.Expect(lbc.Spec.Listeners).To(HaveLen(1))
				policies := lbc.Spec.Listeners[0].Policies
				g.Expect(policies).To(HaveLen(2))

				// Position 1 (most specific) must be the Exact match.
				var exact, prefix *v1alpha1.Policy
				for i := range policies {
					switch {
					case hasL7Rule(policies[i], v2.PolicyRuleTypePATH, v2.PolicyCompareTypeEQUALS, "/api/v1"):
						exact = &policies[i]
					case hasL7Rule(policies[i], v2.PolicyRuleTypePATH, v2.PolicyCompareTypeSTARTSWITH, "/api"):
						prefix = &policies[i]
					}
				}
				g.Expect(exact).NotTo(BeNil(), "expected Exact /api/v1 policy")
				g.Expect(prefix).NotTo(BeNil(), "expected Prefix /api policy")
				g.Expect(exact.Position).NotTo(BeNil())
				g.Expect(prefix.Position).NotTo(BeNil())
				g.Expect(*exact.Position).To(BeNumerically("<", *prefix.Position),
					"Exact match must take a lower (better) Position than Prefix match")
			}, albTimeout*2, albInterval).Should(Succeed())
		})

	})

})
