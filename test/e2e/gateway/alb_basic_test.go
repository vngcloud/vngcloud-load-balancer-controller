package gateway_e2e

import (
	"strings"
	"testing"
	"time"
)

// TestALBBasic provisions a vanilla HTTP Gateway pointing at an echo backend and
// verifies that the rendered status.address responds 200 to a host-header request.
//
// Prerequisites: vngcloud-alb GatewayClass installed; manager has
// --enable-gateway-api-alb=true.
func TestALBBasic(t *testing.T) {
	skipIfNotE2E(t)

	const ns = "gw-e2e-basic"
	const host = "demo-basic.e2e.test"

	manifest := echoBackendManifest(ns, "demo-svc") + `---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: demo, namespace: ` + ns + ` }
spec:
  gatewayClassName: vngcloud-alb
  listeners:
  - { name: http, protocol: HTTP, port: 80 }
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: demo, namespace: ` + ns + ` }
spec:
  parentRefs: [{ name: demo }]
  hostnames: [` + host + `]
  rules:
  - matches: [{ path: { type: PathPrefix, value: / } }]
    backendRefs: [{ name: demo-svc, port: 80 }]
`

	defer kubectlMust(t, "delete", "ns", ns, "--wait=false", "--ignore-not-found=true")
	kubectlMust(t, "create", "ns", ns)
	applyManifest(t, manifest)
	defer deleteManifest(t, manifest)

	addr := waitForGatewayAddress(t, ns, "demo", 5*time.Minute)
	t.Logf("Gateway address: %s", addr)

	status, body := pollHTTP(t, addr, host, 3*time.Minute)
	if status != 200 {
		t.Fatalf("expected 200, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "host") {
		t.Logf("body did not contain 'host' (echo signature) — got: %s", body)
	}
}
