package gateway_e2e

import (
	"strings"
	"testing"
	"time"
)

// TestALBWeightedCanary applies a 90/10 split and issues N requests; asserts the
// observed ratio is within tolerance. The echo backend includes its hostname in
// the response body, which we use to attribute each request to one of the two
// Deployments.
func TestALBWeightedCanary(t *testing.T) {
	skipIfNotE2E(t)

	const ns = "gw-e2e-canary"
	const host = "canary.e2e.test"
	defer kubectlMust(t, "delete", "ns", ns, "--wait=false", "--ignore-not-found=true")
	kubectlMust(t, "create", "ns", ns)

	manifest := echoBackendManifest(ns, "demo-v1") +
		echoBackendManifest(ns, "demo-v2") + `---
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
    backendRefs:
    - { name: demo-v1, port: 80, weight: 90 }
    - { name: demo-v2, port: 80, weight: 10 }
`
	applyManifest(t, manifest)
	defer deleteManifest(t, manifest)

	addr := waitForGatewayAddress(t, ns, "demo", 5*time.Minute)
	pollHTTP(t, addr, host, 3*time.Minute)

	// 1000 requests; count how many hit each backend by host signature in body.
	const total = 1000
	v1, v2 := 0, 0
	for i := range total {
		_, body, err := httpGet(addr, host)
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
		switch {
		case strings.Contains(body, "demo-v1"):
			v1++
		case strings.Contains(body, "demo-v2"):
			v2++
		}
	}

	t.Logf("90/10 split observed: v1=%d v2=%d", v1, v2)
	// Tolerance: ±5 percentage points around the 90/10 target.
	if v1 < 850 || v1 > 950 {
		t.Fatalf("v1 share out of tolerance: got %d/%d, want ~900", v1, total)
	}
	if v2 < 50 || v2 > 150 {
		t.Fatalf("v2 share out of tolerance: got %d/%d, want ~100", v2, total)
	}
}
