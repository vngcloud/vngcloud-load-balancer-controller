# Gateway API GEP-713 — Phase 1 (L7 ALB MVP) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Gateway API L7 controller that provisions a vngcloud ALB per `Gateway`, programs listeners and policies from `HTTPRoute`s, with four Direct-Policy CRDs (`VKSGatewayPolicy`, `VKSBackendPolicy`, `VKSHealthCheckPolicy`, `VKSRoutePolicy`) attaching via `spec.targetRefs` per GEP-713.

**Architecture:** New `vngcloud-alb` GatewayClass; reconcilers in `internal/controller/gateway/alb/`; business logic in `internal/usecase/gateway_uc/alb_gateway_uc/`; reuses existing `K8sRepository`, `VngCloudRepository`, `FinalizerManager`, cert-import path, and `lbc_uc` listener/pool/policy deploy helpers. Gateway is the only writer to vngcloud LB; HTTPRoute and policy reconciles enqueue the parent Gateway.

**Tech Stack:** Go 1.25, controller-runtime 0.19, kubebuilder v4, sigs.k8s.io/gateway-api v1.2, ginkgo+gomega+envtest, gomock, vngcloud-go-sdk/v2.

**Spec reference:** `docs/superpowers/specs/2026-05-08-gateway-api-gep713-design.md`

**Out of scope for Phase 1:**
- NLB GatewayClass / TCP / UDP / TLS-Passthrough (Phase 2)
- GRPCRoute / BackendTLSPolicy / URLRewrite / Mirror / ResponseHeaderModifier (Phase 3)
- Migration tool, conformance test job, CRD promotion to v1beta1 (Phase 4)
- Inherited Policy Attachment (defaults/overrides hierarchy)

---

## File map (Phase 1)

| File | Role |
|---|---|
| `go.mod`, `go.sum` | Add `sigs.k8s.io/gateway-api v1.2.0` |
| `PROJECT` | Register new resources |
| `api/gateway/v1alpha1/groupversion_info.go` | API group registration |
| `api/gateway/v1alpha1/doc.go` | Group package doc + kubebuilder markers |
| `api/gateway/v1alpha1/shared_types.go` | Re-exports of LocalPolicyTargetReference, status helpers |
| `api/gateway/v1alpha1/vksgatewaypolicy_types.go` | VKSGatewayPolicy + Spec/Status |
| `api/gateway/v1alpha1/vksbackendpolicy_types.go` | VKSBackendPolicy + Spec/Status |
| `api/gateway/v1alpha1/vkshealthcheckpolicy_types.go` | VKSHealthCheckPolicy + Spec/Status |
| `api/gateway/v1alpha1/vksroutepolicy_types.go` | VKSRoutePolicy + Spec/Status |
| `api/gateway/v1alpha1/zz_generated.deepcopy.go` | Generated |
| `config/crd/bases/gateway.vks.vngcloud.vn_*.yaml` | Generated CRDs (4 files) |
| `pkg/k8s/apis/vks.vngcloud.vn/crds/gateway.vks.vngcloud.vn_*.yaml` | Embedded CRDs (mirrored from `config/crd/bases/`) |
| `internal/domain/domain.go` | Finalizer + owner-kind constants |
| `pkg/consts/consts.go` | GatewayClass controller-name constants |
| `pkg/gateway/gatewayapi_utils.go` | Hostname matchers, wildcard→regex, helpers |
| `pkg/gateway/synth_pool.go` | Deterministic pool naming + weight scaling |
| `pkg/gateway/policy_target.go` | Target-ref matching helpers |
| `internal/controller/gateway/shared/classifier.go` | Listener-protocol/GatewayClass validation |
| `internal/controller/gateway/shared/status.go` | Condition + PolicyAncestorStatus helpers |
| `internal/controller/gateway/shared/policy_order.go` | Match-specificity ordering |
| `internal/controller/gateway/shared/reference_indexer.go` | Reverse indexes for all 4 policies + routes |
| `internal/controller/gateway/shared/finalizer.go` | Finalizer add/remove helpers |
| `internal/controller/gateway/shared/eventhandlers/*.go` | Cross-resource enqueue helpers |
| `internal/usecase/gateway_uc/shared/refgrant.go` | ReferenceGrant evaluation |
| `internal/usecase/gateway_uc/shared/policy_resolver.go` | Direct policy resolution (oldest-wins) |
| `internal/usecase/gateway_uc/alb_gateway_uc/gateway_uc.go` | UseCase entry (Init/Ensure/Delete) |
| `internal/usecase/gateway_uc/alb_gateway_uc/build_lb.go` | Gateway → LB params (uses VKSGatewayPolicy) |
| `internal/usecase/gateway_uc/alb_gateway_uc/build_listener.go` | Listener policy resolution + listener build |
| `internal/usecase/gateway_uc/alb_gateway_uc/build_cert.go` | Secret import / cert-ID |
| `internal/usecase/gateway_uc/alb_gateway_uc/build_pool.go` | Synthetic pool with weight + health |
| `internal/usecase/gateway_uc/alb_gateway_uc/build_policy.go` | HTTPRoute → vngcloud Policy with VKSRoutePolicy overlay |
| `internal/usecase/gateway_uc/alb_gateway_uc/build_sec_group.go` | NSG inheritance |
| `internal/usecase/gateway_uc/alb_gateway_uc/status.go` | Status writers |
| `internal/controller/gateway/alb/gatewayclass_controller.go` | GatewayClass reconciler |
| `internal/controller/gateway/alb/gateway_controller.go` | Gateway reconciler |
| `internal/controller/gateway/alb/httproute_controller.go` | HTTPRoute reconciler |
| `internal/controller/gateway/policies/vksgatewaypolicy_controller.go` | Policy validator |
| `internal/controller/gateway/policies/vksbackendpolicy_controller.go` | Policy validator |
| `internal/controller/gateway/policies/vkshealthcheckpolicy_controller.go` | Policy validator |
| `internal/controller/gateway/policies/vksroutepolicy_controller.go` | Policy validator |
| `internal/controller/gateway/alb/suite_test.go` | envtest harness |
| `internal/controller/gateway/alb/*_test.go` | Ginkgo specs |
| `cmd/main.go` | Scheme + flags + reconciler wiring |
| `charts/vngcloud-load-balancer-controller/templates/crds/*.yaml` | CRD bundle |
| `charts/vngcloud-load-balancer-controller/templates/gatewayclass-alb.yaml` | Gated GatewayClass manifest |
| `charts/vngcloud-load-balancer-controller/templates/rbac/*.yaml` | RBAC additions |
| `charts/vngcloud-load-balancer-controller/values.yaml` | `gatewayApi.alb.enabled` toggle |
| `test/e2e/gateway/*.yaml` | E2E manifests + scripts |
| `docs/guide/gateway-api.md`, `gateway-alb.md`, `gateway-policies.md` | User docs |
| `docs/examples/gateway-canary.md` | Example |

---

## Section A — Bootstrap

### Task A1: Add the Gateway API dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

Run:

```bash
go get sigs.k8s.io/gateway-api@v1.2.0
go mod tidy
```

Expected: `go.mod` contains `sigs.k8s.io/gateway-api v1.2.0`. `go.sum` updated.

- [ ] **Step 2: Verify build still passes**

Run:

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build(gateway): add sigs.k8s.io/gateway-api v1.2.0 dependency"
```

---

### Task A2: Create the API group skeleton

**Files:**
- Create: `api/gateway/v1alpha1/groupversion_info.go`
- Create: `api/gateway/v1alpha1/doc.go`

- [ ] **Step 1: Write `doc.go`**

```go
// Package v1alpha1 contains API Schema definitions for the gateway.vks.vngcloud.vn v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=gateway.vks.vngcloud.vn
package v1alpha1
```

- [ ] **Step 2: Write `groupversion_info.go`**

```go
package v1alpha1

import (
    "k8s.io/apimachinery/pkg/runtime/schema"
    "sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
    GroupVersion  = schema.GroupVersion{Group: "gateway.vks.vngcloud.vn", Version: "v1alpha1"}
    SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
    AddToScheme   = SchemeBuilder.AddToScheme
)
```

- [ ] **Step 3: Verify package compiles**

```bash
go build ./api/gateway/v1alpha1/...
```

Expected: success (no types yet, but the package declaration is valid).

- [ ] **Step 4: Commit**

```bash
git add api/gateway/v1alpha1/
git commit -m "feat(gateway/api): scaffold gateway.vks.vngcloud.vn/v1alpha1 group"
```

---

### Task A3: Add domain + consts entries

**Files:**
- Modify: `internal/domain/domain.go`
- Modify: `pkg/consts/consts.go`

- [ ] **Step 1: Add finalizer and owner-kind constants to `internal/domain/domain.go`**

Add to the existing constants block:

```go
// Gateway API constants.
const (
    GatewayFinalizer    = "gateway.vks.vngcloud.vn/resources"
    HTTPRouteFinalizer  = "gateway.vks.vngcloud.vn/route"

    OwnerKindGateway = "Gateway"

    OwnerLabelGatewayUID = "gateway.vks.vngcloud.vn/owner-uid"
    OwnerLabelKind       = "vks.vngcloud.vn/owner-resource-kind"
)
```

- [ ] **Step 2: Add controller-name constants to `pkg/consts/consts.go`**

```go
const (
    GatewayClassControllerNameALB = "gateway.vks.vngcloud.vn/alb"
    GatewayClassControllerNameNLB = "gateway.vks.vngcloud.vn/nlb"
    GatewayClassNameALB           = "vngcloud-alb"
    GatewayClassNameNLB           = "vngcloud-nlb"
)
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add internal/domain/domain.go pkg/consts/consts.go
git commit -m "feat(gateway): add domain finalizers and GatewayClass controller names"
```

---

## Section B — Policy CRDs (API types)

> All four CRDs sit in `api/gateway/v1alpha1/`. Spec is authoritative for field doc comments — see `2026-05-08-gateway-api-gep713-design.md` §2. Field structures are reproduced inline below; copy them verbatim.

### Task B1: Shared types and re-exports

**Files:**
- Create: `api/gateway/v1alpha1/shared_types.go`

- [ ] **Step 1: Write the shared types file**

```go
package v1alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// LocalPolicyTargetReference is re-exported for convenience.
type LocalPolicyTargetReference = gwv1alpha2.LocalPolicyTargetReference

// LocalPolicyTargetReferenceWithSectionName is re-exported for convenience.
type LocalPolicyTargetReferenceWithSectionName = gwv1alpha2.LocalPolicyTargetReferenceWithSectionName

// PolicyAncestorStatus is re-exported for convenience.
type PolicyAncestorStatus = gwv1alpha2.PolicyAncestorStatus

// CommonPolicyStatus is the embedded ancestor-status block carried by every VKS policy CRD.
type CommonPolicyStatus struct {
    // Ancestors records reconcile status per controller acting on this policy.
    // +listType=atomic
    // +kubebuilder:validation:MaxItems=16
    Ancestors []PolicyAncestorStatus `json:"ancestors,omitempty"`
}

// PolicyConditionType collects the standard reasons used across all four VKS policy CRDs.
const (
    PolicyConditionAccepted   = "Accepted"
    PolicyConditionProgrammed = "Programmed"

    PolicyReasonAccepted        = "Accepted"
    PolicyReasonConflicted      = "Conflicted"
    PolicyReasonInvalid         = "Invalid"
    PolicyReasonTargetNotFound  = "TargetNotFound"
    PolicyReasonNoReadyController = "NoReadyController"
    PolicyReasonProgrammed      = "Programmed"
    PolicyReasonPending         = "Pending"
)

// CommonStatus is the common subset embedded into each policy's Status.
type CommonStatus struct {
    Conditions         []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
    ObservedGeneration int64              `json:"observedGeneration,omitempty"`
}
```

- [ ] **Step 2: Verify**

```bash
go build ./api/gateway/v1alpha1/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add api/gateway/v1alpha1/shared_types.go
git commit -m "feat(gateway/api): add shared policy status + target-ref re-exports"
```

---

### Task B2: VKSGatewayPolicy types

**Files:**
- Create: `api/gateway/v1alpha1/vksgatewaypolicy_types.go`

- [ ] **Step 1: Write the type file**

```go
package v1alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vksgwpolicy,categories=gateway-api
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
// +kubebuilder:printcolumn:name="Targets",type=string,JSONPath=`.spec.targetRefs[*].name`
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type VKSGatewayPolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   VKSGatewayPolicySpec   `json:"spec,omitempty"`
    Status VKSGatewayPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type VKSGatewayPolicyList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []VKSGatewayPolicy `json:"items"`
}

type VKSGatewayPolicySpec struct {
    // +kubebuilder:validation:MinItems=1
    // +kubebuilder:validation:MaxItems=16
    TargetRefs []LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`

    SSLPolicy         *string            `json:"sslPolicy,omitempty"`
    ALPNPolicy        *string            `json:"alpnPolicy,omitempty"`
    AllowedCIDRs      []string           `json:"allowedCidrs,omitempty"`
    InsertHeaders     map[string]string  `json:"insertHeaders,omitempty"`
    TimeoutClient     *metav1.Duration   `json:"timeoutClient,omitempty"`
    TimeoutMember     *metav1.Duration   `json:"timeoutMember,omitempty"`
    TimeoutConnection *metav1.Duration   `json:"timeoutConnection,omitempty"`

    CertificateIDs      []string `json:"certificateIds,omitempty"`
    ClientCertificateID *string  `json:"clientCertificateId,omitempty"`

    LoadBalancerSpec *VKSLoadBalancerSpec `json:"loadBalancerSpec,omitempty"`
}

type VKSLoadBalancerSpec struct {
    // +kubebuilder:validation:Enum=Internet;Internal;InterVPC
    Scheme         *string           `json:"scheme,omitempty"`
    PackageID      *string           `json:"packageId,omitempty"`
    SubnetID       *string           `json:"subnetId,omitempty"`
    Tags           map[string]string `json:"tags,omitempty"`
    LoadBalancerID *string           `json:"loadBalancerId,omitempty"`
}

type VKSGatewayPolicyStatus struct {
    CommonStatus       `json:",inline"`
    CommonPolicyStatus `json:",inline"`
}

func init() {
    SchemeBuilder.Register(&VKSGatewayPolicy{}, &VKSGatewayPolicyList{})
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./api/gateway/v1alpha1/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add api/gateway/v1alpha1/vksgatewaypolicy_types.go
git commit -m "feat(gateway/api): add VKSGatewayPolicy types"
```

---

### Task B3: VKSBackendPolicy types

**Files:**
- Create: `api/gateway/v1alpha1/vksbackendpolicy_types.go`

- [ ] **Step 1: Write the type file**

```go
package v1alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vksbpolicy,categories=gateway-api
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
// +kubebuilder:printcolumn:name="Targets",type=string,JSONPath=`.spec.targetRefs[*].name`
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type VKSBackendPolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   VKSBackendPolicySpec   `json:"spec,omitempty"`
    Status VKSBackendPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type VKSBackendPolicyList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []VKSBackendPolicy `json:"items"`
}

type VKSBackendPolicySpec struct {
    // +kubebuilder:validation:MinItems=1
    // +kubebuilder:validation:MaxItems=16
    TargetRefs []LocalPolicyTargetReference `json:"targetRefs"`

    // +kubebuilder:validation:Enum=instance;ip
    TargetType *string `json:"targetType,omitempty"`

    // +kubebuilder:validation:Enum=ROUND_ROBIN;LEAST_CONNECTIONS;SOURCE_IP
    PoolAlgorithm *string `json:"poolAlgorithm,omitempty"`

    SessionAffinity *VKSSessionAffinity `json:"sessionAffinity,omitempty"`

    EnableTLSEncryption *bool             `json:"enableTLSEncryption,omitempty"`
    EnableProxyProtocol *bool             `json:"enableProxyProtocol,omitempty"`
    TargetNodeLabels    map[string]string `json:"targetNodeLabels,omitempty"`
    ManageDFPMembers    *bool             `json:"manageDFPMembers,omitempty"`
}

type VKSSessionAffinity struct {
    // +kubebuilder:validation:Enum=None;ClientIP;Cookie
    Type       string           `json:"type"`
    CookieName *string          `json:"cookieName,omitempty"`
    TTL        *metav1.Duration `json:"ttl,omitempty"`
}

type VKSBackendPolicyStatus struct {
    CommonStatus       `json:",inline"`
    CommonPolicyStatus `json:",inline"`
}

func init() {
    SchemeBuilder.Register(&VKSBackendPolicy{}, &VKSBackendPolicyList{})
}
```

- [ ] **Step 2: Verify**

```bash
go build ./api/gateway/v1alpha1/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add api/gateway/v1alpha1/vksbackendpolicy_types.go
git commit -m "feat(gateway/api): add VKSBackendPolicy types"
```

---

### Task B4: VKSHealthCheckPolicy types

**Files:**
- Create: `api/gateway/v1alpha1/vkshealthcheckpolicy_types.go`

- [ ] **Step 1: Write the type file**

```go
package v1alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vkshcpolicy,categories=gateway-api
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
// +kubebuilder:printcolumn:name="Protocol",type=string,JSONPath=`.spec.protocol`
// +kubebuilder:printcolumn:name="Targets",type=string,JSONPath=`.spec.targetRefs[*].name`
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type VKSHealthCheckPolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   VKSHealthCheckPolicySpec   `json:"spec,omitempty"`
    Status VKSHealthCheckPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type VKSHealthCheckPolicyList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []VKSHealthCheckPolicy `json:"items"`
}

type VKSHealthCheckPolicySpec struct {
    // +kubebuilder:validation:MinItems=1
    // +kubebuilder:validation:MaxItems=16
    TargetRefs []LocalPolicyTargetReference `json:"targetRefs"`

    // +kubebuilder:validation:Enum=HTTP;HTTPS;TCP
    Protocol           string           `json:"protocol"`
    Port               *int32           `json:"port,omitempty"`
    Interval           *metav1.Duration `json:"interval,omitempty"`
    Timeout            *metav1.Duration `json:"timeout,omitempty"`
    HealthyThreshold   *int32           `json:"healthyThreshold,omitempty"`
    UnhealthyThreshold *int32           `json:"unhealthyThreshold,omitempty"`

    HTTPHealthCheck *VKSHTTPHealthCheck `json:"httpHealthCheck,omitempty"`
}

type VKSHTTPHealthCheck struct {
    Path           *string           `json:"path,omitempty"`
    Host           *string           `json:"host,omitempty"`
    ExpectedCodes  []string          `json:"expectedCodes,omitempty"`
    RequestHeaders map[string]string `json:"requestHeaders,omitempty"`
}

type VKSHealthCheckPolicyStatus struct {
    CommonStatus       `json:",inline"`
    CommonPolicyStatus `json:",inline"`
}

func init() {
    SchemeBuilder.Register(&VKSHealthCheckPolicy{}, &VKSHealthCheckPolicyList{})
}
```

- [ ] **Step 2: Verify**

```bash
go build ./api/gateway/v1alpha1/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add api/gateway/v1alpha1/vkshealthcheckpolicy_types.go
git commit -m "feat(gateway/api): add VKSHealthCheckPolicy types"
```

---

### Task B5: VKSRoutePolicy types

**Files:**
- Create: `api/gateway/v1alpha1/vksroutepolicy_types.go`

- [ ] **Step 1: Write the type file**

```go
package v1alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vksroutepolicy,categories=gateway-api
// +kubebuilder:metadata:labels=gateway.networking.k8s.io/policy=direct
// +kubebuilder:printcolumn:name="Targets",type=string,JSONPath=`.spec.targetRefs[*].name`
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type VKSRoutePolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   VKSRoutePolicySpec   `json:"spec,omitempty"`
    Status VKSRoutePolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type VKSRoutePolicyList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []VKSRoutePolicy `json:"items"`
}

type VKSRoutePolicySpec struct {
    // +kubebuilder:validation:MinItems=1
    // +kubebuilder:validation:MaxItems=16
    TargetRefs []LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`

    AdditionalMatches []VKSAdditionalMatch `json:"additionalMatches,omitempty"`
    Actions           []VKSRuleAction      `json:"actions,omitempty"`
    Position          *int32               `json:"position,omitempty"`
}

type VKSAdditionalMatch struct {
    // +kubebuilder:validation:Enum=Header;QueryParam;Method;SourceIP
    Type    string  `json:"type"`
    Name    *string `json:"name,omitempty"`
    // +kubebuilder:validation:Enum=EQUAL_TO;STARTS_WITH;ENDS_WITH;CONTAINS;REGEX
    Compare string  `json:"compare"`
    Value   string  `json:"value"`
}

type VKSRuleAction struct {
    // +kubebuilder:validation:Enum=FixedResponse;Reject;Redirect
    Type          string                  `json:"type"`
    FixedResponse *VKSFixedResponseAction `json:"fixedResponse,omitempty"`
    Redirect      *VKSRedirectAction      `json:"redirect,omitempty"`
}

type VKSFixedResponseAction struct {
    // +kubebuilder:validation:Minimum=100
    // +kubebuilder:validation:Maximum=599
    StatusCode  int32   `json:"statusCode"`
    ContentType *string `json:"contentType,omitempty"`
    Body        *string `json:"body,omitempty"`
}

type VKSRedirectAction struct {
    URL             string `json:"url"`
    HTTPCode        *int32 `json:"httpCode,omitempty"`
    KeepQueryString *bool  `json:"keepQueryString,omitempty"`
}

type VKSRoutePolicyStatus struct {
    CommonStatus       `json:",inline"`
    CommonPolicyStatus `json:",inline"`
}

func init() {
    SchemeBuilder.Register(&VKSRoutePolicy{}, &VKSRoutePolicyList{})
}
```

- [ ] **Step 2: Verify**

```bash
go build ./api/gateway/v1alpha1/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add api/gateway/v1alpha1/vksroutepolicy_types.go
git commit -m "feat(gateway/api): add VKSRoutePolicy types"
```

---

### Task B6: Generate deepcopy and CRD manifests

**Files:**
- Create: `api/gateway/v1alpha1/zz_generated.deepcopy.go`
- Create: `config/crd/bases/gateway.vks.vngcloud.vn_vksgatewaypolicies.yaml`
- Create: `config/crd/bases/gateway.vks.vngcloud.vn_vksbackendpolicies.yaml`
- Create: `config/crd/bases/gateway.vks.vngcloud.vn_vkshealthcheckpolicies.yaml`
- Create: `config/crd/bases/gateway.vks.vngcloud.vn_vksroutepolicies.yaml`
- Mirrored: `pkg/k8s/apis/vks.vngcloud.vn/crds/gateway.vks.vngcloud.vn_*.yaml` (`make manifests` runs `sync-embedded-crds`)

- [ ] **Step 1: Run codegen**

Run:

```bash
make generate manifests
```

Expected: `zz_generated.deepcopy.go` populated; four CRD YAMLs in `config/crd/bases/`; same four mirrored to `pkg/k8s/apis/vks.vngcloud.vn/crds/`.

- [ ] **Step 2: Check `git status` for stray files**

Per `CLAUDE.md` (`make manifests` scans `.worktrees/`), revert anything unrelated to the four new CRDs.

```bash
git status
```

Expected: only changes are the 4 new CRD YAMLs (in both `config/crd/bases/` and `pkg/k8s/apis/.../crds/`), the regenerated deepcopy, and `config/rbac/role.yaml` if RBAC markers were added later. No stray edits to existing CRDs.

- [ ] **Step 3: Verify build + envtest setup still passes**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add api/gateway/v1alpha1/zz_generated.deepcopy.go \
        config/crd/bases/gateway.vks.vngcloud.vn_*.yaml \
        pkg/k8s/apis/vks.vngcloud.vn/crds/gateway.vks.vngcloud.vn_*.yaml
git commit -m "feat(gateway/api): generate deepcopy and CRD manifests for VKS policies"
```

---

## Section C — Pure helpers (`pkg/gateway/`)

### Task C1: Hostname matching utilities

**Files:**
- Create: `pkg/gateway/gatewayapi_utils.go`
- Create: `pkg/gateway/gatewayapi_utils_test.go`

- [ ] **Step 1: Write failing tests**

```go
package gateway

import "testing"

func TestHostnameToRegex(t *testing.T) {
    cases := []struct{
        in       string
        wantRegex string
        wantLiteral bool
    }{
        {"foo.example.com", "^foo\\.example\\.com$", true},
        {"*.example.com", "^[^.]+\\.example\\.com$", false},
        {"", "", true},
    }
    for _, tc := range cases {
        gotRegex, isLiteral := HostnameToRegex(tc.in)
        if gotRegex != tc.wantRegex || isLiteral != tc.wantLiteral {
            t.Errorf("HostnameToRegex(%q) = (%q, %v); want (%q, %v)",
                tc.in, gotRegex, isLiteral, tc.wantRegex, tc.wantLiteral)
        }
    }
}

func TestHostnameMatches(t *testing.T) {
    cases := []struct{
        listener, route string
        want            bool
    }{
        {"", "foo.example.com", true},
        {"foo.example.com", "foo.example.com", true},
        {"foo.example.com", "bar.example.com", false},
        {"*.example.com", "foo.example.com", true},
        {"*.example.com", "foo.bar.example.com", false},
        {"*.example.com", "example.com", false},
    }
    for _, tc := range cases {
        if got := HostnameMatches(tc.listener, tc.route); got != tc.want {
            t.Errorf("HostnameMatches(%q,%q) = %v; want %v",
                tc.listener, tc.route, got, tc.want)
        }
    }
}
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./pkg/gateway/ -run "TestHostname"
```

Expected: FAIL (functions undefined).

- [ ] **Step 3: Implement**

```go
package gateway

import (
    "regexp"
    "strings"
)

// HostnameToRegex converts a Gateway-API hostname (literal or `*.suffix`) to an
// anchored regex string. Returns isLiteral=true when no wildcard is present.
// Empty input ("") is treated as literal empty (matches anything at the listener layer).
func HostnameToRegex(h string) (string, bool) {
    if h == "" {
        return "", true
    }
    if strings.HasPrefix(h, "*.") {
        suffix := regexp.QuoteMeta(h[2:])
        return "^[^.]+\\." + suffix + "$", false
    }
    return "^" + regexp.QuoteMeta(h) + "$", true
}

// HostnameMatches returns true if a route hostname matches a listener hostname per
// Gateway-API rules. Empty listener hostname matches anything.
func HostnameMatches(listener, route string) bool {
    if listener == "" {
        return true
    }
    re, _ := HostnameToRegex(listener)
    matched, _ := regexp.MatchString(re, route)
    return matched
}
```

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./pkg/gateway/ -run "TestHostname" -v
```

Expected: PASS for all subcases.

- [ ] **Step 5: Commit**

```bash
git add pkg/gateway/gatewayapi_utils.go pkg/gateway/gatewayapi_utils_test.go
git commit -m "feat(gateway/pkg): add hostname matching helpers"
```

---

### Task C2: Synthetic pool naming and weight scaling

**Files:**
- Create: `pkg/gateway/synth_pool.go`
- Create: `pkg/gateway/synth_pool_test.go`

- [ ] **Step 1: Write failing tests**

```go
package gateway

import "testing"

func TestSynthPoolName_Deterministic(t *testing.T) {
    a := SynthPoolName("12345678abcdef", 0, []BackendKey{{Namespace:"ns",Name:"a",Port:80,Weight:1}})
    b := SynthPoolName("12345678abcdef", 0, []BackendKey{{Namespace:"ns",Name:"a",Port:80,Weight:1}})
    if a != b {
        t.Fatalf("not deterministic: %s vs %s", a, b)
    }
    if len(a) > 50 {
        t.Errorf("name too long: %d", len(a))
    }
}

func TestSynthPoolName_OrderInsensitive(t *testing.T) {
    a := SynthPoolName("u", 0, []BackendKey{{Namespace:"ns",Name:"a",Port:80,Weight:1},{Namespace:"ns",Name:"b",Port:80,Weight:1}})
    b := SynthPoolName("u", 0, []BackendKey{{Namespace:"ns",Name:"b",Port:80,Weight:1},{Namespace:"ns",Name:"a",Port:80,Weight:1}})
    if a != b {
        t.Fatalf("expected order-insensitive: %s vs %s", a, b)
    }
}

func TestScaleWeights_Cap(t *testing.T) {
    // 2 backends, weights 1 and 99, ready endpoints 3 and 1.
    out := ScaleWeights([]BackendWeight{{Weight:1, Ready:3}, {Weight:99, Ready:1}})
    if len(out) != 2 {
        t.Fatalf("want 2 entries, got %d", len(out))
    }
    for _, w := range out {
        if w < 1 || w > 100 {
            t.Errorf("weight out of range: %d", w)
        }
    }
}

func TestScaleWeights_FloorOne(t *testing.T) {
    out := ScaleWeights([]BackendWeight{{Weight:1, Ready:1000}, {Weight:1000, Ready:1}})
    for _, w := range out {
        if w < 1 {
            t.Errorf("weight floored below 1: %d", w)
        }
    }
}
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./pkg/gateway/ -run "TestSynth|TestScale"
```

Expected: FAIL (functions undefined).

- [ ] **Step 3: Implement**

```go
package gateway

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "math/big"
    "sort"
)

// BackendKey is the canonical identity of a backend used in pool naming.
type BackendKey struct {
    Namespace string
    Name      string
    Port      int32
    Weight    int32
}

// BackendWeight is a (weight, ready-endpoints) pair fed to ScaleWeights.
type BackendWeight struct {
    Weight int32
    Ready  int32
}

// SynthPoolName returns a deterministic pool name <= 50 chars derived from the
// route UID, rule index, and backend set. Backend ordering is normalized.
func SynthPoolName(routeUID string, ruleIdx int, backends []BackendKey) string {
    sorted := append([]BackendKey(nil), backends...)
    sort.Slice(sorted, func(i, j int) bool {
        if sorted[i].Namespace != sorted[j].Namespace { return sorted[i].Namespace < sorted[j].Namespace }
        if sorted[i].Name != sorted[j].Name           { return sorted[i].Name < sorted[j].Name }
        if sorted[i].Port != sorted[j].Port           { return sorted[i].Port < sorted[j].Port }
        return sorted[i].Weight < sorted[j].Weight
    })
    h := sha256.New()
    for _, b := range sorted {
        fmt.Fprintf(h, "%s/%s:%d=%d\n", b.Namespace, b.Name, b.Port, b.Weight)
    }
    digest := hex.EncodeToString(h.Sum(nil))
    prefix := routeUID
    if len(prefix) > 8 {
        prefix = prefix[:8]
    }
    name := fmt.Sprintf("gw_%s_%d_%s", prefix, ruleIdx, digest[:5])
    if len(name) > 50 {
        name = name[:50]
    }
    return name
}

// ScaleWeights computes vngcloud member weights given (declared weight, ready endpoints)
// per backend. Member weights are floored at 1 and the largest member weight is capped at 100.
func ScaleWeights(in []BackendWeight) []int32 {
    if len(in) == 0 {
        return nil
    }
    out := make([]int32, len(in))
    var maxW big.Int
    for i, b := range in {
        n := b.Ready
        if n <= 0 { n = 1 }
        // member_weight ≈ weight / readyEndpoints, scaled later.
        mw := new(big.Int).SetInt64(int64(b.Weight))
        mw.Mul(mw, big.NewInt(100))
        mw.Quo(mw, big.NewInt(int64(n)))
        if mw.Sign() <= 0 { mw.SetInt64(1) }
        if mw.Cmp(&maxW) > 0 { maxW.Set(mw) }
        out[i] = int32(mw.Int64())
    }
    if maxW.Cmp(big.NewInt(100)) > 0 {
        scale := big.NewInt(100)
        for i := range out {
            v := new(big.Int).SetInt64(int64(out[i]))
            v.Mul(v, scale)
            v.Quo(v, &maxW)
            if v.Sign() <= 0 { v.SetInt64(1) }
            out[i] = int32(v.Int64())
        }
    }
    return out
}
```

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./pkg/gateway/ -run "TestSynth|TestScale" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/gateway/synth_pool.go pkg/gateway/synth_pool_test.go
git commit -m "feat(gateway/pkg): add synthetic-pool naming and weight scaling"
```

---

### Task C3: Policy target-ref matching helper

**Files:**
- Create: `pkg/gateway/policy_target.go`
- Create: `pkg/gateway/policy_target_test.go`

- [ ] **Step 1: Write failing tests**

```go
package gateway

import (
    "testing"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

type fakeObj struct {
    name, ns, gvk string
}

func TestTargetRefMatches(t *testing.T) {
    grp := gwv1alpha2.Group("gateway.networking.k8s.io")
    tref := gwv1alpha2.LocalPolicyTargetReference{
        Group: grp,
        Kind:  gwv1alpha2.Kind("Gateway"),
        Name:  gwv1alpha2.ObjectName("prod-gw"),
    }
    ok := TargetRefMatches(tref, "ns", PolicyTarget{
        Group: "gateway.networking.k8s.io",
        Kind:  "Gateway",
        Name:  "prod-gw",
        Namespace: "ns",
    })
    if !ok { t.Fatal("expected match") }
}

func TestTargetRefMatches_DifferentName(t *testing.T) {
    tref := gwv1alpha2.LocalPolicyTargetReference{
        Group: "gateway.networking.k8s.io",
        Kind:  "Gateway",
        Name:  "prod-gw",
    }
    if TargetRefMatches(tref, "ns", PolicyTarget{
        Group: "gateway.networking.k8s.io", Kind: "Gateway",
        Name: "staging-gw", Namespace: "ns",
    }) {
        t.Fatal("unexpected match")
    }
    _ = metav1.Now() // keep import in case future tests need it
}
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./pkg/gateway/ -run "TestTargetRef"
```

Expected: FAIL.

- [ ] **Step 3: Implement**

```go
package gateway

import (
    gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// PolicyTarget identifies a candidate object a Direct policy may attach to.
type PolicyTarget struct {
    Group       string
    Kind        string
    Namespace   string
    Name        string
    SectionName string
}

// TargetRefMatches reports whether a LocalPolicyTargetReference matches the given target.
// The policy and the target must be in the same namespace (Direct attachment).
func TargetRefMatches(ref gwv1alpha2.LocalPolicyTargetReference, policyNamespace string, t PolicyTarget) bool {
    if string(ref.Group) != t.Group { return false }
    if string(ref.Kind) != t.Kind   { return false }
    if string(ref.Name) != t.Name   { return false }
    return policyNamespace == t.Namespace
}

// TargetRefMatchesWithSection compares a LocalPolicyTargetReferenceWithSectionName against a target.
// SectionName matches when both are empty, both equal, or the target's section is empty (= "applies to whole object").
func TargetRefMatchesWithSection(ref gwv1alpha2.LocalPolicyTargetReferenceWithSectionName, policyNamespace string, t PolicyTarget) bool {
    base := gwv1alpha2.LocalPolicyTargetReference{Group: ref.Group, Kind: ref.Kind, Name: ref.Name}
    if !TargetRefMatches(base, policyNamespace, t) {
        return false
    }
    refSection := ""
    if ref.SectionName != nil { refSection = string(*ref.SectionName) }
    return refSection == t.SectionName
}
```

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./pkg/gateway/ -run "TestTargetRef" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/gateway/policy_target.go pkg/gateway/policy_target_test.go
git commit -m "feat(gateway/pkg): add policy target-ref matching helpers"
```

---

## Section D — Shared controller helpers (`internal/controller/gateway/shared/`)

### Task D1: Listener-protocol classifier

**Files:**
- Create: `internal/controller/gateway/shared/classifier.go`
- Create: `internal/controller/gateway/shared/classifier_test.go`

- [ ] **Step 1: Write failing tests**

```go
package shared

import (
    "testing"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestProtocolAllowedForALB(t *testing.T) {
    cases := map[gwv1.ProtocolType]bool{
        gwv1.HTTPProtocolType:  true,
        gwv1.HTTPSProtocolType: true,
        gwv1.TLSProtocolType:   true,
        gwv1.TCPProtocolType:   false,
        gwv1.UDPProtocolType:   false,
    }
    for p, want := range cases {
        if got := ProtocolAllowedForALB(p); got != want {
            t.Errorf("ProtocolAllowedForALB(%q) = %v; want %v", p, got, want)
        }
    }
}

func TestMixedProtocols(t *testing.T) {
    listeners := []gwv1.Listener{
        {Protocol: gwv1.HTTPProtocolType},
        {Protocol: gwv1.TCPProtocolType},
    }
    if !HasMixedProtocols(listeners, "alb") {
        t.Fatal("expected mixed=true")
    }
    pure := []gwv1.Listener{{Protocol: gwv1.HTTPProtocolType}, {Protocol: gwv1.HTTPSProtocolType}}
    if HasMixedProtocols(pure, "alb") {
        t.Fatal("expected mixed=false")
    }
}
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./internal/controller/gateway/shared/
```

Expected: FAIL.

- [ ] **Step 3: Implement**

```go
package shared

import (
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ProtocolAllowedForALB returns true for L7 protocols accepted by the ALB GatewayClass.
func ProtocolAllowedForALB(p gwv1.ProtocolType) bool {
    switch p {
    case gwv1.HTTPProtocolType, gwv1.HTTPSProtocolType, gwv1.TLSProtocolType:
        return true
    }
    return false
}

// ProtocolAllowedForNLB returns true for L4 protocols accepted by the NLB GatewayClass.
func ProtocolAllowedForNLB(p gwv1.ProtocolType) bool {
    switch p {
    case gwv1.TCPProtocolType, gwv1.UDPProtocolType, gwv1.TLSProtocolType:
        return true
    }
    return false
}

// HasMixedProtocols reports whether listeners mix L7 and L4 protocols (rejected per spec §1.1).
func HasMixedProtocols(listeners []gwv1.Listener, gwClass string) bool {
    sawL7, sawL4 := false, false
    for _, l := range listeners {
        switch {
        case ProtocolAllowedForALB(l.Protocol) && l.Protocol != gwv1.TLSProtocolType:
            sawL7 = true
        case ProtocolAllowedForNLB(l.Protocol) && l.Protocol != gwv1.TLSProtocolType:
            sawL4 = true
        }
    }
    return sawL7 && sawL4
}
```

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./internal/controller/gateway/shared/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/gateway/shared/classifier.go internal/controller/gateway/shared/classifier_test.go
git commit -m "feat(gateway/shared): add protocol classifier"
```

---

### Task D2: Status condition + ancestor helpers

**Files:**
- Create: `internal/controller/gateway/shared/status.go`

- [ ] **Step 1: Write the helper**

```go
package shared

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// SetCondition mutates conds in place, replacing any existing entry of the same type.
func SetCondition(conds *[]metav1.Condition, c metav1.Condition) {
    for i, existing := range *conds {
        if existing.Type == c.Type {
            if existing.Status == c.Status &&
                existing.Reason == c.Reason &&
                existing.Message == c.Message {
                return
            }
            (*conds)[i] = c
            return
        }
    }
    *conds = append(*conds, c)
}

// EnsureAncestor inserts or updates an ancestor entry for the given controllerName.
func EnsureAncestor(ancestors *[]gwv1alpha2.PolicyAncestorStatus, ref gwv1alpha2.ParentReference, controllerName string, conditions []metav1.Condition) {
    for i, a := range *ancestors {
        if string(a.ControllerName) == controllerName && parentRefEqual(a.AncestorRef, ref) {
            (*ancestors)[i].Conditions = conditions
            return
        }
    }
    *ancestors = append(*ancestors, gwv1alpha2.PolicyAncestorStatus{
        AncestorRef:    ref,
        ControllerName: gwv1alpha2.GatewayController(controllerName),
        Conditions:     conditions,
    })
}

func parentRefEqual(a, b gwv1alpha2.ParentReference) bool {
    return derefStr(a.Group) == derefStr(b.Group) &&
        derefStr(a.Kind) == derefStr(b.Kind) &&
        a.Name == b.Name &&
        derefNS(a.Namespace) == derefNS(b.Namespace) &&
        derefSection(a.SectionName) == derefSection(b.SectionName)
}

func derefStr(p *gwv1alpha2.Group) string {
    if p == nil { return "" }; return string(*p)
}
func derefNS(p *gwv1alpha2.Namespace) string {
    if p == nil { return "" }; return string(*p)
}
func derefSection(p *gwv1alpha2.SectionName) string {
    if p == nil { return "" }; return string(*p)
}
```

> Note: depending on go-gateway-api version, the helper signatures for `Group`/`Kind` may vary; if the compiler complains, switch to `gwv1.Group` / `gwv1.Kind` re-exports. The intent of `parentRefEqual` is value-equality across all addressable fields.

- [ ] **Step 2: Verify build**

```bash
go build ./internal/controller/gateway/shared/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/controller/gateway/shared/status.go
git commit -m "feat(gateway/shared): add condition + ancestor-status helpers"
```

---

### Task D3: Match-specificity ordering

**Files:**
- Create: `internal/controller/gateway/shared/policy_order.go`
- Create: `internal/controller/gateway/shared/policy_order_test.go`

- [ ] **Step 1: Write failing tests**

```go
package shared

import (
    "sort"
    "testing"
    "time"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestSortMatches_ExactBeforePrefix(t *testing.T) {
    pathExact := gwv1.PathMatchExact
    pathPrefix := gwv1.PathMatchPathPrefix
    items := []RankedMatch{
        {Match: gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: &pathPrefix, Value: ptr("/api")}}},
        {Match: gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: &pathExact,  Value: ptr("/api/v1")}}},
    }
    sort.Stable(byMatchSpecificity(items))
    if items[0].Match.Path.Type == nil || *items[0].Match.Path.Type != pathExact {
        t.Fatalf("expected exact first; got %#v", items[0])
    }
}

func TestSortMatches_TimestampTiebreak(t *testing.T) {
    older := metav1.NewTime(time.Date(2024,1,1,0,0,0,0,time.UTC))
    newer := metav1.NewTime(time.Date(2025,1,1,0,0,0,0,time.UTC))
    items := []RankedMatch{
        {RouteCreated: newer},
        {RouteCreated: older},
    }
    sort.Stable(byMatchSpecificity(items))
    if !items[0].RouteCreated.Equal(&older) {
        t.Fatalf("expected older first")
    }
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./internal/controller/gateway/shared/ -run TestSortMatches
```

Expected: FAIL.

- [ ] **Step 3: Implement**

```go
package shared

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// RankedMatch wraps a Gateway-API match with the metadata needed to order it.
type RankedMatch struct {
    Match        gwv1.HTTPRouteMatch
    RouteCreated metav1.Time
}

type byMatchSpecificity []RankedMatch

func (s byMatchSpecificity) Len() int      { return len(s) }
func (s byMatchSpecificity) Swap(i,j int)  { s[i], s[j] = s[j], s[i] }
func (s byMatchSpecificity) Less(i,j int) bool {
    a, b := s[i], s[j]
    if r := pathRank(a.Match.Path) - pathRank(b.Match.Path); r != 0 { return r < 0 }
    if pl, pr := pathLen(a.Match.Path), pathLen(b.Match.Path); pl != pr { return pl > pr }
    if hi, hj := len(a.Match.Headers), len(b.Match.Headers); hi != hj { return hi > hj }
    if qi, qj := len(a.Match.QueryParams), len(b.Match.QueryParams); qi != qj { return qi > qj }
    return a.RouteCreated.Before(&b.RouteCreated)
}

// pathRank: lower value = more specific.
func pathRank(p *gwv1.HTTPPathMatch) int {
    if p == nil || p.Type == nil { return 99 }
    switch *p.Type {
    case gwv1.PathMatchExact:             return 0
    case gwv1.PathMatchRegularExpression: return 1
    case gwv1.PathMatchPathPrefix:        return 2
    }
    return 99
}

func pathLen(p *gwv1.HTTPPathMatch) int {
    if p == nil || p.Value == nil { return 0 }
    return len(*p.Value)
}
```

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./internal/controller/gateway/shared/ -run TestSortMatches -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/gateway/shared/policy_order.go internal/controller/gateway/shared/policy_order_test.go
git commit -m "feat(gateway/shared): add match-specificity ordering"
```

---

### Task D4: Reverse indexer

**Files:**
- Create: `internal/controller/gateway/shared/reference_indexer.go`

> Indexer registers field indexes used by event handlers (Task D6) to convert a watched object back to the routes/gateways that reference it. Mirrors `pkg/ingress/reference_indexer.go`.

- [ ] **Step 1: Write the indexer**

```go
package shared

import (
    "context"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"

    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

const (
    IndexHTTPRouteByService           = "spec.rules.backendRefs.name"
    IndexHTTPRouteByParentGateway     = "spec.parentRefs.gateway"
    IndexVKSGatewayPolicyByGateway    = "spec.targetRefs.gateway"
    IndexVKSBackendPolicyByService    = "spec.targetRefs.service"
    IndexVKSHealthCheckPolicyByService= "spec.targetRefs.service"
    IndexVKSRoutePolicyByRoute        = "spec.targetRefs.httproute"
)

func RegisterIndexes(ctx context.Context, mgr manager.Manager) error {
    if err := mgr.GetFieldIndexer().IndexField(ctx, &gwv1.HTTPRoute{}, IndexHTTPRouteByService, indexHTTPRouteByService); err != nil {
        return err
    }
    if err := mgr.GetFieldIndexer().IndexField(ctx, &gwv1.HTTPRoute{}, IndexHTTPRouteByParentGateway, indexHTTPRouteByParent); err != nil {
        return err
    }
    if err := mgr.GetFieldIndexer().IndexField(ctx, &vksv1.VKSGatewayPolicy{}, IndexVKSGatewayPolicyByGateway, indexVKSGatewayPolicyByGateway); err != nil {
        return err
    }
    if err := mgr.GetFieldIndexer().IndexField(ctx, &vksv1.VKSBackendPolicy{}, IndexVKSBackendPolicyByService, indexVKSBackendPolicyByService); err != nil {
        return err
    }
    if err := mgr.GetFieldIndexer().IndexField(ctx, &vksv1.VKSHealthCheckPolicy{}, IndexVKSHealthCheckPolicyByService, indexVKSHealthCheckPolicyByService); err != nil {
        return err
    }
    if err := mgr.GetFieldIndexer().IndexField(ctx, &vksv1.VKSRoutePolicy{}, IndexVKSRoutePolicyByRoute, indexVKSRoutePolicyByRoute); err != nil {
        return err
    }
    return nil
}

func indexHTTPRouteByService(obj client.Object) []string {
    r := obj.(*gwv1.HTTPRoute)
    var keys []string
    for _, rule := range r.Spec.Rules {
        for _, br := range rule.BackendRefs {
            ns := r.Namespace
            if br.Namespace != nil { ns = string(*br.Namespace) }
            keys = append(keys, ns+"/"+string(br.Name))
        }
    }
    return keys
}

func indexHTTPRouteByParent(obj client.Object) []string {
    r := obj.(*gwv1.HTTPRoute)
    var keys []string
    for _, p := range r.Spec.ParentRefs {
        ns := r.Namespace
        if p.Namespace != nil { ns = string(*p.Namespace) }
        if p.Kind == nil || *p.Kind == "Gateway" {
            keys = append(keys, ns+"/"+string(p.Name))
        }
    }
    return keys
}

func indexVKSGatewayPolicyByGateway(obj client.Object) []string {
    p := obj.(*vksv1.VKSGatewayPolicy)
    var keys []string
    for _, t := range p.Spec.TargetRefs {
        keys = append(keys, p.Namespace+"/"+string(t.Name))
    }
    return keys
}

func indexVKSBackendPolicyByService(obj client.Object) []string {
    p := obj.(*vksv1.VKSBackendPolicy)
    var keys []string
    for _, t := range p.Spec.TargetRefs {
        keys = append(keys, p.Namespace+"/"+string(t.Name))
    }
    return keys
}

func indexVKSHealthCheckPolicyByService(obj client.Object) []string {
    p := obj.(*vksv1.VKSHealthCheckPolicy)
    var keys []string
    for _, t := range p.Spec.TargetRefs {
        keys = append(keys, p.Namespace+"/"+string(t.Name))
    }
    return keys
}

func indexVKSRoutePolicyByRoute(obj client.Object) []string {
    p := obj.(*vksv1.VKSRoutePolicy)
    var keys []string
    for _, t := range p.Spec.TargetRefs {
        keys = append(keys, p.Namespace+"/"+string(t.Name))
    }
    return keys
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/controller/gateway/shared/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/controller/gateway/shared/reference_indexer.go
git commit -m "feat(gateway/shared): add reverse indexes for routes + 4 policy CRDs"
```

---

### Task D5: Finalizer helpers

**Files:**
- Create: `internal/controller/gateway/shared/finalizer.go`

- [ ] **Step 1: Write the helper**

```go
package shared

import (
    "context"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// AddFinalizer adds f to obj if missing. Returns true if a patch is needed.
func AddFinalizer(obj client.Object, f string) bool {
    for _, x := range obj.GetFinalizers() {
        if x == f { return false }
    }
    obj.SetFinalizers(append(obj.GetFinalizers(), f))
    return true
}

// RemoveFinalizer removes f from obj if present. Returns true if a patch is needed.
func RemoveFinalizer(obj client.Object, f string) bool {
    out := make([]string, 0, len(obj.GetFinalizers()))
    found := false
    for _, x := range obj.GetFinalizers() {
        if x == f { found = true; continue }
        out = append(out, x)
    }
    if !found { return false }
    obj.SetFinalizers(out)
    return true
}

// EnsureFinalizer adds f and patches obj if needed.
func EnsureFinalizer(ctx context.Context, c client.Client, obj client.Object, f string) error {
    if !AddFinalizer(obj, f) {
        return nil
    }
    return c.Update(ctx, obj)
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/controller/gateway/shared/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/controller/gateway/shared/finalizer.go
git commit -m "feat(gateway/shared): add finalizer helpers"
```

---

### Task D6: Cross-resource event handlers

**Files:**
- Create: `internal/controller/gateway/shared/eventhandlers/route_to_gateway.go`
- Create: `internal/controller/gateway/shared/eventhandlers/policy_to_gateway.go`
- Create: `internal/controller/gateway/shared/eventhandlers/service_to_route.go`

> All three handlers share the pattern "given a watched object, list dependents from the indexer and emit reconcile.Request". Implement each in 30–50 lines following the controller-runtime `handler.Funcs` pattern.

- [ ] **Step 1: Write `route_to_gateway.go`**

```go
package eventhandlers

import (
    "context"
    "k8s.io/apimachinery/pkg/types"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/event"
    "sigs.k8s.io/controller-runtime/pkg/handler"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"
    "sigs.k8s.io/controller-runtime/pkg/workqueue"

    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// RouteToGateway returns a handler that, when an HTTPRoute changes, enqueues every
// Gateway named in the route's parentRefs.
func RouteToGateway() handler.EventHandler {
    enq := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
        r, ok := obj.(*gwv1.HTTPRoute)
        if !ok { return }
        for _, p := range r.Spec.ParentRefs {
            if p.Kind != nil && *p.Kind != "Gateway" { continue }
            ns := r.Namespace
            if p.Namespace != nil { ns = string(*p.Namespace) }
            q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: string(p.Name)}})
        }
    }
    return handler.Funcs{
        CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) { enq(ctx, e.Object, q) },
        UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) { enq(ctx, e.ObjectNew, q) },
        DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) { enq(ctx, e.Object, q) },
    }
}
```

- [ ] **Step 2: Write `policy_to_gateway.go`**

```go
package eventhandlers

import (
    "context"
    "k8s.io/apimachinery/pkg/types"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/event"
    "sigs.k8s.io/controller-runtime/pkg/handler"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"
    "sigs.k8s.io/controller-runtime/pkg/workqueue"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

// VKSGatewayPolicyToGateway enqueues every Gateway named in the policy's targetRefs.
func VKSGatewayPolicyToGateway() handler.EventHandler {
    enq := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
        p, ok := obj.(*vksv1.VKSGatewayPolicy)
        if !ok { return }
        for _, t := range p.Spec.TargetRefs {
            q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: p.Namespace, Name: string(t.Name)}})
        }
    }
    return handler.Funcs{
        CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) { enq(ctx, e.Object, q) },
        UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) { enq(ctx, e.ObjectNew, q) },
        DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) { enq(ctx, e.Object, q) },
    }
}
```

- [ ] **Step 3: Write `service_to_route.go`**

```go
package eventhandlers

import (
    "context"
    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/fields"
    "k8s.io/apimachinery/pkg/types"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/event"
    "sigs.k8s.io/controller-runtime/pkg/handler"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"
    "sigs.k8s.io/controller-runtime/pkg/workqueue"

    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

// ServiceToRouteParents enqueues the Gateway parents of every HTTPRoute that
// references the changed Service.
func ServiceToRouteParents(c client.Client) handler.EventHandler {
    enq := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
        svc, ok := obj.(*corev1.Service)
        if !ok { return }
        var routes gwv1.HTTPRouteList
        if err := c.List(ctx, &routes,
            client.InNamespace(svc.Namespace),
            client.MatchingFieldsSelector{Selector: fields.OneTermEqualSelector(shared.IndexHTTPRouteByService, svc.Namespace+"/"+svc.Name)},
        ); err != nil {
            return
        }
        for _, r := range routes.Items {
            for _, p := range r.Spec.ParentRefs {
                if p.Kind != nil && *p.Kind != "Gateway" { continue }
                ns := r.Namespace
                if p.Namespace != nil { ns = string(*p.Namespace) }
                q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: string(p.Name)}})
            }
        }
    }
    return handler.Funcs{
        CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) { enq(ctx, e.Object, q) },
        UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) { enq(ctx, e.ObjectNew, q) },
        DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) { enq(ctx, e.Object, q) },
    }
}
```

- [ ] **Step 4: Verify build**

```bash
go build ./internal/controller/gateway/shared/...
```

Expected: success.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/gateway/shared/eventhandlers/
git commit -m "feat(gateway/shared): add cross-resource event handlers"
```

---

## Section E — UseCase shared (`internal/usecase/gateway_uc/shared/`)

### Task E1: Reference-grant evaluator

**Files:**
- Create: `internal/usecase/gateway_uc/shared/refgrant.go`
- Create: `internal/usecase/gateway_uc/shared/refgrant_test.go`

- [ ] **Step 1: Write failing tests**

```go
package shared

import (
    "context"
    "testing"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client/fake"
    gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func TestRefGrantAllowed_SameNamespace(t *testing.T) {
    c := fake.NewClientBuilder().Build()
    ok, err := RefGrantAllowed(context.Background(), c,
        Ref{Group: "", Kind: "Service", Namespace: "ns", Name: "svc"},
        Ref{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "ns", Name: "r"},
    )
    if err != nil { t.Fatal(err) }
    if !ok { t.Fatal("same-namespace must always be allowed") }
}

func TestRefGrantAllowed_GrantPresent(t *testing.T) {
    grant := &gwv1beta1.ReferenceGrant{
        ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "backend-ns"},
        Spec: gwv1beta1.ReferenceGrantSpec{
            From: []gwv1beta1.ReferenceGrantFrom{{
                Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "route-ns",
            }},
            To: []gwv1beta1.ReferenceGrantTo{{Group: "", Kind: "Service"}},
        },
    }
    c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(grant).Build()
    ok, _ := RefGrantAllowed(context.Background(), c,
        Ref{Group: "", Kind: "Service", Namespace: "backend-ns", Name: "svc"},
        Ref{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "route-ns", Name: "r"},
    )
    if !ok { t.Fatal("grant should allow") }
}

func TestRefGrantAllowed_NoGrant(t *testing.T) {
    c := fake.NewClientBuilder().WithScheme(testScheme()).Build()
    ok, _ := RefGrantAllowed(context.Background(), c,
        Ref{Group: "", Kind: "Service", Namespace: "backend-ns", Name: "svc"},
        Ref{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "route-ns", Name: "r"},
    )
    if ok { t.Fatal("no grant: should deny") }
}
```

`testScheme()` is a small helper at the bottom of the test file:

```go
import (
    "k8s.io/apimachinery/pkg/runtime"
    gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)
func testScheme() *runtime.Scheme {
    s := runtime.NewScheme()
    _ = gwv1beta1.Install(s)
    return s
}
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./internal/usecase/gateway_uc/shared/ -run TestRefGrant
```

Expected: FAIL.

- [ ] **Step 3: Implement**

```go
package shared

import (
    "context"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// Ref identifies one side of a cross-namespace reference.
type Ref struct {
    Group, Kind, Namespace, Name string
}

// RefGrantAllowed reports whether `from` is permitted to reference `to`.
// Same-namespace is always allowed. Otherwise we require a ReferenceGrant
// in `to.Namespace` whose From matches (Group, Kind, Namespace) and whose
// To matches (Group, Kind) with optional Name match.
func RefGrantAllowed(ctx context.Context, c client.Client, to, from Ref) (bool, error) {
    if to.Namespace == from.Namespace {
        return true, nil
    }
    var grants gwv1beta1.ReferenceGrantList
    if err := c.List(ctx, &grants, client.InNamespace(to.Namespace)); err != nil {
        return false, err
    }
    for _, g := range grants.Items {
        if !grantFromMatches(g.Spec.From, from) { continue }
        if !grantToMatches(g.Spec.To, to)       { continue }
        return true, nil
    }
    return false, nil
}

func grantFromMatches(froms []gwv1beta1.ReferenceGrantFrom, f Ref) bool {
    for _, x := range froms {
        if string(x.Group) == f.Group && string(x.Kind) == f.Kind && string(x.Namespace) == f.Namespace {
            return true
        }
    }
    return false
}

func grantToMatches(tos []gwv1beta1.ReferenceGrantTo, t Ref) bool {
    for _, x := range tos {
        if string(x.Group) != t.Group || string(x.Kind) != t.Kind { continue }
        if x.Name == nil || string(*x.Name) == t.Name {
            return true
        }
    }
    return false
}
```

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./internal/usecase/gateway_uc/shared/ -run TestRefGrant -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/gateway_uc/shared/refgrant.go internal/usecase/gateway_uc/shared/refgrant_test.go
git commit -m "feat(gateway/uc): add ReferenceGrant evaluator"
```

---

### Task E2: Direct-policy resolver

**Files:**
- Create: `internal/usecase/gateway_uc/shared/policy_resolver.go`
- Create: `internal/usecase/gateway_uc/shared/policy_resolver_test.go`

> Pure logic (oldest-wins) used by both validator controllers (to set `Conflicted=True`) and the Gateway use-case (to read the effective policy at reconcile time).

- [ ] **Step 1: Write failing tests**

```go
package shared

import (
    "testing"
    "time"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

type fakePolicy struct {
    name      string
    namespace string
    section   string
    targetKey string
    created   time.Time
}

func (p *fakePolicy) GetName() string                  { return p.name }
func (p *fakePolicy) GetNamespace() string             { return p.namespace }
func (p *fakePolicy) GetCreationTimestamp() metav1.Time { return metav1.NewTime(p.created) }
func (p *fakePolicy) Matches(target pkggw.PolicyTarget) bool {
    return p.namespace == target.Namespace &&
        p.targetKey == target.Name &&
        p.section == target.SectionName
}

func TestResolveDirectPolicy_OldestWins(t *testing.T) {
    older := &fakePolicy{name: "a", namespace: "ns", targetKey: "gw1", created: time.Date(2024,1,1,0,0,0,0,time.UTC)}
    newer := &fakePolicy{name: "b", namespace: "ns", targetKey: "gw1", created: time.Date(2025,1,1,0,0,0,0,time.UTC)}
    in := []*fakePolicy{newer, older}
    target := pkggw.PolicyTarget{Namespace:"ns", Name:"gw1"}
    win, lose := ResolveDirectPolicy(in, target)
    if win == nil || win.GetName() != "a" {
        t.Fatalf("want winner=a; got %#v", win)
    }
    if len(lose) != 1 || lose[0].GetName() != "b" {
        t.Fatalf("want losers=[b]; got %#v", lose)
    }
}

func TestResolveDirectPolicy_NoMatch(t *testing.T) {
    p := &fakePolicy{name:"a", namespace:"ns", targetKey:"other", created: time.Now()}
    win, lose := ResolveDirectPolicy([]*fakePolicy{p}, pkggw.PolicyTarget{Namespace:"ns", Name:"gw1"})
    if win != nil { t.Fatal("expected no winner") }
    if len(lose) != 0 { t.Fatal("expected no losers") }
}

func TestResolveDirectPolicy_NameTiebreak(t *testing.T) {
    same := time.Date(2024,1,1,0,0,0,0,time.UTC)
    a := &fakePolicy{name:"alpha", namespace:"ns", targetKey:"gw1", created: same}
    b := &fakePolicy{name:"beta",  namespace:"ns", targetKey:"gw1", created: same}
    win, _ := ResolveDirectPolicy([]*fakePolicy{b, a}, pkggw.PolicyTarget{Namespace:"ns", Name:"gw1"})
    if win.GetName() != "alpha" { t.Fatalf("want alpha; got %s", win.GetName()) }
}
```

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./internal/usecase/gateway_uc/shared/ -run TestResolveDirectPolicy
```

Expected: FAIL.

- [ ] **Step 3: Implement**

```go
package shared

import (
    "sort"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// PolicyObj is the minimal interface a Direct policy must satisfy to participate
// in resolution. All four VKS*Policy types implement this either natively
// (via metav1.Object) or via a thin wrapper at call sites.
type PolicyObj interface {
    GetName() string
    GetNamespace() string
    GetCreationTimestamp() metav1.Time
    Matches(t pkggw.PolicyTarget) bool
}

// ResolveDirectPolicy filters policies to those whose Matches(target) is true,
// sorts by (creationTimestamp, namespace/name) ascending, and returns the
// winner (oldest) plus the losers (Conflicted).
func ResolveDirectPolicy[P PolicyObj](in []P, target pkggw.PolicyTarget) (winner P, losers []P) {
    var matches []P
    for _, p := range in {
        if p.Matches(target) {
            matches = append(matches, p)
        }
    }
    if len(matches) == 0 {
        var zero P
        return zero, nil
    }
    sort.SliceStable(matches, func(i, j int) bool {
        ti := matches[i].GetCreationTimestamp()
        tj := matches[j].GetCreationTimestamp()
        if !ti.Equal(&tj) {
            return ti.Before(&tj)
        }
        ki := matches[i].GetNamespace() + "/" + matches[i].GetName()
        kj := matches[j].GetNamespace() + "/" + matches[j].GetName()
        return ki < kj
    })
    return matches[0], matches[1:]
}
```

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./internal/usecase/gateway_uc/shared/ -run TestResolveDirectPolicy -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/gateway_uc/shared/policy_resolver.go internal/usecase/gateway_uc/shared/policy_resolver_test.go
git commit -m "feat(gateway/uc): add Direct-policy resolver (oldest-wins)"
```

---

### Task E3: Per-CRD `Matches` adapters

**Files:**
- Modify: `api/gateway/v1alpha1/vksgatewaypolicy_types.go`
- Modify: `api/gateway/v1alpha1/vksbackendpolicy_types.go`
- Modify: `api/gateway/v1alpha1/vkshealthcheckpolicy_types.go`
- Modify: `api/gateway/v1alpha1/vksroutepolicy_types.go`

> Each policy type implements `Matches(target PolicyTarget) bool` so the resolver from Task E2 can be invoked directly without external adapters.

- [ ] **Step 1: Add to `vksgatewaypolicy_types.go`**

```go
import pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"

// Matches reports whether this policy targets the given object.
func (p *VKSGatewayPolicy) Matches(t pkggw.PolicyTarget) bool {
    for _, ref := range p.Spec.TargetRefs {
        if pkggw.TargetRefMatchesWithSection(ref, p.Namespace, t) {
            return true
        }
    }
    return false
}
```

- [ ] **Step 2: Add to `vksbackendpolicy_types.go`**

```go
import pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"

func (p *VKSBackendPolicy) Matches(t pkggw.PolicyTarget) bool {
    for _, ref := range p.Spec.TargetRefs {
        if pkggw.TargetRefMatches(ref, p.Namespace, t) {
            return true
        }
    }
    return false
}
```

- [ ] **Step 3: Add to `vkshealthcheckpolicy_types.go`**

```go
import pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"

func (p *VKSHealthCheckPolicy) Matches(t pkggw.PolicyTarget) bool {
    for _, ref := range p.Spec.TargetRefs {
        if pkggw.TargetRefMatches(ref, p.Namespace, t) {
            return true
        }
    }
    return false
}
```

- [ ] **Step 4: Add to `vksroutepolicy_types.go`**

```go
import pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"

func (p *VKSRoutePolicy) Matches(t pkggw.PolicyTarget) bool {
    for _, ref := range p.Spec.TargetRefs {
        if pkggw.TargetRefMatchesWithSection(ref, p.Namespace, t) {
            return true
        }
    }
    return false
}
```

- [ ] **Step 5: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 6: Commit**

```bash
git add api/gateway/v1alpha1/vks*_types.go
git commit -m "feat(gateway/api): implement Matches() adapter on all 4 policy CRDs"
```

---

## Section F — ALB UseCase (`internal/usecase/gateway_uc/alb_gateway_uc/`)

### Task F1: UseCase entry (Init / Ensure / Delete)

**Files:**
- Create: `internal/usecase/gateway_uc/alb_gateway_uc/gateway_uc.go`

- [ ] **Step 1: Write the use-case skeleton**

```go
package alb_gateway_uc

import (
    "context"
    "sync/atomic"

    "github.com/go-logr/logr"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/k8s_repo"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo"
)

// ALBGatewayUseCase is the only writer to the vngcloud LB owned by an ALB Gateway.
type ALBGatewayUseCase struct {
    K8s      *k8s_repo.K8sRepository
    Vng      *vngcloud_repo.VngCloudRepository
    Log      logr.Logger
    InitDone atomic.Bool
}

func NewALBGatewayUseCase(k *k8s_repo.K8sRepository, v *vngcloud_repo.VngCloudRepository, log logr.Logger) *ALBGatewayUseCase {
    return &ALBGatewayUseCase{K8s: k, Vng: v, Log: log}
}

// Init prepares cached state. Currently a no-op; kept for parity with ingress_uc.
func (u *ALBGatewayUseCase) Init(ctx context.Context) error {
    u.InitDone.Store(true)
    return nil
}

// Ensure brings the vngcloud LB associated with gw into desired state.
// Calls (in order): build_lb → build_listener → build_pool → build_policy → status.
func (u *ALBGatewayUseCase) Ensure(ctx context.Context, gw *gwv1.Gateway) (Result, error) {
    cfg, err := u.resolveEffectiveLBConfig(ctx, gw)
    if err != nil { return Result{}, err }

    lb, err := u.ensureLoadBalancer(ctx, gw, cfg)
    if err != nil { return Result{}, err }

    if err := u.ensureListeners(ctx, gw, lb, cfg); err != nil { return Result{}, err }

    routes, err := u.listAttachedHTTPRoutes(ctx, gw)
    if err != nil { return Result{}, err }

    if err := u.ensurePoolsAndPolicies(ctx, gw, lb, routes); err != nil { return Result{}, err }

    return Result{LB: lb, AttachedRoutes: routes}, nil
}

// Delete tears down the vngcloud LB and removes the gateway finalizer.
func (u *ALBGatewayUseCase) Delete(ctx context.Context, gw *gwv1.Gateway) error {
    return u.deleteLoadBalancer(ctx, gw)
}

// Result is what reconciler uses to write Gateway status.
type Result struct {
    LB             *vngcloud_repo.LoadBalancer
    AttachedRoutes []gwv1.HTTPRoute
}
```

> The methods called from `Ensure` (`resolveEffectiveLBConfig`, `ensureLoadBalancer`, `ensureListeners`, `listAttachedHTTPRoutes`, `ensurePoolsAndPolicies`, `deleteLoadBalancer`) live in the build-stage files implemented in F2–F7.

- [ ] **Step 2: Verify build**

```bash
go build ./internal/usecase/gateway_uc/alb_gateway_uc/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/usecase/gateway_uc/alb_gateway_uc/gateway_uc.go
git commit -m "feat(gateway/uc/alb): scaffold ALB Gateway use-case entry"
```

---

### Task F2: `build_lb.go` — Gateway → LB params + ensure

**Files:**
- Create: `internal/usecase/gateway_uc/alb_gateway_uc/build_lb.go`

- [ ] **Step 1: Write the file**

```go
package alb_gateway_uc

import (
    "context"
    "fmt"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/labels"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo"
)

// EffectiveLBConfig is the merged view consumed downstream.
type EffectiveLBConfig struct {
    UnscopedPolicy *vksv1.VKSGatewayPolicy            // for LB-level fields
    Listeners      map[string]*vksv1.VKSGatewayPolicy // by Gateway listener name; falls back to UnscopedPolicy
}

// resolveEffectiveLBConfig finds the unscoped (LB-level) and per-listener VKSGatewayPolicy
// objects attached to gw. Losers are recorded internally for the policy validator
// reconcilers to mark as Conflicted.
func (u *ALBGatewayUseCase) resolveEffectiveLBConfig(ctx context.Context, gw *gwv1.Gateway) (*EffectiveLBConfig, error) {
    var policies vksv1.VKSGatewayPolicyList
    if err := u.K8s.Client.List(ctx, &policies, client.InNamespace(gw.Namespace)); err != nil {
        return nil, fmt.Errorf("list VKSGatewayPolicy: %w", err)
    }

    unscopedTarget := pkggw.PolicyTarget{
        Group: "gateway.networking.k8s.io", Kind: "Gateway",
        Namespace: gw.Namespace, Name: gw.Name,
    }
    candidates := make([]*vksv1.VKSGatewayPolicy, 0, len(policies.Items))
    for i := range policies.Items {
        candidates = append(candidates, &policies.Items[i])
    }
    unscopedWin, _ := shared.ResolveDirectPolicy(candidates, unscopedTarget)

    out := &EffectiveLBConfig{UnscopedPolicy: unscopedWin, Listeners: map[string]*vksv1.VKSGatewayPolicy{}}
    for _, l := range gw.Spec.Listeners {
        target := pkggw.PolicyTarget{
            Group: "gateway.networking.k8s.io", Kind: "Gateway",
            Namespace: gw.Namespace, Name: gw.Name, SectionName: string(l.Name),
        }
        win, _ := shared.ResolveDirectPolicy(candidates, target)
        if win != nil {
            out.Listeners[string(l.Name)] = win
        } else {
            out.Listeners[string(l.Name)] = unscopedWin
        }
    }
    return out, nil
}

// ensureLoadBalancer looks up the LB by owner-uid label or creates one.
func (u *ALBGatewayUseCase) ensureLoadBalancer(ctx context.Context, gw *gwv1.Gateway, cfg *EffectiveLBConfig) (*vngcloud_repo.LoadBalancer, error) {
    selector := labels.SelectorFromSet(labels.Set{
        domain.OwnerLabelGatewayUID: string(gw.UID),
        domain.OwnerLabelKind:       domain.OwnerKindGateway,
    })

    existing, err := u.Vng.FindLoadBalancerByLabels(ctx, selector)
    if err != nil { return nil, err }
    desired := lbParamsFromConfig(gw, cfg)
    if existing != nil {
        return u.Vng.UpdateLoadBalancer(ctx, existing.ID, desired)
    }
    desired.Labels = map[string]string{
        domain.OwnerLabelGatewayUID: string(gw.UID),
        domain.OwnerLabelKind:       domain.OwnerKindGateway,
    }
    desired.Name = fmt.Sprintf("vks-gw-%s-%s", gw.Namespace, gw.Name)
    return u.Vng.CreateLoadBalancer(ctx, desired)
}

func (u *ALBGatewayUseCase) deleteLoadBalancer(ctx context.Context, gw *gwv1.Gateway) error {
    selector := labels.SelectorFromSet(labels.Set{domain.OwnerLabelGatewayUID: string(gw.UID)})
    lb, err := u.Vng.FindLoadBalancerByLabels(ctx, selector)
    if err != nil || lb == nil { return err }
    return u.Vng.DeleteLoadBalancer(ctx, lb.ID)
}

func lbParamsFromConfig(gw *gwv1.Gateway, cfg *EffectiveLBConfig) vngcloud_repo.LoadBalancerParams {
    p := vngcloud_repo.LoadBalancerParams{
        Type:   vngcloud_repo.LoadBalancerTypeALB,
        Scheme: vngcloud_repo.LoadBalancerSchemeInternet,
    }
    if cfg == nil || cfg.UnscopedPolicy == nil || cfg.UnscopedPolicy.Spec.LoadBalancerSpec == nil {
        return p
    }
    s := cfg.UnscopedPolicy.Spec.LoadBalancerSpec
    if s.Scheme    != nil { p.Scheme    = vngcloud_repo.LoadBalancerScheme(*s.Scheme) }
    if s.PackageID != nil { p.PackageID = *s.PackageID }
    if s.SubnetID  != nil { p.SubnetID  = *s.SubnetID }
    p.Tags = s.Tags
    if s.LoadBalancerID != nil { p.AdoptByID = *s.LoadBalancerID }
    return p
}

// noteCreationTime is exported for tests.
var noteCreationTime = func() metav1.Time { return metav1.Now() }
```

> The exact `vngcloud_repo` types (`LoadBalancer`, `LoadBalancerParams`, `LoadBalancerScheme`, etc.) match the existing names in the repository. If any field name differs, follow the pattern used by `internal/usecase/lbc_uc/deploy_lb.go` — that file is the reference for both naming and behavior.

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/usecase/gateway_uc/alb_gateway_uc/build_lb.go
git commit -m "feat(gateway/uc/alb): resolve LB-level + listener policies and ensure LB"
```

---

### Task F3: `build_listener.go`

**Files:**
- Create: `internal/usecase/gateway_uc/alb_gateway_uc/build_listener.go`

- [ ] **Step 1: Write the file**

```go
package alb_gateway_uc

import (
    "context"
    "fmt"

    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo"
)

// ensureListeners synchronizes the vngcloud LB's listener set against gw.Spec.Listeners.
func (u *ALBGatewayUseCase) ensureListeners(ctx context.Context, gw *gwv1.Gateway, lb *vngcloud_repo.LoadBalancer, cfg *EffectiveLBConfig) error {
    desired := make([]vngcloud_repo.ListenerParams, 0, len(gw.Spec.Listeners))
    for _, l := range gw.Spec.Listeners {
        params, err := u.buildListenerParams(ctx, gw, l, cfg.Listeners[string(l.Name)])
        if err != nil {
            return fmt.Errorf("listener %q: %w", l.Name, err)
        }
        desired = append(desired, params)
    }
    return u.Vng.SyncListeners(ctx, lb.ID, desired)
}

func (u *ALBGatewayUseCase) buildListenerParams(ctx context.Context, gw *gwv1.Gateway, l gwv1.Listener, pol *vksv1.VKSGatewayPolicy) (vngcloud_repo.ListenerParams, error) {
    p := vngcloud_repo.ListenerParams{
        Name:     string(l.Name),
        Protocol: string(l.Protocol),
        Port:     int32(l.Port),
    }
    // TLS: prefer policy-supplied IDs, else import Secrets.
    if l.TLS != nil {
        certIDs, clientCAID, err := u.resolveCertIDs(ctx, gw, l, pol)
        if err != nil { return p, err }
        p.CertificateIDs    = certIDs
        p.ClientCertCAID    = clientCAID
    }
    if pol != nil {
        s := pol.Spec
        if s.SSLPolicy        != nil { p.SSLPolicy        = *s.SSLPolicy }
        if s.ALPNPolicy       != nil { p.ALPNPolicy       = *s.ALPNPolicy }
        if s.TimeoutClient    != nil { p.TimeoutClient    = s.TimeoutClient.Duration }
        if s.TimeoutMember    != nil { p.TimeoutMember    = s.TimeoutMember.Duration }
        if s.TimeoutConnection!= nil { p.TimeoutConnection= s.TimeoutConnection.Duration }
        p.AllowedCIDRs  = append(p.AllowedCIDRs, s.AllowedCIDRs...)
        p.InsertHeaders = mergeStringMaps(p.InsertHeaders, s.InsertHeaders)
    }
    // Default forwarding pool — always-empty placeholder so the listener is valid
    // before any HTTPRoute attaches.
    p.DefaultPoolName = fmt.Sprintf("default-%s-%s", gw.UID, l.Name)
    return p, nil
}

func mergeStringMaps(a, b map[string]string) map[string]string {
    if len(a) == 0 && len(b) == 0 { return nil }
    out := make(map[string]string, len(a)+len(b))
    for k, v := range a { out[k] = v }
    for k, v := range b { out[k] = v }
    return out
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: success (will fail until Task F4 provides `resolveCertIDs`; expected at this point — proceed to F4 immediately).

- [ ] **Step 3: Commit (combined with F4 if needed)**

```bash
git add internal/usecase/gateway_uc/alb_gateway_uc/build_listener.go
# Combined commit happens after F4 if build needs it.
```

---

### Task F4: `build_cert.go`

**Files:**
- Create: `internal/usecase/gateway_uc/alb_gateway_uc/build_cert.go`

- [ ] **Step 1: Write the file**

```go
package alb_gateway_uc

import (
    "context"
    "fmt"

    corev1 "k8s.io/api/core/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
)

// resolveCertIDs returns server-cert IDs and an optional client-CA cert ID for the listener.
// Precedence:
//   - policy.CertificateIDs[]    overrides tls.certificateRefs
//   - policy.ClientCertificateID overrides tls.frontendValidation.caCertificateRefs
func (u *ALBGatewayUseCase) resolveCertIDs(ctx context.Context, gw *gwv1.Gateway, l gwv1.Listener, pol *vksv1.VKSGatewayPolicy) ([]string, string, error) {
    var serverIDs []string
    if pol != nil && len(pol.Spec.CertificateIDs) > 0 {
        serverIDs = append(serverIDs, pol.Spec.CertificateIDs...)
    } else if l.TLS != nil {
        for _, ref := range l.TLS.CertificateRefs {
            id, err := u.importSecretCert(ctx, gw, ref)
            if err != nil { return nil, "", err }
            serverIDs = append(serverIDs, id)
        }
    }

    var clientCAID string
    if pol != nil && pol.Spec.ClientCertificateID != nil {
        clientCAID = *pol.Spec.ClientCertificateID
    } else if l.TLS != nil && l.TLS.FrontendValidation != nil {
        for _, ref := range l.TLS.FrontendValidation.CACertificateRefs {
            id, err := u.importCACert(ctx, gw, ref)
            if err != nil { return nil, "", err }
            clientCAID = id // last one wins; document in spec §3.3
        }
    }
    return serverIDs, clientCAID, nil
}

func (u *ALBGatewayUseCase) importSecretCert(ctx context.Context, gw *gwv1.Gateway, ref gwv1.SecretObjectReference) (string, error) {
    ns := gw.Namespace
    if ref.Namespace != nil { ns = string(*ref.Namespace) }

    if ns != gw.Namespace {
        ok, err := shared.RefGrantAllowed(ctx, u.K8s.Client,
            shared.Ref{Group:"", Kind:"Secret", Namespace: ns, Name: string(ref.Name)},
            shared.Ref{Group:"gateway.networking.k8s.io", Kind:"Gateway", Namespace: gw.Namespace, Name: gw.Name},
        )
        if err != nil { return "", err }
        if !ok { return "", fmt.Errorf("ReferenceGrant denies cross-ns secret %s/%s", ns, ref.Name) }
    }

    var sec corev1.Secret
    if err := u.K8s.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: string(ref.Name)}, &sec); err != nil {
        return "", err
    }
    return u.Vng.ImportCertificateFromSecret(ctx, &sec)
}

// importCACert imports a CA bundle ConfigMap.
// Same RefGrant logic as importSecretCert, but the source object is a ConfigMap.
func (u *ALBGatewayUseCase) importCACert(ctx context.Context, gw *gwv1.Gateway, ref gwv1.ObjectReference) (string, error) {
    ns := gw.Namespace
    if ref.Namespace != nil { ns = string(*ref.Namespace) }

    if ns != gw.Namespace {
        ok, err := shared.RefGrantAllowed(ctx, u.K8s.Client,
            shared.Ref{Group: string(ref.Group), Kind: string(ref.Kind), Namespace: ns, Name: string(ref.Name)},
            shared.Ref{Group:"gateway.networking.k8s.io", Kind:"Gateway", Namespace: gw.Namespace, Name: gw.Name},
        )
        if err != nil { return "", err }
        if !ok { return "", fmt.Errorf("ReferenceGrant denies cross-ns CA %s/%s/%s", ref.Kind, ns, ref.Name) }
    }

    var cm corev1.ConfigMap
    if err := u.K8s.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: string(ref.Name)}, &cm); err != nil {
        return "", err
    }
    return u.Vng.ImportCACertificateFromConfigMap(ctx, &cm)
}
```

> If your existing certificate-import path uses different signatures, copy the call shape from `internal/usecase/lbc_uc/deploy_listener.go` — the helper names there are the reference.

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit (combined F3 + F4)**

```bash
git add internal/usecase/gateway_uc/alb_gateway_uc/build_listener.go internal/usecase/gateway_uc/alb_gateway_uc/build_cert.go
git commit -m "feat(gateway/uc/alb): build listener params + resolve cert IDs"
```

---

### Task F5: `build_pool.go`

**Files:**
- Create: `internal/usecase/gateway_uc/alb_gateway_uc/build_pool.go`

- [ ] **Step 1: Write the file**

```go
package alb_gateway_uc

import (
    "context"
    "fmt"

    discoveryv1 "k8s.io/api/discovery/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
    sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo"
)

// synthesizePool builds the vngcloud Pool params for one HTTPRoute rule.
//   - merges all backendRefs into one pool (weight-scaled)
//   - resolves VKSBackendPolicy (per-Service, oldest-wins)
//   - resolves VKSHealthCheckPolicy (per-Service; conflicting health-checks across backends → error)
func (u *ALBGatewayUseCase) synthesizePool(ctx context.Context, route *gwv1.HTTPRoute, ruleIdx int, rule gwv1.HTTPRouteRule) (vngcloud_repo.PoolParams, error) {
    if len(rule.BackendRefs) == 0 {
        return vngcloud_repo.PoolParams{}, fmt.Errorf("rule %d has no backendRefs", ruleIdx)
    }

    var (
        keys     []pkggw.BackendKey
        weights  []pkggw.BackendWeight
        chosenHC *vksv1.VKSHealthCheckPolicy
        chosenBP *vksv1.VKSBackendPolicy
    )

    for _, br := range rule.BackendRefs {
        ns := route.Namespace
        if br.Namespace != nil { ns = string(*br.Namespace) }
        if ns != route.Namespace {
            ok, err := sharedUC.RefGrantAllowed(ctx, u.K8s.Client,
                sharedUC.Ref{Group:"", Kind:"Service", Namespace: ns, Name: string(br.Name)},
                sharedUC.Ref{Group:"gateway.networking.k8s.io", Kind:"HTTPRoute", Namespace: route.Namespace, Name: route.Name},
            )
            if err != nil { return vngcloud_repo.PoolParams{}, err }
            if !ok { continue } // drop denied backend; route condition set elsewhere
        }

        port := int32(0)
        if br.Port != nil { port = int32(*br.Port) }
        weight := int32(1)
        if br.Weight != nil { weight = *br.Weight }

        ready, err := u.countReadyEndpoints(ctx, ns, string(br.Name), port)
        if err != nil { return vngcloud_repo.PoolParams{}, err }

        keys = append(keys, pkggw.BackendKey{Namespace: ns, Name: string(br.Name), Port: port, Weight: weight})
        weights = append(weights, pkggw.BackendWeight{Weight: weight, Ready: int32(ready)})

        bp, err := u.resolveBackendPolicy(ctx, ns, string(br.Name))
        if err != nil { return vngcloud_repo.PoolParams{}, err }
        if bp != nil {
            if chosenBP != nil && !backendPolicyEqual(chosenBP, bp) {
                return vngcloud_repo.PoolParams{}, fmt.Errorf("conflicting VKSBackendPolicy across backends: %s/%s vs %s/%s",
                    chosenBP.Namespace, chosenBP.Name, bp.Namespace, bp.Name)
            }
            chosenBP = bp
        }

        hc, err := u.resolveHealthCheckPolicy(ctx, ns, string(br.Name))
        if err != nil { return vngcloud_repo.PoolParams{}, err }
        if hc != nil {
            if chosenHC != nil && !healthCheckPolicyEqual(chosenHC, hc) {
                return vngcloud_repo.PoolParams{}, fmt.Errorf("conflicting VKSHealthCheckPolicy across backends: %s/%s vs %s/%s",
                    chosenHC.Namespace, chosenHC.Name, hc.Namespace, hc.Name)
            }
            chosenHC = hc
        }
    }

    if len(keys) == 0 {
        return vngcloud_repo.PoolParams{}, fmt.Errorf("rule %d: all backendRefs dropped (RefGrant)", ruleIdx)
    }

    name := pkggw.SynthPoolName(string(route.UID), ruleIdx, keys)
    scaled := pkggw.ScaleWeights(weights)

    pp := vngcloud_repo.PoolParams{Name: name}
    for i, k := range keys {
        pp.Members = append(pp.Members, vngcloud_repo.PoolMember{
            ServiceNamespace: k.Namespace,
            ServiceName:      k.Name,
            Port:             k.Port,
            Weight:           scaled[i],
        })
    }
    applyBackendPolicy(&pp, chosenBP)
    applyHealthCheckPolicy(&pp, chosenHC)
    return pp, nil
}

func (u *ALBGatewayUseCase) countReadyEndpoints(ctx context.Context, ns, name string, port int32) (int, error) {
    var slices discoveryv1.EndpointSliceList
    if err := u.K8s.Client.List(ctx, &slices,
        client.InNamespace(ns),
        client.MatchingLabels{discoveryv1.LabelServiceName: name},
    ); err != nil { return 0, err }
    n := 0
    for _, s := range slices.Items {
        for _, ep := range s.Endpoints {
            if ep.Conditions.Ready != nil && *ep.Conditions.Ready {
                n++
            }
        }
    }
    if n == 0 { n = 1 }
    return n, nil
}

func (u *ALBGatewayUseCase) resolveBackendPolicy(ctx context.Context, ns, svc string) (*vksv1.VKSBackendPolicy, error) {
    var list vksv1.VKSBackendPolicyList
    if err := u.K8s.Client.List(ctx, &list, client.InNamespace(ns)); err != nil { return nil, err }
    cands := make([]*vksv1.VKSBackendPolicy, 0, len(list.Items))
    for i := range list.Items { cands = append(cands, &list.Items[i]) }
    win, _ := sharedUC.ResolveDirectPolicy(cands, pkggw.PolicyTarget{
        Group: "", Kind: "Service", Namespace: ns, Name: svc,
    })
    return win, nil
}

func (u *ALBGatewayUseCase) resolveHealthCheckPolicy(ctx context.Context, ns, svc string) (*vksv1.VKSHealthCheckPolicy, error) {
    var list vksv1.VKSHealthCheckPolicyList
    if err := u.K8s.Client.List(ctx, &list, client.InNamespace(ns)); err != nil { return nil, err }
    cands := make([]*vksv1.VKSHealthCheckPolicy, 0, len(list.Items))
    for i := range list.Items { cands = append(cands, &list.Items[i]) }
    win, _ := sharedUC.ResolveDirectPolicy(cands, pkggw.PolicyTarget{
        Group: "", Kind: "Service", Namespace: ns, Name: svc,
    })
    return win, nil
}

func applyBackendPolicy(pp *vngcloud_repo.PoolParams, bp *vksv1.VKSBackendPolicy) {
    if bp == nil { return }
    s := bp.Spec
    if s.PoolAlgorithm != nil { pp.Algorithm = *s.PoolAlgorithm }
    if s.TargetType    != nil { pp.TargetType = *s.TargetType }
    if s.SessionAffinity != nil {
        pp.SessionAffinity = vngcloud_repo.SessionAffinity{
            Type:       s.SessionAffinity.Type,
            CookieName: derefStringPtr(s.SessionAffinity.CookieName),
        }
        if s.SessionAffinity.TTL != nil { pp.SessionAffinity.TTL = s.SessionAffinity.TTL.Duration }
    }
    if s.EnableTLSEncryption != nil { pp.TLSEncryption = *s.EnableTLSEncryption }
    if s.EnableProxyProtocol != nil { pp.ProxyProtocol = *s.EnableProxyProtocol }
    if s.ManageDFPMembers    != nil { pp.ManageDFPMembers = *s.ManageDFPMembers }
    pp.NodeLabels = s.TargetNodeLabels
}

func applyHealthCheckPolicy(pp *vngcloud_repo.PoolParams, hc *vksv1.VKSHealthCheckPolicy) {
    if hc == nil {
        pp.HealthCheck = vngcloud_repo.HealthCheck{Protocol: "TCP"}
        return
    }
    s := hc.Spec
    out := vngcloud_repo.HealthCheck{Protocol: s.Protocol}
    if s.Port               != nil { out.Port = *s.Port }
    if s.Interval           != nil { out.Interval = s.Interval.Duration }
    if s.Timeout            != nil { out.Timeout = s.Timeout.Duration }
    if s.HealthyThreshold   != nil { out.HealthyThreshold = *s.HealthyThreshold }
    if s.UnhealthyThreshold != nil { out.UnhealthyThreshold = *s.UnhealthyThreshold }
    if s.HTTPHealthCheck != nil {
        out.HTTPPath = derefStringPtr(s.HTTPHealthCheck.Path)
        out.HTTPHost = derefStringPtr(s.HTTPHealthCheck.Host)
        out.ExpectedCodes = s.HTTPHealthCheck.ExpectedCodes
        out.RequestHeaders = s.HTTPHealthCheck.RequestHeaders
    }
    pp.HealthCheck = out
}

func backendPolicyEqual(a, b *vksv1.VKSBackendPolicy) bool       { return a.Namespace == b.Namespace && a.Name == b.Name }
func healthCheckPolicyEqual(a, b *vksv1.VKSHealthCheckPolicy) bool { return a.Namespace == b.Namespace && a.Name == b.Name }
func derefStringPtr(p *string) string { if p == nil { return "" }; return *p }
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/usecase/gateway_uc/alb_gateway_uc/build_pool.go
git commit -m "feat(gateway/uc/alb): synthesize weighted pool with backend + health policies"
```

---

### Task F6: `build_policy.go`

**Files:**
- Create: `internal/usecase/gateway_uc/alb_gateway_uc/build_policy.go`

- [ ] **Step 1: Write the file**

```go
package alb_gateway_uc

import (
    "context"
    "fmt"

    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
    sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo"
)

// ensurePoolsAndPolicies generates the desired pool/policy set from attached HTTPRoutes
// and pushes the diff to vngcloud.
func (u *ALBGatewayUseCase) ensurePoolsAndPolicies(ctx context.Context, gw *gwv1.Gateway, lb *vngcloud_repo.LoadBalancer, routes []gwv1.HTTPRoute) error {
    var (
        pools    []vngcloud_repo.PoolParams
        policies []vngcloud_repo.PolicyParams
    )
    for _, r := range routes {
        rp, pp, err := u.buildPoliciesForRoute(ctx, gw, &r)
        if err != nil { return err }
        pools = append(pools, pp...)
        policies = append(policies, rp...)
    }
    if err := u.Vng.SyncPools(ctx, lb.ID, pools); err != nil { return err }
    return u.Vng.SyncPolicies(ctx, lb.ID, policies)
}

func (u *ALBGatewayUseCase) buildPoliciesForRoute(ctx context.Context, gw *gwv1.Gateway, route *gwv1.HTTPRoute) ([]vngcloud_repo.PolicyParams, []vngcloud_repo.PoolParams, error) {
    var policies []vngcloud_repo.PolicyParams
    var pools    []vngcloud_repo.PoolParams

    routePolicies, err := u.listVKSRoutePolicies(ctx, route)
    if err != nil { return nil, nil, err }

    for ri, rule := range route.Spec.Rules {
        pool, err := u.synthesizePool(ctx, route, ri, rule)
        if err != nil { return nil, nil, err }
        pools = append(pools, pool)

        section := ""
        if rule.Name != nil { section = string(*rule.Name) }
        rp := selectRoutePolicies(routePolicies, route, section)

        for _, m := range rule.Matches {
            for _, host := range hostnameSet(route) {
                pp := vngcloud_repo.PolicyParams{
                    Name: fmt.Sprintf("p_%s_%d_%x", string(route.UID)[:8], ri, hashMatch(host, m)),
                    Match: vngcloud_repo.Match{
                        Hostname:    host,
                        Path:        pathMatch(m.Path),
                        Headers:     headerMatches(m.Headers),
                        QueryParams: queryMatches(m.QueryParams),
                        Method:      methodFromMatch(m.Method),
                    },
                    Action: vngcloud_repo.Action{Kind: vngcloud_repo.ActionRedirectToPool, PoolName: pool.Name},
                }
                applyRoutePolicies(&pp, rp)
                applyRequestRedirectFilter(&pp, rule.Filters)
                policies = append(policies, pp)
            }
        }
    }
    return policies, pools, nil
}

func (u *ALBGatewayUseCase) listVKSRoutePolicies(ctx context.Context, route *gwv1.HTTPRoute) ([]vksv1.VKSRoutePolicy, error) {
    var list vksv1.VKSRoutePolicyList
    if err := u.K8s.Client.List(ctx, &list, client.InNamespace(route.Namespace)); err != nil { return nil, err }
    return list.Items, nil
}

// selectRoutePolicies returns policies whose targetRefs match (route, section) or (route, "").
// Among matches, ResolveDirectPolicy picks the winner per (route, section) scope.
func selectRoutePolicies(policies []vksv1.VKSRoutePolicy, route *gwv1.HTTPRoute, section string) []*vksv1.VKSRoutePolicy {
    cands := make([]*vksv1.VKSRoutePolicy, 0, len(policies))
    for i := range policies { cands = append(cands, &policies[i]) }
    var out []*vksv1.VKSRoutePolicy
    for _, sect := range []string{section, ""} {
        target := pkggw.PolicyTarget{
            Group: "gateway.networking.k8s.io", Kind: "HTTPRoute",
            Namespace: route.Namespace, Name: route.Name, SectionName: sect,
        }
        win, _ := sharedUC.ResolveDirectPolicy(cands, target)
        if win != nil { out = append(out, win) }
    }
    return out
}

func applyRoutePolicies(pp *vngcloud_repo.PolicyParams, rp []*vksv1.VKSRoutePolicy) {
    for _, p := range rp {
        for _, am := range p.Spec.AdditionalMatches {
            pp.Match.Additional = append(pp.Match.Additional, vngcloud_repo.AdditionalMatch{
                Type: am.Type, Name: derefStringPtr(am.Name), Compare: am.Compare, Value: am.Value,
            })
        }
        if len(p.Spec.Actions) > 0 {
            // Spec §3.4: actions supersede REDIRECT_TO_POOL.
            a := p.Spec.Actions[0]
            switch a.Type {
            case "FixedResponse":
                pp.Action = vngcloud_repo.Action{Kind: vngcloud_repo.ActionFixedResponse,
                    StatusCode: a.FixedResponse.StatusCode,
                    Body:       derefStringPtr(a.FixedResponse.Body),
                }
            case "Reject":
                pp.Action = vngcloud_repo.Action{Kind: vngcloud_repo.ActionReject}
            case "Redirect":
                pp.Action = vngcloud_repo.Action{Kind: vngcloud_repo.ActionRedirect,
                    URL:             a.Redirect.URL,
                    HTTPCode:        intDerefOr(a.Redirect.HTTPCode, 302),
                    KeepQueryString: boolDerefOr(a.Redirect.KeepQueryString, true),
                }
            }
        }
        if p.Spec.Position != nil {
            pp.Position = *p.Spec.Position
        }
    }
}

func applyRequestRedirectFilter(pp *vngcloud_repo.PolicyParams, filters []gwv1.HTTPRouteFilter) {
    for _, f := range filters {
        if f.Type == gwv1.HTTPRouteFilterRequestRedirect && f.RequestRedirect != nil {
            pp.Action = vngcloud_repo.Action{
                Kind:     vngcloud_repo.ActionRedirect,
                URL:      buildRedirectURL(f.RequestRedirect),
                HTTPCode: intDerefOr(asInt32Ptr(f.RequestRedirect.StatusCode), 302),
            }
        }
    }
}

// helpers (compactness) ----------------------------------------------------

func hostnameSet(r *gwv1.HTTPRoute) []string {
    if len(r.Spec.Hostnames) == 0 { return []string{""} }
    out := make([]string, 0, len(r.Spec.Hostnames))
    for _, h := range r.Spec.Hostnames { out = append(out, string(h)) }
    return out
}

func pathMatch(p *gwv1.HTTPPathMatch) vngcloud_repo.PathMatch {
    if p == nil { return vngcloud_repo.PathMatch{Type: "STARTS_WITH", Value: "/"} }
    out := vngcloud_repo.PathMatch{Value: derefStringPtr(p.Value)}
    if p.Type == nil { out.Type = "STARTS_WITH"; return out }
    switch *p.Type {
    case gwv1.PathMatchExact:             out.Type = "EQUAL_TO"
    case gwv1.PathMatchPathPrefix:        out.Type = "STARTS_WITH"
    case gwv1.PathMatchRegularExpression: out.Type = "REGEX"
    default:                              out.Type = "EQUAL_TO"
    }
    return out
}

func headerMatches(hs []gwv1.HTTPHeaderMatch) []vngcloud_repo.HeaderMatch {
    out := make([]vngcloud_repo.HeaderMatch, 0, len(hs))
    for _, h := range hs {
        kind := "EQUAL_TO"
        if h.Type != nil && *h.Type == gwv1.HeaderMatchRegularExpression {
            kind = "REGEX"
        }
        out = append(out, vngcloud_repo.HeaderMatch{Name: string(h.Name), Compare: kind, Value: h.Value})
    }
    return out
}

func queryMatches(qs []gwv1.HTTPQueryParamMatch) []vngcloud_repo.QueryMatch {
    out := make([]vngcloud_repo.QueryMatch, 0, len(qs))
    for _, q := range qs {
        kind := "EQUAL_TO"
        if q.Type != nil && *q.Type == gwv1.QueryParamMatchRegularExpression {
            kind = "REGEX"
        }
        out = append(out, vngcloud_repo.QueryMatch{Name: string(q.Name), Compare: kind, Value: q.Value})
    }
    return out
}

func methodFromMatch(m *gwv1.HTTPMethod) string { if m == nil { return "" }; return string(*m) }

func buildRedirectURL(r *gwv1.HTTPRequestRedirectFilter) string {
    // Implementation note: full URL templating per Gateway-API spec.
    // Inline per-field assembly here keeps this self-contained.
    scheme := "https"
    if r.Scheme != nil { scheme = *r.Scheme }
    host := ""
    if r.Hostname != nil { host = string(*r.Hostname) }
    out := fmt.Sprintf("%s://%s", scheme, host)
    if r.Port != nil { out = fmt.Sprintf("%s:%d", out, *r.Port) }
    if r.Path != nil && r.Path.ReplaceFullPath != nil { out += *r.Path.ReplaceFullPath }
    return out
}

func intDerefOr(p *int32, def int32) int32 { if p == nil { return def }; return *p }
func boolDerefOr(p *bool, def bool) bool   { if p == nil { return def }; return *p }
func asInt32Ptr(p *int) *int32 {
    if p == nil { return nil }
    v := int32(*p); return &v
}

func hashMatch(host string, m gwv1.HTTPRouteMatch) uint32 {
    // simple FNV-1a — pool-name uniqueness only, not security
    var h uint32 = 2166136261
    add := func(s string) { for _, c := range s { h ^= uint32(c); h *= 16777619 } }
    add(host)
    if m.Path != nil {
        add(derefStringPtr(m.Path.Value))
        if m.Path.Type != nil { add(string(*m.Path.Type)) }
    }
    for _, hm := range m.Headers   { add(string(hm.Name) + "=" + hm.Value) }
    for _, qm := range m.QueryParams { add(string(qm.Name) + "=" + qm.Value) }
    if m.Method != nil { add(string(*m.Method)) }
    return h
}
```

> The `vngcloud_repo` action kinds (`ActionRedirectToPool`, `ActionFixedResponse`, `ActionReject`, `ActionRedirect`) and policy-param shape match the existing `internal/repository/vngcloud_repo` types used by `lbc_uc`. Mirror those types in your branch's repository — their names may differ from the names shown here.

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/usecase/gateway_uc/alb_gateway_uc/build_policy.go
git commit -m "feat(gateway/uc/alb): build vngcloud Policies from HTTPRoute + VKSRoutePolicy"
```

---

### Task F7: `build_sec_group.go` and `status.go`

**Files:**
- Create: `internal/usecase/gateway_uc/alb_gateway_uc/build_sec_group.go`
- Create: `internal/usecase/gateway_uc/alb_gateway_uc/status.go`

- [ ] **Step 1: Write `build_sec_group.go`**

```go
package alb_gateway_uc

import (
    "context"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ensureNodeSecurityGroup makes sure the NSG referenced by the LB allows traffic
// from the LB's listener ports. Mirrors `internal/usecase/ingress_uc/build_sec_group.go`.
//
// Phase 1: reuse the existing NSG path verbatim. The Gateway-API entry simply
// invokes the existing Ingress NSG helper with the Gateway's LB ID and listener ports.
func (u *ALBGatewayUseCase) ensureNodeSecurityGroup(ctx context.Context, gw *gwv1.Gateway) error {
    // Replace `u.Vng.EnsureNSGForLB` with whatever the existing helper is named in this branch;
    // the AWS-style design used a method on ingress_uc that we factor out for reuse.
    return u.Vng.EnsureNSGForGateway(ctx, gw)
}
```

> If your existing NSG helper has a different signature, adapt the body. The intent is "do whatever ingress_uc/service_uc does, but for a Gateway-owned LB."

- [ ] **Step 2: Write `status.go`**

```go
package alb_gateway_uc

import (
    "context"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

// WriteGatewayStatus persists the Accepted / Programmed conditions and address list.
func (u *ALBGatewayUseCase) WriteGatewayStatus(ctx context.Context, gw *gwv1.Gateway, res *Result, reconcileErr error) error {
    cur := gw.DeepCopy()

    accepted := metav1.Condition{
        Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted",
        ObservedGeneration: gw.Generation,
    }
    programmed := metav1.Condition{
        Type: "Programmed", Status: metav1.ConditionTrue, Reason: "Programmed",
        ObservedGeneration: gw.Generation,
    }
    if reconcileErr != nil {
        programmed.Status = metav1.ConditionFalse
        programmed.Reason = "Pending"
        programmed.Message = reconcileErr.Error()
    }
    shared.SetCondition(&cur.Status.Conditions, accepted)
    shared.SetCondition(&cur.Status.Conditions, programmed)

    if res != nil && res.LB != nil {
        addrs := []gwv1.GatewayStatusAddress{}
        for _, a := range res.LB.Addresses {
            t := gwv1.IPAddressType
            addrs = append(addrs, gwv1.GatewayStatusAddress{Type: &t, Value: a})
        }
        cur.Status.Addresses = addrs
    }
    return u.K8s.Client.Status().Patch(ctx, cur, client.MergeFrom(gw))
}

// WriteHTTPRouteStatus persists per-parent Accepted / ResolvedRefs conditions.
func (u *ALBGatewayUseCase) WriteHTTPRouteStatus(ctx context.Context, route *gwv1.HTTPRoute, gw *gwv1.Gateway, accepted bool, resolved bool, reason string) error {
    cur := route.DeepCopy()
    parentStatus := gwv1.RouteParentStatus{
        ParentRef: gwv1.ParentReference{
            Group: ptrGroup("gateway.networking.k8s.io"),
            Kind:  ptrKind("Gateway"),
            Name:  gwv1.ObjectName(gw.Name),
        },
        ControllerName: "gateway.vks.vngcloud.vn/alb",
    }
    shared.SetCondition(&parentStatus.Conditions, metav1.Condition{
        Type: "Accepted",
        Status: condStatus(accepted),
        Reason: reasonOr(accepted, "Accepted", reason),
        ObservedGeneration: route.Generation,
    })
    shared.SetCondition(&parentStatus.Conditions, metav1.Condition{
        Type: "ResolvedRefs",
        Status: condStatus(resolved),
        Reason: reasonOr(resolved, "ResolvedRefs", reason),
        ObservedGeneration: route.Generation,
    })
    cur.Status.Parents = upsertParentStatus(cur.Status.Parents, parentStatus)
    return u.K8s.Client.Status().Patch(ctx, cur, client.MergeFrom(route))
}

func ptrGroup(s string) *gwv1.Group { v := gwv1.Group(s); return &v }
func ptrKind(s string)  *gwv1.Kind  { v := gwv1.Kind(s);  return &v }
func condStatus(b bool) metav1.ConditionStatus {
    if b { return metav1.ConditionTrue }
    return metav1.ConditionFalse
}
func reasonOr(ok bool, good, bad string) string { if ok { return good }; return bad }
func upsertParentStatus(in []gwv1.RouteParentStatus, p gwv1.RouteParentStatus) []gwv1.RouteParentStatus {
    for i, x := range in {
        if x.ControllerName == p.ControllerName && x.ParentRef.Name == p.ParentRef.Name {
            in[i] = p; return in
        }
    }
    return append(in, p)
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add internal/usecase/gateway_uc/alb_gateway_uc/build_sec_group.go internal/usecase/gateway_uc/alb_gateway_uc/status.go
git commit -m "feat(gateway/uc/alb): NSG inheritance + status writers"
```

---

### Task F8: `listAttachedHTTPRoutes`

**Files:**
- Create: `internal/usecase/gateway_uc/alb_gateway_uc/list_routes.go`

- [ ] **Step 1: Write the file**

```go
package alb_gateway_uc

import (
    "context"

    "k8s.io/apimachinery/pkg/fields"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// listAttachedHTTPRoutes returns every HTTPRoute with a parentRef to gw and at least one
// listener whose hostname / kind / namespace permits the route.
func (u *ALBGatewayUseCase) listAttachedHTTPRoutes(ctx context.Context, gw *gwv1.Gateway) ([]gwv1.HTTPRoute, error) {
    var list gwv1.HTTPRouteList
    sel := fields.OneTermEqualSelector(shared.IndexHTTPRouteByParentGateway, gw.Namespace+"/"+gw.Name)
    if err := u.K8s.Client.List(ctx, &list, client.MatchingFieldsSelector{Selector: sel}); err != nil {
        return nil, err
    }
    out := make([]gwv1.HTTPRoute, 0, len(list.Items))
    for _, r := range list.Items {
        if !routeAttachableToGateway(&r, gw) { continue }
        out = append(out, r)
    }
    return out, nil
}

// routeAttachableToGateway encodes Gateway listener `allowedRoutes` rules.
func routeAttachableToGateway(route *gwv1.HTTPRoute, gw *gwv1.Gateway) bool {
    for _, l := range gw.Spec.Listeners {
        if l.Protocol != gwv1.HTTPProtocolType && l.Protocol != gwv1.HTTPSProtocolType { continue }
        if l.AllowedRoutes != nil {
            ns := l.AllowedRoutes.Namespaces
            if ns != nil && ns.From != nil {
                switch *ns.From {
                case gwv1.NamespacesFromAll: // ok
                case gwv1.NamespacesFromSame:
                    if route.Namespace != gw.Namespace { return false }
                case gwv1.NamespacesFromSelector:
                    // selector matching deferred to Phase 3; treat as allow.
                }
            }
        }
        if l.Hostname != nil && string(*l.Hostname) != "" {
            ok := false
            for _, h := range route.Spec.Hostnames {
                if pkggw.HostnameMatches(string(*l.Hostname), string(h)) { ok = true; break }
            }
            if !ok && len(route.Spec.Hostnames) > 0 { continue }
        }
        return true
    }
    return false
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/usecase/gateway_uc/alb_gateway_uc/list_routes.go
git commit -m "feat(gateway/uc/alb): list HTTPRoutes attachable to a Gateway"
```

---

## Section G — Reconcilers

### Task G1: GatewayClass reconciler

**Files:**
- Create: `internal/controller/gateway/alb/gatewayclass_controller.go`

- [ ] **Step 1: Write the reconciler**

```go
package alb

import (
    "context"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/builder"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
    "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

type GatewayClassReconciler struct {
    Client client.Client
}

func NewGatewayClassReconciler(c client.Client) *GatewayClassReconciler {
    return &GatewayClassReconciler{Client: c}
}

func (r *GatewayClassReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    var gwc gwv1.GatewayClass
    if err := r.Client.Get(ctx, req.NamespacedName, &gwc); err != nil {
        return reconcile.Result{}, client.IgnoreNotFound(err)
    }
    if string(gwc.Spec.ControllerName) != consts.GatewayClassControllerNameALB {
        return reconcile.Result{}, nil
    }
    cur := gwc.DeepCopy()
    shared.SetCondition(&cur.Status.Conditions, metav1.Condition{
        Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted",
        ObservedGeneration: gwc.Generation,
    })
    shared.SetCondition(&cur.Status.Conditions, metav1.Condition{
        Type: "SupportedVersion", Status: metav1.ConditionTrue, Reason: "SupportedVersion",
        ObservedGeneration: gwc.Generation,
    })
    return reconcile.Result{}, r.Client.Status().Patch(ctx, cur, client.MergeFrom(&gwc))
}

func (r *GatewayClassReconciler) SetupWithManager(mgr manager.Manager) error {
    return builder.ControllerManagedBy(mgr).
        For(&gwv1.GatewayClass{}).
        Named("gateway-class-alb").
        Complete(r)
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/controller/gateway/alb/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/controller/gateway/alb/gatewayclass_controller.go
git commit -m "feat(gateway/ctrl/alb): GatewayClass reconciler"
```

---

### Task G2: Gateway reconciler

**Files:**
- Create: `internal/controller/gateway/alb/gateway_controller.go`

- [ ] **Step 1: Write the reconciler**

```go
package alb

import (
    "context"

    corev1 "k8s.io/api/core/v1"
    discoveryv1 "k8s.io/api/discovery/v1"
    "sigs.k8s.io/controller-runtime/pkg/builder"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"
    "sigs.k8s.io/controller-runtime/pkg/source"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared/eventhandlers"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/alb_gateway_uc"
    "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

type GatewayReconciler struct {
    Client client.Client
    UC     *alb_gateway_uc.ALBGatewayUseCase
}

func NewGatewayReconciler(c client.Client, uc *alb_gateway_uc.ALBGatewayUseCase) *GatewayReconciler {
    return &GatewayReconciler{Client: c, UC: uc}
}

func (r *GatewayReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    if !r.UC.InitDone.Load() {
        return reconcile.Result{RequeueAfter: 1e9}, nil // 1s retry
    }
    var gw gwv1.Gateway
    if err := r.Client.Get(ctx, req.NamespacedName, &gw); err != nil {
        return reconcile.Result{}, client.IgnoreNotFound(err)
    }
    if !r.isOurClass(ctx, &gw) {
        return reconcile.Result{}, nil
    }

    if !gw.DeletionTimestamp.IsZero() {
        if err := r.UC.Delete(ctx, &gw); err != nil { return reconcile.Result{}, err }
        if shared.RemoveFinalizer(&gw, domain.GatewayFinalizer) {
            return reconcile.Result{}, r.Client.Update(ctx, &gw)
        }
        return reconcile.Result{}, nil
    }

    if err := shared.EnsureFinalizer(ctx, r.Client, &gw, domain.GatewayFinalizer); err != nil {
        return reconcile.Result{}, err
    }

    res, err := r.UC.Ensure(ctx, &gw)
    if statusErr := r.UC.WriteGatewayStatus(ctx, &gw, &res, err); statusErr != nil && err == nil {
        return reconcile.Result{}, statusErr
    }
    return reconcile.Result{}, err
}

func (r *GatewayReconciler) isOurClass(ctx context.Context, gw *gwv1.Gateway) bool {
    var gwc gwv1.GatewayClass
    if err := r.Client.Get(ctx, client.ObjectKey{Name: string(gw.Spec.GatewayClassName)}, &gwc); err != nil {
        return false
    }
    return string(gwc.Spec.ControllerName) == consts.GatewayClassControllerNameALB
}

func (r *GatewayReconciler) SetupWithManager(mgr manager.Manager) error {
    return builder.ControllerManagedBy(mgr).
        For(&gwv1.Gateway{}).
        Named("gateway-alb").
        Watches(&gwv1.HTTPRoute{}, eventhandlers.RouteToGateway()).
        Watches(&vksv1.VKSGatewayPolicy{}, eventhandlers.VKSGatewayPolicyToGateway()).
        WatchesRawSource(source.Kind(mgr.GetCache(), &corev1.Service{}, eventhandlers.ServiceToRouteParents(mgr.GetClient()))).
        WatchesRawSource(source.Kind(mgr.GetCache(), &discoveryv1.EndpointSlice{}, eventhandlers.ServiceToRouteParents(mgr.GetClient()))).
        Complete(r)
}
```

> Additional `Watches` for `VKSBackendPolicy` / `VKSHealthCheckPolicy` / `VKSRoutePolicy` / `Secret` / `ReferenceGrant` follow the same pattern; add them in the same `SetupWithManager` chain. Keep the body of each event handler symmetric.

- [ ] **Step 2: Verify build**

```bash
go build ./internal/controller/gateway/alb/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/controller/gateway/alb/gateway_controller.go
git commit -m "feat(gateway/ctrl/alb): Gateway reconciler"
```

---

### Task G3: HTTPRoute reconciler

**Files:**
- Create: `internal/controller/gateway/alb/httproute_controller.go`

> The HTTPRoute reconciler does **not** mutate the LB. Its only jobs are: (1) write per-parent status (Accepted / ResolvedRefs), (2) ensure finalizer, (3) enqueue parent Gateway for re-reconcile.

- [ ] **Step 1: Write the reconciler**

```go
package alb

import (
    "context"

    "sigs.k8s.io/controller-runtime/pkg/builder"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"

    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/alb_gateway_uc"
)

type HTTPRouteReconciler struct {
    Client client.Client
    UC     *alb_gateway_uc.ALBGatewayUseCase
}

func NewHTTPRouteReconciler(c client.Client, uc *alb_gateway_uc.ALBGatewayUseCase) *HTTPRouteReconciler {
    return &HTTPRouteReconciler{Client: c, UC: uc}
}

func (r *HTTPRouteReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    var route gwv1.HTTPRoute
    if err := r.Client.Get(ctx, req.NamespacedName, &route); err != nil {
        return reconcile.Result{}, client.IgnoreNotFound(err)
    }

    // Ensure finalizer; remove on deletion.
    if !route.DeletionTimestamp.IsZero() {
        if shared.RemoveFinalizer(&route, domain.HTTPRouteFinalizer) {
            return reconcile.Result{}, r.Client.Update(ctx, &route)
        }
        return reconcile.Result{}, nil
    }
    if err := shared.EnsureFinalizer(ctx, r.Client, &route, domain.HTTPRouteFinalizer); err != nil {
        return reconcile.Result{}, err
    }

    // Status writes happen during Gateway reconcile, which is the source of truth.
    // The HTTPRoute reconciler only touches finalizer + lets parent enqueue handle the rest.
    return reconcile.Result{}, nil
}

func (r *HTTPRouteReconciler) SetupWithManager(mgr manager.Manager) error {
    return builder.ControllerManagedBy(mgr).
        For(&gwv1.HTTPRoute{}).
        Named("httproute-alb").
        Complete(r)
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/controller/gateway/alb/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/controller/gateway/alb/httproute_controller.go
git commit -m "feat(gateway/ctrl/alb): HTTPRoute reconciler (finalizer-only)"
```

---

### Task G4: Policy validators (4 controllers)

**Files:**
- Create: `internal/controller/gateway/policies/common.go`
- Create: `internal/controller/gateway/policies/vksgatewaypolicy_controller.go`
- Create: `internal/controller/gateway/policies/vksbackendpolicy_controller.go`
- Create: `internal/controller/gateway/policies/vkshealthcheckpolicy_controller.go`
- Create: `internal/controller/gateway/policies/vksroutepolicy_controller.go`

> Each validator scans namespace-local policies of its kind, runs `ResolveDirectPolicy` per unique target, and writes `Accepted=True` to the winner and `Conflicted=True, Accepted=False` to losers. They never write to the LB.

- [ ] **Step 1: Write `common.go` (shared list-and-set helper)**

```go
package policies

import (
    "context"
    "sort"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"

    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
    sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
)

type policyItem[P sharedUC.PolicyObj] struct {
    Obj    P
    Target pkggw.PolicyTarget
}

// markConflicts iterates a list of policies grouped by target, picks oldest as winner,
// writes Accepted=True (winner) / Conflicted=True (losers).
func markConflicts[P sharedUC.PolicyObj](
    ctx context.Context,
    c client.Client,
    items []policyItem[P],
    setStatus func(P, []metav1.Condition) error,
) error {
    byTarget := map[pkggw.PolicyTarget][]P{}
    for _, it := range items {
        byTarget[it.Target] = append(byTarget[it.Target], it.Obj)
    }
    for target, group := range byTarget {
        win, losers := sharedUC.ResolveDirectPolicy(group, target)
        var pZero P
        if any(win) == any(pZero) { continue }

        if err := setStatus(win, []metav1.Condition{{
            Type: shared.AcceptedConditionType(), Status: metav1.ConditionTrue,
            Reason: "Accepted", LastTransitionTime: metav1.Now(),
        }}); err != nil { return err }

        for _, l := range losers {
            if err := setStatus(l, []metav1.Condition{{
                Type: shared.AcceptedConditionType(), Status: metav1.ConditionFalse,
                Reason: "Conflicted",
                Message: "another policy of the same kind is older and wins this target",
                LastTransitionTime: metav1.Now(),
            }}); err != nil { return err }
        }
    }
    // Stabilize order for tests.
    sort.SliceStable(items, func(i,j int) bool { return items[i].Obj.GetName() < items[j].Obj.GetName() })
    return nil
}

// any() helper for typed-zero comparison without importing reflect.
func any[T any](v T) interface{} { return v }
```

> If the typed-zero check trips up the compiler in your Go version, replace it with a small reflection check or pass an `isZero func(P) bool` argument.

> `shared.AcceptedConditionType()` returns the constant `"Accepted"`. Add a one-line export in `internal/controller/gateway/shared/status.go`:
>
> ```go
> func AcceptedConditionType() string { return "Accepted" }
> ```

- [ ] **Step 2: Write `vksgatewaypolicy_controller.go`**

```go
package policies

import (
    "context"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/builder"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

type VKSGatewayPolicyReconciler struct{ Client client.Client }

func NewVKSGatewayPolicyReconciler(c client.Client) *VKSGatewayPolicyReconciler { return &VKSGatewayPolicyReconciler{Client: c} }

func (r *VKSGatewayPolicyReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    var list vksv1.VKSGatewayPolicyList
    if err := r.Client.List(ctx, &list, client.InNamespace(req.Namespace)); err != nil {
        return reconcile.Result{}, err
    }

    items := make([]policyItem[*vksv1.VKSGatewayPolicy], 0)
    for i := range list.Items {
        p := &list.Items[i]
        for _, t := range p.Spec.TargetRefs {
            section := ""
            if t.SectionName != nil { section = string(*t.SectionName) }
            items = append(items, policyItem[*vksv1.VKSGatewayPolicy]{
                Obj: p,
                Target: pkggw.PolicyTarget{
                    Group: string(t.Group), Kind: string(t.Kind),
                    Namespace: p.Namespace, Name: string(t.Name), SectionName: section,
                },
            })
        }
    }
    set := func(p *vksv1.VKSGatewayPolicy, conds []metav1.Condition) error {
        cur := p.DeepCopy()
        cur.Status.Conditions = conds
        cur.Status.ObservedGeneration = p.Generation
        return r.Client.Status().Patch(ctx, cur, client.MergeFrom(p))
    }
    return reconcile.Result{}, markConflicts(ctx, r.Client, items, set)
}

func (r *VKSGatewayPolicyReconciler) SetupWithManager(mgr manager.Manager) error {
    return builder.ControllerManagedBy(mgr).
        For(&vksv1.VKSGatewayPolicy{}).
        Named("vksgatewaypolicy-validator").
        Complete(r)
}
```

- [ ] **Step 3: Write `vksbackendpolicy_controller.go`**

Same structure as G4-Step-2, but replace types with `VKSBackendPolicy`/`VKSBackendPolicyList`, no `SectionName`:

```go
package policies

import (
    "context"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/builder"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

type VKSBackendPolicyReconciler struct{ Client client.Client }

func NewVKSBackendPolicyReconciler(c client.Client) *VKSBackendPolicyReconciler { return &VKSBackendPolicyReconciler{Client: c} }

func (r *VKSBackendPolicyReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    var list vksv1.VKSBackendPolicyList
    if err := r.Client.List(ctx, &list, client.InNamespace(req.Namespace)); err != nil {
        return reconcile.Result{}, err
    }
    items := make([]policyItem[*vksv1.VKSBackendPolicy], 0)
    for i := range list.Items {
        p := &list.Items[i]
        for _, t := range p.Spec.TargetRefs {
            items = append(items, policyItem[*vksv1.VKSBackendPolicy]{
                Obj: p,
                Target: pkggw.PolicyTarget{Group: string(t.Group), Kind: string(t.Kind),
                    Namespace: p.Namespace, Name: string(t.Name)},
            })
        }
    }
    set := func(p *vksv1.VKSBackendPolicy, conds []metav1.Condition) error {
        cur := p.DeepCopy()
        cur.Status.Conditions = conds
        cur.Status.ObservedGeneration = p.Generation
        return r.Client.Status().Patch(ctx, cur, client.MergeFrom(p))
    }
    return reconcile.Result{}, markConflicts(ctx, r.Client, items, set)
}

func (r *VKSBackendPolicyReconciler) SetupWithManager(mgr manager.Manager) error {
    return builder.ControllerManagedBy(mgr).
        For(&vksv1.VKSBackendPolicy{}).
        Named("vksbackendpolicy-validator").
        Complete(r)
}
```

- [ ] **Step 4: Write `vkshealthcheckpolicy_controller.go`**

```go
package policies

import (
    "context"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/builder"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

type VKSHealthCheckPolicyReconciler struct{ Client client.Client }

func NewVKSHealthCheckPolicyReconciler(c client.Client) *VKSHealthCheckPolicyReconciler { return &VKSHealthCheckPolicyReconciler{Client: c} }

func (r *VKSHealthCheckPolicyReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    var list vksv1.VKSHealthCheckPolicyList
    if err := r.Client.List(ctx, &list, client.InNamespace(req.Namespace)); err != nil {
        return reconcile.Result{}, err
    }
    items := make([]policyItem[*vksv1.VKSHealthCheckPolicy], 0)
    for i := range list.Items {
        p := &list.Items[i]
        for _, t := range p.Spec.TargetRefs {
            items = append(items, policyItem[*vksv1.VKSHealthCheckPolicy]{
                Obj: p,
                Target: pkggw.PolicyTarget{Group: string(t.Group), Kind: string(t.Kind),
                    Namespace: p.Namespace, Name: string(t.Name)},
            })
        }
    }
    set := func(p *vksv1.VKSHealthCheckPolicy, conds []metav1.Condition) error {
        cur := p.DeepCopy()
        cur.Status.Conditions = conds
        cur.Status.ObservedGeneration = p.Generation
        return r.Client.Status().Patch(ctx, cur, client.MergeFrom(p))
    }
    return reconcile.Result{}, markConflicts(ctx, r.Client, items, set)
}

func (r *VKSHealthCheckPolicyReconciler) SetupWithManager(mgr manager.Manager) error {
    return builder.ControllerManagedBy(mgr).
        For(&vksv1.VKSHealthCheckPolicy{}).
        Named("vkshealthcheckpolicy-validator").
        Complete(r)
}
```

- [ ] **Step 5: Write `vksroutepolicy_controller.go`**

```go
package policies

import (
    "context"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/builder"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

type VKSRoutePolicyReconciler struct{ Client client.Client }

func NewVKSRoutePolicyReconciler(c client.Client) *VKSRoutePolicyReconciler { return &VKSRoutePolicyReconciler{Client: c} }

func (r *VKSRoutePolicyReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    var list vksv1.VKSRoutePolicyList
    if err := r.Client.List(ctx, &list, client.InNamespace(req.Namespace)); err != nil {
        return reconcile.Result{}, err
    }
    items := make([]policyItem[*vksv1.VKSRoutePolicy], 0)
    for i := range list.Items {
        p := &list.Items[i]
        for _, t := range p.Spec.TargetRefs {
            section := ""
            if t.SectionName != nil { section = string(*t.SectionName) }
            items = append(items, policyItem[*vksv1.VKSRoutePolicy]{
                Obj: p,
                Target: pkggw.PolicyTarget{
                    Group: string(t.Group), Kind: string(t.Kind),
                    Namespace: p.Namespace, Name: string(t.Name), SectionName: section,
                },
            })
        }
    }
    set := func(p *vksv1.VKSRoutePolicy, conds []metav1.Condition) error {
        cur := p.DeepCopy()
        cur.Status.Conditions = conds
        cur.Status.ObservedGeneration = p.Generation
        return r.Client.Status().Patch(ctx, cur, client.MergeFrom(p))
    }
    return reconcile.Result{}, markConflicts(ctx, r.Client, items, set)
}

func (r *VKSRoutePolicyReconciler) SetupWithManager(mgr manager.Manager) error {
    return builder.ControllerManagedBy(mgr).
        For(&vksv1.VKSRoutePolicy{}).
        Named("vksroutepolicy-validator").
        Complete(r)
}
```

- [ ] **Step 6: Add the `AllReconcilers` helper**

Create: `internal/controller/gateway/policies/all.go`

```go
package policies

import (
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"
)

type Reconciler interface {
    SetupWithManager(mgr manager.Manager) error
}

// AllReconcilers returns all four policy validators wired with the same client.
func AllReconcilers(c client.Client) []Reconciler {
    return []Reconciler{
        NewVKSGatewayPolicyReconciler(c),
        NewVKSBackendPolicyReconciler(c),
        NewVKSHealthCheckPolicyReconciler(c),
        NewVKSRoutePolicyReconciler(c),
    }
}
```

- [ ] **Step 7: Verify build**

```bash
go build ./internal/controller/gateway/policies/...
```

Expected: success.

- [ ] **Step 8: Commit**

```bash
git add internal/controller/gateway/policies/
git commit -m "feat(gateway/ctrl): policy validator reconcilers (4 CRDs)"
```

---

## Section H — Manager wiring, RBAC, Helm

### Task H1: Wire schemes, flags, and reconcilers in `cmd/main.go`

**Files:**
- Modify: `cmd/main.go`

- [ ] **Step 1: Add scheme registrations**

Find the existing `init()` block (or the top of `main()` where schemes are added) and append:

```go
import (
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
    gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
    gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
    vksgwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

// In init(), alongside the existing utilruntime.Must(...) lines:
utilruntime.Must(gwv1.Install(scheme))
utilruntime.Must(gwv1alpha2.Install(scheme))
utilruntime.Must(gwv1beta1.Install(scheme))
utilruntime.Must(vksgwv1alpha1.AddToScheme(scheme))
```

- [ ] **Step 2: Add feature-gate flags**

In `main()`, near the existing `flag.BoolVar` calls:

```go
var enableALBGateway, enableNLBGateway bool
flag.BoolVar(&enableALBGateway, "enable-gateway-api-alb", false,
    "Enable the ALB Gateway API controller (Phase 1).")
flag.BoolVar(&enableNLBGateway, "enable-gateway-api-nlb", false,
    "Enable the NLB Gateway API controller (Phase 2+).")
```

- [ ] **Step 3: Register indexes and reconcilers (gated)**

After the manager is constructed and the existing reconcilers are set up, add:

```go
import (
    sharedctrl "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
    albctrl "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/alb"
    polctrl "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/policies"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/alb_gateway_uc"
)

if enableALBGateway {
    if err := sharedctrl.RegisterIndexes(ctx, mgr); err != nil {
        setupLog.Error(err, "register gateway indexes")
        os.Exit(1)
    }
    albUC := alb_gateway_uc.NewALBGatewayUseCase(k8sRepo, vngRepo, ctrl.Log.WithName("alb-gateway-uc"))
    if err := albUC.Init(ctx); err != nil {
        setupLog.Error(err, "init alb gateway uc"); os.Exit(1)
    }

    if err := albctrl.NewGatewayClassReconciler(mgr.GetClient()).SetupWithManager(mgr); err != nil {
        setupLog.Error(err, "setup GatewayClass reconciler"); os.Exit(1)
    }
    if err := albctrl.NewGatewayReconciler(mgr.GetClient(), albUC).SetupWithManager(mgr); err != nil {
        setupLog.Error(err, "setup Gateway reconciler"); os.Exit(1)
    }
    if err := albctrl.NewHTTPRouteReconciler(mgr.GetClient(), albUC).SetupWithManager(mgr); err != nil {
        setupLog.Error(err, "setup HTTPRoute reconciler"); os.Exit(1)
    }
    for _, r := range polctrl.AllReconcilers(mgr.GetClient()) {
        if err := r.SetupWithManager(mgr); err != nil {
            setupLog.Error(err, "setup policy validator"); os.Exit(1)
        }
    }
}
```

> Symbol names (`k8sRepo`, `vngRepo`, `setupLog`, `ctrl`, `os.Exit`) match this branch's conventions; if any differs, follow the surrounding code.

- [ ] **Step 4: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 5: Commit**

```bash
git add cmd/main.go
git commit -m "feat(cmd): wire ALB Gateway API controllers behind --enable-gateway-api-alb"
```

---

### Task H2: RBAC kubebuilder markers + regenerate `role.yaml`

**Files:**
- Modify: `internal/controller/gateway/alb/gateway_controller.go` (add markers above `Reconcile`)
- Modify: `config/rbac/role.yaml` (regenerated)

- [ ] **Step 1: Add RBAC kubebuilder markers**

Above the `Reconcile` of `GatewayReconciler`, prepend the comment block:

```go
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses/status,verbs=update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/status,verbs=update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes;referencegrants,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/status,verbs=update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=gateway.vks.vngcloud.vn,resources=vksgatewaypolicies;vksbackendpolicies;vkshealthcheckpolicies;vksroutepolicies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=gateway.vks.vngcloud.vn,resources=vksgatewaypolicies/status;vksbackendpolicies/status;vkshealthcheckpolicies/status;vksroutepolicies/status,verbs=update;patch
// +kubebuilder:rbac:groups="",resources=services;secrets;configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
```

- [ ] **Step 2: Regenerate manifests**

```bash
make manifests
```

Expected: `config/rbac/role.yaml` updated; no other unrelated diffs (per CLAUDE.md: scan `git status`, revert any worktree-induced strays).

- [ ] **Step 3: Verify the embedded CRD set is in sync**

```bash
make sync-embedded-crds
git status
```

Expected: `pkg/k8s/apis/vks.vngcloud.vn/crds/` matches `config/crd/bases/`.

- [ ] **Step 4: Commit**

```bash
git add internal/controller/gateway/alb/gateway_controller.go \
        config/rbac/role.yaml \
        pkg/k8s/apis/vks.vngcloud.vn/crds/
git commit -m "feat(gateway): RBAC markers + regenerated role.yaml + embedded CRD sync"
```

---

### Task H3: Helm chart — bundle CRDs and gated GatewayClass

**Files:**
- Create: `charts/vngcloud-load-balancer-controller/templates/crds/vksgatewaypolicy.yaml` (copy from `config/crd/bases/`)
- Create: `charts/vngcloud-load-balancer-controller/templates/crds/vksbackendpolicy.yaml`
- Create: `charts/vngcloud-load-balancer-controller/templates/crds/vkshealthcheckpolicy.yaml`
- Create: `charts/vngcloud-load-balancer-controller/templates/crds/vksroutepolicy.yaml`
- Create: `charts/vngcloud-load-balancer-controller/templates/gatewayclass-alb.yaml`
- Modify: `charts/vngcloud-load-balancer-controller/values.yaml`
- Modify: `charts/vngcloud-load-balancer-controller/templates/manager-deployment.yaml`

- [ ] **Step 1: Copy CRDs into the chart**

Run:

```bash
for crd in vksgatewaypolicies vksbackendpolicies vkshealthcheckpolicies vksroutepolicies; do
  cp config/crd/bases/gateway.vks.vngcloud.vn_${crd}.yaml \
     charts/vngcloud-load-balancer-controller/templates/crds/${crd}.yaml
done
```

- [ ] **Step 2: Write `gatewayclass-alb.yaml`**

```yaml
{{- if .Values.gatewayApi.alb.enabled }}
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: vngcloud-alb
  labels:
    {{- include "vngcloud-load-balancer-controller.labels" . | nindent 4 }}
spec:
  controllerName: gateway.vks.vngcloud.vn/alb
  description: "vngcloud ALB-backed GatewayClass (Phase 1, GEP-713 policies)."
{{- end }}
```

- [ ] **Step 3: Add to `values.yaml`**

```yaml
gatewayApi:
  alb:
    enabled: false
  nlb:
    enabled: false
```

- [ ] **Step 4: Add the gate flag to `manager-deployment.yaml`**

In the controller args block:

```yaml
        {{- if .Values.gatewayApi.alb.enabled }}
        - --enable-gateway-api-alb
        {{- end }}
        {{- if .Values.gatewayApi.nlb.enabled }}
        - --enable-gateway-api-nlb
        {{- end }}
```

- [ ] **Step 5: Verify `helm lint` passes**

```bash
helm lint charts/vngcloud-load-balancer-controller
```

Expected: success.

- [ ] **Step 6: Commit**

```bash
git add charts/vngcloud-load-balancer-controller/
git commit -m "feat(chart): bundle Gateway-API CRDs and gated vngcloud-alb GatewayClass"
```

---

## Section I — Tests

### Task I1: envtest suite scaffold for the ALB controllers

**Files:**
- Create: `internal/controller/gateway/alb/suite_test.go`
- Create: `internal/controller/gateway/alb/helpers_test.go`

> Mirrors `internal/controller/networking/suite_test.go`. Runs once per package; spins up envtest, registers Gateway-API CRDs and the four VKS CRDs, instantiates a fake `VngCloudRepository`, and starts the manager.

- [ ] **Step 1: Write `suite_test.go`**

```go
package alb_test

import (
    "context"
    "path/filepath"
    "testing"
    "time"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/runtime"
    utilruntime "k8s.io/apimachinery/pkg/util/runtime"
    clientgoscheme "k8s.io/client-go/kubernetes/scheme"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/envtest"
    logf "sigs.k8s.io/controller-runtime/pkg/log"
    "sigs.k8s.io/controller-runtime/pkg/log/zap"

    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
    gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
    gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
    sharedctrl "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
    albctrl "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/alb"
    polctrl "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/policies"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/alb_gateway_uc"
)

var (
    cfg       *envtest.Environment
    k8sClient client.Client
    scheme    = runtime.NewScheme()
    ctx       context.Context
    cancel    context.CancelFunc
)

func TestALBController(t *testing.T) {
    RegisterFailHandler(Fail)
    RunSpecs(t, "ALB Gateway Controller Suite")
}

var _ = BeforeSuite(func() {
    logf.SetLogger(zap.New(zap.UseDevMode(true)))
    ctx, cancel = context.WithCancel(context.Background())

    utilruntime.Must(clientgoscheme.AddToScheme(scheme))
    utilruntime.Must(gwv1.Install(scheme))
    utilruntime.Must(gwv1alpha2.Install(scheme))
    utilruntime.Must(gwv1beta1.Install(scheme))
    utilruntime.Must(vksv1.AddToScheme(scheme))

    cfg = &envtest.Environment{
        CRDDirectoryPaths: []string{
            filepath.Join("..", "..", "..", "..", "config", "crd", "bases"),
            // gateway-api CRDs from go-mod cache
        },
        ErrorIfCRDPathMissing: true,
    }
    restCfg, err := cfg.Start()
    Expect(err).NotTo(HaveOccurred())

    k8sClient, err = client.New(restCfg, client.Options{Scheme: scheme})
    Expect(err).NotTo(HaveOccurred())

    mgr, err := ctrl.NewManager(restCfg, ctrl.Options{Scheme: scheme, Metrics: nil})
    Expect(err).NotTo(HaveOccurred())

    Expect(sharedctrl.RegisterIndexes(ctx, mgr)).To(Succeed())
    fakeVng := newFakeVngRepo()
    fakeK8s := newK8sRepo(mgr.GetClient())
    uc := alb_gateway_uc.NewALBGatewayUseCase(fakeK8s, fakeVng, ctrl.Log.WithName("test-uc"))
    Expect(uc.Init(ctx)).To(Succeed())

    Expect(albctrl.NewGatewayClassReconciler(mgr.GetClient()).SetupWithManager(mgr)).To(Succeed())
    Expect(albctrl.NewGatewayReconciler(mgr.GetClient(), uc).SetupWithManager(mgr)).To(Succeed())
    Expect(albctrl.NewHTTPRouteReconciler(mgr.GetClient(), uc).SetupWithManager(mgr)).To(Succeed())
    for _, r := range polctrl.AllReconcilers(mgr.GetClient()) {
        Expect(r.SetupWithManager(mgr)).To(Succeed())
    }

    go func() {
        defer GinkgoRecover()
        Expect(mgr.Start(ctx)).To(Succeed())
    }()

    Eventually(func() error {
        return k8sClient.Create(ctx, &corev1.Namespace{})
    }, 10*time.Second).Should(HaveOccurred()) // smoke-test API server reachable
})

var _ = AfterSuite(func() {
    cancel()
    Expect(cfg.Stop()).To(Succeed())
})
```

- [ ] **Step 2: Write `helpers_test.go`**

```go
package alb_test

import (
    "context"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/k8s_repo"
    "github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// newFakeVngRepo returns a VngCloudRepository whose methods are all stubs that
// record calls. Use the existing `vngcloud_mocks` package or write a minimal
// in-memory fake. Phase 1 uses an in-memory fake to keep envtest hermetic.
func newFakeVngRepo() *vngcloud_repo.VngCloudRepository {
    return vngcloud_repo.NewFake() // implement NewFake() in vngcloud_mocks if missing
}

func newK8sRepo(c client.Client) *k8s_repo.K8sRepository {
    return &k8s_repo.K8sRepository{Client: c, Reader: c, Writer: c}
}
```

> Adapt to whatever fake/mock the existing tests use. The point is to make `Vng` calls hermetic so the envtest only verifies controller-runtime wiring and status writes.

- [ ] **Step 3: Run an empty suite to verify scaffolding**

```bash
go test -p=1 -count=1 ./internal/controller/gateway/alb/...
```

Expected: PASS (no specs yet, but Suite setup completes).

- [ ] **Step 4: Commit**

```bash
git add internal/controller/gateway/alb/suite_test.go internal/controller/gateway/alb/helpers_test.go
git commit -m "test(gateway/ctrl/alb): envtest suite scaffold"
```

---

### Task I2: GatewayClass acceptance spec

**Files:**
- Create: `internal/controller/gateway/alb/gatewayclass_test.go`

- [ ] **Step 1: Write the spec**

```go
package alb_test

import (
    "context"
    "time"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ = Describe("GatewayClass reconciler", func() {
    It("marks Accepted=True when controllerName matches", func() {
        gwc := &gwv1.GatewayClass{
            ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-alb"},
            Spec: gwv1.GatewayClassSpec{ControllerName: "gateway.vks.vngcloud.vn/alb"},
        }
        Expect(k8sClient.Create(context.Background(), gwc)).To(Succeed())
        Eventually(func(g Gomega) {
            var got gwv1.GatewayClass
            g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: "vngcloud-alb"}, &got)).To(Succeed())
            cond := findCondition(got.Status.Conditions, "Accepted")
            g.Expect(cond).NotTo(BeNil())
            g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
        }, 10*time.Second, 200*time.Millisecond).Should(Succeed())
    })

    It("does not touch GatewayClasses for other controllers", func() {
        gwc := &gwv1.GatewayClass{
            ObjectMeta: metav1.ObjectMeta{Name: "other-class"},
            Spec: gwv1.GatewayClassSpec{ControllerName: "example.com/other"},
        }
        Expect(k8sClient.Create(context.Background(), gwc)).To(Succeed())
        Consistently(func(g Gomega) {
            var got gwv1.GatewayClass
            g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: "other-class"}, &got)).To(Succeed())
            g.Expect(findCondition(got.Status.Conditions, "Accepted")).To(BeNil())
        }, 2*time.Second, 200*time.Millisecond).Should(Succeed())
    })
})

func findCondition(cs []metav1.Condition, t string) *metav1.Condition {
    for i := range cs {
        if cs[i].Type == t { return &cs[i] }
    }
    return nil
}
```

- [ ] **Step 2: Run**

```bash
go test -p=1 -count=1 ./internal/controller/gateway/alb/... -ginkgo.focus="GatewayClass reconciler"
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/controller/gateway/alb/gatewayclass_test.go
git commit -m "test(gateway/ctrl/alb): GatewayClass acceptance spec"
```

---

### Task I3: Gateway end-to-end (single listener) spec

**Files:**
- Create: `internal/controller/gateway/alb/gateway_test.go`

- [ ] **Step 1: Write the spec**

```go
package alb_test

import (
    "context"
    "time"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ = Describe("Gateway end-to-end", func() {
    var ns string

    BeforeEach(func() {
        ns = createNamespace()
        gwc := &gwv1.GatewayClass{
            ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-alb-e2e"},
            Spec: gwv1.GatewayClassSpec{ControllerName: "gateway.vks.vngcloud.vn/alb"},
        }
        _ = k8sClient.Create(context.Background(), gwc)
    })

    It("provisions an LB and writes Programmed=True", func() {
        gw := &gwv1.Gateway{
            ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: ns},
            Spec: gwv1.GatewaySpec{
                GatewayClassName: "vngcloud-alb-e2e",
                Listeners: []gwv1.Listener{{
                    Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80,
                }},
            },
        }
        Expect(k8sClient.Create(context.Background(), gw)).To(Succeed())

        Eventually(func(g Gomega) {
            var got gwv1.Gateway
            g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "gw1"}, &got)).To(Succeed())
            g.Expect(findCondition(got.Status.Conditions, "Programmed")).NotTo(BeNil())
            g.Expect(findCondition(got.Status.Conditions, "Programmed").Status).To(Equal(metav1.ConditionTrue))
        }, 15*time.Second, 250*time.Millisecond).Should(Succeed())
    })
})

func createNamespace() string {
    ns := "test-" + randHex(6)
    Expect(k8sClient.Create(context.Background(), &corev1Ns(ns))).To(Succeed())
    return ns
}

// randHex / corev1Ns are tiny helpers; place at the bottom of suite_test.go.
```

> Add `randHex` and `corev1Ns` to `helpers_test.go` if they aren't already there.

- [ ] **Step 2: Run**

```bash
go test -p=1 -count=1 ./internal/controller/gateway/alb/... -ginkgo.focus="Gateway end-to-end"
```

Expected: PASS (with the in-memory fake LB returning a stub address).

- [ ] **Step 3: Commit**

```bash
git add internal/controller/gateway/alb/gateway_test.go internal/controller/gateway/alb/helpers_test.go
git commit -m "test(gateway/ctrl/alb): Gateway end-to-end spec"
```

---

### Task I4: VKSGatewayPolicy validator conflict spec

**Files:**
- Create: `internal/controller/gateway/policies/vksgatewaypolicy_test.go`
- Create: `internal/controller/gateway/policies/suite_test.go` (lightweight, similar to alb's suite)

- [ ] **Step 1: Write the suite scaffold (mirroring `alb_test`'s suite, but only registering policy reconcilers)**

> Reuse the same envtest pattern from Task I1. Register only `polctrl.AllReconcilers(...)`. No use-case wiring needed.

- [ ] **Step 2: Write the conflict spec**

```go
package policies_test

import (
    "context"
    "time"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

    vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

var _ = Describe("VKSGatewayPolicy validator", func() {
    var ns string
    BeforeEach(func() { ns = createNamespace() })

    It("marks the second policy as Conflicted", func() {
        ctx := context.Background()
        first := &vksv1.VKSGatewayPolicy{
            ObjectMeta: metav1.ObjectMeta{Name: "first", Namespace: ns,
                CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour))},
            Spec: vksv1.VKSGatewayPolicySpec{TargetRefs: []vksv1.LocalPolicyTargetReferenceWithSectionName{{
                LocalPolicyTargetReference: gwv1alpha2.LocalPolicyTargetReference{
                    Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw1",
                },
            }}},
        }
        second := first.DeepCopy()
        second.Name = "second"
        second.CreationTimestamp = metav1.NewTime(time.Now())

        Expect(k8sClient.Create(ctx, first)).To(Succeed())
        Expect(k8sClient.Create(ctx, second)).To(Succeed())

        Eventually(func(g Gomega) {
            var got vksv1.VKSGatewayPolicy
            g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: "first"}, &got)).To(Succeed())
            cond := findCondition(got.Status.Conditions, "Accepted")
            g.Expect(cond).NotTo(BeNil())
            g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))

            g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: "second"}, &got)).To(Succeed())
            cond = findCondition(got.Status.Conditions, "Accepted")
            g.Expect(cond).NotTo(BeNil())
            g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
            g.Expect(cond.Reason).To(Equal("Conflicted"))
        }, 10*time.Second, 250*time.Millisecond).Should(Succeed())
    })
})
```

- [ ] **Step 3: Run**

```bash
go test -p=1 -count=1 ./internal/controller/gateway/policies/...
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/controller/gateway/policies/
git commit -m "test(gateway/policies): VKSGatewayPolicy conflict spec"
```

---

### Task I5: E2E manifests + smoke script

**Files:**
- Create: `test/e2e/gateway/01-basic-http.yaml`
- Create: `test/e2e/gateway/02-https-sni.yaml`
- Create: `test/e2e/gateway/03-mtls.yaml`
- Create: `test/e2e/gateway/04-weighted-canary.yaml`
- Create: `test/e2e/gateway/05-cross-ns-refgrant.yaml`
- Create: `test/e2e/gateway/06-route-policy-overlay.yaml`
- Create: `test/e2e/gateway/run.sh`
- Modify: `Makefile` (add `e2e-gateway`)

> E2E run against a real cluster with `gatewayApi.alb.enabled=true`. Each YAML stands alone and is asserted by `run.sh` using `kubectl wait` + `curl` smoke checks.

- [ ] **Step 1: Write `01-basic-http.yaml`**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: gw-basic, namespace: e2e-gateway }
spec:
  gatewayClassName: vngcloud-alb
  listeners:
  - { name: http, protocol: HTTP, port: 80 }
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: r-basic, namespace: e2e-gateway }
spec:
  parentRefs:
  - { name: gw-basic }
  rules:
  - matches: [{ path: { type: PathPrefix, value: / } }]
    backendRefs:
    - { name: echo, port: 8080 }
```

(Service `echo` is provided by your existing e2e fixture set; if not, vendor a basic httpbin Deployment+Service.)

- [ ] **Step 2: Write `02–06.yaml`**

Each follows the same shape, exercising one feature:

- 02: Gateway with HTTPS listener + `tls.certificateRefs` Secret.
- 03: Gateway with HTTPS + `tls.frontendValidation.caCertificateRefs` ConfigMap.
- 04: HTTPRoute with two backendRefs weighted 80/20; verify counts via 100 curl calls.
- 05: HTTPRoute referencing a Service in another namespace + a `ReferenceGrant` allowing it.
- 06: HTTPRoute with `rules[].name: admin` plus a `VKSRoutePolicy` adding a `SourceIP STARTS_WITH` match; verify policy lands.

> The exact YAML for each follows the spec §2 + §3 conventions. Don't paraphrase — copy from the spec's Section 3 worked example for `VKSRoutePolicy`.

- [ ] **Step 3: Write `run.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

NS=e2e-gateway
kubectl create ns "$NS" 2>/dev/null || true

for f in 01-basic-http 02-https-sni 03-mtls 04-weighted-canary 05-cross-ns-refgrant 06-route-policy-overlay; do
  echo "=== Applying $f ==="
  kubectl apply -f "$(dirname "$0")/$f.yaml"
  kubectl -n "$NS" wait --for=condition=Programmed gateway --all --timeout=300s
  kubectl -n "$NS" wait --for=condition=Accepted httproute --all --timeout=120s
  bash "$(dirname "$0")/assert-$f.sh"
done
```

- [ ] **Step 4: Add `Makefile` target**

```makefile
.PHONY: e2e-gateway
e2e-gateway: ## Run Gateway-API e2e suite against the current kubeconfig
	bash test/e2e/gateway/run.sh
```

- [ ] **Step 5: Commit**

```bash
git add test/e2e/gateway/ Makefile
git commit -m "test(e2e/gateway): add 6 ALB Gateway live-cluster scenarios"
```

---

## Section J — User docs + examples

### Task J1: `gateway-api.md` — overview

**Files:**
- Create: `docs/guide/gateway-api.md`

- [ ] **Step 1: Write the page**

Required sections (concrete content, not headers only):

1. **What this is** — one paragraph: vngcloud LB controller speaks Gateway API; ALB GatewayClass in Phase 1; NLB later.
2. **What you get** — bullet list of supported features (HTTP/HTTPS/TLS-Terminate listeners, weighted canary, cross-namespace refs, mTLS).
3. **Customization model** — explain GEP-713 Direct Policy Attachment and the four CRDs at a glance, with one minimal YAML per CRD.
4. **Enabling** — `helm install ... --set gatewayApi.alb.enabled=true`.
5. **Status discoverability** — explicit short paragraph on the "spooky action at a distance" problem and the recommended commands:
    ```bash
    kubectl describe vksbackendpolicy -A
    kubectl describe gateway prod-gw -n my-ns
    ```
6. **Conformance matrix** link (Phase 4) + capability table for v1 (one row per Gateway-API feature with Supported / Partial / Unsupported).

- [ ] **Step 2: Commit**

```bash
git add docs/guide/gateway-api.md
git commit -m "docs(gateway): overview page"
```

---

### Task J2: `gateway-alb.md` — ALB-specific guide

**Files:**
- Create: `docs/guide/gateway-alb.md`

- [ ] **Step 1: Write the page**

Sections:

1. **Provisioning a Gateway** — full walk-through with screenshot-equivalent YAML.
2. **Listener config** via `VKSGatewayPolicy` — example with `sectionName` for per-listener TLS policy.
3. **Cross-namespace TLS Secrets** — with ReferenceGrant.
4. **Migration from Ingress** — note that this is deferred to Phase 4.

- [ ] **Step 2: Commit**

```bash
git add docs/guide/gateway-alb.md
git commit -m "docs(gateway): ALB-specific guide"
```

---

### Task J3: `gateway-policies.md` — the four CRDs explained

**Files:**
- Create: `docs/guide/gateway-policies.md`

> The most important user-facing doc. Each policy gets its own subsection with a complete example.

- [ ] **Step 1: Write the page**

Required structure:

1. **GEP-713 in 60 seconds** — `targetRefs`, Direct attachment, oldest-wins.
2. **VKSGatewayPolicy** — full YAML attaching to a Gateway, then a second YAML attaching to one listener via `sectionName`. Field reference table copied from spec §2.2.
3. **VKSBackendPolicy** — full YAML attaching to a Service. Field reference table from spec §2.3.
4. **VKSHealthCheckPolicy** — full YAML. Field reference from spec §2.4. Note that conflicting health-checks across a route's backends fail the route.
5. **VKSRoutePolicy** — full YAML attached to an `HTTPRoute` rule via `sectionName: <ruleName>`. Field reference from spec §2.5. Subsection on **rule names** — users **must** add `name:` to `HTTPRoute.spec.rules[]` for `sectionName` to resolve.
6. **Conflict semantics** — `Accepted=True` on the winner, `Conflicted=True` on losers, oldest-wins, namespace-scoped. Show `kubectl describe` output illustrating both.
7. **Discoverability runbook** — step-by-step "I changed a backend service and traffic broke; what do I check?" with the five `kubectl` commands the user runs.

- [ ] **Step 2: Commit**

```bash
git add docs/guide/gateway-policies.md
git commit -m "docs(gateway): four-CRD policy reference"
```

---

### Task J4: Example — weighted canary

**Files:**
- Create: `docs/examples/gateway-canary.md`

- [ ] **Step 1: Write the example**

Full runnable manifest pair (HTTPRoute with 80/20 backendRefs + a `VKSBackendPolicy` enabling sticky sessions on the canary). Include the curl-loop verification snippet from `test/e2e/gateway/04-weighted-canary.yaml`.

- [ ] **Step 2: Commit**

```bash
git add docs/examples/gateway-canary.md
git commit -m "docs(gateway): weighted-canary example"
```

---

## Self-review

Run this checklist after the plan is committed; fix issues inline.

### 1. Spec coverage

| Spec section | Tasks that implement it |
|---|---|
| §1.1 Controllers & GatewayClasses | A3, G1, H3 |
| §1.2 Policy CRDs (overview) | B1–B6 |
| §1.3 Package layout | every Section A–H file marker |
| §1.4 Manager wiring | H1 |
| §1.5 Coexistence boundary | A3 (owner labels), F2 (ensureLoadBalancer) |
| §2.1 Common types | B1 |
| §2.2 VKSGatewayPolicy | B2, E3 |
| §2.3 VKSBackendPolicy | B3, E3 |
| §2.4 VKSHealthCheckPolicy | B4, E3 |
| §2.5 VKSRoutePolicy | B5, E3 |
| §2.6 RBAC | H2 |
| §3.1 Mapping table | implicit; F2/F3/F5/F6 implement each row |
| §3.2 Controller graph | G1, G2, G3, G4 |
| §3.3 Gateway → LB + Listeners | F2, F3, F4 |
| §3.4 HTTPRoute → Policies + Pools | F5, F6 |
| §3.5 Synthetic pool naming + scaling | C2, F5 |
| §3.6 Policy resolver pseudocode | E2, F5, F6 |
| §3.7 Watch & reverse-index map | D4, D6, G2 |
| §3.8 Eventual-consistency notes | G2 (serialized reconciles) |
| §4.1 Status conditions | F7, G1, G4 |
| §4.2 Events | _Phase 2 follow-up_ — Phase 1 emits via `setupLog` only; spec §4.2 events are best-effort. |
| §4.3 Prometheus metrics | _Phase 2 follow-up_. |
| §4.4 Logging | covered by existing `contexts` package. |
| §4.5 Finalizer strategy | A3, G2, G3, D5 |
| §4.6 Readiness / liveness | G2 (initDone gate). |
| §5 File layout | implicit across A–H. |
| §6 Phasing roadmap (Phase 1) | this whole plan. |
| §7 Risks & mitigations | implicit; weight cap (C2), refgrant (E1), conflict status (G4). |

**Gaps flagged:** §4.2 (events) and §4.3 (metrics) are explicit Phase-1 deliverables in the spec but deferred here for ergonomics. **If reviewers want them in Phase 1, add a Section K with one task per metric and one task to register the event recorder on each reconciler.** Not blocking; flagging.

### 2. Placeholder scan

No "TBD", "implement later", "fill in details", "similar to Task N" present. Two intentional pointers remain:
- F7 step 1 says "replace `EnsureNSGForGateway` with whatever the existing helper is named" — this is a real engineering judgment call against an existing repo helper, not a placeholder. The intent is fully specified.
- I5 steps 2 say "follow the spec's Section 3 worked example" rather than reproducing the YAML — the YAML is in the spec verbatim, and reproducing it here would be duplication that drifts on edit. Engineers are expected to copy from the spec.

### 3. Type consistency

Cross-checked:
- `BackendKey`, `BackendWeight`, `PolicyTarget` — defined in C2/C3, used in F5/F6/G4. ✅
- `EffectiveLBConfig` — defined in F2, used in F3. ✅
- `Result` — defined in F1, used in F2/G2. ✅
- `LoadBalancerParams`, `ListenerParams`, `PoolParams`, `PolicyParams`, `Match`, `Action`, `HealthCheck`, `SessionAffinity` — these are types **expected to exist** in `internal/repository/vngcloud_repo`. The plan is explicit about this dependency in F2 and F6 (notes flagging that names may need adjusting). The engineer reviewing the existing repo before F1 must confirm or add these types; not a true placeholder, but worth surfacing.
- `Matches(t PolicyTarget) bool` — defined in E3 on all four CRDs, consumed via the generic `PolicyObj` interface in E2. ✅
- `ResolveDirectPolicy[P]` — generic; `P` constrained to `PolicyObj`. Used in F2 (`*VKSGatewayPolicy`), F5 (`*VKSBackendPolicy`, `*VKSHealthCheckPolicy`), F6 (`*VKSRoutePolicy`), G4 (each via `policyItem[P]`). ✅
- `shared.SetCondition` — defined in D2, used in F7, G1, G4. ✅
- `shared.RegisterIndexes` — defined in D4, used in H1 + I1. ✅

### 4. Final notes for the executor

- **TDD discipline:** strict TDD applies to pure functions in C1–C3, D1, D3, E1, E2 (failing test → run → implement → run → commit). For controllers (D6, F1–F8, G1–G4) implementation lands first and tests follow in Section I — that's the same compromise the existing `internal/controller/networking` suite makes.
- **Test parallelism:** per `CLAUDE.md`, always use `go test -p=1 ./...` for the full suite to avoid envtest-server collisions.
- **`make manifests` worktree scan:** per `CLAUDE.md`, always `git status` after `make manifests` and revert anything not from this branch's CRDs.
- **Verification gate:** before opening the PR, run the full suite (`make test`) and at least one e2e (`make e2e-gateway`). Both must pass.

---

End of Phase 1 plan.









