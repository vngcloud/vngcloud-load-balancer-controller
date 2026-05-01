/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package gateway_e2e contains end-to-end tests for the ALB Gateway feature.
// These tests require a live vngcloud cluster and the upstream Gateway API CRDs.
//
// Run them with:
//
//	E2E=1 KUBECONFIG=~/.kube/config go test ./test/e2e/gateway/... -v -timeout=30m
//
// Without E2E=1 the entire suite is skipped so this package never blocks `make test`.
package gateway_e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// e2eEnabled reports whether the live-cluster suite should run.
func e2eEnabled() bool { return os.Getenv("E2E") == "1" }

// kubeconfig returns the KUBECONFIG path, falling back to ~/.kube/config.
func kubeconfig() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home + "/.kube/config"
	}
	return ""
}

// kubectl runs `kubectl <args...>` and returns combined stdout+stderr.
// The KUBECONFIG flag is injected automatically.
func kubectl(t *testing.T, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"--kubeconfig=" + kubeconfig()}, args...)
	cmd := exec.Command("kubectl", full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// kubectlMust runs kubectl, t.Fatal-ing on error.
func kubectlMust(t *testing.T, args ...string) string {
	t.Helper()
	out, err := kubectl(t, args...)
	if err != nil {
		t.Fatalf("kubectl %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// waitForGatewayAddress polls until Gateway.status.addresses[0].value is non-empty.
//
//nolint:unparam // helper kept generic for future scenarios
func waitForGatewayAddress(t *testing.T, ns, name string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := kubectl(t, "-n", ns, "get", "gateway", name,
			"-o", "jsonpath={.status.addresses[0].value}")
		if v := strings.TrimSpace(out); v != "" {
			return v
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for Gateway %s/%s status.addresses", ns, name)
	return ""
}

// httpGet performs HTTP GET against `addr` with the given Host header. Caller is
// responsible for any retry needed during LB warm-up.
func httpGet(addr, host string) (int, string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		return 0, "", err
	}
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), nil
}

// pollHTTP retries httpGet until it returns 2xx or the deadline expires.
func pollHTTP(t *testing.T, addr, host string, timeout time.Duration) (int, string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	var status int
	var body string
	for time.Now().Before(deadline) {
		status, body, lastErr = httpGet(addr, host)
		if lastErr == nil && status >= 200 && status < 400 {
			return status, body
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("HTTP poll to %s (host=%s) never succeeded: status=%d err=%v body=%s",
		addr, host, status, lastErr, body)
	return 0, ""
}

// applyManifest writes content to a temp file and `kubectl apply -f` it.
func applyManifest(t *testing.T, content string) {
	t.Helper()
	f, err := os.CreateTemp("", "gw-e2e-*.yaml")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()
	kubectlMust(t, "apply", "-f", f.Name())
}

// deleteManifest deletes a previously-applied manifest by content; ignores not-found.
func deleteManifest(t *testing.T, content string) {
	t.Helper()
	f, err := os.CreateTemp("", "gw-e2e-*.yaml")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, _ = f.WriteString(content)
	_ = f.Close()
	_, _ = kubectl(t, "delete", "-f", f.Name(), "--ignore-not-found=true", "--wait=false")
}

// skipIfNotE2E skips the calling test unless E2E=1 is set. Intended as the first
// statement in every test function in this package.
func skipIfNotE2E(t *testing.T) {
	t.Helper()
	if !e2eEnabled() {
		t.Skip("E2E not set — skipping live-cluster test")
	}
}

// echoBackendManifest creates a tiny echo Service + Deployment in the given ns.
// Used by every flow test as the target backend.
func echoBackendManifest(ns, name string) string {
	return fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata: { name: %[2]s, namespace: %[1]s }
spec:
  replicas: 2
  selector: { matchLabels: { app: %[2]s } }
  template:
    metadata: { labels: { app: %[2]s } }
    spec:
      containers:
      - name: echo
        image: ealen/echo-server:0.9.2
        ports: [{ containerPort: 80 }]
---
apiVersion: v1
kind: Service
metadata: { name: %[2]s, namespace: %[1]s }
spec:
  selector: { app: %[2]s }
  ports: [{ port: 80, targetPort: 80, protocol: TCP }]
`, ns, name)
}
