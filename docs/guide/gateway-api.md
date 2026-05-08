# Gateway API

The vngcloud Load Balancer Controller supports the Kubernetes [Gateway API](https://gateway-api.sigs.k8s.io/) as the future-facing alternative to Ingress. Phase 1 ships **L7 (ALB) only** — `vngcloud-alb` GatewayClass, `Gateway`, `HTTPRoute`, plus two extension CRDs (`TargetGroupConfig`, `ListenerRuleConfig`).

L4 (NLB) support — `TCPRoute`, `UDPRoute`, `TLSRoute` on a `vngcloud-nlb` GatewayClass — is planned for Phase 2.

## Why Gateway API

- **Role split:** Cluster operators own `GatewayClass` / `Gateway`; app teams own `HTTPRoute`. Ingress conflates both.
- **Cross-namespace backends** via `ReferenceGrant`.
- **Better filter model:** redirect, header modification, mirroring all in spec rather than annotation soup.
- **Industry direction:** Ingress is in maintenance; new feature investment is on Gateway API.

For existing clusters, **Ingress and Gateway can coexist** in Phase 1, but each Gateway always provisions its own vngcloud LB (no sharing with Ingress). Migration tooling is planned for Phase 4.

## Prerequisites

Install the upstream Gateway API CRDs (Standard channel, v1.2.0):

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml
```

The ALB Gateway controller is enabled by default. Just install / upgrade the chart:

```bash
helm upgrade --install vngcloud-load-balancer-controller \
  charts/vngcloud-load-balancer-controller
```

To **opt out** (e.g. on a cluster that hasn't installed the upstream Gateway-API CRDs):

```bash
helm upgrade vngcloud-load-balancer-controller \
  charts/vngcloud-load-balancer-controller \
  --set gatewayApi.alb.enabled=false
# Also append `--disable-gateway-api-alb=true` to manager.manager.args
# in your values file (the chart passes args verbatim).
```

## Quickstart

```bash
kubectl apply -f config/samples/gateway_v1_alb_basic.yaml
kubectl get gateway demo -w
# Wait for status.addresses to populate.
ADDR=$(kubectl get gateway demo -o jsonpath='{.status.addresses[0].value}')
curl -H "Host: demo.example.com" "http://${ADDR}/"
```

## Capability matrix (Phase 1)

| Gateway API feature | Status | Notes |
|---|---|---|
| `GatewayClass` (vngcloud-alb) | ✅ | parametersRef → cluster-scope `LoadBalancerConfig` |
| `Gateway` (HTTP/HTTPS/TLS-terminate) | ✅ | 1 Gateway = 1 vngcloud ALB |
| `HTTPRoute` hostname / path matches | ✅ | Exact, PathPrefix, RegularExpression |
| `HTTPRoute` weighted backendRefs | ✅ | Synthetic merged pool with weight-scaled members |
| `RequestRedirect` filter | ✅ | Maps to vngcloud `REDIRECT_TO_URL` action |
| `RequestHeaderModifier(set)` filter (uniform) | ✅ | Promoted to listener-level `insertHeaders` |
| `ReferenceGrant` (Service + Secret) | ✅ | Cross-namespace backendRefs and certificateRefs |
| `TargetGroupConfig` per-Service overrides | ✅ | Health check, target-type, sticky session |
| `ListenerRuleConfig` Header/Query/Method/SourceIP | 🟡 | Schema accepted; matches dropped (vngcloud LB awaiting native support) |
| `URLRewrite`, `RequestMirror`, `ResponseHeaderModifier` | ❌ | Phase 3 |
| `GRPCRoute` | ❌ | Phase 3 |
| `BackendTLSPolicy` | ❌ | Phase 3 |
| `TCPRoute` / `UDPRoute` / `TLSRoute` | ❌ | Phase 2 (NLB GatewayClass) |
| Multi-Gateway sharing one LB | ❌ | Matches AWS LBC; not planned |

## Coexistence with Ingress

Each Gateway always provisions a fresh vngcloud LB (no `loadBalancerId` adoption on Gateway in v1). Existing Ingress controllers continue to operate unchanged. To migrate from Ingress to Gateway, currently the path is "create new Gateway → cut over DNS → delete Ingress." A first-class migration tool is planned for Phase 4.

## See also

- [`gateway-alb.md`](gateway-alb.md) — L7 walkthrough (HTTP, HTTPS+SNI, mTLS, weighted canary, cross-namespace).
- [`gateway-extensions.md`](gateway-extensions.md) — `LoadBalancerConfig`, `TargetGroupConfig`, `ListenerRuleConfig`.
- [`../examples/gateway-canary.md`](../examples/gateway-canary.md) — weighted backend deep dive.
