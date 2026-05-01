# Gateway Extensions

vngcloud-specific configuration that doesn't fit the standard Gateway API model lives in three CRDs:
- **`LoadBalancerConfig`** (existing) — referenced from `GatewayClass.parametersRef` (class defaults) and `Gateway.spec.infrastructure.parametersRef` (per-Gateway overrides).
- **`TargetGroupConfig`** — per-Service backend tuning.
- **`ListenerRuleConfig`** — per-HTTPRoute filter extensions.

## LoadBalancerConfig at two levels

```yaml
# Class-level defaults (cluster admin):
apiVersion: vks.vngcloud.vn/v1alpha1
kind: LoadBalancerConfig
metadata: { name: alb-defaults }
spec:
  type: Layer 7
  packageId: lbp-cluster-default
  scheme: Internal
  mergingMode: PreferGateway
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata: { name: vngcloud-alb }
spec:
  controllerName: gateway.vks.vngcloud.vn/alb
  parametersRef:
    group: vks.vngcloud.vn
    kind: LoadBalancerConfig
    name: alb-defaults
---
# Per-Gateway override (app team):
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: demo, namespace: default }
spec:
  gatewayClassName: vngcloud-alb
  infrastructure:
    parametersRef:
      group: vks.vngcloud.vn
      kind: LoadBalancerConfig
      name: demo-overrides
```

### Merging mode

Both LBCs feed into a single effective config. The class-level LBC's `mergingMode` field (only that one — the gateway-level value is ignored) chooses precedence:

| `mergingMode` | Behavior |
|---|---|
| `PreferGateway` (default) | Gateway-level wins for fields it sets non-nil; class fills in the rest. Use this for "class is a fallback." |
| `PreferGatewayClass` | Class-level wins for fields it sets non-nil; gateway fills in the rest. Use this when admins want to lock down config. |

`Listeners[]` and `Tags` merge by name/key; per-item values resolve per the same mode. `loadBalancerId` is gateway-only (a class-level value would be a singleton trap and is ignored).

## TargetGroupConfig — per-Service backend tuning

```yaml
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: TargetGroupConfig
metadata: { name: demo-tgc, namespace: default }
spec:
  targetReference:
    name: demo-service        # Service in the same namespace
  defaultConfig:
    targetType: ip             # or "instance"
    poolAlgorithm: ROUND_ROBIN
    enableStickySession: true
    enableTLSEncryption: false
    enableProxyProtocol: false
    healthCheck:
      protocol: HTTP
      successCodes: "200"
      healthCheckPath: /healthz
      intervalSeconds: 10
      timeoutSeconds: 3
      healthyThreshold: 2
      unhealthyThreshold: 3
  routeConfigurations:           # optional per-route overrides
  - routeIdentifier:
      group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: demo-route
      ruleName: canary           # optional; matches HTTPRoute.spec.rules[].name
    config:
      poolAlgorithm: LEAST_CONNECTIONS
```

**Selection cascade** (high → low specificity):
1. `routeConfigurations[]` matching the active route AND its rule name → "rule-specific."
2. `routeConfigurations[]` matching the active route, no rule name → "route-specific."
3. `defaultConfig` → fallback for any consumer.

When two TGCs target the same Service at the same specificity level, the resolver reports `Conflicted=True` on the loser; oldest creation timestamp wins for selection.

## ListenerRuleConfig — per-HTTPRoute filter extensions

Carries match types and actions vngcloud LB will eventually support natively (Header, QueryParam, Method, SourceIP, FixedResponse). Phase 1 accepts the schema as a forward-compat contract; matches whose underlying primitive is missing are dropped from the policy with a warning.

```yaml
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: ListenerRuleConfig
metadata: { name: header-internal, namespace: default }
spec:
  additionalMatches:
  - { type: Header, name: X-Source, compare: CONTAINS, value: internal }
  - { type: SourceIP, compare: STARTS_WITH, value: "10.0." }
  actions:
  - type: Reject
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: admin, namespace: default }
spec:
  parentRefs: [{ name: demo }]
  rules:
  - matches: [{ path: { type: PathPrefix, value: /admin } }]
    filters:
    - type: ExtensionRef
      extensionRef:
        group: gateway.vks.vngcloud.vn
        kind: ListenerRuleConfig
        name: header-internal
```

When vngcloud LB ships native Header/QueryParam/Method/SourceIP rule types, the controller will:
1. Promote those `additionalMatches` into actual L7 rules transparently — no manifest changes required.
2. Mark deprecated those `additionalMatches` types whose equivalents exist in core HTTPRoute matches at that point.

This forward-compat path is why the CRD lives separately from `HTTPRoute` rather than as annotations.
