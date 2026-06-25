/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gateway

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// The comprehensive suite provisions ONE ALB from a kitchen-sink of Gateway-API
// resources + all four VKS policies, then asserts the whole resulting
// LoadBalancerConfig — the same surface the Ingress annotations cover. Specs
// share the single LBC (Ordered) so we don't provision an ALB per assertion.
var _ = Describe("ALB Gateway -> LoadBalancerConfig (comprehensive)", Ordered, func() {
	var lbc *v1alpha1.LoadBalancerConfig
	var listener *v1alpha1.Listener

	BeforeAll(func() {
		By("creating two weighted backends")
		kubectlApply(echoBackendsYAML)
		kubectl("-n", testNamespace, "rollout", "status", "deploy/echo", "--timeout=120s")
		kubectl("-n", testNamespace, "rollout", "status", "deploy/echo-v2", "--timeout=120s")

		By("applying all policies BEFORE the Gateway (loadBalancerName is create-only)")
		kubectlApply(kitchenPoliciesYAML)

		By("applying the Gateway + HTTPRoute (4 supported rules + 1 unsupported header rule)")
		kubectlApply(kitchenGatewayRouteYAML)

		By("waiting until the LBC is fully translated")
		Eventually(func(g Gomega) {
			lbc = getOwnedLBC(testNamespace, "kitchen-gw")
			g.Expect(lbc).NotTo(BeNil())
			listener = listenerByPort(lbc, 80)
			g.Expect(listener).NotTo(BeNil())
			// 4 supported rules -> 4 policies; the header-match rule is dropped.
			g.Expect(listener.Policies).To(HaveLen(4))
			// the weighted rule aggregated 2 services into one pool with members.
			wp := weightedPool(lbc)
			g.Expect(wp).NotTo(BeNil())
			g.Expect(wp.Members).NotTo(BeEmpty())
		}, 3*time.Minute, 5*time.Second).Should(Succeed())
	})

	AfterAll(func() {
		kubectlQuiet("-n", testNamespace, "delete", "gateway", "kitchen-gw",
			"--ignore-not-found", "--wait=true", "--timeout=5m")
	})

	It("maps LB-level fields from VKSGatewayPolicy.loadBalancerSpec", func() {
		Expect(lbc.Spec.Scheme).NotTo(BeNil())
		Expect(string(*lbc.Spec.Scheme)).To(Equal("Internet"))
		Expect(lbc.Spec.Tags).To(HaveKeyWithValue("env", "e2e"))
		Expect(lbc.Spec.EnableAutoscale).NotTo(BeNil())
		Expect(*lbc.Spec.EnableAutoscale).To(BeFalse())
		Expect(lbc.Spec.IsPoc).NotTo(BeNil())
		Expect(*lbc.Spec.IsPoc).To(BeFalse())
		// policy-applied-before-Gateway, so the create-only name takes effect.
		Expect(lbc.Spec.LoadBalancerName).To(Equal("e2e-kitchen"))
	})

	It("maps listener fields (timeouts, allowedCidrs, insertHeaders)", func() {
		Expect(*listener.TimeoutClient).To(Equal(int32(30)))
		Expect(*listener.TimeoutMember).To(Equal(int32(60)))
		Expect(*listener.TimeoutConnection).To(Equal(int32(5)))
		Expect(listener.AllowedCidrs).NotTo(BeNil())
		Expect(*listener.AllowedCidrs).To(Equal("0.0.0.0/0,10.0.0.0/8"))
		var headerNames []string
		for _, h := range listener.InsertHeaders {
			headerNames = append(headerNames, h.HeaderName)
		}
		Expect(headerNames).To(ContainElement("X-Custom"))
	})

	It("maps pool tuning + full health monitor", func() {
		wp := weightedPool(lbc)
		Expect(wp.Algorithm).NotTo(BeNil())
		Expect(string(*wp.Algorithm)).To(Equal("LEAST_CONNECTIONS"))
		Expect(*wp.Stickiness).To(BeTrue())
		Expect(*wp.TLSEncryption).To(BeTrue())

		hm := wp.HealthMonitor
		Expect(string(hm.Protocol)).To(Equal("HTTP"))
		Expect(*hm.Interval).To(Equal(15))
		Expect(*hm.Timeout).To(Equal(4))
		Expect(*hm.HealthyThreshold).To(Equal(2))
		Expect(*hm.UnhealthyThreshold).To(Equal(3))
		Expect(string(*hm.HealthCheckMethod)).To(Equal("POST"))
		Expect(string(*hm.HttpVersion)).To(Equal("1.0"))
		Expect(*hm.HealthCheckPath).To(Equal("/healthz"))
		Expect(*hm.DomainName).To(Equal("app.example.com"))
		Expect(*hm.SuccessCode).To(Equal("200,201"))
		for _, m := range wp.Members {
			Expect(m.MonitorPort).To(Equal(8080))
		}
	})

	It("aggregates weighted backendRefs into one pool with scaled member weights", func() {
		wp := weightedPool(lbc)
		ports := map[int]bool{}
		weights := map[int]bool{}
		for _, m := range wp.Members {
			ports[m.Port] = true
			if m.Weight != nil {
				weights[*m.Weight] = true
			}
		}
		Expect(len(ports)).To(BeNumerically(">=", 2), "members from 2 backend services")
		Expect(len(weights)).To(BeNumerically(">=", 2), "90:10 split -> distinct scaled weights")
	})

	It("maps HTTPRoute path match types to L7 compareType + hostname rule", func() {
		exact := policyByPathValue(listener, "/api")
		Expect(exact).NotTo(BeNil())
		Expect(string(ruleByType(exact, "PATH").CompareType)).To(Equal("EQUAL_TO"))

		host := ruleByType(exact, "HOST_NAME")
		Expect(host).NotTo(BeNil())
		Expect(string(host.CompareType)).To(Equal("EQUAL_TO"))
		Expect(host.RuleValue).To(Equal("app.example.com"))

		prefix := policyByPathValue(listener, "/assets")
		Expect(prefix).NotTo(BeNil())
		Expect(string(ruleByType(prefix, "PATH").CompareType)).To(Equal("STARTS_WITH"))

		var regexSeen bool
		for i := range listener.Policies {
			if r := ruleByType(&listener.Policies[i], "PATH"); r != nil && string(r.CompareType) == "REGEX" {
				regexSeen = true
			}
		}
		Expect(regexSeen).To(BeTrue(), "RegularExpression match -> REGEX compareType")
	})

	It("defaults to REDIRECT_TO_POOL for a routed rule", func() {
		p := policyByPathValue(listener, "/api")
		Expect(string(p.Action)).To(Equal("REDIRECT_TO_POOL"))
		Expect(p.RedirectPoolName).NotTo(BeNil())
	})

	It("honors VKSRoutePolicy Redirect action + explicit position", func() {
		p := policyByPathValue(listener, "/assets")
		Expect(string(p.Action)).To(Equal("REDIRECT_TO_URL"))
		Expect(*p.RedirectUrl).To(Equal("https://moved.example.com"))
		Expect(*p.RedirectHttpCode).To(Equal(int32(302)))
		Expect(*p.KeepQueryString).To(BeTrue())
		Expect(p.Position).NotTo(BeNil())
		Expect(*p.Position).To(Equal(int32(10)))
	})

	It("honors VKSRoutePolicy Reject action", func() {
		var rejected bool
		for i := range listener.Policies {
			if string(listener.Policies[i].Action) == "REJECT" {
				rejected = true
			}
		}
		Expect(rejected).To(BeTrue())
	})

	It("maps an HTTPRoute RequestRedirect filter to REDIRECT_TO_URL", func() {
		p := policyByPathValue(listener, "/old")
		Expect(p).NotTo(BeNil())
		Expect(string(p.Action)).To(Equal("REDIRECT_TO_URL"))
		Expect(*p.RedirectUrl).To(ContainSubstring("new.example.com"))
	})

	It("drops a rule whose match uses an unsupported dimension (header)", func() {
		Expect(policyByPathValue(listener, "/h")).To(BeNil(), "header-match rule must not produce a policy")
		Expect(listener.Policies).To(HaveLen(4))
	})

	It("reports PartiallyInvalid on the route for the dropped header rule", func() {
		Eventually(func() string {
			return routeParentStatus(testNamespace, "kitchen-route", "PartiallyInvalid")
		}, time.Minute, 3*time.Second).Should(Equal("True"))
		Expect(routeParentReason(testNamespace, "kitchen-route", "PartiallyInvalid")).To(Equal("UnsupportedValue"))
	})

	// The remaining specs mutate/delete shared fixtures, so any spec that
	// asserts the pre-update state must precede them (Ordered container).
	It("reconciles policy updates (every-reconcile field changes; create-only name stays)", func() {
		kubectl("-n", testNamespace, "patch", "vksgatewaypolicy", "kitchen-lb",
			"--type=merge", "-p", `{"spec":{"timeoutClient":"45s"}}`)
		Eventually(func(g Gomega) {
			cur := getOwnedLBC(testNamespace, "kitchen-gw")
			g.Expect(cur).NotTo(BeNil())
			l := listenerByPort(cur, 80)
			g.Expect(l).NotTo(BeNil())
			g.Expect(l.TimeoutClient).NotTo(BeNil())
			g.Expect(*l.TimeoutClient).To(Equal(int32(45)), "every-reconcile field must update")
			g.Expect(cur.Spec.LoadBalancerName).To(Equal("e2e-kitchen"), "create-only name must NOT change")
		}, time.Minute, 3*time.Second).Should(Succeed())
	})

	It("reverts the rule action when its VKSRoutePolicy is deleted", func() {
		kubectl("-n", testNamespace, "delete", "vksroutepolicy", "regex-reject")
		Eventually(func(g Gomega) {
			cur := getOwnedLBC(testNamespace, "kitchen-gw")
			g.Expect(cur).NotTo(BeNil())
			l := listenerByPort(cur, 80)
			g.Expect(l).NotTo(BeNil())
			p := policyByPathValue(l, "^/u/[0-9]+$")
			g.Expect(p).NotTo(BeNil())
			g.Expect(string(p.Action)).To(Equal("REDIRECT_TO_POOL"), "REJECT overlay must revert to default")
		}, time.Minute, 3*time.Second).Should(Succeed())
	})

	It("drops all listener policies when the HTTPRoute is deleted", func() {
		kubectl("-n", testNamespace, "delete", "httproute", "kitchen-route")
		Eventually(func(g Gomega) {
			cur := getOwnedLBC(testNamespace, "kitchen-gw")
			g.Expect(cur).NotTo(BeNil())
			l := listenerByPort(cur, 80)
			g.Expect(l).NotTo(BeNil())
			g.Expect(l.Policies).To(BeEmpty())
		}, time.Minute, 3*time.Second).Should(Succeed())
	})

	// preferZoneId positive twin of the fail-closed negative below: a second
	// tiny gateway pinned to the zone the kitchen LB landed in must produce an
	// LBC in that zone. (Single-zone cluster: this exercises the valid-zone
	// path; the multi-zone node-scan path is unit-tested.)
	It("creates the LBC in the preferred zone for a valid preferZoneId", func() {
		zone := string(lbc.Spec.ZoneId)
		Expect(zone).NotTo(BeEmpty())
		kubectlApply(gwPolicyYAML("zonepos-gw", fmt.Sprintf("preferZoneId: %q", zone)))
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "zonepos-gw",
				"--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(plainGatewayYAML("zonepos-gw"))
		Eventually(func(g Gomega) {
			cur := getOwnedLBC(testNamespace, "zonepos-gw")
			g.Expect(cur).NotTo(BeNil())
			g.Expect(string(cur.Spec.ZoneId)).To(Equal(zone))
		}, time.Minute, 5*time.Second).Should(Succeed())
	})
})

// Negative / fail-closed inputs. These error during subnet/zone resolution,
// before the LBC is created, so they provision no cloud LB.
var _ = Describe("ALB Gateway fail-closed inputs", func() {
	It("creates no LBC for a non-existent loadBalancerId (adoption lookup fails)", func() {
		kubectlApply(gwPolicyYAML("adopt-gw", "loadBalancerId: \"lb-does-not-exist-0000\""))
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "adopt-gw",
				"--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(plainGatewayYAML("adopt-gw"))
		Consistently(func() string { return ownedLBCName(testNamespace, "adopt-gw") }, 30*time.Second, 5*time.Second).
			Should(BeEmpty(), "bogus loadBalancerId must fail closed")
	})

	It("creates no LBC for a preferZoneId with no matching node", func() {
		kubectlApply(gwPolicyYAML("zone-gw", `preferZoneId: "ZONE-DOES-NOT-EXIST"`))
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "zone-gw",
				"--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(plainGatewayYAML("zone-gw"))
		Consistently(func() string { return ownedLBCName(testNamespace, "zone-gw") }, 30*time.Second, 5*time.Second).
			Should(BeEmpty(), "unresolvable preferZoneId must fail closed")
	})
})

// --- fixtures ---

var echoBackendsYAML = fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata: {name: echo, namespace: %[1]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: echo}}
  template:
    metadata: {labels: {app: echo}}
    spec:
      containers: [{name: nginx, image: nginx:alpine, ports: [{containerPort: 80}]}]
---
apiVersion: v1
kind: Service
metadata: {name: echo, namespace: %[1]s}
spec: {type: NodePort, selector: {app: echo}, ports: [{port: 80, targetPort: 80}]}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: echo-v2, namespace: %[1]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: echo-v2}}
  template:
    metadata: {labels: {app: echo-v2}}
    spec:
      containers: [{name: nginx, image: nginx:alpine, ports: [{containerPort: 80}]}]
---
apiVersion: v1
kind: Service
metadata: {name: echo-v2, namespace: %[1]s}
spec: {type: NodePort, selector: {app: echo-v2}, ports: [{port: 80, targetPort: 80}]}
`, testNamespace)

var kitchenPoliciesYAML = fmt.Sprintf(`
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSGatewayPolicy
metadata: {name: kitchen-lb, namespace: %[1]s}
spec:
  targetRefs:
    [{group: gateway.networking.k8s.io, kind: Gateway, name: kitchen-gw}]
  loadBalancerSpec:
    {scheme: Internet, loadBalancerName: e2e-kitchen, isPOC: false,
      enableAutoscale: false, tags: {env: e2e}}
  timeoutClient: 30s
  timeoutMember: 60s
  timeoutConnection: 5s
  allowedCidrs: ["0.0.0.0/0", "10.0.0.0/8"]
  insertHeaders: {X-Custom: "true"}
---
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSBackendPolicy
metadata: {name: echo-bp, namespace: %[1]s}
spec:
  # both weighted backends share one policy — divergent per-backend policies
  # would fail the rule closed (BackendConfigMismatch)
  targetRefs: [{group: "", kind: Service, name: echo}, {group: "", kind: Service, name: echo-v2}]
  targetType: instance
  poolAlgorithm: LEAST_CONNECTIONS
  stickiness: true
  enableTLSEncryption: true
---
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSHealthCheckPolicy
metadata: {name: echo-hc, namespace: %[1]s}
spec:
  targetRefs: [{group: "", kind: Service, name: echo}, {group: "", kind: Service, name: echo-v2}]
  protocol: HTTP
  interval: 15s
  timeout: 4s
  healthyThreshold: 2
  unhealthyThreshold: 3
  port: 8080
  httpHealthCheck:
    {path: /healthz, host: app.example.com, method: POST,
      httpVersion: "1.0", expectedCodes: ["200", "201"]}
---
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSRoutePolicy
metadata: {name: prefix-redirect, namespace: %[1]s}
spec:
  targetRefs: [{group: gateway.networking.k8s.io, kind: HTTPRoute, name: kitchen-route, sectionName: prefix}]
  position: 10
  actions: [{type: Redirect, redirect: {url: "https://moved.example.com", httpCode: 302, keepQueryString: true}}]
---
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSRoutePolicy
metadata: {name: regex-reject, namespace: %[1]s}
spec:
  targetRefs: [{group: gateway.networking.k8s.io, kind: HTTPRoute, name: kitchen-route, sectionName: regex}]
  actions: [{type: Reject}]
`, testNamespace)

var kitchenGatewayRouteYAML = fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: kitchen-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners: [{name: http, protocol: HTTP, port: 80, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: kitchen-route, namespace: %[1]s}
spec:
  parentRefs: [{name: kitchen-gw}]
  hostnames: [app.example.com]
  rules:
  - name: exact
    matches: [{path: {type: Exact, value: /api}}]
    backendRefs: [{name: echo, port: 80, weight: 90}, {name: echo-v2, port: 80, weight: 10}]
  - name: prefix
    matches: [{path: {type: PathPrefix, value: /assets}}]
    backendRefs: [{name: echo, port: 80}]
  - name: regex
    matches: [{path: {type: RegularExpression, value: "^/u/[0-9]+$"}}]
    backendRefs: [{name: echo, port: 80}]
  - name: filter
    matches: [{path: {type: PathPrefix, value: /old}}]
    filters: [{type: RequestRedirect, requestRedirect: {scheme: https, hostname: new.example.com, statusCode: 301}}]
  - name: header
    matches: [{path: {type: PathPrefix, value: /h}, headers: [{name: X-Env, value: canary}]}]
    backendRefs: [{name: echo, port: 80}]
`, testNamespace, gatewayClassName)

func gwPolicyYAML(gw, lbSpecLine string) string {
	return fmt.Sprintf(`
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSGatewayPolicy
metadata: {name: %[2]s-pol, namespace: %[1]s}
spec:
  targetRefs: [{group: gateway.networking.k8s.io, kind: Gateway, name: %[2]s}]
  loadBalancerSpec: {%[3]s}
`, testNamespace, gw, lbSpecLine)
}

func plainGatewayYAML(gw string) string {
	return fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: %[2]s, namespace: %[1]s}
spec:
  gatewayClassName: %[3]s
  listeners: [{name: http, protocol: HTTP, port: 80, allowedRoutes: {namespaces: {from: Same}}}]
`, testNamespace, gw, gatewayClassName)
}
