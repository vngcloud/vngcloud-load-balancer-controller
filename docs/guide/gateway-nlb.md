# Gateway API (NLB / L4)

Phase 2 extends [Gateway API](gateway-api.md) support to VNGCloud **Network Load Balancers** (L4). A `Gateway` under the `vngcloud-nlb` GatewayClass with `TCPRoute`/`UDPRoute` produces a Layer-4 `LoadBalancerConfig` — the same CR the `Service` type=LoadBalancer path emits — so the existing LBC controller drives the cloud NLB.

| | |
|---|---|
| GatewayClass | `vngcloud-nlb` |
| Controller name | `gateway.vks.vngcloud.vn/nlb` |
| Listener protocols | `TCP`, `UDP` |
| Supported routes | `TCPRoute`, `UDPRoute` |

!!! warning "Experimental-channel CRDs are a prerequisite"
    `TCPRoute` and `UDPRoute` live only in the Gateway API **experimental** channel and are **not** installed by the standard-channel manifests used for the ALB path. Install them before enabling:

    ```bash
    kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/experimental-install.yaml
    ```

    Because of this, the NLB controller is **disabled by default**. The controller does not embed or install these CRDs.

!!! note "Phase 2 scope"
    TCP and UDP routing are supported. `TLSRoute` (SNI passthrough) is planned for a follow-up. Mixed L4+L7 on one Gateway is not supported — HTTP/HTTPS listeners on an `vngcloud-nlb` Gateway are reported `UnsupportedProtocol` and skipped (use the `vngcloud-alb` class for L7).

## Enabling

```yaml
# values.yaml
gatewayApi:
  nlb:
    enabled: true   # installs the vngcloud-nlb GatewayClass + enables the controller
```

This sets `--disable-nlb-gateway-controller=false`. Only enable it on clusters that have the experimental CRDs installed.

## Quick start

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: tcp-gateway
  namespace: default
spec:
  gatewayClassName: vngcloud-nlb
  listeners:
    - name: redis
      protocol: TCP
      port: 6379
      allowedRoutes:
        namespaces:
          from: Same
---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: redis-route
  namespace: default
spec:
  parentRefs:
    - name: tcp-gateway
      sectionName: redis     # optional: pin to a listener (also matchable by port)
  rules:
    - backendRefs:
        - name: redis
          port: 6379
```

A `UDPRoute` is identical with `kind: UDPRoute` and a `UDP` listener.

```bash
kubectl get gateway tcp-gateway
NAME          CLASS          ADDRESS         PROGRAMMED   AGE
tcp-gateway   vngcloud-nlb   203.0.113.50    True         3m
```

## How routes map to listeners

Each Gateway TCP/UDP listener gets **one default pool**, built from the backendRefs of the route attached to it:

- A `TCPRoute` attaches to a `TCP` listener; a `UDPRoute` to a `UDP` listener.
- A `parentRef` attaches to a listener when its `sectionName` (if set) names the listener **and** its `port` (if set) equals the listener's port. With neither, it attaches to every compatible listener.
- If several routes target one listener, the **oldest** (by creationTimestamp) wins — an L4 listener has a single default pool.
- A listener with no attached route produces no cloud listener (an L4 listener has nothing to forward without a backend).

Weighted backendRefs across multiple Services are aggregated into one pool with scaled member weights, same as the ALB path.

## Policy CRDs

The L4 path honors three of the four [policy CRDs](gateway-api.md#policy-crds-gep-713):

| CRD | Targets | L4 effect |
|---|---|---|
| `VKSGatewayPolicy` | `Gateway` | LB creation spec (scheme, package, name, subnet/zone, InterVPC private subnet + zone, BYO-LB adoption, tags), listener timeouts, allowed CIDRs |
| `VKSBackendPolicy` | `Service` | Pool algorithm, stickiness, member target type (`instance`/`ip`), node-label selection, `proxyProtocol` (PROXY protocol to the backend — see below) |
| `VKSHealthCheckPolicy` | `Service` | Health monitor — TCP (default), PING-UDP (UDP pools), or an HTTP/HTTPS probe; interval/timeout/thresholds/port |

`VKSRoutePolicy` does **not** apply to L4 — there are no L7 actions (Reject/Redirect) or path/host matching on a TCP/UDP listener. `insertHeaders`, certificates, and TLS termination are also L7-only and ignored here.

Health-check defaults: a TCP listener probes its members over **TCP**; a UDP listener uses **PING-UDP** (mirrors the Service controller). A `VKSHealthCheckPolicy` can override the protocol (including an HTTP/HTTPS probe over a TCP pool) and the thresholds/interval/timeout/port.

!!! tip "One app behind an external **and** internal NLB"
    `VKSBackendPolicy` attaches to a **Service**, so its `targetType` is global to that Service across every Gateway. To front one app with both an `Internet` and an `Internal` NLB — especially with a different `targetType` per LB — see [Policy scope & multi-Gateway exposure](gateway-api.md#policy-scope-multi-gateway-exposure).

### PROXY protocol (real client IP)

`VKSBackendPolicy.proxyProtocol: true` switches a **TCP** pool to the vngcloud **PROXY** pool protocol, so the NLB prepends a PROXY header and an L4 backend (an HAProxy/nginx ingress controller, a TCP proxy, ...) can recover the real client IP behind the NLB. UDP pools ignore it. Mirrors the Service controller's `vks.vngcloud.vn/enable-proxy-protocol` annotation.

```yaml
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSBackendPolicy
metadata: {name: backend-proxyproto, namespace: default}
spec:
  targetRefs: [{group: "", kind: Service, name: my-l4-backend}]
  proxyProtocol: true
```

!!! warning "Set proxyProtocol before the Gateway is created"
    The cloud pool protocol is fixed at creation. Apply the `VKSBackendPolicy` *before* the Gateway so the pool is created as `PROXY` from the start; toggling it on a live pool requires recreating the Gateway. The backend must be configured to **accept** PROXY protocol (e.g. HAProxy ingress `proxy-protocol` config), otherwise it will reject connections.

See [Real client IP with NLB + HAProxy](gateway-nlb-haproxy-realip.md) for an end-to-end example.

### InterVPC scheme

An `InterVPC` LB is reachable from a client subnet in a **different VPC**. Set `scheme: InterVPC` plus the client subnet's ID and zone — `privateZoneId` is required because the client subnet lives in another VPC. Mirrors the Service controller's `private-subnet-id` / `private-zone-id` annotations.

```yaml
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSGatewayPolicy
metadata: {name: intervpc-lb, namespace: default}
spec:
  targetRefs: [{group: gateway.networking.k8s.io, kind: Gateway, name: my-nlb}]
  loadBalancerSpec:
    scheme: InterVPC
    privateSubnetId: subnet-xxxxxxxx   # client subnet in the other VPC
    privateZoneId: HCM03-1A            # zone of that client subnet
```

Like the other LB-creation fields, these are applied when the LB is first created.

## Status

- **Gateway** — `Accepted` (always, we own the class) and `Programmed` (reflects LBC readiness); `status.addresses` carries the NLB IP. Per-listener `Accepted` is `UnsupportedProtocol` for any HTTP/HTTPS listener.
- **TCPRoute / UDPRoute** — `Accepted` (attached to a listener) and `ResolvedRefs` (`ResolvedRefs`, `BackendNotFound`, or `RefNotPermitted` for a cross-namespace backend without a [`ReferenceGrant`](gateway-api.md#cross-namespace-backends)).

## Relationship to `Service` type=LoadBalancer

Both produce a Layer-4 `LoadBalancerConfig` and reconcile through the same LBC controller. The Gateway path is declarative routing config (Gateway + routes + policies) instead of a `Service` plus `vks.vngcloud.vn/*` annotations. Pick one per workload; they don't share an LB.
