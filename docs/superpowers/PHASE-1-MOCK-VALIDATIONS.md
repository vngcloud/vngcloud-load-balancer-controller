# Phase 1 — Live test bugs and the vngcloud-mock validations that catch them

**Last updated:** 2026-05-01
**Branch:** `feature/gateway-api-phase1`

## Naming convention — `vks-` prefix on every controller-managed resource

Every vngcloud resource the Gateway controller creates carries a `vks-` prefix in
its `name` field so operators can identify what the controller owns at a glance:

| Resource | Format | Example |
|---|---|---|
| LoadBalancer | `vks-gw-<gw-namespace>-<gw-name>` | `vks-gw-gw-demo-demo` |
| Listener | `vks-<gateway-listener-name>` | `vks-http`, `vks-https` |
| Pool (synthetic per HTTPRoute rule) | `vks-pool-<route-uid8>-<rule-idx>-<hash5>` | `vks-pool-6f4952d0-0-9f23a` |
| Pool member | `vks-m-<dashed-ip>-<port>` | `vks-m-10-0-100-3-32428` |
| Policy (per host × match) | `vks-pol-<route-uid8>-<rule>-<host>-<match>` | `vks-pol-6f4952d0-0-0-0` |
| Certificate (auto-imported) | `vks-<hash>-<random>-<seq>` | inherited from `lbc_uc` |

The 5-char floor is naturally satisfied because every prefix already exceeds it.

## Default LoadBalancer scheme

The default `LoadBalancerConfig.spec.scheme` for a Gateway-provisioned LB is
**`Internet`** — public-facing with an Internet-routable IP. This is shipped via
the Helm chart's [`values.yaml`](../../charts/vngcloud-load-balancer-controller/values.yaml)
under `manager.config.loadBalancerOpts.defaultScheme` and consumed by the
manager via `Config.LoadBalancerOpts.DefaultScheme` in `pkg/config/config.go`.
It applies whenever neither the class-level `LoadBalancerConfig.parametersRef`
nor a per-Gateway `infrastructure.parametersRef` overrides it.

Valid scheme values (per the LBC CRD enum):

| Scheme | Use |
|---|---|
| `Internet` (default) | Public-facing with an Internet-routable IP |
| `Internal` | Cluster-internal VPC traffic only — east-west or service-mesh exposure |
| `InterVPC` | VPC-to-VPC inside vngcloud (set `privateSubnetId` + `privateZoneId` on the LBC) |

To override, point the GatewayClass or the Gateway at a `LoadBalancerConfig` with
the desired `scheme`:

```yaml
apiVersion: vks.vngcloud.vn/v1alpha1
kind: LoadBalancerConfig
metadata: { name: alb-internal }
spec:
  type: Layer 7
  scheme: Internal
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata: { name: vngcloud-alb-internal }
spec:
  controllerName: gateway.vks.vngcloud.vn/alb
  parametersRef:
    group: vks.vngcloud.vn
    kind: LoadBalancerConfig
    name: alb-internal
```

**Linked specs:** [`docs/superpowers/specs/2026-04-30-gateway-api-design.md`](specs/2026-04-30-gateway-api-design.md), [`docs/superpowers/PHASE-1-SUMMARY.md`](PHASE-1-SUMMARY.md)

This document records every Phase-1 bug discovered by running the controller against a real vngcloud cluster, the validation rule the cluster enforces, and where that rule now lives in `internal/repository/vngcloud_repo/vngcloud_mocks` so future tests catch the regression without a real LB warm-up cycle (typically 60-180 seconds per round).

## Why mirror the platform's validations into the mock

`vngcloud_mocks.MockProvider` is the in-memory stand-in for the vngcloud LB API. Originally it accepted any well-typed input. The Phase-1 live test pass found 8 places where the controller emitted well-typed-but-platform-invalid specs — each surfaced only after the LB provisioned (multiple minutes), failed, and we had to roll back. By teaching the mock the same field rules the real API enforces, every one of those failures is now a unit-test-fast assertion.

When you add a new validation to the real API or discover one the hard way, **add it to the mock and reference it from this doc**. The pattern is small, well-isolated, and pays back the next time someone adds a new build helper.

## Validations now enforced by the mock

| # | Rule | Location in mock | Trigger | Real-API error string |
|---|---|---|---|---|
| 1 | Listener name 5–50 chars, `[a-zA-Z0-9_.-]` | `validateVngcloudName("listenerName", ...)` in `CreateListener` | `Gateway.spec.listeners[].name` shorter than 5 (e.g. `http`) | `listenerName: Only letters (a-z, A-Z, 0-9, '_', '-', '.') are allowed and must be between 5 and 50 characters.` |
| 2 | Pool name 5–50 chars, same charset | `validateVngcloudName("poolName", ...)` in `CreatePool` | Synthetic pool naming bug (caught in design — never tripped live) | same message, different `kind` |
| 3 | `PoolMember.Name` 5–50 chars, same charset | `validateVngcloudName("members[i].name", ...)` in `CreatePool` | `Name` field omitted on the member | `members[0].name: Only letters ... 5 and 50 characters.` |
| 4 | `PoolMember.MonitorPort` non-zero | inline check in `CreatePool` (`mr.MonitorPort == 0`) | `MonitorPort` defaulted to zero by the builder | `members[0].monitorPort: Required value` |
| 5 | When `HealthCheckProtocol ∈ {TCP, PING-UDP}` no HTTP-only field may be set (`healthCheckPath`, `healthCheckMethod`, `successCode`, `domainName`, `httpVersion`) | `validateHealthCheckProtocolFields(...)` called from both `CreatePool` and `UpdatePool` | TGC swap mid-life: pool went HTTP→TCP but the update payload still carried the old HTTP path | `If healthCheckProtocol field is TCP or PING-UDP, following fields cannot be specified: healthCheckPath, healthCheckMethod, successCode, domainName, httpVersion` |

These four rules are reproduced verbatim from production error messages so the test failures will match what an operator would see live.

## Bugs found during the live pass and the regression tests that pin them

The complete list of Phase-1 issues — including the ones that don't need mock-side validation — and the unit tests asserting the fix. Run them with:

```bash
go test ./internal/usecase/gateway_uc/alb_gateway_uc/... -run TestRegression -v
```

| # | Bug | Fix lives in | Regression test |
|---|---|---|---|
| 1 | Short listener name `http` (4 chars) rejected by vngcloud | `vngcloudListenerName()` in `build_listener.go` (prefix `gw-`, then pad with `-` to 5) | `TestRegression_Bug1_ListenerName` |
| 2 | `Gateway.status.addresses` always empty | `gatherGatewayAddresses()` in `gateway_uc_status.go` (lists owned LBCs, copies `status.address`) | covered by existing `TestMarkAcceptedAndProgrammed_*` |
| 3 | After fix #1, `attachHTTPRoutes` couldn't match the LBC listener name back to the Gateway listener | `findGatewayListenerByName` accepts both raw and munged names | `TestRegression_Bug3_FindGatewayListenerByMungedName` |
| 4 | Default `EndpointResolveOptions.NodeSelector = labels.Nothing()` matched zero nodes | `route_resolve.go` always passes `SelectorFromSet` (empty set → `Everything`) | `TestRegression_Bug4_EndpointResolution_Structural` |
| 5 | `PoolMember.MonitorPort` is CRD-required; builder didn't set it | `build_pool.go` defaults `MonitorPort` to traffic port | `TestRegression_Bug5And6_PoolMemberFields` |
| 6 | `PoolMember.Name` is required and length-validated; builder didn't set it | `build_pool.go` `memberName(ip, port) = "m-<ip-with-dashes>-<port>"` | `TestRegression_Bug5And6_PoolMemberFields` |
| 7 | `LoadBalancerConfigSpec.CreateCertificates` empty for HTTPS Gateway listeners; lbc_uc cert-import never ran | `collectCreateCertificates(gw)` in `gateway_uc_resolve.go` | `TestRegression_Bug7_CreateCertificatesPopulated` + `TestRegression_Bug7_CrossNamespaceCertRefsSkipped` |
| 8 | Gateway never had the controller's finalizer; deletion bypassed cleanup and orphaned LBC + LB | `Reconcile()` in `gateway_controller.go` adds/removes `gateway.vks.vngcloud.vn/resources` | covered by envtest path; live-verified S8 |
| 9 | `vngcloudListenerName("x")` produced `gw-x` (4 chars, still under floor) | added pad-to-5 loop in same function | `TestRegression_Bug1_ListenerName` (case `x → gw-x-`) — caught by the new mock-driven regression suite, not live |

## Live-test scenarios covered

8 scenarios run against the real cluster on 2026-05-01:

| ID | Scenario | Result |
|---|---|---|
| S1 | Basic HTTP Gateway + HTTPRoute | ✅ HTTP 200 |
| S2 | HTTPS termination via K8s TLS Secret | ✅ HTTPS 200 |
| S3 | Weighted backendRefs 90:10 | ⚠ Spec correct (member weight 9:1); vngcloud's default round-robin doesn't shape by weight — **product-side limitation, not a controller bug** |
| S4 | TargetGroupConfig per-Service override | ✅ Algorithm + healthCheck propagated to pool |
| S5 | ListenerRuleConfig ExtensionRef | ✅ Resolved; Header match correctly dropped (vngcloud LB lacks Header rule type — forward-compat behavior as designed) |
| S6 | Cross-namespace + ReferenceGrant | ✅ Denied without grant; routes correctly with grant |
| S7 | mTLS via `frontendValidation` | 🟡 **Implementation gap** — `BuildListener` doesn't process `tls.frontendValidation.caCertificateRefs` yet. The `LoadBalancerConfig.spec.listeners[].clientCertificateId` override path works for users with a pre-provisioned vngcloud CA cert. Standard Gateway-API path needs follow-up |
| S8 | Gateway delete flow | ✅ Finalizer attached on Ensure; cleanup cascades on delete |

## How to extend this when you find another live-only bug

1. **Capture the real-API error string verbatim** in the failing reconcile log.
2. **Add the rule to `vngcloud_mocks.validate*` helpers** — pattern: a small named function returning `error`, called from the mock entry point that hits it (Create/Update Listener/Pool/Member, etc.).
3. **Add a regression test** under `internal/usecase/gateway_uc/alb_gateway_uc/vngcloud_mock_regression_test.go` (or the equivalent for whichever use case fails) that drives the build helper and asserts the artifact passes the mock validator.
4. **Update the table above** — bug number, location, regression-test name, and the live error string.
5. **Pin the fix with a commit message** referencing the regression test.

The end goal is that the next person who refactors `build_pool.go` doesn't have to wait 5 minutes for vngcloud to tell them they broke `monitorPort` — the unit test fails locally in 50 ms.
