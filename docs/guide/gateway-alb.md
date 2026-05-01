# L7 ALB Gateway walkthrough

This guide walks through every supported L7 feature on the `vngcloud-alb` GatewayClass.

## 1. Plain HTTP Gateway

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: demo, namespace: default }
spec:
  gatewayClassName: vngcloud-alb
  listeners:
  - { name: http, protocol: HTTP, port: 80 }
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: demo, namespace: default }
spec:
  parentRefs: [{ name: demo }]
  hostnames: [demo.example.com]
  rules:
  - matches: [{ path: { type: PathPrefix, value: / } }]
    backendRefs: [{ name: demo-service, port: 80 }]
```

The controller provisions a vngcloud ALB, attaches one HTTP listener on port 80, and creates a synthetic pool whose members are the ready endpoints of `demo-service` (target-type honors the per-Service `TargetGroupConfig` if present, otherwise defaults to `instance` / NodePort).

## 2. HTTPS with K8s TLS Secret

```yaml
spec:
  listeners:
  - name: https
    protocol: HTTPS
    port: 443
    tls:
      mode: Terminate
      certificateRefs:
      - kind: Secret
        name: demo-tls
```

The controller imports the Secret (kubernetes.io/tls) into a vngcloud certificate via the existing `lbc_uc` cert-import path before listener creation.

## 3. HTTPS with vngcloud cert ID (LBC override)

When you already have a vngcloud certificate provisioned out of band, point a per-Gateway `LoadBalancerConfig` at it:

```yaml
apiVersion: vks.vngcloud.vn/v1alpha1
kind: LoadBalancerConfig
metadata: { name: demo-tls-config, namespace: default }
spec:
  type: Layer 7
  listeners:
  - name: https
    protocol: HTTPS
    protocolPort: 443
    certificateDefault:
      id: cert-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
    sslPolicy: TLS-1-2-2021
    alpnPolicy: HTTP2Optional
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: demo-tls, namespace: default }
spec:
  gatewayClassName: vngcloud-alb
  infrastructure:
    parametersRef:
      group: vks.vngcloud.vn
      kind: LoadBalancerConfig
      name: demo-tls-config
  listeners:
  - name: https
    protocol: HTTPS
    port: 443
```

The Gateway listener and the LBC `Listeners[]` entry are matched by `name`. The LBC's `certificateDefault.id` wins over `certificateRefs` Secret import.

## 4. Multi-cert SNI

Either supply multiple `certificateRefs` (first = default, rest = SNI alternates) on the Gateway listener, or list multiple certificate IDs in the LBC entry:

```yaml
listeners:
- name: https
  protocol: HTTPS
  port: 443
  tls:
    mode: Terminate
    certificateRefs:
    - { kind: Secret, name: a-com-tls }
    - { kind: Secret, name: b-com-tls }
```

## 5. mTLS (`frontendValidation`)

Gateway-API v1.2 promoted `tls.frontendValidation`. Either reference a CA certificate via Secret/ConfigMap, or set the LBC `clientCertificateId` for a pre-provisioned vngcloud client cert:

```yaml
listeners:
- name: https
  protocol: HTTPS
  port: 443
  tls:
    mode: Terminate
    certificateRefs: [{ kind: Secret, name: demo-tls }]
    frontendValidation:
      caCertificateRefs:
      - { kind: Secret, name: client-ca }
```

## 6. Cross-namespace backendRefs

```yaml
# In ns-a:
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: demo, namespace: ns-a }
spec:
  parentRefs: [{ name: demo }]
  rules:
  - backendRefs:
    - { name: demo-service, namespace: ns-b, port: 80 }
---
# In ns-b: permit refs from ns-a HTTPRoutes targeting Service demo-service
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata: { name: from-ns-a, namespace: ns-b }
spec:
  from:
  - { group: gateway.networking.k8s.io, kind: HTTPRoute, namespace: ns-a }
  to:
  - { group: "", kind: Service, name: demo-service }
```

Without the grant, the route's `ResolvedRefs` condition flips to `False, reason=RefNotPermitted` and that backend is dropped from the synthesized pool (other backends still flow).

## Status conditions

Each Gateway and route reports the [standard Gateway-API conditions](https://gateway-api.sigs.k8s.io/concepts/api-overview/#status):
- Gateway: `Accepted`, `Programmed`. Listener-level: `Accepted`, `Programmed`, `ResolvedRefs`.
- HTTPRoute (per parentRef): `Accepted`, `ResolvedRefs`. Use `kubectl describe` to read.
