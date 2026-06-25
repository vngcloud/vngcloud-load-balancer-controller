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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// HTTPS/TLS termination via certificateRefs (Secret import) + per-listener
// VKSGatewayPolicy scoping by sectionName. One ALB.
var _ = Describe("ALB Gateway HTTPS + per-listener policy scoping", func() {
	It("imports a TLS Secret on the HTTPS listener and scopes a per-listener policy by sectionName", func() {
		kubectlApply(selfSignedTLSSecretYAML(testNamespace, "tls-secret", "app.example.com"))
		kubectlApply(tlsPoliciesYAML) // unscoped (timeoutClient 20s) + sectionName=https (99s)
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "tls-gw", "--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(tlsGatewayYAML) // http :80 + https :443 (certificateRefs: tls-secret)

		var lbc *v1alpha1.LoadBalancerConfig
		Eventually(func(g Gomega) {
			lbc = getOwnedLBC(testNamespace, "tls-gw")
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(listenerByPort(lbc, 80)).NotTo(BeNil())
			g.Expect(listenerByPort(lbc, 443)).NotTo(BeNil())
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		https := listenerByPort(lbc, 443)
		http := listenerByPort(lbc, 80)

		By("the Secret is imported as the HTTPS listener's default certificate")
		Expect(https.CertificateDefault).NotTo(BeNil())
		Expect(https.CertificateDefault.SecretName).NotTo(BeNil())
		Expect(*https.CertificateDefault.SecretName).To(Equal("tls-secret"))
		Expect(lbc.Spec.CreateCertificates).To(ContainElement(HaveField("SecretName", "tls-secret")))

		By("per-listener policy (sectionName=https) wins over the unscoped default")
		Expect(https.TimeoutClient).NotTo(BeNil())
		Expect(*https.TimeoutClient).To(Equal(int32(99)))
		Expect(http.TimeoutClient).NotTo(BeNil())
		Expect(*http.TimeoutClient).To(Equal(int32(20)))
	})
})

// targetType=ip (pod IPs as members) + VKSHealthCheckPolicy conflict
// (oldest-wins). One ALB.
var _ = Describe("ALB Gateway backend targetType=ip and health-check conflict", func() {
	It("resolves pod IPs and applies the oldest health-check policy on conflict", func() {
		kubectlApply(echo3BackendYAML)
		kubectl("-n", testNamespace, "rollout", "status", "deploy/echo3", "--timeout=120s")

		// Two health-check policies on the same Service conflict; the
		// oldest-created (also lexicographically first) wins the LBC, and the
		// policy-validator controller reports the loser Conflicted.
		kubectlApply(hcConflictYAML("hc-a-older", 2)) // applied first -> older -> wins
		kubectlApply(hcConflictYAML("hc-b-newer", 9))
		kubectlApply(advBackendPolicyYAML) // targetType: ip on echo3
		DeferCleanup(func() {
			kubectlQuiet("-n", testNamespace, "delete", "gateway", "adv-gw", "--ignore-not-found", "--wait=true", "--timeout=5m")
		})
		kubectlApply(advGatewayRouteYAML)

		var lbc *v1alpha1.LoadBalancerConfig
		Eventually(func(g Gomega) {
			lbc = getOwnedLBC(testNamespace, "adv-gw")
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Pools).NotTo(BeEmpty())
			g.Expect(lbc.Spec.Pools[0].Members).NotTo(BeEmpty())
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		pool := lbc.Spec.Pools[0]
		By("targetType ip -> members use the pod port (80), not a nodePort")
		for _, m := range pool.Members {
			Expect(m.Port).To(Equal(80))
		}
		By("conflict -> oldest health-check policy (healthyThreshold 2) wins")
		Expect(pool.HealthMonitor.HealthyThreshold).NotTo(BeNil())
		Expect(*pool.HealthMonitor.HealthyThreshold).To(Equal(2))

		By("the policy validator reports the older policy Accepted and the newer Conflicted")
		Eventually(func(g Gomega) {
			g.Expect(jsonpath(testNamespace, "vkshealthcheckpolicy", "hc-a-older",
				`{.status.conditions[?(@.type=="Accepted")].status}`)).To(Equal("True"))
			g.Expect(jsonpath(testNamespace, "vkshealthcheckpolicy", "hc-b-newer",
				`{.status.conditions[?(@.type=="Accepted")].reason}`)).To(Equal("Conflicted"))
		}, time.Minute, 5*time.Second).Should(Succeed())
	})
})

// Gateway / HTTPRoute status reporting, on a SIMPLE cloud-valid gateway (no
// health-check override, so the default TCP probe on the member port passes and
// the LB reaches Programmed). Kept separate from the kitchen suite, which
// deliberately breaks the health check (port 8080, nothing listening) to test
// the monitorPort override — so it must not be coupled to provisioning success.
var _ = Describe("ALB Gateway status reporting", Ordered, func() {
	BeforeAll(func() {
		kubectlApply(statusBackendYAML)
		kubectl("-n", testNamespace, "rollout", "status", "deploy/echo-status", "--timeout=120s")
		kubectlApply(statusPolicyYAML)
		kubectlApply(statusGatewayRouteYAML)
	})
	AfterAll(func() {
		kubectlQuiet("-n", testNamespace, "delete", "gateway", "status-gw",
			"--ignore-not-found", "--wait=true", "--timeout=5m")
	})

	It("reports Gateway Programmed + an address once the LB provisions", func() {
		Eventually(func(g Gomega) {
			g.Expect(jsonpath(testNamespace, "gateway", "status-gw",
				`{.status.conditions[?(@.type=="Programmed")].status}`)).To(Equal("True"))
			g.Expect(jsonpath(testNamespace, "gateway", "status-gw",
				`{.status.addresses[0].value}`)).NotTo(BeEmpty())
		}, 5*time.Minute, 5*time.Second).Should(Succeed())
	})

	// Requires the controller to be running the route-status writer
	// (writeRouteStatuses). Verified in-process by the alb envtest suite; on a
	// live cluster the running controller must include this build.
	It("reports HTTPRoute Accepted + ResolvedRefs on its parent", func() {
		Eventually(func(g Gomega) {
			g.Expect(jsonpath(testNamespace, "httproute", "status-route",
				`{.status.parents[0].conditions[?(@.type=="Accepted")].status}`)).To(Equal("True"))
			g.Expect(jsonpath(testNamespace, "httproute", "status-route",
				`{.status.parents[0].conditions[?(@.type=="ResolvedRefs")].status}`)).To(Equal("True"))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())
	})
})

var statusBackendYAML = fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata: {name: echo-status, namespace: %[1]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: echo-status}}
  template:
    metadata: {labels: {app: echo-status}}
    spec:
      containers: [{name: nginx, image: nginx:alpine, ports: [{containerPort: 80}]}]
---
apiVersion: v1
kind: Service
metadata: {name: echo-status, namespace: %[1]s}
spec: {type: NodePort, selector: {app: echo-status}, ports: [{port: 80, targetPort: 80}]}
`, testNamespace)

var statusPolicyYAML = fmt.Sprintf(`
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSGatewayPolicy
metadata: {name: status-pol, namespace: %[1]s}
spec:
  targetRefs: [{group: gateway.networking.k8s.io, kind: Gateway, name: status-gw}]
  loadBalancerSpec: {scheme: Internet}
`, testNamespace)

var statusGatewayRouteYAML = fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: status-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners: [{name: http, protocol: HTTP, port: 80, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: status-route, namespace: %[1]s}
spec:
  parentRefs: [{name: status-gw}]
  rules:
  - matches: [{path: {type: PathPrefix, value: /}}]
    backendRefs: [{name: echo-status, port: 80}]
`, testNamespace, gatewayClassName)

// --- fixtures ---

// selfSignedTLSSecretYAML returns a kubernetes.io/tls Secret manifest carrying a
// freshly generated self-signed cert/key for cn.
func selfSignedTLSSecretYAML(ns, name, cn string) string {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{cn},
		BasicConstraintsValid: true,
	}
	der, _ := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(priv)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata: {name: %[1]s, namespace: %[2]s}
type: kubernetes.io/tls
data:
  tls.crt: %[3]s
  tls.key: %[4]s
`, name, ns, base64.StdEncoding.EncodeToString(certPEM), base64.StdEncoding.EncodeToString(keyPEM))
}

var tlsPoliciesYAML = fmt.Sprintf(`
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSGatewayPolicy
metadata: {name: tls-unscoped, namespace: %[1]s}
spec:
  targetRefs: [{group: gateway.networking.k8s.io, kind: Gateway, name: tls-gw}]
  loadBalancerSpec: {scheme: Internet}
  timeoutClient: 20s
---
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSGatewayPolicy
metadata: {name: tls-https, namespace: %[1]s}
spec:
  targetRefs: [{group: gateway.networking.k8s.io, kind: Gateway, name: tls-gw, sectionName: https}]
  timeoutClient: 99s
`, testNamespace)

var tlsGatewayYAML = fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: tls-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners:
  - {name: http, protocol: HTTP, port: 80, allowedRoutes: {namespaces: {from: Same}}}
  - name: https
    protocol: HTTPS
    port: 443
    tls:
      mode: Terminate
      certificateRefs: [{name: tls-secret}]
    allowedRoutes: {namespaces: {from: Same}}
`, testNamespace, gatewayClassName)

var echo3BackendYAML = fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata: {name: echo3, namespace: %[1]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: echo3}}
  template:
    metadata: {labels: {app: echo3}}
    spec:
      containers: [{name: nginx, image: nginx:alpine, ports: [{containerPort: 80}]}]
---
apiVersion: v1
kind: Service
metadata: {name: echo3, namespace: %[1]s}
spec: {selector: {app: echo3}, ports: [{port: 80, targetPort: 80}]}
`, testNamespace)

func hcConflictYAML(name string, healthyThreshold int) string {
	return fmt.Sprintf(`
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSHealthCheckPolicy
metadata: {name: %[2]s, namespace: %[1]s}
spec:
  targetRefs: [{group: "", kind: Service, name: echo3}]
  protocol: TCP
  healthyThreshold: %[3]d
`, testNamespace, name, healthyThreshold)
}

var advBackendPolicyYAML = fmt.Sprintf(`
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSBackendPolicy
metadata: {name: echo3-bp, namespace: %[1]s}
spec:
  targetRefs: [{group: "", kind: Service, name: echo3}]
  targetType: ip
`, testNamespace)

var advGatewayRouteYAML = fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: adv-gw, namespace: %[1]s}
spec:
  gatewayClassName: %[2]s
  listeners: [{name: http, protocol: HTTP, port: 80, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: adv-route, namespace: %[1]s}
spec:
  parentRefs: [{name: adv-gw}]
  rules:
  - matches: [{path: {type: PathPrefix, value: /}}]
    backendRefs: [{name: echo3, port: 80}]
`, testNamespace, gatewayClassName)
