# Weighted canary with Gateway API

Gateway API expresses progressive delivery natively: each `HTTPRoute.rule` carries a list of `backendRefs` with weights. The controller folds these into a single vngcloud pool whose members are weight-scaled to preserve the requested ratio.

## Manifest

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: demo-canary, namespace: default }
spec:
  parentRefs: [{ name: demo }]
  hostnames: [demo.example.com]
  rules:
  - matches: [{ path: { type: PathPrefix, value: / } }]
    backendRefs:
    - { name: demo-service-v1, port: 80, weight: 90 }
    - { name: demo-service-v2, port: 80, weight: 10 }
```

## What the controller does

1. **Resolve endpoints** — for each backendRef, look up the Service's ready endpoints (or NodePort + ready nodes when target-type=instance).
2. **Compute weight per endpoint** — `share = backend.Weight / count(endpoints)`.
3. **Rescale** — divide every share by the smallest non-zero share to produce integer member weights. Floor at 1, cap at 100.
4. **Synthesize a single pool** — name = `gw_<route-uid-prefix>_<rule-idx>_<backendset-hash5>` (deterministic; stable under reorder; changes when the backend set or weights change).
5. **Single L7 policy** points at this pool via `REDIRECT_TO_POOL` — vngcloud's load-balancing algorithm distributes by member weight.

## Worked example

```
v1: 3 ready endpoints, weight 90 → per-endpoint share = 30
v2: 1 ready endpoint,  weight 10 → per-endpoint share = 10
min share = 10
v1 member weight = 30 / 10 = 3   (each of 3 endpoints)
v2 member weight = 10 / 10 = 1   (the single endpoint)
Total weight = 9 + 1 = 10 → 90/10 ratio preserved.
```

## Health checks

The synthetic pool has a single health-check config. Resolution priority:
1. `TargetGroupConfig.routeConfigurations[]` matching this route + rule.
2. First backendRef's `TargetGroupConfig.defaultConfig`.
3. Controller fallback (TCP probe on the backend port).

If two backends carry different `TargetGroupConfig` health-check settings and neither is the route-specific override, the route reports `ResolvedRefs=False, reason=BackendConfigMismatch` until the conflict is resolved.

## Caveats

- **Pool churn on big weight changes** — adding/removing a backend changes the backendset hash, so the pool name changes too. The controller creates the new pool, switches the policy, then deletes the old pool. There's a short window where both exist.
- **Endpoint changes don't churn the pool** — only the member set inside it. Pod scale events go straight to the existing pool's member list.
- **Gateway-API `weight` field is per-backend, not per-endpoint.** Scaling preserves the per-backend ratio regardless of how many endpoints each backend has.

## Related

- [`../guide/gateway-api.md`](../guide/gateway-api.md) — overview and capability matrix.
- [`../guide/gateway-extensions.md`](../guide/gateway-extensions.md) — `TargetGroupConfig` health-check tuning.
