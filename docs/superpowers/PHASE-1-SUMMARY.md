# Gateway API — Phase 1 Summary

**Branch:** `feature/gateway-api-phase1`
**Status:** Implementation complete except D6 (envtest suite) and Block G (e2e — needs live cluster).
**Goal:** Ship L7 Gateway-API support — `vngcloud-alb` GatewayClass, `Gateway`, `HTTPRoute`, plus `TargetGroupConfig` and `ListenerRuleConfig` extension CRDs.

## What ships

### New CRDs
- `gateway.vks.vngcloud.vn/v1alpha1.TargetGroupConfig` — per-Service backend tuning (target-type, sticky session, health checks) with optional per-route overrides via `routeConfigurations[]`.
- `gateway.vks.vngcloud.vn/v1alpha1.ListenerRuleConfig` — per-HTTPRoute filter extension carrying header/query/method/source-IP additional matches and fixed-response/reject/redirect actions. Forward-compat contract: when vngcloud LB ships native support for these match types, they promote into core HTTPRoute matches transparently.

### Extended CRD
- `vks.vngcloud.vn/v1alpha1.LoadBalancerConfig` — additive fields:
  - `spec.mergingMode` (`PreferGateway` default, `PreferGatewayClass` opt-in) controls how a class-level LBC merges with a gateway-level LBC when both are referenced.
  - `spec.listeners[].sslPolicy`, `spec.listeners[].alpnPolicy` for TLS-listener tuning.

### Reconcilers
| Resource | Reconciler | Behavior |
|---|---|---|
| `GatewayClass` | `internal/controller/gateway/alb` | Validates parametersRef → LoadBalancerConfig; reports Accepted. |
| `Gateway` | `internal/controller/gateway/alb` | 1 Gateway = 1 vngcloud ALB. Resolves effective LBC, validates listeners, deploys an owned `LoadBalancerConfig` CRD that the existing `lbc_uc` reconciles. Writes Accepted/Programmed status. |
| `HTTPRoute` | `internal/controller/gateway/alb` | Thin: enqueues parent Gateway via `EnqueueParentGatewayForRoute`. All LB writes serialize through the Gateway reconciler. |
| `TargetGroupConfig` | `internal/controller/gateway/targetgroupconfig` | Validates schema; reports Accepted. |
| `ListenerRuleConfig` | `internal/controller/gateway/listenerruleconfig` | Validates schema; reports Accepted. |

### Use-case (`internal/usecase/gateway_uc/alb_gateway_uc/`)
Split into focused units (Init, Resolve, Attach, Deploy, Status, Delete) with TDD-covered helpers:
- `build_lb.go` — Gateway → LB params (forces type=Layer 7, defaults LB name).
- `build_listener.go` — Gateway listener → vngcloud listener; LBC overrides (TLS, mTLS, timeouts, allowed CIDRs, SSL/ALPN policy) applied by name.
- `build_cert.go` — Secret → ListenerCertificate placeholder; lbc_uc imports.
- `build_pool.go` — Synthesize a single pool from N weighted backends; integer member weights preserve the route's backendRef ratio.
- `build_policy.go` — One vngcloud Policy per (hostname × match) cartesian; Default action `REDIRECT_TO_POOL`, RequestRedirect promotes to `REDIRECT_TO_URL`.
- `build_sec_group.go` — Phase 1 passthrough; Phase 3 hook.

### Shared helpers (`internal/usecase/gateway_uc/shared/`)
- `merge_config.go` — LBC merging with `PreferGateway` / `PreferGatewayClass`.
- `refgrant.go` — ReferenceGrant evaluation for cross-namespace refs.
- `tgc_resolver.go` — TargetGroupConfig cascade (rule > route > default).
- `lrc_resolver.go` — ListenerRuleConfig ExtensionRef extraction.

### Controller helpers (`internal/controller/gateway/shared/`)
- `classifier.go` — Listener protocol → ALB-allowed validation (rejects TCP/UDP/dup-port).
- `policy_order.go` — Match-specificity scorer for Gateway-API policy ordering.
- `status.go` — `metav1.Condition` upsert helper.

### Pure helpers (`pkg/gateway/`)
- `gatewayapi_utils.go` — Hostname / path → vngcloud L7 rule converters (literal, wildcard→regex).
- `synth_pool.go` — Deterministic pool naming via SHA-1 over sorted `(ns, name, port, weight)` tuples.

### Wiring
- `cmd/main.go`: registers Gateway-API schemes (v1, v1alpha2, v1beta1) + new local group; new `--enable-gateway-api-alb` feature gate (default false). When set, all 5 reconcilers spin up.
- `pkg/metrics/util/reconcile_counter.go`: adds `IncrementGateway` and `IncrementHTTPRoute`.

### Helm chart (`charts/vngcloud-load-balancer-controller/`)
- New CRD chart templates: `targetgroupconfig-crd.yaml`, `listenerruleconfig-crd.yaml`.
- `gatewayclass-alb.yaml` template gated on `gatewayApi.alb.enabled`; honors `gatewayApi.alb.parametersRef` for an optional class-default LBC reference.
- `values.yaml` documents the upstream Gateway-API CRD prerequisite and how to append `--enable-gateway-api-alb=true` to the manager args.

### Samples & docs
- 5 sample manifests under `config/samples/`: basic HTTP, HTTPS+SNI, weighted canary, TGC, LRC.
- 4 user docs under `docs/guide/` and `docs/examples/`: `gateway-api.md` overview + capability matrix, `gateway-alb.md` walkthrough, `gateway-extensions.md` (CRD deep dive), `examples/gateway-canary.md` (weight scaling math).

## Test coverage

**~60 unit tests** across 4 new packages. All passing:

| Package | Tests | Coverage |
|---|---|---|
| `pkg/gateway` | 5 | hostname/path mapping, synthetic pool naming determinism + weight changes |
| `internal/controller/gateway/shared` | 8 | listener validation, condition upsert, match specificity |
| `internal/usecase/gateway_uc/shared` | 18 | LBC merging (both modes + nil cases), ReferenceGrant matrix, TGC cascade with conflicts, LRC extraction |
| `internal/usecase/gateway_uc/alb_gateway_uc` | ~30 (incl. the C9-split helpers' own tests) | LB spec, listener, pool, policy build helpers; resolve/attach/deploy/status/delete |

`go build ./...` clean. `go vet ./...` clean. `helm template … --set gatewayApi.alb.enabled=true` produces 26 resource kinds.

## Deferred

| Item | Reason | Resume from |
|---|---|---|
| **D6 — envtest suite** | Copy-adapt from `internal/controller/networking/suite_test.go` is ~200 lines + needs upstream Gateway-API CRDs downloaded; benefits from a fresh focused session. The reconcilers' core logic is covered by use-case unit tests. | `docs/superpowers/plans/2026-04-30-gateway-api-phase1.md` Task D6 |
| **G1–G6 — e2e tests** | Require live vngcloud cluster, KUBECONFIG, real backend services. Cannot run in this sandbox. | Plan tasks G1–G6; harness scaffold included; tests author-and-skip until cluster available. |
| **Phase 2 — NLB GatewayClass** | TCPRoute/UDPRoute/TLSRoute on `vngcloud-nlb`. Separate plan when scheduled. | New plan needed (per spec). |
| **Phase 3 — GRPCRoute, BackendTLSPolicy, URLRewrite/Mirror/ResponseHeaderModifier** | Some need vngcloud LB feature support first. | New plan when scheduled. |
| **Phase 4 — Migration tool, conformance suite** | Adopts existing Ingress LB; runs upstream Gateway-API conformance. | New plan when scheduled. |

## Operational

### Enabling Phase 1 in a cluster

```bash
# 1. Install upstream Gateway API CRDs
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml

# 2. Upgrade chart with the gate enabled
helm upgrade vngcloud-load-balancer-controller charts/vngcloud-load-balancer-controller \
  --set gatewayApi.alb.enabled=true \
  --reuse-values
# (Also append --enable-gateway-api-alb=true to manager.manager.args.)

# 3. Apply a sample
kubectl apply -f config/samples/gateway_v1_alb_basic.yaml
kubectl get gateway demo -w
```

### Rollback safety

- All CRD changes are additive (no field removals/renames), so old manifests keep applying cleanly.
- Feature-gate defaults to `false`; no behavior change unless explicitly enabled.
- Existing Service / Ingress / GLB / LBC / NSG controllers are untouched.

## Branch state

```
$ git log --oneline feature/gateway-api-phase1 ^v3 | wc -l
~40 commits
```

Major commit groups:
- `aa315bc..7fd0820` — Block A foundation (8 commits)
- `8bbad83..d6b21fa` — Block B utilities (10 commits)
- `ccc6f04..7d9f1c5` — Block C use case incl. C9 expansion (~12 commits)
- `…` — Block D reconcilers (4 commits)
- `5a435b3..f353b15` — Block E wiring + Block F chart/samples/docs (5 commits)
