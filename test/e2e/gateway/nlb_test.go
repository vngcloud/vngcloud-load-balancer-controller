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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// The NLB (L4) specs need TWO extra preconditions beyond RUN_GATEWAY_E2E that
// the ALB specs don't: the Gateway-API *experimental* CRDs (TCPRoute/UDPRoute)
// installed, and the controller running with --disable-nlb-gateway-controller=false.
// They are therefore gated behind a second opt-in flag and skip cleanly when the
// vngcloud-nlb GatewayClass can't be made Accepted.
const nlbGatewayClassName = "vngcloud-nlb"

func nlbEnabled() bool { return os.Getenv("RUN_GATEWAY_NLB_E2E") == "true" }

var _ = Describe("NLB Gateway -> Layer-4 LoadBalancerConfig", Ordered, func() {
	BeforeAll(func() {
		if !nlbEnabled() {
			Skip("set RUN_GATEWAY_NLB_E2E=true (needs experimental TCP/UDP CRDs + NLB controller enabled)")
		}
		ensureNLBGatewayClass()
	})

	It("provisions a Layer-4 LBC from a TCP listener + TCPRoute", func() {
		kubectlApply(nlbTCPBackendYAML)
		kubectl("-n", testNamespace, "rollout", "status", "deploy/echo-tcp", "--timeout=120s")
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "nlb-tcp-gw",
				"--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(nlbTCPGatewayRouteYAML)

		var lbc *v1alpha1.LoadBalancerConfig
		Eventually(func(g Gomega) {
			lbc = getOwnedLBC(testNamespace, "nlb-tcp-gw")
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Type).To(Equal(v2.LoadBalancerTypeLayer4))
			l := listenerByPort(lbc, 6379)
			g.Expect(l).NotTo(BeNil())
			g.Expect(l.Protocol).To(Equal(v2.ListenerProtocolTCP))
			g.Expect(l.DefaultPoolName).NotTo(BeNil())
			g.Expect(lbc.Spec.Pools).NotTo(BeEmpty())
			g.Expect(lbc.Spec.Pools[0].Protocol).To(Equal(v2.PoolProtocolTCP))
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		By("the Gateway reports Programmed + an address once the NLB provisions")
		Eventually(func(g Gomega) {
			g.Expect(jsonpath(testNamespace, "gateway", "nlb-tcp-gw",
				`{.status.conditions[?(@.type=="Programmed")].status}`)).To(Equal("True"))
			g.Expect(jsonpath(testNamespace, "gateway", "nlb-tcp-gw",
				`{.status.addresses[0].value}`)).NotTo(BeEmpty())
		}, 5*time.Minute, 5*time.Second).Should(Succeed())

		By("the TCPRoute reports Accepted on its parent")
		Eventually(func() string {
			return routeKindParentStatus("tcproute", testNamespace, "tcp-route", "Accepted")
		}, 2*time.Minute, 5*time.Second).Should(Equal("True"))
	})

	It("provisions a UDP listener + pool from a UDPRoute", func() {
		kubectlApply(nlbUDPBackendYAML)
		kubectl("-n", testNamespace, "rollout", "status", "deploy/echo-udp", "--timeout=120s")
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "nlb-udp-gw",
				"--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(nlbUDPGatewayRouteYAML)

		Eventually(func(g Gomega) {
			lbc := getOwnedLBC(testNamespace, "nlb-udp-gw")
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Type).To(Equal(v2.LoadBalancerTypeLayer4))
			l := listenerByPort(lbc, 5353)
			g.Expect(l).NotTo(BeNil())
			g.Expect(l.Protocol).To(Equal(v2.ListenerProtocolUDP))
			g.Expect(lbc.Spec.Pools).NotTo(BeEmpty())
			g.Expect(lbc.Spec.Pools[0].Protocol).To(Equal(v2.PoolProtocolUDP))
		}, 3*time.Minute, 5*time.Second).Should(Succeed())
	})

	// VKSBackendPolicy.targetNodeLabels restricts which nodes become LB members
	// (instance mode). Selecting a single node by its hostname must yield exactly
	// one pool member — proving the label is wired into endpoint resolution.
	It("filters LB pool members by VKSBackendPolicy.targetNodeLabels", func() {
		names := nodeNames()
		if len(names) < 2 {
			Skip(fmt.Sprintf("needs >= 2 Ready nodes to prove node-label filtering (have %d)", len(names)))
		}
		target := names[0]
		targetIP := nodeInternalIP(target)
		Expect(targetIP).NotTo(BeEmpty(), "could not read InternalIP of node %s", target)

		kubectlApply(nlbTCPBackendYAML) // reuse the echo-tcp NodePort backend
		kubectl("-n", testNamespace, "rollout", "status", "deploy/echo-tcp", "--timeout=120s")
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "nlb-nodelabel-gw",
				"--ignore-not-found", "--wait=true", "--timeout=5m")
			kubectlQuiet("-n", testNamespace, "delete", "tcproute", "nodelabel-route",
				"--ignore-not-found")
			kubectlQuiet("-n", testNamespace, "delete", "vksbackendpolicy", "echo-tcp-nodes",
				"--ignore-not-found")
		})
		kubectlApply(nlbNodeLabelYAML(target))

		By("the LBC pool carries exactly the one selected node as a member")
		Eventually(func(g Gomega) {
			lbc := getOwnedLBC(testNamespace, "nlb-nodelabel-gw")
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Pools).NotTo(BeEmpty())
			g.Expect(lbc.Spec.Pools[0].Members).To(HaveLen(1),
				"targetNodeLabels selecting one node must yield one member (cluster has %d nodes)", len(names))
			g.Expect(lbc.Spec.Pools[0].Members[0].IP).To(Equal(targetIP))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())
	})
})

// nodeNames returns the names of all nodes in the cluster.
func nodeNames() []string {
	out := kubectl("get", "nodes", "-o", "jsonpath={.items[*].metadata.name}")
	return strings.Fields(out)
}

// nodeInternalIP returns the InternalIP of the named node, or "".
func nodeInternalIP(name string) string {
	return jsonpath("", "node", name, `{.status.addresses[?(@.type=="InternalIP")].address}`)
}

// routeKindParentStatus reads .status.parents[0].conditions[type==cond].status
// for an arbitrary route kind (tcproute/udproute).
func routeKindParentStatus(kind, ns, name, cond string) string {
	return jsonpath(ns, kind, name,
		fmt.Sprintf(`{.status.parents[0].conditions[?(@.type==%q)].status}`, cond))
}

func ensureNLBGatewayClass() {
	kubectlApply(fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: %s
spec:
  controllerName: gateway.vks.vngcloud.vn/nlb
`, nlbGatewayClassName))

	Eventually(func() string {
		return jsonpath("", "gatewayclass", nlbGatewayClassName,
			`{.status.conditions[?(@.type=="Accepted")].status}`)
	}, time.Minute, 3*time.Second).Should(Equal("True"),
		"vngcloud-nlb GatewayClass not Accepted — "+
			"is the controller running with --disable-nlb-gateway-controller=false "+
			"and the experimental CRDs installed?")
}

// --- fixtures ---

var nlbTCPBackendYAML = fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata: {name: echo-tcp, namespace: %[1]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: echo-tcp}}
  template:
    metadata: {labels: {app: echo-tcp}}
    spec:
      containers: [{name: redis, image: redis:7-alpine, ports: [{containerPort: 6379}]}]
---
apiVersion: v1
kind: Service
metadata: {name: echo-tcp, namespace: %[1]s}
spec: {type: NodePort, selector: {app: echo-tcp}, ports: [{port: 6379, targetPort: 6379, protocol: TCP}]}
`, testNamespace)

var nlbTCPGatewayRouteYAML = fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: nlb-tcp-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners: [{name: tcp, protocol: TCP, port: 6379, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata: {name: tcp-route, namespace: %[1]s}
spec:
  parentRefs: [{name: nlb-tcp-gw, sectionName: tcp}]
  rules:
  - backendRefs: [{name: echo-tcp, port: 6379}]
`, testNamespace, nlbGatewayClassName)

var nlbUDPBackendYAML = fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata: {name: echo-udp, namespace: %[1]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: echo-udp}}
  template:
    metadata: {labels: {app: echo-udp}}
    spec:
      # Any Ready pod works: the spec asserts the LBC's UDP listener/pool shape,
      # not UDP traffic. nginx becomes Ready without extra config (coredns
      # crash-loops without a Corefile), so endpoints resolve into the pool.
      containers: [{name: echo, image: nginx:alpine, ports: [{containerPort: 5353, protocol: UDP}]}]
---
apiVersion: v1
kind: Service
metadata: {name: echo-udp, namespace: %[1]s}
spec: {type: NodePort, selector: {app: echo-udp}, ports: [{port: 5353, targetPort: 5353, protocol: UDP}]}
`, testNamespace)

var nlbUDPGatewayRouteYAML = fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: nlb-udp-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners: [{name: udp, protocol: UDP, port: 5353, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: UDPRoute
metadata: {name: udp-route, namespace: %[1]s}
spec:
  parentRefs: [{name: nlb-udp-gw, sectionName: udp}]
  rules:
  - backendRefs: [{name: echo-udp, port: 5353}]
`, testNamespace, nlbGatewayClassName)

// nlbNodeLabelYAML fronts the echo-tcp backend with a Gateway whose
// VKSBackendPolicy pins the LB members to a single node via hostname.
func nlbNodeLabelYAML(nodeName string) string {
	return fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: nlb-nodelabel-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners: [{name: tcp, protocol: TCP, port: 6379, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata: {name: nodelabel-route, namespace: %[1]s}
spec:
  parentRefs: [{name: nlb-nodelabel-gw, sectionName: tcp}]
  rules:
  - backendRefs: [{name: echo-tcp, port: 6379}]
---
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSBackendPolicy
metadata: {name: echo-tcp-nodes, namespace: %[1]s}
spec:
  targetRefs: [{group: "", kind: Service, name: echo-tcp}]
  targetNodeLabels: {kubernetes.io/hostname: %[3]s}
`, testNamespace, nlbGatewayClassName, nodeName)
}
