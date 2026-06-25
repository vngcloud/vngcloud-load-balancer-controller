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
)

func routeParentReason(ns, route, condType string) string {
	return jsonpath(ns, "httproute", route,
		fmt.Sprintf(`{.status.parents[0].conditions[?(@.type==%q)].reason}`, condType))
}
func routeParentStatus(ns, route, condType string) string {
	return jsonpath(ns, "httproute", route,
		fmt.Sprintf(`{.status.parents[0].conditions[?(@.type==%q)].status}`, condType))
}

// Cross-namespace backendRef is honored only when a ReferenceGrant permits it.
var _ = Describe("ALB Gateway cross-namespace backend via ReferenceGrant", func() {
	const backendNS = "gw-e2e-xns-backend"
	It("includes the cross-ns backend only after a ReferenceGrant exists", func() {
		kubectlApply(fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata: {name: %s}\n", backendNS))
		DeferCleanup(func() { kubectlQuiet("delete", "namespace", backendNS, "--ignore-not-found", "--wait=false") })
		kubectlApply(xnsBackendYAML(backendNS))
		kubectl("-n", backendNS, "rollout", "status", "deploy/echo", "--timeout=120s")

		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "xns-gw", "--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(xnsGatewayRouteYAML(backendNS))

		By("without a ReferenceGrant the route reports ResolvedRefs=RefNotPermitted")
		Eventually(func() string {
			return routeParentReason(testNamespace, "xns-route", "ResolvedRefs")
		}, 90*time.Second, 5*time.Second).Should(Equal("RefNotPermitted"))

		By("after creating a ReferenceGrant the route resolves")
		kubectlApply(xnsReferenceGrantYAML(backendNS, testNamespace))
		Eventually(func() string {
			return routeParentStatus(testNamespace, "xns-route", "ResolvedRefs")
		}, 2*time.Minute, 5*time.Second).Should(Equal("True"))

		By("deleting the ReferenceGrant revokes the permission again")
		kubectl("-n", backendNS, "delete", "referencegrant", "allow-routes")
		Eventually(func() string {
			return routeParentReason(testNamespace, "xns-route", "ResolvedRefs")
		}, 2*time.Minute, 5*time.Second).Should(Equal("RefNotPermitted"))
	})
})

// The policy validator reports TargetNotFound for a policy whose target is
// missing; no Gateway exists, so no LBC (and no cloud LB) is created.
var _ = Describe("ALB Gateway policy targeting a missing object", func() {
	It("reports Accepted=False with reason TargetNotFound", func() {
		kubectlApply(ghostPolicyYAML)
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "vksgatewaypolicy", "ghost-pol", "--ignore-not-found")
		})
		Eventually(func() string {
			return jsonpath(testNamespace, "vksgatewaypolicy", "ghost-pol",
				`{.status.conditions[?(@.type=="Accepted")].reason}`)
		}, time.Minute, 5*time.Second).Should(Equal("TargetNotFound"))
	})
})

// allowedRoutes NamespacesFromSelector matches the route namespace's labels.
var _ = Describe("ALB Gateway allowedRoutes NamespacesFromSelector", func() {
	const routeNS = "gw-e2e-sel"
	It("attaches a route from a namespace whose labels match the listener selector", func() {
		kubectlApply(fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata: {name: %s, labels: {team: blue}}\n", routeNS))
		DeferCleanup(func() { kubectlQuiet("delete", "namespace", routeNS, "--ignore-not-found", "--wait=false") })

		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "sel-gw", "--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(selGatewayYAML)
		kubectlApply(selBackendRouteYAML(routeNS))
		kubectl("-n", routeNS, "rollout", "status", "deploy/echo", "--timeout=120s")

		By("the route in the team=blue namespace is Accepted by the selector listener")
		Eventually(func() string {
			return routeParentStatus(routeNS, "sel-route", "Accepted")
		}, 90*time.Second, 5*time.Second).Should(Equal("True"))
	})
})

// A rule whose backends carry divergent policies fails closed.
var _ = Describe("ALB Gateway backend-config mismatch", func() {
	It("reports BackendConfigMismatch when a rule's backends carry divergent policies", func() {
		kubectlApply(mismatchBackendsYAML)
		kubectl("-n", testNamespace, "rollout", "status", "deploy/echo-a", "--timeout=120s")
		kubectl("-n", testNamespace, "rollout", "status", "deploy/echo-b", "--timeout=120s")
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "mm-gw", "--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(mismatchGatewayRouteYAML)

		Eventually(func() string {
			return routeParentReason(testNamespace, "mm-route", "ResolvedRefs")
		}, 90*time.Second, 5*time.Second).Should(Equal("BackendConfigMismatch"))
	})
})

// --- fixtures ---

var ghostPolicyYAML = fmt.Sprintf(`
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSGatewayPolicy
metadata: {name: ghost-pol, namespace: %[1]s}
spec:
  targetRefs: [{group: gateway.networking.k8s.io, kind: Gateway, name: no-such-gw}]
`, testNamespace)

func xnsBackendYAML(ns string) string {
	return fmt.Sprintf(`
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
`, ns)
}

func xnsGatewayRouteYAML(backendNS string) string {
	return fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: xns-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners: [{name: http, protocol: HTTP, port: 80, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: xns-route, namespace: %[1]s}
spec:
  parentRefs: [{name: xns-gw}]
  rules:
  - matches: [{path: {type: PathPrefix, value: /}}]
    backendRefs: [{name: echo, namespace: %[3]s, port: 80}]
`, testNamespace, gatewayClassName, backendNS)
}

func xnsReferenceGrantYAML(backendNS, fromNS string) string {
	return fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata: {name: allow-routes, namespace: %[1]s}
spec:
  from: [{group: gateway.networking.k8s.io, kind: HTTPRoute, namespace: %[2]s}]
  to: [{group: "", kind: Service}]
`, backendNS, fromNS)
}

var selGatewayYAML = fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: sel-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners:
  - name: http
    protocol: HTTP
    port: 80
    allowedRoutes:
      namespaces:
        from: Selector
        selector: {matchLabels: {team: blue}}
`, testNamespace, gatewayClassName)

func selBackendRouteYAML(routeNS string) string {
	return fmt.Sprintf(`
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
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: sel-route, namespace: %[1]s}
spec:
  parentRefs: [{name: sel-gw, namespace: %[2]s}]
  rules:
  - matches: [{path: {type: PathPrefix, value: /}}]
    backendRefs: [{name: echo, port: 80}]
`, routeNS, testNamespace)
}

var mismatchBackendsYAML = fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata: {name: echo-a, namespace: %[1]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: echo-a}}
  template:
    metadata: {labels: {app: echo-a}}
    spec: {containers: [{name: nginx, image: nginx:alpine, ports: [{containerPort: 80}]}]}
---
apiVersion: v1
kind: Service
metadata: {name: echo-a, namespace: %[1]s}
spec: {type: NodePort, selector: {app: echo-a}, ports: [{port: 80, targetPort: 80}]}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: echo-b, namespace: %[1]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: echo-b}}
  template:
    metadata: {labels: {app: echo-b}}
    spec: {containers: [{name: nginx, image: nginx:alpine, ports: [{containerPort: 80}]}]}}
---
apiVersion: v1
kind: Service
metadata: {name: echo-b, namespace: %[1]s}
spec: {type: NodePort, selector: {app: echo-b}, ports: [{port: 80, targetPort: 80}]}
---
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSHealthCheckPolicy
metadata: {name: echo-a-hc, namespace: %[1]s}
spec:
  targetRefs: [{group: "", kind: Service, name: echo-a}]
  protocol: HTTP
  httpHealthCheck: {path: /healthz}
`, testNamespace)

var mismatchGatewayRouteYAML = fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: mm-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners: [{name: http, protocol: HTTP, port: 80, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: mm-route, namespace: %[1]s}
spec:
  parentRefs: [{name: mm-gw}]
  rules:
  - matches: [{path: {type: PathPrefix, value: /}}]
    backendRefs: [{name: echo-a, port: 80}, {name: echo-b, port: 80}]
`, testNamespace, gatewayClassName)
