package gateway_e2e

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestALBmTLS verifies frontendValidation: requests without a client cert get
// rejected (typically 400 / connection-reset), requests presenting a cert
// signed by the CA succeed.
//
// Setup needed in env:
//
//	CERT_FILE / KEY_FILE — server cert/key (PEM) for the listener.
//	CA_FILE              — CA cert (PEM) added to a Secret referenced by frontendValidation.
//	CLIENT_CERT_FILE / CLIENT_KEY_FILE — client cert/key signed by CA.
func TestALBmTLS(t *testing.T) {
	skipIfNotE2E(t)

	const ns = "gw-e2e-mtls"
	const host = "mtls.e2e.test"
	defer kubectlMust(t, "delete", "ns", ns, "--wait=false", "--ignore-not-found=true")
	kubectlMust(t, "create", "ns", ns)

	cert, key := envOrSkip(t, "CERT_FILE", "KEY_FILE")
	caFile, _ := envOrSkip(t, "CA_FILE", "CA_FILE")
	clientCert, clientKey := envOrSkip(t, "CLIENT_CERT_FILE", "CLIENT_KEY_FILE")

	kubectlMust(t, "-n", ns, "create", "secret", "tls", "demo-tls", "--cert="+cert, "--key="+key)
	kubectlMust(t, "-n", ns, "create", "secret", "generic", "demo-ca", "--from-file=ca.crt="+caFile)

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
      frontendValidation:
        caCertificateRefs: [{ group: "", kind: Secret, name: demo-ca }]
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

	// 1) Request without client cert — must NOT succeed (400 or TLS error).
	{
		tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: host}}
		c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
		req, _ := http.NewRequest(http.MethodGet, "https://"+addr+"/", nil)
		req.Host = host
		resp, err := c.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				t.Fatalf("expected 4xx without client cert, got %d", resp.StatusCode)
			}
		}
	}

	// 2) Request WITH client cert — must succeed.
	clientPair, err := tls.LoadX509KeyPair(clientCert, clientKey)
	if err != nil {
		t.Fatalf("load client cert: %v", err)
	}
	caPool := x509.NewCertPool()
	caBytes, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	caPool.AppendCertsFromPEM(caBytes)
	tr := &http.Transport{TLSClientConfig: &tls.Config{
		Certificates: []tls.Certificate{clientPair},
		RootCAs:      caPool,
		ServerName:   host,
	}}
	c := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, "https://"+addr+"/", nil)
		req.Host = host
		resp, err := c.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return
			}
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("mTLS request never succeeded against %s", addr)
}
