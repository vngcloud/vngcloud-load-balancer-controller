# VNGCloud Load Balancer Controller

[![Go Version](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](https://www.apache.org/licenses/LICENSE-2.0)
[![Helm Chart](https://img.shields.io/badge/helm-OCI-0F1689?logo=helm)](https://vcr.vngcloud.vn/81-vks-public/vks-helm-charts/vngcloud-load-balancer-controller)
[![Documentation](https://img.shields.io/badge/docs-mkdocs-526CFE?logo=materialformkdocs)](https://vngcloud.github.io/vngcloud-load-balancer-controller/)

A Kubernetes controller that provisions and manages [VNGCloud](https://vngcloud.vn/) load balancers
for clusters running on **VNGCloud Kubernetes Service (VKS)**. It watches `Service` (type
`LoadBalancer`) and `Ingress` resources and reconciles the corresponding VNGCloud Network and
Application Load Balancers automatically.

> **Status:** General availability. Latest release: see [`charts/vngcloud-load-balancer-controller/Chart.yaml`](charts/vngcloud-load-balancer-controller/Chart.yaml).

---

## Features

- **L4 Load Balancing** — Network Load Balancers for `Service` resources of type `LoadBalancer`.
- **L7 Load Balancing** — Application Load Balancers driven by Kubernetes `Ingress` resources.
- **Gateway API** — Application Load Balancers driven by `Gateway`/`HTTPRoute`, and Network Load Balancers driven by `TCPRoute`/`UDPRoute`, customised via GEP-713 policy CRDs.
- **`LoadBalancerConfig` CRD** — Fine-grained control over listeners, pools, policies, and TLS certificates.
- **`NodeSecurityGroup` CRD** — Declarative management of node security-group rules.
- **`GlobalLoadBalancerConfig` / `VngcloudGlobalLoadBalancer` CRDs** — Multi-region traffic distribution.
- **Annotation-driven configuration** — Tune behaviour via `vks.vngcloud.vn/*` annotations on `Service` / `Ingress`.
- **Status conditions and Kubernetes events** — Surfaces reconcile state on the owning resource for `kubectl describe`.
- **Prometheus metrics** — Built-in `/metrics` endpoint for observability.
- **Leader election & graceful shutdown** — Safe to run in HA deployments.

## Architecture

The controller follows a layered **Controller → UseCase → Repository** architecture:

```
Kubernetes Event
      │
      ▼
 EventHandler
      │
      ▼
  Controller (Reconciler)
      │
      ▼
  UseCase Layer ◄── Annotation Parser
      │
      ├── K8s Repository ──► Kubernetes API
      │
      └── VNGCloud Repository ──► VNGCloud VLB API
```

| Layer | Responsibility |
|---|---|
| Controller | Watches Kubernetes resources, enqueues reconcile requests |
| UseCase | Business logic — desired state computation and reconciliation |
| Repository | I/O abstraction — Kubernetes API and VNGCloud API |
| Domain | Shared constants, finalizers, error types |

See the [architecture overview](https://vngcloud.github.io/vngcloud-load-balancer-controller/) in the documentation for a deeper dive.

## Prerequisites

- A running VKS (VNGCloud Kubernetes Service) cluster.
- Kubernetes **v1.20+** (tested on current VKS-supported versions).
- `kubectl` and `helm` v3+.
- VNGCloud IAM credentials (`Client ID` and `Client Secret`) with permission to manage VLB resources.

## Quick Start

Install via Helm from the official OCI registry:

```bash
# HCM region
helm install vngcloud-load-balancer-controller \
  oci://vcr.vngcloud.vn/81-vks-public/vks-helm-charts/vngcloud-load-balancer-controller \
  --namespace kube-system \
  --set mysecret.global.clientID="<YOUR_CLIENT_ID>" \
  --set mysecret.global.clientSecret="<YOUR_CLIENT_SECRET>" \
  --set mysecret.global.vserverURL="https://hcm-3.api.vngcloud.vn/vserver"
```

```bash
# HAN region
helm install vngcloud-load-balancer-controller \
  oci://vcr-han.vngcloud.vn/81-vks-public/vks-helm-charts/vngcloud-load-balancer-controller \
  --namespace kube-system \
  --set mysecret.global.clientID="<YOUR_CLIENT_ID>" \
  --set mysecret.global.clientSecret="<YOUR_CLIENT_SECRET>" \
  --set mysecret.global.vserverURL="https://han-1.api.vngcloud.vn/vserver"
```

Verify the install:

```bash
kubectl get pods -n kube-system -l app.kubernetes.io/name=vngcloud-load-balancer-controller
```

For raw-manifest installation, upgrade procedures, and the full configuration reference, see the
[Installation guide](docs/deploy/installation.md), [Configuration reference](docs/deploy/configuration.md),
and [Upgrade guide](docs/deploy/upgrade.md).

## Usage

### Service (L4)

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-app
  annotations:
    vks.vngcloud.vn/load-balancer-name: my-app-lb
spec:
  type: LoadBalancer
  selector:
    app: my-app
  ports:
    - port: 80
      targetPort: 8080
```

### Ingress (L7)

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app
  annotations:
    vks.vngcloud.vn/load-balancer-name: my-app-alb
spec:
  ingressClassName: vngcloud
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-app
                port:
                  number: 80
```

More guides:

- [Service (L4)](docs/guide/service.md) · [Ingress (L7)](docs/guide/ingress.md) · [Gateway API (ALB)](docs/guide/gateway-api.md) · [Gateway API (NLB)](docs/guide/gateway-nlb.md)
- [`LoadBalancerConfig` CRD](docs/guide/load-balancer-config.md)
- [`NodeSecurityGroup` CRD](docs/guide/node-security-group.md)
- [Global Load Balancer](docs/guide/global-load-balancer.md)
- Annotations: [Service](docs/annotations/service.md) · [Ingress](docs/annotations/ingress.md)
- Examples: [TLS termination](docs/examples/tls-termination.md) · [Internal LB](docs/examples/internal-lb.md) · [Custom health check](docs/examples/custom-healthcheck.md)

## Documentation

Full docs are published at <https://vngcloud.github.io/vngcloud-load-balancer-controller/> and the
sources live under [`docs/`](docs/).

## Roadmap

- [x] Gateway API support — Phase 1: ALB (`Gateway` + `HTTPRoute` + policy CRDs)
- [x] Gateway API Phase 2: NLB (`TCPRoute`/`UDPRoute`)
- [ ] Gateway API Phase 3+: `TLSRoute`, `GRPCRoute`, conformance
- [ ] End-to-end test suite
- [ ] Validating / mutating webhooks
- [ ] Out-of-band drift reconciliation (detect external load-balancer changes)
- [ ] Migration from `EndpointSlice` (deprecated `v1.Endpoints` watchers)

## Contributing

Contributions are welcome. See [`docs/contributing.md`](docs/contributing.md) for the full
development setup, local-run instructions, code-generation workflow, and PR guidelines.

Quick reference:

```bash
make help           # list all make targets
make test           # run unit and integration tests
make lint lint-fix  # static analysis
make install run    # run the controller locally against the current kubeconfig
```

## Support

- **Issues / bug reports:** <https://github.com/vngcloud/vngcloud-load-balancer-controller/issues>
- **VNGCloud support:** <https://support.vngcloud.vn/>

## License

Copyright 2024 VNGCloud.

Licensed under the [Apache License, Version 2.0](https://www.apache.org/licenses/LICENSE-2.0).
Unless required by applicable law or agreed to in writing, software distributed under the License
is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express
or implied.
