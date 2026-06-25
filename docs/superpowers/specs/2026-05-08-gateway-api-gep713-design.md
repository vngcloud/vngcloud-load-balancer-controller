# Gateway API Support — Design (GEP-713 Policy Attachment)

**Date:** 2026-05-08
**Status:** Draft (supersedes 2026-04-30 AWS-LBC-style design for the v3 branch)
**Scope:** Add Kubernetes Gateway API (https://gateway-api.sigs.k8s.io) support to the vngcloud Load Balancer Controller using the **GEP-713 Policy Attachment** pattern, matching the model used by GKE / Envoy Gateway / Cilium / Istio / NGINX Gateway Fabric / Kong.

This document is a clean re-design. It does **not** reuse `LoadBalancerConfig.parametersRef`, `TargetGroupConfig.targetReference`, or HTTPRoute `filters[].extensionRef` as configuration attachment surfaces. Every vngcloud-specific knob is a Direct Policy CR with `spec.targetRefs`.

---

## Decisions summary

| # | Decision | Choice |
|---|---|---|
| 1 | Attachment pattern | **GEP-713 Direct Policy Attachment** only. No `parametersRef`, no `extensionRef`. Every CR uses `spec.targetRefs`. |
| 2 | Gateway → LB mapping | Two GatewayClasses (`vngcloud-alb`, `vngcloud-nlb`); 1 Gateway = 1 LB; mixed-protocol Gateways rejected. |
| 3 | GatewayClass-level config | **None.** GatewayClass is a controller selector only. `parametersRef` is left unused. Cluster admin defaults are not modeled in v1. |
| 4 | Policy CRDs | Four namespaced CRDs in group `gateway.vks.vngcloud.vn/v1alpha1`: `VKSGatewayPolicy`, `VKSBackendPolicy`, `VKSHealthCheckPolicy`, `VKSRoutePolicy`. |
| 5 | Conflict resolution | Direct only. One policy per `(targetRef, sectionName)` pair. Second attachment is `Conflicted=True, reason=Conflicted`; oldest-creates wins. |
| 6 | Coexistence | Strict separation; Gateway-owned LBs carry distinct owner labels; no shared LBs with Ingress / Service controllers. **BYO-LB adoption is supported** when the user explicitly sets `VKSGatewayPolicy.loadBalancerSpec.loadBalancerId` — the controller mirrors that LB's subnet/zone and the LBC controller adopts it (same path as the Ingress `load-balancer-id` annotation). The controller never *auto*-adopts. |
| 7 | Weighted backends in HTTPRoute | Synthetic merged pool with weight-scaled members (single vngcloud Pool aggregates all backends in a route rule). Implementation detail unchanged from earlier design. |
| 8 | HTTPRoute match / filter coverage | Strict reject for unsupported core features. Vngcloud-specific match/action overlay via `VKSRoutePolicy.targetRefs[].sectionName: <ruleName>`. No `extensionRef` filter. |
| 9 | TLS strategy | Secrets via `certificateRefs` (auto-imported, existing path). Optional vngcloud cert IDs via `VKSGatewayPolicy.listeners[].certificateIds[]`. mTLS via standard `frontendValidation` or policy-level `clientCertificateId`. |
| 10 | Phasing | Phase 1 = ALB + L7; Phase 2 = NLB + L4; Phase 3 = `BackendTLSPolicy` + `GRPCRoute`; Phase 4 = conformance + migration tool. |

---

## 1. Architecture overview

### 1.1 Controllers & GatewayClasses

Two new controllers added to the existing manager binary, each watching a distinct GatewayClass:

- **`vngcloud-alb` GatewayClass** — `controllerName: gateway.vks.vngcloud.vn/alb`. Provisions vngcloud ALB. Accepts listeners with protocol `HTTP`, `HTTPS`, `TLS` (mode=Terminate). Routes: `HTTPRoute`, later `GRPCRoute`.
- **`vngcloud-nlb` GatewayClass** — `controllerName: gateway.vks.vngcloud.vn/nlb`. Provisions vngcloud NLB. Accepts listeners with protocol `TCP`, `UDP`, `TLS` (mode=Passthrough). Routes: `TCPRoute`, `UDPRoute`, `TLSRoute`.

Mixed protocols on a single Gateway are rejected with `Accepted=False, reason=UnsupportedProtocol`. 1 Gateway = 1 vngcloud LoadBalancer; no sharing.

`GatewayClass.spec.parametersRef` is **left unset and ignored** by the controller — there is intentionally no GatewayClass-level configuration surface. Per-cluster defaults are out of scope for v1; users author one `VKSGatewayPolicy` per Gateway when they want non-defaults.

### 1.2 Policy CRDs (overview)

All four CRDs live in API group `gateway.vks.vngcloud.vn/v1alpha1`. All are **Direct** Policies per GEP-713: they affect only their `targetRef`, do not cascade, and use the `None` merge strategy (oldest-wins on duplicate target).

Each CRD carries the standard CRD label so tooling discovers it:

```yaml
metadata:
  labels:
    gateway.networking.k8s.io/policy: direct
```

| CRD | What it configures | Valid targetRef kinds |
|---|---|---|
| `VKSGatewayPolicy` | Frontend / listener properties on the LB: SSL policy, ALPN, allowed CIDRs, listener timeouts, vngcloud cert IDs, mTLS cert IDs, header injection. Optional `sectionName` scopes to one Gateway listener. | `Gateway` |
| `VKSBackendPolicy` | Backend pool properties: pool algorithm, sticky session, TLS encryption, PROXY protocol, target type (instance / ip), node-label filter. | `Service`, `ServiceImport` |
| `VKSHealthCheckPolicy` | Health check on a backend pool: protocol (HTTP/HTTPS/TCP), interval, timeout, thresholds, request path, expected codes. | `Service`, `ServiceImport` |
| `VKSRoutePolicy` | Per-rule overlay: alt actions (`Reject` / `Redirect`) and explicit position. Targets a route's `sectionName` (rule name). The vngcloud LB API supports only `HOST_NAME` and `PATH` policy rules, so additional match dimensions (header / queryParam / method / source-IP) are not exposed here. | `HTTPRoute` (later: `GRPCRoute`, `TCPRoute`, `UDPRoute`, `TLSRoute`) |

`VKSRoutePolicy` is a vngcloud-specific extension; GKE has no equivalent because GCP's LB doesn't expose those features. We model it the same way GCP would have if it did: a Direct policy with `targetRefs` pointing at the route, scoped by `sectionName` to a specific `HTTPRoute.spec.rules[].name`. **No HTTPRoute filter is involved.**

### 1.3 Package layout (top-level orientation)

```
api/gateway/v1alpha1/                        # new group, all four policy CRDs + shared types
internal/controller/gateway/                 # new
    alb/                                     # alb gatewayclass + gateway + httproute reconcilers
    nlb/                                     # phase 2
    policies/                                # one validation reconciler per policy CRD
    shared/                                  # status helpers, indexers, finalizers, eventhandlers
internal/usecase/gateway_uc/                 # new
    alb_gateway_uc/                          # build_lb / build_listener / build_pool / build_policy
    nlb_gateway_uc/                          # phase 2
    shared/                                  # policy resolution, refgrant, hostname matching
pkg/gateway/                                 # utils: synth pool naming, hostname → regex
charts/.../templates/                        # CRDs, GatewayClasses (gated), RBAC
```

Reconcilers stay thin (request → useCase). UseCases use the existing `K8sRepository` and `VngCloudRepository`. The Gateway controller is the only writer to the vngcloud LB; HTTPRoute and policy reconciles update reverse-indexes and enqueue the parent Gateway.

### 1.4 Manager wiring

`cmd/main.go` adds:
- Register Gateway API scheme (`sigs.k8s.io/gateway-api/apis/v1`, `v1alpha2`).
- Register `gateway.vks.vngcloud.vn/v1alpha1` scheme.
- Feature gate flags: `--enable-gateway-api-alb` (default `false` initially, `true` after stable), `--enable-gateway-api-nlb` (false until Phase 2).
- Conditional reconciler registration based on feature gates.
- New finalizer constants: `gateway.vks.vngcloud.vn/resources` (Gateway), `gateway.vks.vngcloud.vn/route` (route kinds).

### 1.5 Coexistence boundary

Gateway-controller-owned vngcloud LBs carry the owner label `vks.vngcloud.vn/owner-resource-kind=Gateway`. Existing Ingress / Service controllers ignore non-matching owners. No shared-LB code paths in v1.

### 1.6 VNGCloud LB API — supported feature surface

The CRD schemas and translation rules below are **bounded by what the vngcloud LB v2 API actually exposes**. Verified against `github.com/vngcloud/vngcloud-go-sdk/v2` request schemas (latest available SDK as of design date).

**Supported on Listener:** `protocol` (TCP/UDP/HTTP/HTTPS), `protocolPort`, `allowedCidrs` (single comma-joined string), `defaultPoolId`, `timeoutClient/Connection/Member`, `certificateAuthorities[]` (server-cert IDs), `defaultCertificateAuthority`, `clientCertificate` (mTLS CA ID), `insertHeaders[{headerName, headerValue}]`.

**Supported on Pool:** `poolName`, `poolProtocol` (TCP/UDP/HTTP/PROXY), `algorithm` (`ROUND_ROBIN`/`LEAST_CONNECTIONS`/`SOURCE_IP`), `stickiness *bool`, `tlsEncryption *bool`, `healthMonitor`, `members[{ipAddress, port, monitorPort, weight, backup}]`.

**Supported on HealthMonitor:** `healthCheckProtocol` (TCP/HTTP/HTTPS/PING-UDP), thresholds, `interval`, `timeout`, `healthCheckMethod` (GET/PUT/POST), `httpVersion` (1.0/1.1), `healthCheckPath`, `domainName`, `successCode`.

**Supported on L7 Policy:** `action` ∈ {`REJECT`, `REDIRECT_TO_URL`, `REDIRECT_TO_POOL`}. `rules[]` of `{compareType, ruleType, ruleValue}` where `compareType` ∈ {`EQUAL_TO`, `STARTS_WITH`, `ENDS_WITH`, `CONTAINS`, `REGEX`} and `ruleType` ∈ {`HOST_NAME`, `PATH`}. Plus `redirectPoolId` / `redirectUrl` / `redirectHttpCode` / `keepQueryString`.

**Confirmed not in the API (would need vngcloud upstream changes):**

| Feature | Surface | Phase 1 handling |
|---|---|---|
| SSL policy / TLS-version / cipher hardening | Listener | not exposed in `VKSGatewayPolicy` |
| ALPN policy | Listener | not exposed in `VKSGatewayPolicy` |
| Cookie-name + TTL session affinity | Pool | `VKSBackendPolicy.Stickiness` is `*bool` only |
| Custom request headers on health checks | HealthMonitor | not exposed in `VKSHealthCheckPolicy` |
| Header / queryParam / method / source-IP at policy rules | L7 Policy | HTTPRoute matches with these → route `Accepted=False, reason=UnsupportedMatch` |
| `FIXED_RESPONSE` action | L7 Policy | not exposed in `VKSRoutePolicy.Actions` |
| `instance` vs `ip` target type | Members | API stores IPs only; the distinction is which IPs the *controller* resolves (pod-IP vs node-IP+nodePort). Exposed as `VKSBackendPolicy.TargetType` (controller-side toggle, not an API field). |
| ProxyProtocol toggle on a pool | Pool | use `PoolProtocol=PROXY` instead |
| Health-check monitor port / HTTP method / HTTP version | HealthMonitor | **Now exposed** for Ingress parity via `VKSHealthCheckPolicy.port`, `.httpHealthCheck.method` (GET/PUT/POST), `.httpHealthCheck.httpVersion` (1.0/1.1). `port` overrides every member's `monitorPort`; method/version are HTTP/HTTPS-only (ignored for TCP) and default to GET / 1.1. |

**Implication for HTTPRoute conformance:** `HTTPRouteMatch` has 4 dimensions — path, headers, queryParams, method. Only `path` (plus `HTTPRoute.Spec.Hostnames`) maps to a vngcloud policy rule. Routes using header / queryParam / method matching are rejected with `UnsupportedMatch`. The supported canary mechanism is weighted `backendRefs` (handled by the synth-pool weight scaler in `pkg/gateway/synth_pool.go`), not header-based routing.

---

## 2. CRD schemas

All schemas use `LocalPolicyTargetReference` (or `LocalPolicyTargetReferenceWithSectionName` where sub-object scoping is supported), the standard types from `sigs.k8s.io/gateway-api/apis/v1alpha2`. We do not invent a custom target reference shape.

### 2.1 Common types

```go
// LocalPolicyTargetReference = re-export from sigs.k8s.io/gateway-api/apis/v1alpha2.
// LocalPolicyTargetReferenceWithSectionName = same plus an optional SectionName.

// PolicyAncestorStatus mirrors gateway-api's PolicyAncestorStatus shape.
type PolicyStatus struct {
    Ancestors []gwv1alpha2.PolicyAncestorStatus `json:"ancestors,omitempty"`
}
```

Standard status reasons used across all four CRDs:

| Reason | Meaning |
|---|---|
| `Accepted` | Policy is recognized, target exists, schema valid, no conflict. |
| `Conflicted` | Another policy of the same kind already targets `(targetRef, sectionName)`; this one is shadowed. |
| `Invalid` | Schema valid but values are nonsensical for the target (e.g. listener-only fields on a Gateway target with no `sectionName`). |
| `TargetNotFound` | The `targetRef` does not resolve. |
| `NoReadyController` | No controller of the matching `controllerName` is currently running (informational; helps debug feature-gated installs). |

### 2.2 `VKSGatewayPolicy`

Direct policy on a `Gateway`. Optional `sectionName` scopes the policy to a single listener.

```go
// +kubebuilder:resource:shortName=vksgwpolicy,categories=gateway-api
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
type VKSGatewayPolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   VKSGatewayPolicySpec   `json:"spec,omitempty"`
    Status VKSGatewayPolicyStatus `json:"status,omitempty"`
}

type VKSGatewayPolicySpec struct {
    // TargetRefs identify the Gateway (and optionally a single listener) this policy applies to.
    // Same-namespace only. Multiple targets allowed (max 16) — useful for applying identical
    // settings to a fleet of Gateways. SectionName, when set, scopes to one listener by name.
    // +kubebuilder:validation:MinItems=1
    // +kubebuilder:validation:MaxItems=16
    TargetRefs []gwv1alpha2.LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`

    // Listener-level fields. Honored only when SectionName matches a listener.
    // If SectionName is unset on the targetRef, these apply as the Gateway's listener defaults
    // (every listener on the target Gateway uses them unless a more specific policy attaches
    // to that listener via SectionName).
    AllowedCIDRs      []string           `json:"allowedCidrs,omitempty"`
    InsertHeaders     map[string]string  `json:"insertHeaders,omitempty"`
    TimeoutClient     *metav1.Duration   `json:"timeoutClient,omitempty"`
    TimeoutMember     *metav1.Duration   `json:"timeoutMember,omitempty"`
    TimeoutConnection *metav1.Duration   `json:"timeoutConnection,omitempty"`

    // Vngcloud cert ID overrides. When set, override Secret-based certificateRefs on the
    // matching listener; otherwise certificateRefs continue to be honored (existing import path).
    CertificateIDs        []string `json:"certificateIds,omitempty"`         // server cert(s)
    ClientCertificateID   *string  `json:"clientCertificateId,omitempty"`    // mTLS CA, mutually exclusive with frontendValidation

    // Gateway-level fields. Honored only when SectionName is unset (or controller derives them
    // from the unscoped policy on the Gateway).
    LoadBalancerSpec *VKSLoadBalancerSpec `json:"loadBalancerSpec,omitempty"`
}

type VKSLoadBalancerSpec struct {
    Scheme         *string           `json:"scheme,omitempty"`     // Internet | Internal | InterVPC
    PackageID      *string           `json:"packageId,omitempty"`
    SubnetID       *string           `json:"subnetId,omitempty"`
    Tags           map[string]string `json:"tags,omitempty"`
    LoadBalancerID *string           `json:"loadBalancerId,omitempty"` // BYO LB
}

type VKSGatewayPolicyStatus struct {
    PolicyStatus       `json:",inline"`
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
    ObservedGeneration int64              `json:"observedGeneration,omitempty"`
}
```

**Conflict rule.** For a given `(Gateway, sectionName)` only one `VKSGatewayPolicy` may attach. A second one is `Accepted=False, Conflicted=True`; the older one continues to apply. `Gateway`-without-`sectionName` and `Gateway`-with-`sectionName` are different conflict scopes — a Gateway may have one unscoped policy plus per-listener policies.

**Field precedence within a Gateway.** Per-listener policy (with `sectionName`) wins over unscoped policy on the same Gateway, field by field. Both are still Direct (no inherited cascade); the controller resolves the effective listener config at reconcile time.

### 2.3 `VKSBackendPolicy`

Direct policy on a `Service` or `ServiceImport`.

```go
// +kubebuilder:resource:shortName=vksbpolicy,categories=gateway-api
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
type VKSBackendPolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   VKSBackendPolicySpec   `json:"spec,omitempty"`
    Status VKSBackendPolicyStatus `json:"status,omitempty"`
}

type VKSBackendPolicySpec struct {
    // +kubebuilder:validation:MinItems=1
    // +kubebuilder:validation:MaxItems=16
    TargetRefs []gwv1alpha2.LocalPolicyTargetReference `json:"targetRefs"`

    // TargetType selects which addresses the controller resolves into pool
    // members. "ip" puts pod IPs in the pool (works on flat networks /
    // Cilium native routing). "instance" puts node IPs + nodePort in the
    // pool (works on overlay networks where pod IPs aren't routable from
    // the cloud LB). Defaults to "instance" — same default the Ingress
    // controller uses today.
    //
    // Controller-side translation toggle, not a vngcloud LB API field;
    // the resolved member IPs are what land on the cloud pool. See §1.6.
    //
    // +kubebuilder:validation:Enum=instance;ip
    TargetType *string `json:"targetType,omitempty"`

    // +kubebuilder:validation:Enum=ROUND_ROBIN;LEAST_CONNECTIONS;SOURCE_IP
    PoolAlgorithm *string `json:"poolAlgorithm,omitempty"`

    // Stickiness enables sticky sessions. The vngcloud LB API exposes only an
    // on/off flag; cookie name and TTL are not configurable.
    Stickiness *bool `json:"stickiness,omitempty"`

    EnableTLSEncryption *bool             `json:"enableTLSEncryption,omitempty"`
    TargetNodeLabels    map[string]string `json:"targetNodeLabels,omitempty"`
    ManageDFPMembers    *bool             `json:"manageDFPMembers,omitempty"`
}

type VKSBackendPolicyStatus struct {
    PolicyStatus       `json:",inline"`
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
    ObservedGeneration int64              `json:"observedGeneration,omitempty"`
}
```

**Conflict rule.** One `VKSBackendPolicy` per Service. Second attachment → `Conflicted=True`; oldest wins.

### 2.4 `VKSHealthCheckPolicy`

Split out from BackendPolicy because a health-check is often defined by a different team (platform / SRE) than the backend tuning (app team). Direct on Service or ServiceImport.

```go
// +kubebuilder:resource:shortName=vkshcpolicy,categories=gateway-api
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
type VKSHealthCheckPolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   VKSHealthCheckPolicySpec   `json:"spec,omitempty"`
    Status VKSHealthCheckPolicyStatus `json:"status,omitempty"`
}

type VKSHealthCheckPolicySpec struct {
    // +kubebuilder:validation:MinItems=1
    // +kubebuilder:validation:MaxItems=16
    TargetRefs []gwv1alpha2.LocalPolicyTargetReference `json:"targetRefs"`

    // +kubebuilder:validation:Enum=HTTP;HTTPS;TCP
    Protocol           string             `json:"protocol"`
    Interval           *metav1.Duration   `json:"interval,omitempty"`
    Timeout            *metav1.Duration   `json:"timeout,omitempty"`
    HealthyThreshold   *int32             `json:"healthyThreshold,omitempty"`
    UnhealthyThreshold *int32             `json:"unhealthyThreshold,omitempty"`

    HTTPHealthCheck *VKSHTTPHealthCheck `json:"httpHealthCheck,omitempty"`
}

type VKSHTTPHealthCheck struct {
    Path          *string  `json:"path,omitempty"`
    Host          *string  `json:"host,omitempty"`
    ExpectedCodes []string `json:"expectedCodes,omitempty"`   // e.g. ["200-299","301"]
}
```

**Conflict rule.** One `VKSHealthCheckPolicy` per Service. Second attachment → `Conflicted=True`; oldest wins.

### 2.5 `VKSRoutePolicy`

Direct policy on an `HTTPRoute` (and later other route kinds). `sectionName` scopes to a single rule by `HTTPRoute.spec.rules[].name`. Multiple `VKSRoutePolicy` objects may attach to the same route as long as they target distinct `(targetRef, sectionName)` pairs.

```go
// +kubebuilder:resource:shortName=vksroutepolicy,categories=gateway-api
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
type VKSRoutePolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   VKSRoutePolicySpec   `json:"spec,omitempty"`
    Status VKSRoutePolicyStatus `json:"status,omitempty"`
}

type VKSRoutePolicySpec struct {
    // +kubebuilder:validation:MinItems=1
    // +kubebuilder:validation:MaxItems=16
    TargetRefs []gwv1alpha2.LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`

    // Actions, when set, supersede the rule's default REDIRECT_TO_POOL action.
    // The vngcloud LB API supports REJECT and REDIRECT_TO_URL only.
    // +optional
    Actions []VKSRuleAction `json:"actions,omitempty"`

    // Position pins policy ordering on the listener. Default = controller-assigned
    // (Gateway-API spec match-specificity ordering).
    // +optional
    Position *int32 `json:"position,omitempty"`
}

type VKSRuleAction struct {
    // +kubebuilder:validation:Enum=Reject;Redirect
    Type     string             `json:"type"`
    Redirect *VKSRedirectAction `json:"redirect,omitempty"`
}

type VKSRedirectAction struct {
    URL             string `json:"url"`
    HTTPCode        *int32 `json:"httpCode,omitempty"`
    KeepQueryString *bool  `json:"keepQueryString,omitempty"`
}
```

**Conflict rule.** Two `VKSRoutePolicy` objects targeting the same `(HTTPRoute, sectionName)` conflict — second is `Conflicted=True`. Different `sectionName` values do not conflict. A policy with no `sectionName` applies to **every** rule in the route and conflicts with any policy that scopes to a specific rule on the same route.

**Required route schema:** users must give every targeted rule a `name` in `HTTPRoute.spec.rules[].name`. The controller writes `Accepted=False, reason=TargetNotFound` if a `sectionName` doesn't match any rule.

**Forward-compat note.** When vngcloud LB later adds native header / query / method matching, the controller will start honoring those dimensions of `HTTPRoute.matches` instead of rejecting routes with `UnsupportedMatch`. No breakage.

### 2.6 RBAC additions

```
+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses,verbs=get;list;watch
+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses/status,verbs=update;patch
+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;update;patch
+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/status,verbs=update;patch
+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/finalizers,verbs=update
+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes;referencegrants,verbs=get;list;watch
+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/status,verbs=update;patch
+kubebuilder:rbac:groups=gateway.vks.vngcloud.vn,resources=vksgatewaypolicies;vksbackendpolicies;vkshealthcheckpolicies;vksroutepolicies,verbs=get;list;watch;update;patch
+kubebuilder:rbac:groups=gateway.vks.vngcloud.vn,resources=vksgatewaypolicies/status;vksbackendpolicies/status;vkshealthcheckpolicies/status;vksroutepolicies/status,verbs=update;patch
```

(Phase 2 adds `tcproutes`, `udproutes`, `tlsroutes`; Phase 3 adds `grpcroutes`, `backendtlspolicies`.)

---

## 3. Resource mapping & data flow

### 3.1 Mapping table — Gateway API + VKS Policies → vngcloud primitives

| Gateway API resource | vngcloud entity | Notes |
|---|---|---|
| `GatewayClass` (`gateway.vks.vngcloud.vn/alb`) | none (declarative gate) | `Accepted=True` once controller is registered. No `parametersRef` consumed. |
| `GatewayClass` (`gateway.vks.vngcloud.vn/nlb`) | none | same; Phase 2 |
| `Gateway` | 1 `LoadBalancer` (ALB or NLB) + N `Listener` | LB type fixed by GatewayClass; 1:1 binding via owner labels; LB-level config from a `VKSGatewayPolicy` with no `sectionName` |
| `Gateway.spec.listeners[]` | vngcloud `Listener` | TLS / port / protocol from spec; per-listener overrides from `VKSGatewayPolicy` with `sectionName: <listener-name>` |
| `Gateway.spec.addresses` | informational only | vngcloud assigns address; user-requested IP rejected unless feature exists |
| `HTTPRoute` (per parentRef listener) | 1+ `Policy` + 1+ `Pool` (synthetic) | One policy per `rule × match` (cartesian when multi-match) |
| `HTTPRoute.hostnames[]` | `HOST_NAME` rule on each generated policy | `*.foo.com` → `REGEX` rule |
| `HTTPRoute.rule.matches[].path` | `PATH` rule (EQUAL_TO / STARTS_WITH / REGEX) | Implementation-specific path types fall back to EQUAL_TO |
| `HTTPRoute.rule.backendRefs[]` (weighted) | 1 synthetic `Pool` (members from union) | Weight-scaled members; pool name = stable hash |
| `HTTPRoute.rule.filters[RequestRedirect]` | `Policy.Action = REDIRECT_TO_URL` | URL templated from filter fields |
| `HTTPRoute.rule.filters[RequestHeaderModifier]` (set) | listener `insertHeaders` | Only if uniform across rules; otherwise reject |
| `ReferenceGrant` | gating only | Resolves cross-namespace `backendRefs` and TLS Secret refs |
| `VKSGatewayPolicy` (`targetRefs.kind=Gateway`) | LB + listener config | Direct; per-listener via `sectionName` |
| `VKSBackendPolicy` (`targetRefs.kind=Service`) | Pool config | Direct; one per Service |
| `VKSHealthCheckPolicy` (`targetRefs.kind=Service`) | Pool health check | Direct; one per Service |
| `VKSRoutePolicy` (`targetRefs.kind=HTTPRoute`) | Alt policy action (Reject / Redirect) + position override | Direct; per-rule via `sectionName`. No additional-match dimensions — vngcloud rules support only `HOST_NAME` and `PATH`. |

### 3.2 Controller graph

```
┌──────────────────────────┐
│ GatewayClassController   │  (registration + status only)
└──────────────────────────┘

┌──────────────────────────┐    parentRef
│ GatewayController        │◄─────────── HTTPRoute
│ (per GatewayClass)       │
└────────────┬─────────────┘
             │ owns
             ▼
   vngcloud LoadBalancer + Listeners + Policies + Pools

┌──────────────────────────┐  resolves at reconcile
│ Policy resolver (in UC)  │──── reads ─► VKSGatewayPolicy  (by Gateway ref)
└──────────────────────────┘──── reads ─► VKSBackendPolicy  (by Service ref)
                                ──── reads ─► VKSHealthCheckPolicy (by Service ref)
                                ──── reads ─► VKSRoutePolicy (by Route ref + sectionName)

┌──────────────────────────┐
│ Policy validators        │──── enqueue ─► Gateway / HTTPRoute
│ (one per policy CRD,     │           (the targets they affect)
│  status-only writers)    │
└──────────────────────────┘
```

**Ownership model.** The Gateway controller is the only writer to the vngcloud LB. HTTPRoute and policy reconciles do not touch vngcloud directly — they resolve their indexed targets and enqueue the owning Gateway. This serializes all LB mutations through one reconciler per Gateway and avoids races.

**Policy validators** run in their own controllers but write status only. They never write to the LB. They enqueue affected Gateways so a real reconcile applies the change.

### 3.3 Gateway → LoadBalancer + Listeners

Reconcile order per Gateway:

1. **Resolve effective Gateway-level config.** Lookup all `VKSGatewayPolicy` whose `targetRefs[].kind=Gateway` matches this Gateway and whose `sectionName` is unset; pick the oldest (`Accepted=True` for it, others `Conflicted=True`). Use its `LoadBalancerSpec` and listener-default fields.
2. **Validate listeners.** Protocol allowed by GatewayClass type? Port unique? Hostname valid? Set per-listener `Accepted` condition.
3. **Resolve effective per-listener config.** For each listener: lookup `VKSGatewayPolicy` whose `targetRefs[].sectionName` matches the listener's name; pick oldest. Fall back to the unscoped Gateway-level policy's listener-default fields.
4. **Lookup or create LB.** By owner label `gateway.vks.vngcloud.vn/owner-uid=<gateway-uid>`. Type = ALB or NLB. Apply LB-level config from step 1.
5. **Compute desired Listener set.** For each `Gateway.spec.listeners[]` with `Accepted=True`:
   - protocol / port from Gateway listener
   - TLS: prefer `VKSGatewayPolicy.CertificateIDs` if present, else import Secrets from `tls.certificateRefs` (existing import path)
   - mTLS: `VKSGatewayPolicy.ClientCertificateID` if set, else import from `tls.frontendValidation.caCertificateRefs`
   - other fields (timeouts, allowedCidrs, insertHeaders) from the resolved listener policy
   - default pool: synthetic "default-forwarding-pool" with no members (fed by route layer)
6. **Diff against vngcloud.** Reuse existing per-listener helpers in `internal/usecase/lbc_uc/deploy_listener.go`.
7. **Trigger route layer.** Emit policy / pool sync for every accepted route attached to this Gateway.
8. **Write status.** Gateway and listener conditions per §4.

### 3.4 HTTPRoute → Policies + Pools

For each HTTPRoute attached to an accepted Gateway listener:

```
for each rule in route.spec.rules:
    backendPolicy = resolveBackendPolicy(rule.backendRefs[0..n])
    healthCheck   = resolveHealthCheckPolicy(rule.backendRefs[0..n])
    pool = synthesizePool(rule.backendRefs, backendPolicy, healthCheck, route, ruleIdx)
    routePolicies = lookupRoutePolicies(route, rule.name)   // 0..n with same sectionName
    for each match in rule.matches:
        for each hostname in route.hostnames (or [""] if empty):
            policy = buildPolicy(hostname, match, rule.filters, routePolicies, pool)
            policies.append(policy)
```

**One policy per (hostname × match × resolved-action).** L7 rules — only the dimensions the vngcloud API supports:

- `HOST_NAME` per hostname (literal → `EQUAL_TO`; wildcard → `REGEX`)
- `PATH` per `match.path`
- Action precedence:
  - `VKSRoutePolicy.Actions[Reject|Redirect]` → respective vngcloud action (`REJECT` / `REDIRECT_TO_URL`)
  - `RequestRedirect` filter → `REDIRECT_TO_URL`
  - default → `REDIRECT_TO_POOL` pointing at the synthetic pool

**Match-dimension rejection.** If any `HTTPRouteMatch` in the route uses `headers`, `queryParams`, or `method`, the route is rejected with `Accepted=False, reason=UnsupportedMatch` per parent — these dimensions don't have a vngcloud rule equivalent, and silently dropping them produces traffic surprises. Path + hostname matching alone is honored; weighted `backendRefs` are the supported canary mechanism.

**Policy ordering.** Gateway API spec: most-specific path first, then header count, then route creation timestamp. `VKSRoutePolicy.Position` overrides this order if set. Existing `auto-reorder-policies` machinery is reused.

**Cross-namespace backendRefs.** Gated by ReferenceGrant. If the grant is absent, set `ResolvedRefs=False, reason=RefNotPermitted` on the route and drop just that backend from the synthesized pool (don't fail the whole rule).

### 3.5 Synthetic pool — naming & weight scaling

Same algorithm as the prior design — this is implementation, not attachment.

**Name** (deterministic, ≤ 50 chars):

```
gw_<route-uid-prefix8>_<rule-idx>_<backendset-hash5>
```

**Weight scaling.** Floor 1; cap product at 100; `member_weight = round(w_b * scale / n_b)` with `scale = lcm(n_b) / gcd(weights)`.

**Health check resolution** for the synthetic pool, single config picked in priority order:

1. `VKSHealthCheckPolicy` attached to **any** of the rule's backend Services. If multiple rules' backends carry conflicting policies → `ResolvedRefs=False, reason=BackendConfigMismatch` on the route.
2. Controller fallback (TCP check on backend port).

Conflict detection runs at the route level — if backend Services A and B carry different `VKSHealthCheckPolicy` configs, the route fails closed instead of silently picking one.

### 3.6 Policy attachment resolver — pseudocode

```go
// resolveDirectPolicy returns the oldest accepted policy whose targetRefs match (kind, name, sectionName).
// Other matches get Accepted=False, Conflicted=True.
func resolveDirectPolicy[P PolicyObj](policies []P, target ref) (winner *P, losers []*P) {
    matches := filter(policies, p => p.matchesTarget(target))
    if len(matches) == 0 { return nil, nil }
    sort.SliceStable(matches, byCreationTimeThenName)   // oldest, then ns/name lex
    return matches[0], matches[1:]
}
```

Status updates for losers happen in the policy validator controllers, not in the Gateway use-case (which only consumes the resolved view).

### 3.7 Watch & reverse-index map

| Watch source | Triggers requeue of |
|---|---|
| `GatewayClass` | Self |
| `Gateway` | Self + all routes whose `parentRefs` resolve to it |
| `HTTPRoute` | Self + parent Gateway |
| `Service` | All HTTPRoutes whose `backendRefs` include it + parent Gateway |
| `EndpointSlice` | Same as Service |
| `Secret` (TLS) | All Gateways whose `certificateRefs` / `frontendValidation` include it |
| `ReferenceGrant` | All routes / Gateways whose cross-ns refs were previously denied |
| `VKSGatewayPolicy` | All Gateways listed in `targetRefs[].name` |
| `VKSBackendPolicy` | All HTTPRoutes whose `backendRefs` include any `targetRefs[].name` Service + their parent Gateways |
| `VKSHealthCheckPolicy` | Same as `VKSBackendPolicy` |
| `VKSRoutePolicy` | All HTTPRoutes named in `targetRefs[].name` + their parent Gateways |

Indexes via controller-runtime `Manager.GetFieldIndexer()` and `pkg/gateway/reference_indexer.go`.

### 3.8 Eventual-consistency notes

- A change to one HTTPRoute or any policy CR requeues only its parent Gateway → Gateway reconcile rebuilds the full policy / pool set for that LB. Idempotent diff drives create / update / delete.
- Per-Gateway reconciles serialized by controller-runtime work queues.
- Status updates batched at end of reconcile (one PATCH per object) to minimize API churn.
- Long-running LB ops (provisioning, listener create) reuse existing requeue-with-backoff machinery from `lbc_uc`.

---

## 4. Status conditions, events, metrics

> **Implementation status (Phase 1): all of §4.1 below is implemented.**
> - `GatewayClass` + `Gateway` (Accepted/Programmed/addresses/listeners) — `internal/controller/gateway/alb`.
> - `HTTPRoute.status.parents[]` (Accepted / ResolvedRefs / PartiallyInvalid) — written each Gateway reconcile by `writeRouteStatuses` in `internal/usecase/gateway_uc/alb_gateway_uc/route_status.go`; idempotent, owns only this controller's parent entry.
> - The four policy CRDs' status (Accepted / Conflicted / TargetNotFound + `PolicyAncestorStatus`) — `internal/controller/gateway/policies` (one generic Direct-policy validator instantiated per CRD; status-only, never writes the LB).
>
> Earlier "Phase F" / "follow-up" markers in code comments referring to route/policy status are superseded by this.

### 4.1 Status conditions

#### `GatewayClass.status.conditions`

| Type | True | False | Reason |
|---|---|---|---|
| `Accepted` | controller registered for this className | controller missing or feature-gated off | `Accepted` / `Pending` / `Disabled` |
| `SupportedVersion` | controller version supports the Gateway API version | version skew | `SupportedVersion` / `UnsupportedVersion` |

#### `Gateway.status.conditions`

| Type | True | False | Reason |
|---|---|---|---|
| `Accepted` | spec valid; this controller owns the class | invalid spec / unsupported feature / class not accepted | `Accepted` / `InvalidParameters` / `NotReconciled` / `UnsupportedAddress` |
| `Programmed` | vngcloud LB created, listeners synced, addresses populated | LB provisioning failed; listener creation failed | `Programmed` / `Pending` / `Invalid` / `NoResources` / `VngcloudAPIError` |

#### `Gateway.status.listeners[].conditions`

| Type | True | False | Reason |
|---|---|---|---|
| `Accepted` | protocol allowed for this GatewayClass; port unique within Gateway | mixed protocols / dup port / unsupported feature | `Accepted` / `UnsupportedProtocol` / `PortUnavailable` / `Invalid` |
| `Programmed` | listener exists on vngcloud LB | not yet created / failed | `Programmed` / `Pending` / `Invalid` |
| `ResolvedRefs` | all `certificateRefs` and `frontendValidation.caCertificateRefs` resolved | grant missing / Secret not found / unsupported group | `ResolvedRefs` / `RefNotPermitted` / `InvalidCertificateRef` / `InvalidCACertificateRef` |
| `Conflicted` | always `False` in v1 (no shared LBs) | n/a | `NoConflicts` |

`Gateway.status.listeners[].attachedRoutes` recomputed each Gateway reconcile.

`Gateway.status.addresses[]` populated from vngcloud LB once `Programmed=True`.

#### `HTTPRoute.status.parents[].conditions` (per parentRef)

| Type | True | False | Reason |
|---|---|---|---|
| `Accepted` | parent Gateway exists, allows route kind, namespace allowed | listener restricts kind / ns / hostname | `Accepted` / `NotAllowedByListeners` / `NoMatchingListenerHostname` / `NoMatchingParent` |
| `ResolvedRefs` | all `backendRefs` resolved | backend missing / wrong kind / cross-ns denied | `ResolvedRefs` / `BackendNotFound` / `RefNotPermitted` / `InvalidKind` |
| `PartiallyInvalid` (when at least one rule programmed but some dropped) | per spec | n/a | `UnsupportedValue` |

#### Policy CR status — common to all four CRDs

| Type | True | False | Reason |
|---|---|---|---|
| `Accepted` | target found, schema valid, no conflict | conflict / target missing / invalid for context | `Accepted` / `Conflicted` / `Invalid` / `TargetNotFound` |
| `Programmed` | target Gateway has been reconciled including this policy | pending Gateway reconcile / Gateway controller absent | `Programmed` / `Pending` / `NoReadyController` |

`PolicyAncestorStatus` (one entry per `controllerName` that handled this policy) — populated on every policy CR. Allows future GLB / cross-controller scenarios without schema change.

### 4.2 Kubernetes Events

| Reconciler | Event reason examples |
|---|---|
| GatewayClass | `Accepted`, `Pending`, `Disabled` |
| Gateway | `LoadBalancerCreated`, `ListenerCreated`, `LoadBalancerProvisioningFailed`, `PolicyConflict` |
| HTTPRoute | `RouteAttached`, `RouteDetached`, `BackendDropped`, `UnsupportedFilter`, `BackendConfigMismatch` |
| VKSGatewayPolicy | `Conflicted`, `TargetNotFound`, `Applied` |
| VKSBackendPolicy | `Conflicted`, `TargetNotFound`, `Applied` |
| VKSHealthCheckPolicy | `Conflicted`, `TargetNotFound`, `Applied` |
| VKSRoutePolicy | `Conflicted`, `TargetNotFound`, `RuleNotFound`, `Applied` |

### 4.3 Prometheus metrics

```
# counters
gateway_api_reconcile_total{controller,result}
gateway_api_resource_count{kind,namespace,gateway_class}
gateway_api_listener_count{gateway_class,protocol}
gateway_api_route_attached_total{gateway_class}
gateway_api_unsupported_feature_total{kind,feature}
gateway_api_refgrant_denied_total{namespace,kind}
gateway_api_policy_conflict_total{kind}

# histograms
gateway_api_reconcile_duration_seconds{controller,result}
gateway_api_lb_provisioning_seconds{gateway_class}

# gauges
gateway_api_lb_total{gateway_class,scheme}
gateway_api_synthetic_pool_total
gateway_api_policy_attached_total{kind,target_kind}
```

### 4.4 Logging

Reuses existing `contexts` package. Log-name conventions: `gw/<ns>/<name>`, `httproute/<ns>/<name>`, `gwc/<name>`, `vksgwp/<ns>/<name>`, `vksbp/<ns>/<name>`, `vkshcp/<ns>/<name>`, `vksrp/<ns>/<name>`.

### 4.5 Finalizer strategy

| Resource | Finalizer | Cleanup action |
|---|---|---|
| `Gateway` | `gateway.vks.vngcloud.vn/resources` | Delete vngcloud LB, drop owner labels, deindex routes |
| `HTTPRoute` | `gateway.vks.vngcloud.vn/route` | Trigger parent Gateway reconcile |
| `VKS*Policy` | none | Pure config; deletion = recompute downstream Gateway |

Deletion ordering: HTTPRoutes detach → parent Gateway reconciles → finalizer removed when policies / pools gone. Gateway finalizer removed last when LB cleanup completes.

### 4.6 Readiness / liveness

No probe-endpoint changes. New reconcilers respect the `initDone` atomic-bool gate pattern (1s requeue while uninitialized). Fail-open on probe.

---

## 5. Implementation plan & file layout

New files marked **[new]**, modified existing files marked **[mod]**.

```
api/
└── gateway/v1alpha1/                                [new group]
    ├── groupversion_info.go                         [new]
    ├── vksgatewaypolicy_types.go                    [new]
    ├── vksbackendpolicy_types.go                    [new]
    ├── vkshealthcheckpolicy_types.go                [new]
    ├── vksroutepolicy_types.go                      [new]
    ├── shared_types.go                              [new] — re-exports of LocalPolicyTargetReference, status helpers
    └── zz_generated.deepcopy.go                     [new generated]

cmd/
└── main.go                                          [mod] register Gateway API + vks scheme,
                                                          feature gates, new reconcilers

internal/controller/
└── gateway/                                         [new]
    ├── shared/                                      [new]
    │   ├── classifier.go                            protocol → GatewayClass type, listener validation
    │   ├── status.go                                condition helpers (Direct + Ancestor)
    │   ├── reference_indexer.go                     reverse indexes (Service, Secret, RG, all 4 policies)
    │   ├── finalizer.go
    │   ├── policy_resolver.go                       Direct policy resolution (oldest-wins)
    │   ├── policy_order.go                          Gateway API match-specificity ordering
    │   └── eventhandlers/                           cross-resource enqueue helpers
    ├── alb/                                         [new — Phase 1]
    │   ├── gatewayclass_controller.go
    │   ├── gateway_controller.go
    │   ├── httproute_controller.go
    │   ├── *_test.go
    │   └── suite_test.go
    ├── nlb/                                         [new — Phase 2]
    │   └── ...
    └── policies/                                    [new — Phase 1, validation only]
        ├── vksgatewaypolicy_controller.go
        ├── vksbackendpolicy_controller.go
        ├── vkshealthcheckpolicy_controller.go
        ├── vksroutepolicy_controller.go
        └── *_test.go

internal/usecase/
├── contracts.go                                     [mod] add GatewayClassUseCase, GatewayUseCase, RouteUseCase
├── mocks.go                                         [mod] regenerated
├── gateway_uc/                                      [new]
│   ├── shared/                                      [new]
│   │   ├── policy_resolver.go                       cascading lookups for the 4 policy kinds
│   │   ├── refgrant.go                              ReferenceGrant evaluation
│   │   └── *_test.go
│   ├── alb_gateway_uc/                              [new — Phase 1]
│   │   ├── gateway_uc.go                            Init / Ensure / Delete
│   │   ├── build_lb.go                              Gateway → vngcloud LB params (uses VKSGatewayPolicy)
│   │   ├── build_listener.go                        listener policy resolution + listener build
│   │   ├── build_pool.go                            synthetic pool (uses VKSBackendPolicy + VKSHealthCheckPolicy)
│   │   ├── build_policy.go                          HTTPRoute rule → vngcloud Policy (uses VKSRoutePolicy)
│   │   ├── build_cert.go                            Secret import or cert-ID
│   │   ├── build_sec_group.go                       inherits NSG behavior from Ingress
│   │   ├── status.go
│   │   └── *_test.go
│   └── nlb_gateway_uc/                              [new — Phase 2]

internal/repository/
├── k8s_repo/                                        unchanged in v1
├── vngcloud_repo/                                   unchanged
└── mocks.go                                         [mod] regenerated

internal/domain/
└── domain.go                                        [mod] add GatewayFinalizer, owner-kind constants

pkg/
├── consts/consts.go                                 [mod] new GatewayClass controller-name constants
├── metrics/util/reconcile_counters.go               [mod] add Gateway / HTTPRoute / Policy counters
└── gateway/                                         [new]
    ├── gatewayapi_utils.go                          hostname matchers, wildcard → regex, proto helpers
    ├── synth_pool.go                                pool-name hashing helpers
    └── *_test.go

charts/vngcloud-load-balancer-controller/
├── templates/
│   ├── crds/                                        [mod] add the 4 policy CRDs
│   ├── gatewayclass-alb.yaml                        [new — Phase 1]
│   ├── gatewayclass-nlb.yaml                        [new — Phase 2]
│   ├── rbac/                                        [mod]
│   └── manager-deployment.yaml                      [mod] feature-gate flags
└── values.yaml                                      [mod] gatewayApi.{alb,nlb}.enabled toggles

config/                                              [mod throughout — kubebuilder-managed]

docs/                                                [mod throughout]
├── guide/
│   ├── gateway-api.md                               [new]
│   ├── gateway-alb.md                               [new — Phase 1]
│   ├── gateway-nlb.md                               [new — Phase 2]
│   └── gateway-policies.md                          [new — Phase 1] — the 4 policy CRDs explained
└── examples/                                        [new]

test/
├── e2e/gateway/                                     [new]
└── conformance/                                     [new — Phase 4]

go.mod                                               [mod] add sigs.k8s.io/gateway-api
PROJECT                                              [mod] register new resources via kubebuilder
```

### 5.1 New Go module dependencies

```go
require (
    sigs.k8s.io/gateway-api v1.2.0   // Standard channel covering HTTPRoute / GRPCRoute / RG /
                                     // PolicyAncestorStatus types
)
```

### 5.2 Manager wiring (`cmd/main.go` deltas)

```go
import (
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
    gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
    vksgwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    albgw "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/alb"
    policiesctrl "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/policies"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/alb_gateway_uc"
)

utilruntime.Must(gwv1.Install(scheme))
utilruntime.Must(gwv1alpha2.Install(scheme))
utilruntime.Must(vksgwv1alpha1.AddToScheme(scheme))

flag.BoolVar(&cfg.Gateway.ALBEnabled, "enable-gateway-api-alb", false, "Enable the ALB Gateway API controller (Phase 1).")
flag.BoolVar(&cfg.Gateway.NLBEnabled, "enable-gateway-api-nlb", false, "Enable the NLB Gateway API controller (Phase 2+).")

if cfg.Gateway.ALBEnabled {
    albUC := alb_gateway_uc.NewALBGatewayUseCase(...)
    if err := albgw.NewGatewayClassReconciler(...).SetupWithManager(mgr); err != nil { ... }
    if err := albgw.NewGatewayReconciler(albUC, ...).SetupWithManager(mgr); err != nil { ... }
    if err := albgw.NewHTTPRouteReconciler(albUC, ...).SetupWithManager(mgr); err != nil { ... }

    // Policy validators always run when any gateway controller is on.
    for _, r := range policiesctrl.AllReconcilers(...) {
        if err := r.SetupWithManager(mgr); err != nil { ... }
    }
}
```

### 5.3 Mocking & test strategy

**Unit tests** — table-driven, gomock-generated mocks for `K8sRepository` / `VngCloudRepository`:
- `policy_resolver_test.go` — oldest-wins semantics, sectionName scoping, conflict status emission.
- `build_listener_test.go` — listener-policy precedence, cert ID vs Secret precedence, mTLS resolution.
- `build_pool_test.go` — synthetic-pool naming determinism, weight scaling, RG-denied-backend dropping, conflicting health-check detection.
- `build_policy_test.go` — match cartesian, position ordering, VKSRoutePolicy additional-match composition, action override precedence.
- `refgrant_test.go` — allow / deny matrix; covers Secrets, Services, custom CRDs.

**Suite tests** (envtest) — one per reconciler, mirroring `internal/controller/networking/suite_test.go`. Validates reconcile-on-event for each watch.

**E2E tests** — under `test/e2e/gateway/`, run against a real vngcloud LB. Gated by env var.

**Conformance** — `test/conformance/conformance_test.go` runs the upstream Gateway API conformance suite. Phase 4.

### 5.4 Codegen / make targets

Existing `make generate manifests test` covers all generation. Same `gateway-conformance` target as the prior design.

### 5.5 Backward-compat / rollout safety

- **No new fields on `LoadBalancerConfig` for vngcloud features that don't exist** (e.g. `SSLPolicy`, `ALPNPolicy`, cookie-name session affinity). Phase 1 rolls out the Gateway translator on the LBC schema as it stands today. If the underlying vngcloud LB API later gains those features, the LBC schema and Gateway translator extend together.
- **Feature gates default-off initially** — opt-in upgrade. ALB flips to `true` after one minor cycle in production.
- **CRD bundle versioning** — Helm chart's `Chart.yaml` minor-version bumped per phase. Policy CRDs ship at `v1alpha1` only initially; promotion to `v1beta1`/`v1` deferred until conformance (Phase 4).
- **Webhook conversion** — none required in v1alpha1.

---

## 6. Phasing roadmap

### Phase 1 — L7 MVP

**Scope**

- `vngcloud-alb` GatewayClass + reconciler.
- `Gateway` reconciler (ALB only): provisions LB; listeners HTTP / HTTPS / TLS-Terminate.
- `HTTPRoute` reconciler: hostnames, path matches (Exact / PathPrefix / RegularExpression), weighted backendRefs (synthetic merged pool), `RequestRedirect` filter, listener-uniform `RequestHeaderModifier(set)` filter.
- All four `VKS*Policy` CRDs + their validation reconcilers (Direct, oldest-wins).
- `ReferenceGrant` honored for backendRefs and TLS Secret refs.
- TLS via Secret import + cert IDs via `VKSGatewayPolicy.CertificateIDs`.
- mTLS via `frontendValidation.caCertificateRefs` + `VKSGatewayPolicy.ClientCertificateID`.
- Status conditions per §4 (including `PolicyAncestorStatus`).
- Events + new Prometheus metrics.
- Helm chart updates: opt-in via `gatewayApi.alb.enabled`.

**Deliverables**

- All [new] / [mod] files in §5 marked Phase 1.
- Unit + suite test coverage parity with existing Ingress controller (~70% line).
- 6 e2e tests: basic HTTP, HTTPS+SNI, mTLS, weighted canary, cross-namespace via RG, per-rule VKSRoutePolicy overlay.
- 4 user docs (`gateway-api.md`, `gateway-alb.md`, `gateway-policies.md`, `examples/gateway-canary.md`).

**Success criteria**

- A user can install the chart with `gatewayApi.alb.enabled=true`, apply a sample manifest plus 1 of each policy kind, see `Gateway.status.addresses` populated, and curl through the LB.
- Each policy CR's `status.conditions[Accepted]` correctly reports `True` / `Conflicted=True` / `TargetNotFound`.
- Deleting a Gateway cleans up the vngcloud LB; deleting a single HTTPRoute removes its policies / pools without disturbing the LB or sibling routes; deleting a VKSBackendPolicy reverts the pool to controller defaults.
- Existing controllers behave identically before and after upgrade.
- `make test` passes; `make e2e-gateway` passes against a real cluster.

### Phase 2 — L4

**Scope**

- `vngcloud-nlb` GatewayClass + reconciler.
- `Gateway` reconciler (NLB): TCP / UDP listeners, TLS-Passthrough listeners.
- `TCPRoute`, `UDPRoute`, `TLSRoute` reconcilers.
- `VKSRoutePolicy` extended to target L4 route kinds.
- Reuses NLB build paths from existing `service_uc`.
- Helm chart: `gatewayApi.nlb.enabled`.

**Deliverables**

- All [new]/[mod] files in §5 marked Phase 2.
- 4 e2e tests (TCP echo, UDP DNS, TLS-Passthrough SNI routing, mixed-RouteKinds attachment).
- 2 user docs (`gateway-nlb.md`, `examples/gateway-tls-passthrough.md`).

**Success criteria**

- A `Gateway` with TCP/UDP listener provisions an NLB and routes traffic to the referenced backend.
- TLSRoute SNI selection works for ≥ 2 hostnames on one listener.
- Mixed L4+L7 listeners on a single Gateway are explicitly rejected.

### Phase 3 — Standard policies & advanced routes

**Scope**

- `BackendTLSPolicy` (Standard channel since Gateway API v1.2): wires to existing pool `EnableTLSEncryption` and a new TLS verification context. Honored alongside `VKSBackendPolicy.EnableTLSEncryption` (BackendTLSPolicy wins if both set; emit `Deprecated` on the VKS field).
- `GRPCRoute` reconciler (depends on vngcloud HTTP/2 + gRPC support; if absent, ship as Unsupported with documented reason).
- `VKSRoutePolicy` updated to target `GRPCRoute`.
- Optional: `URLRewrite`, `RequestMirror`, `ResponseHeaderModifier` if vngcloud LB ships native support; otherwise stay `Unsupported`.
- Validation webhook for the four policy CRDs (catches `(targetRef, sectionName)` schema misuse at apply time).

**Deliverables**

- New reconcilers + tests for whichever items are in scope.
- Updates to `gateway-policies.md`.

**Success criteria**

- For each implemented Phase-3 feature: e2e test green, conformance suite picks up the new capability flag.

### Phase 4 — Migration tool, conformance, polish

**Scope**

- One-shot migration: `gateway.vks.vngcloud.vn/adopt-from-ingress: <namespace>/<name>` annotation. Controller verifies the named Ingress is in the same namespace, transfers ownership labels on the existing vngcloud LB, marks the Ingress with a "migrated" finalizer, and reports `Programmed=True` against the old LB.
- Upstream Gateway API conformance suite (`test/conformance/`) green for: `Gateway`, `HTTPRoute` (Core), `ReferenceGrant`, `Mesh:false`. Documented capability matrix.
- CRD promotion path: `v1alpha1` → `v1beta1` (no schema changes if possible).
- Stable feature gates (default-on); doc audit.

**Deliverables**

- Migration controller code + `gateway-migration.md` runbook.
- Conformance test job in CI (`make gateway-conformance`).
- `docs/guide/gateway-conformance-matrix.md` listing every Gateway API feature with `Supported / Partial / Unsupported (reason)`.

**Success criteria**

- One existing Ingress can be cut over to a Gateway without downtime or DNS change (verified e2e).
- Conformance tests pass for every feature claimed `Supported` in the capability matrix.

---

## 7. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| **Discoverability** ("spooky action at a distance" — user reads HTTPRoute, policy effects come from elsewhere) | High | Med | Standard `gateway.networking.k8s.io/policy: direct` label on every CRD; per-policy `kubectl describe` shows `Accepted / Conflicted` reasons; `gateway_api_policy_attached_total` metric; Phase 4 docs runbook for "how do I see what's affecting my Gateway / Service / Route". |
| **Policy conflicts surprise users** (oldest-wins is non-obvious) | Med | Med | `Conflicted=True` on the loser plus a Warning event with the winner's name/UID. Metric `gateway_api_policy_conflict_total{kind}`. Document explicitly in `gateway-policies.md`. |
| **vngcloud LB API rate limits** under heavy reconciles | Med | High | Per-Gateway serialized reconcile; coalesce policy diff into one batched listener-update; existing backoff machinery in `lbc_uc`. |
| **Synthetic pool weight scaling explodes integers** | Med | Med | Cap product at 100; round with bias toward larger backends; unit-test with fuzz inputs. |
| **Reference-Grant denial silently drops a backend** | Med | High | `ResolvedRefs=False, reason=RefNotPermitted` + Warning event + Prometheus counter `gateway_api_refgrant_denied_total`. |
| **Concurrent endpoint churn during weight rescaling causes pool flapping** | Low | Med | 2 s coalescing window when triggered by EndpointSlice change. |
| **Listener cert-ID in policy conflicts with Secret-imported cert** | Low | Low | Policy wins; document precedence; emit Warning event. |
| **Unsupported-feature requests pile up** (URLRewrite, mirror, header / queryParam / method matching, FixedResponse, SSL / ALPN policies, cookie session affinity, custom HC headers, etc.) | Med | Med | `gateway_api_unsupported_feature_total` metric → product feedback to vngcloud LB team; documented capability matrix (§1.6) sets expectations. |
| **CRD conversion needed later** | Low | Med | Plan field additions only (no removals/renames) until v1beta1 promotion; webhook conversion deferred. |
| **Gateway API spec churn** between releases | Low | Med | Pin SDK to a known-good Gateway API minor version (v1.2 for Phase 1); upgrade in a dedicated PR with conformance re-run. |
| **Two LB types in one cluster eat budget** (no sharing per Q6) | Med | Low | Document clearly; cost guidance in `gateway-api.md`; Phase 4 migration path lets users switch without doubling cost. |
| **Existing controllers regress** during refactor | Low | High | This design touches **no** existing controller code paths. Full existing test suite gates the merge. |
| **`HTTPRoute.spec.rules[].name` not provided by users** (required for `VKSRoutePolicy.sectionName`) | Med | Low | `VKSRoutePolicy` with `sectionName` that doesn't match any rule → `Accepted=False, reason=TargetNotFound`. Documented in `gateway-policies.md` with examples. Validation webhook in Phase 3 catches at apply time. |

---

## 8. Open items deferred to implementation

1. Helm-chart default for `gatewayApi.alb.enabled` — propose `false` for first Phase-1 release; flip to `true` after one minor cycle.
2. Per-Gateway concurrency — start with `MaxConcurrentReconciles=5` (matches existing Ingress); revisit if rate-limit issues appear.
3. `Gateway.spec.addresses[]` user requests — reject for v1 (vngcloud assigns IP). Revisit if LB product gains BYOIP.
4. Cross-namespace policy targeting — same-namespace only in v1 (matches GEP-713 recommendation for Direct policies). Revisit when ReferenceGrant gains policy-CR support.
5. Inherited Policy adoption — if real cluster-admin-defaults use cases emerge, introduce an `Inherited` variant (e.g. `targetRefs: GatewayClass` with `default` / `override` blocks) in Phase 3+. Not needed for v1.
6. `PolicyAncestorStatus` consumers — populated from day one but only used by the single ALB / NLB controllers. Multi-controller (e.g. GLB) coexistence deferred to Phase 3.

---

## 9. Out of scope (explicitly not planned)

- Mesh use cases (Gateway-API east-west).
- `GatewayClass.parametersRef` configuration. The CRD field exists on the upstream type and is silently ignored.
- HTTPRoute `filters[].extensionRef` interpretation. Vngcloud-specific behavior is exclusively via `VKSRoutePolicy`.
- `LoadBalancerConfig` extensions for Gateway API. The existing CRD remains Ingress / Service-only.
- Per-route load balancer addresses (one Gateway = one address set).
- In-place GatewayClass type swap (e.g., `vngcloud-alb` → `vngcloud-nlb` on a live Gateway). Forces recreate.
- Inherited Policy Attachment (defaults / overrides hierarchy). Reconsidered after v1 if demand exists.

---

## 10. What this design intentionally doesn't reuse from `2026-04-30-gateway-api-design.md`

For reviewers comparing the two docs:

| Earlier design used | This design uses instead | Why |
|---|---|---|
| `LoadBalancerConfig` extended with `mergingMode`, referenced via `parametersRef` at GatewayClass + Gateway | `VKSGatewayPolicy` (Direct, `targetRefs.kind: Gateway`, optional `sectionName`) | Single attachment pattern across all knobs; aligns with GEP-713 / GKE / Envoy GW / Cilium / Istio. |
| `TargetGroupConfig.spec.targetReference` (inverse `targetRef`) | `VKSBackendPolicy.targetRefs[]` + separate `VKSHealthCheckPolicy.targetRefs[]` | Standard `targetRefs` shape; tooling and conformance recognize it; splitting health check follows GKE's split (different teams own each). |
| `ListenerRuleConfig` attached via `HTTPRoute.spec.rules[].filters[].extensionRef` | `VKSRoutePolicy` (Direct, `targetRefs.kind: HTTPRoute`, `sectionName: <ruleName>`) | Same uniform attachment pattern. Trade-off: route authors author one extra YAML doc; in exchange, RBAC can split route-author vs match-author cleanly and the policy is discoverable via `kubectl get vksroutepolicy`. |
| `mergingMode: PreferGateway \| PreferGatewayClass` | none — no GatewayClass-level config in v1 | Inherited Policy is heavier than v1 needs. If real demand emerges, add an Inherited variant later. |

The earlier design's domain model (synthetic pool, hostname → regex, listener diff, weight scaling, NSG inheritance, finalizer protocol, owner labels) is preserved verbatim — those decisions are independent of attachment style.

---

## References

- [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/)
- [GEP-713: Metaresources and Policy Attachment](https://gateway-api.sigs.k8s.io/geps/gep-713/)
- [Policy Attachment reference page](https://gateway-api.sigs.k8s.io/reference/policy-attachment/)
- [Gateway API implementations list](https://gateway-api.sigs.k8s.io/implementations/)
- [GKE: Configure Gateway resources using Policies](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/configure-gateway-resources)
- [GKE Gateway API Types reference](https://googlecloudplatform.github.io/gke-gateway-api/)
- [Envoy Gateway BackendTrafficPolicy / ClientTrafficPolicy](https://gateway.envoyproxy.io/docs/api/extension_types/)
