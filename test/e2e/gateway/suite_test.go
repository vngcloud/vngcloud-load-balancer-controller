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

// Package gateway holds the Gateway-API (GEP-713) end-to-end suite.
//
// Unlike the Kind-based default e2e in test/e2e, these specs provision REAL
// vngcloud ALBs, so they run only against a real cluster that already has the
// controller deployed with the ALB gateway controller enabled (the default;
// see --disable-alb-gateway-controller). The suite is opt-in:
//
//	RUN_GATEWAY_E2E=true KUBECONFIG=/path/to/cluster.yaml go test ./test/e2e/gateway/ -v
//
// Without RUN_GATEWAY_E2E the whole suite is skipped, so `make test-e2e`
// (Kind, no vngcloud API) is unaffected. The controller is assumed to be
// already running against the target cluster — the suite does not build or
// deploy it; it only applies Gateway-API resources and asserts on the
// LoadBalancerConfig the controller generates (both the Ingress and Gateway
// paths emit the same LBC, so the LBC is the verification ground).
package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

const (
	gatewayClassName = "vngcloud-alb"
	controllerName   = "gateway.vks.vngcloud.vn/alb"
	testNamespace    = "gw-e2e"
)

func TestGatewayE2E(t *testing.T) {
	if os.Getenv("RUN_GATEWAY_E2E") != "true" {
		t.Skip("set RUN_GATEWAY_E2E=true (and KUBECONFIG) to run gateway-api e2e against a real vngcloud cluster")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "gateway-api e2e suite")
}

var _ = BeforeSuite(func() {
	ensureGatewayClass()
	// Recreate the namespace clean so a previous aborted run can't leak state.
	kubectlQuiet("delete", "namespace", testNamespace, "--ignore-not-found", "--wait=true", "--timeout=5m")
	kubectl("create", "namespace", testNamespace)
})

var _ = AfterSuite(func() {
	// Deleting the namespace removes the Gateways; their finalizers tear down
	// the cloud LBs. Best-effort — leaked LBs are surfaced by the warning.
	kubectlQuiet("delete", "namespace", testNamespace, "--wait=true", "--timeout=5m")
})

// ensureGatewayClass creates the ALB GatewayClass and proves the controller is
// live by waiting for it to be Accepted — a fast precondition that fails the
// whole suite early if the ALB gateway controller isn't running.
func ensureGatewayClass() {
	kubectlApply(fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: %s
spec:
  controllerName: %s
`, gatewayClassName, controllerName))

	Eventually(func() string {
		return jsonpath("", "gatewayclass", gatewayClassName,
			`{.status.conditions[?(@.type=="Accepted")].status}`)
	}, time.Minute, 3*time.Second).Should(Equal("True"),
		"GatewayClass not Accepted — is the controller running with --disable-alb-gateway-controller=false?")
}

// --- kubectl helpers (mirrors test/e2e's shell-out style; no client-go) ---

// kubectl runs `kubectl <args...>` and fails the spec on error.
func kubectl(args ...string) string {
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "kubectl %s\n%s", strings.Join(args, " "), out)
	return string(out)
}

// kubectlQuiet runs kubectl best-effort (for cleanup); errors are warnings only.
func kubectlQuiet(args ...string) {
	if out, err := exec.Command("kubectl", args...).CombinedOutput(); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "warning: kubectl %s: %v\n%s\n", strings.Join(args, " "), err, out)
	}
}

// kubectlApply pipes a manifest to `kubectl apply -f -`.
func kubectlApply(manifest string) {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "kubectl apply:\n%s\n--- manifest ---\n%s", out, manifest)
}

// jsonpath returns the value at expr, or "" on error. Pass ns="" for
// cluster-scoped resources.
func jsonpath(ns, resource, name, expr string) string {
	args := []string{}
	if ns != "" {
		args = append(args, "-n", ns)
	}
	args = append(args, "get", resource, name, "-o", "jsonpath="+expr)
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ownedLBCName returns the name of the LoadBalancerConfig owned by gwName, or "".
func ownedLBCName(ns, gwName string) string {
	out, err := exec.Command("kubectl", "-n", ns, "get", "loadbalancerconfig",
		"-l", "vks.vngcloud.vn/owner-resource-name="+gwName,
		"-o", "jsonpath={.items[0].metadata.name}").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getOwnedLBC fetches the LBC owned by gwName and unmarshals it into the typed
// struct, so specs can assert on nested fields directly. Returns nil if not
// found yet (callers wrap in Eventually).
func getOwnedLBC(ns, gwName string) *v1alpha1.LoadBalancerConfig {
	name := ownedLBCName(ns, gwName)
	if name == "" {
		return nil
	}
	out, err := exec.Command("kubectl", "-n", ns, "get", "loadbalancerconfig", name, "-o", "json").CombinedOutput()
	if err != nil {
		return nil
	}
	var lbc v1alpha1.LoadBalancerConfig
	if err := json.Unmarshal(out, &lbc); err != nil {
		return nil
	}
	return &lbc
}

// listenerByPort returns the LBC listener bound to the given port, or nil.
func listenerByPort(lbc *v1alpha1.LoadBalancerConfig, port int32) *v1alpha1.Listener {
	for i := range lbc.Spec.Listeners {
		if lbc.Spec.Listeners[i].ProtocolPort == port {
			return &lbc.Spec.Listeners[i]
		}
	}
	return nil
}

// policyByPathValue returns the listener policy whose PATH rule equals pathVal.
func policyByPathValue(l *v1alpha1.Listener, pathVal string) *v1alpha1.Policy {
	for i := range l.Policies {
		for _, r := range l.Policies[i].L7Rules {
			if string(r.RuleType) == "PATH" && r.RuleValue == pathVal {
				return &l.Policies[i]
			}
		}
	}
	return nil
}

// ruleByType returns the policy's L7 rule of the given ruleType (HOST_NAME/PATH), or nil.
func ruleByType(p *v1alpha1.Policy, ruleType string) *v1alpha1.L7Rule {
	for i := range p.L7Rules {
		if string(p.L7Rules[i].RuleType) == ruleType {
			return &p.L7Rules[i]
		}
	}
	return nil
}

// weightedPool returns the pool whose members span 2+ distinct ports, i.e. the
// synthetic pool that aggregated multiple weighted backendRefs.
func weightedPool(lbc *v1alpha1.LoadBalancerConfig) *v1alpha1.Pool {
	for i := range lbc.Spec.Pools {
		ports := map[int]struct{}{}
		for _, m := range lbc.Spec.Pools[i].Members {
			ports[m.Port] = struct{}{}
		}
		if len(ports) >= 2 {
			return &lbc.Spec.Pools[i]
		}
	}
	return nil
}
