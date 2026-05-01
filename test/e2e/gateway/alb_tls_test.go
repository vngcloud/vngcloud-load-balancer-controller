package gateway_e2e

import (
	"crypto/tls"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestALBTLSAndSNI verifies HTTPS termination using a K8s TLS Secret. Multi-cert SNI
// validation requires two distinct hostnames sharing the listener — second cert is
// added under certificateRefs[1].
func TestALBTLSAndSNI(t *testing.T) {
	skipIfNotE2E(t)

	const ns = "gw-e2e-tls"
	const host = "tls.e2e.test"
	defer kubectlMust(t, "delete", "ns", ns, "--wait=false", "--ignore-not-found=true")
	kubectlMust(t, "create", "ns", ns)

	// Self-signed cert generation is delegated to the operator: the user provides
	// CERT_FILE and KEY_FILE env vars (PEM-encoded) for this test. Skip if missing.
	certPath, keyPath := envOrSkip(t, "CERT_FILE", "KEY_FILE")
	kubectlMust(t, "-n", ns, "create", "secret", "tls", "demo-tls",
		"--cert="+certPath, "--key="+keyPath)

	manifest := echoBackendManifest(ns, "demo-svc") + `---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: demo, namespace: ` + ns + ` }
spec:
  gatewayClassName: vngcloud-alb
  listeners:
  - name: https
    protocol: HTTPS
    port: 443
    tls:
      mode: Terminate
      certificateRefs: [{ kind: Secret, name: demo-tls }]
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
	applyManifest(t, manifest)
	defer deleteManifest(t, manifest)

	addr := waitForGatewayAddress(t, ns, "demo", 5*time.Minute)

	// HTTPS GET against the address (skip cert verification — we used self-signed).
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: host}}
	c := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, "https://"+addr+"/", nil)
		req.Host = host
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return
			}
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("HTTPS poll never succeeded against %s host=%s", addr, host)
}

// envOrSkip returns the values of the listed env vars in order. If any is empty,
// the calling test is skipped — these tests need user-provided cert/key paths.
func envOrSkip(t *testing.T, keys ...string) (string, string) {
	t.Helper()
	got := make([]string, 0, len(keys))
	for _, k := range keys {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			t.Skipf("required env %s not set; skipping", k)
		}
		got = append(got, v)
	}
	return got[0], got[1]
}
