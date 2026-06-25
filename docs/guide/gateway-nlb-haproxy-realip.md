# Real client IP: NLB (Gateway API) + HAProxy + PROXY protocol

A common production pattern: front an HTTP ingress controller with a VNGCloud **NLB** (L4) and still have the application see the **real client IP**. Because an L4 load balancer SNATs the source address, the app would otherwise only see a cluster-internal IP. The fix is **PROXY protocol**: the NLB prepends a header carrying the original client IP, and the ingress controller (HAProxy here) reads it and forwards it as `X-Forwarded-For`.

```
client (1.2.3.4)
   │  TCP :80
   ▼
[ VNGCloud NLB ]                         Gateway(vngcloud-nlb) + TCPRoute
   │  PROXY protocol  ← VKSBackendPolicy.proxyProtocol: true
   ▼
[ HAProxy ingress ]  accept-proxy → recovers 1.2.3.4 → sets X-Forwarded-For
   │  HTTP (host/path routing via Ingress)
   ▼
[ app ]  sees X-Forwarded-For: 1.2.3.4
```

!!! note "Prerequisites"
    - The [NLB Gateway controller](gateway-nlb.md) enabled (`gatewayApi.nlb.enabled=true`) and the experimental `TCPRoute` CRD installed.
    - The `vngcloud-nlb` GatewayClass `Accepted`.

## 1. Install HAProxy ingress controller

Install it as a **NodePort** Service (not `LoadBalancer`, otherwise the Service controller would provision a *second* LB), and configure it to accept PROXY protocol from the NLB. The connection reaches HAProxy from the node/pod network after node-port forwarding, so trust those ranges (tighten to your node/LB CIDR in production):

```yaml
# haproxy-values.yaml
controller:
  kind: Deployment
  replicaCount: 2
  service:
    type: NodePort
  config:
    proxy-protocol: "10.0.0.0/8,172.16.0.0/12"
```

```bash
helm repo add haproxytech https://haproxytech.github.io/helm-charts
helm install haproxy-ingress haproxytech/kubernetes-ingress \
  -n haproxy-ingress --create-namespace -f haproxy-values.yaml
```

This creates the controller Service (e.g. `haproxy-ingress-kubernetes-ingress`, ports `80`/`443`) and the `haproxy` IngressClass.

## 2. Deploy an app + Ingress

```yaml
apiVersion: apps/v1
kind: Deployment
metadata: {name: whoami, namespace: realip-lab}
spec:
  replicas: 1
  selector: {matchLabels: {app: whoami}}
  template:
    metadata: {labels: {app: whoami}}
    spec:
      containers: [{name: whoami, image: traefik/whoami:v1.10, ports: [{containerPort: 80}]}]
---
apiVersion: v1
kind: Service
metadata: {name: whoami, namespace: realip-lab}
spec: {selector: {app: whoami}, ports: [{port: 80, targetPort: 80}]}
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: whoami, namespace: realip-lab}
spec:
  ingressClassName: haproxy
  rules:
  - host: whoami.lab
    http:
      paths:
      - {path: /, pathType: Prefix, backend: {service: {name: whoami, port: {number: 80}}}}
```

## 3. Front HAProxy with an NLB (Gateway API + PROXY protocol)

Apply the policies **before** the Gateway — `proxyProtocol` and the LB scheme are resolved when the pool/LB is first created (the cloud pool protocol is immutable afterward):

```yaml
# PROXY protocol to the HAProxy controller Service
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSBackendPolicy
metadata: {name: haproxy-proxyproto, namespace: haproxy-ingress}
spec:
  targetRefs: [{group: "", kind: Service, name: haproxy-ingress-kubernetes-ingress}]
  proxyProtocol: true
---
# Internet-facing NLB
apiVersion: gateway.vks.vngcloud.vn/v1alpha1
kind: VKSGatewayPolicy
metadata: {name: nlb-lbspec, namespace: haproxy-ingress}
spec:
  targetRefs: [{group: gateway.networking.k8s.io, kind: Gateway, name: realip-nlb}]
  loadBalancerSpec: {scheme: Internet}
```

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: realip-nlb, namespace: haproxy-ingress}
spec:
  gatewayClassName: vngcloud-nlb
  listeners: [{name: tcp, protocol: TCP, port: 80, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata: {name: realip-route, namespace: haproxy-ingress}
spec:
  parentRefs: [{name: realip-nlb, sectionName: tcp}]
  rules:
  - backendRefs: [{name: haproxy-ingress-kubernetes-ingress, port: 80}]
```

The generated `LoadBalancerConfig` has a `Layer 4` LB with a `PROXY` pool:

```bash
kubectl -n haproxy-ingress get loadbalancerconfig \
  -l gateway.vks.vngcloud.vn/owner-uid \
  -o jsonpath='{.items[0].spec.type}{"  "}{.items[0].spec.pools[0].protocol}{"\n"}'
# Layer 4  PROXY
```

Wait for the NLB address:

```bash
kubectl -n haproxy-ingress get gateway realip-nlb \
  -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}  {.status.addresses[0].value}{"\n"}'
# True  180.93.181.199
```

## 4. Verify

```bash
curl -s -H "Host: whoami.lab" http://180.93.181.199/
```

```
Hostname: whoami-54dd685ffc-xlhwf
RemoteAddr: 172.17.1.122:36898        # HAProxy pod → whoami
...
X-Forwarded-For: 49.213.89.8          # ← the REAL client IP (your egress IP)
```

`X-Forwarded-For` is your public client IP, not a cluster-internal `10.x`/`172.x` address — PROXY protocol is working end-to-end.

## Notes

- **Why PROXY protocol?** An L4 NLB SNATs the source address, so without it the app only sees the node/HAProxy IP. PROXY protocol carries the original client IP at the TCP layer; HAProxy decodes it and sets `X-Forwarded-For` for HTTP backends.
- **Set `proxyProtocol` before creating the Gateway.** The cloud pool protocol is fixed at create time. To change it on a running LB, recreate the Gateway.
- **Trust ranges.** HAProxy's `proxy-protocol` config lists the source ranges allowed to send the header. This example uses broad private ranges for simplicity; scope it to your node/LB CIDR in production. A backend that does **not** accept PROXY protocol will reject the connection.
- **Health checks.** The NLB health-checks the member with a plain TCP connect (no PROXY header); HAProxy accepts the connection, so members stay healthy.
- **HTTPS.** Add a second listener `{name: https, protocol: TCP, port: 443}` and a `TCPRoute` to the controller Service port `443` to pass TLS through to HAProxy (HAProxy terminates TLS). Keep `proxyProtocol: true` on the same backend Service.
- **targetType.** Defaults to `instance` (node IP + nodePort). On a flat/native-routing CNI you can set `VKSBackendPolicy.targetType: ip` to target HAProxy pods directly.
