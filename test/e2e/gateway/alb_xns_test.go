package gateway_e2e

import (
	"strings"
	"testing"
	"time"
)

// TestALBCrossNamespaceWithReferenceGrant verifies that an HTTPRoute in ns-a
// pointing at a Service in ns-b is rejected (ResolvedRefs=False) until a
// matching ReferenceGrant is created in ns-b.
func TestALBCrossNamespaceWithReferenceGrant(t *testing.T) {
	skipIfNotE2E(t)

	const nsA = "gw-e2e-xns-a"
	const nsB = "gw-e2e-xns-b"
	const host = "xns.e2e.test"
	defer kubectlMust(t, "delete", "ns", nsA, "--wait=false", "--ignore-not-found=true")
	defer kubectlMust(t, "delete", "ns", nsB, "--wait=false", "--ignore-not-found=true")
	kubectlMust(t, "create", "ns", nsA)
	kubectlMust(t, "create", "ns", nsB)

	manifest := echoBackendManifest(nsB, "demo-svc") + `---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: demo, namespace: ` + nsA + ` }
spec:
  gatewayClassName: vngcloud-alb
  listeners:
  - { name: http, protocol: HTTP, port: 80 }
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: demo, namespace: ` + nsA + ` }
spec:
  parentRefs: [{ name: demo }]
  hostnames: [` + host + `]
  rules:
  - matches: [{ path: { type: PathPrefix, value: / } }]
    backendRefs:
    - { name: demo-svc, namespace: ` + nsB + `, port: 80 }
`
	applyManifest(t, manifest)
	defer deleteManifest(t, manifest)

	// Without grant: ResolvedRefs=False with reason=RefNotPermitted.
	if !waitForRouteCondition(t, nsA, "demo", "ResolvedRefs", "False", "RefNotPermitted", 90*time.Second) {
		t.Fatalf("expected ResolvedRefs=False/RefNotPermitted before grant")
	}

	grant := `---
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata: { name: from-` + nsA + `, namespace: ` + nsB + ` }
spec:
  from:
  - { group: gateway.networking.k8s.io, kind: HTTPRoute, namespace: ` + nsA + ` }
  to:
  - { group: "", kind: Service }
`
	applyManifest(t, grant)
	defer deleteManifest(t, grant)

	if !waitForRouteCondition(t, nsA, "demo", "ResolvedRefs", "True", "ResolvedRefs", 90*time.Second) {
		t.Fatalf("expected ResolvedRefs=True after grant")
	}

	addr := waitForGatewayAddress(t, nsA, "demo", 5*time.Minute)
	pollHTTP(t, addr, host, 3*time.Minute)
}

// waitForRouteCondition polls the HTTPRoute's parent[0] conditions until the
// requested type/status/reason appears. Returns false on timeout.
func waitForRouteCondition(t *testing.T, ns, name, condType, status, reason string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	jp := `-o=jsonpath={range .status.parents[0].conditions[?(@.type=="` + condType + `")]}{.status}={.reason};{end}`
	for time.Now().Before(deadline) {
		out, _ := kubectl(t, "-n", ns, "get", "httproute", name, jp)
		want := status + "=" + reason + ";"
		if strings.Contains(out, want) {
			return true
		}
		time.Sleep(3 * time.Second)
	}
	return false
}
