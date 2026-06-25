# VNGCloud Load Balancer Controller

The **VNGCloud Load Balancer Controller** is a Kubernetes controller that manages VNGCloud load balancers for Kubernetes clusters running on VNGCloud Kubernetes Service (VKS).

It watches Kubernetes `Service` (type `LoadBalancer`) and `Ingress` resources and automatically provisions and manages the corresponding VNGCloud load balancer resources.

## Features

- **L4 Load Balancing** — Automatically provisions Network Load Balancers for `Service` resources of type `LoadBalancer`
- **L7 Load Balancing** — Manages Application Load Balancers via Kubernetes `Ingress` resources
- **Gateway API** — Provisions Application Load Balancers from `Gateway`/`HTTPRoute` resources (and Network Load Balancers from `TCPRoute`/`UDPRoute`), customised via GEP-713 policy CRDs (`VKSGatewayPolicy`, `VKSBackendPolicy`, `VKSHealthCheckPolicy`, `VKSRoutePolicy`)
- **LoadBalancerConfig CRD** — Fine-grained control over load balancer configuration (listeners, pools, policies, certificates)
- **NodeSecurityGroup CRD** — Manages security group rules for cluster nodes
- **Global Load Balancer** — Multi-region traffic distribution via the `VngcloudGlobalLoadBalancer` CRD
- **Annotation-driven configuration** — Customise load balancer behaviour using `vks.vngcloud.vn/*` annotations
- **Prometheus metrics** — Built-in metrics for observability

## How It Works

```
Kubernetes Event
      │
      ▼
 EventHandler  ──────────────────────────────────────┐
      │                                               │
      ▼                                               │
  Controller (Reconciler)                             │
      │                                               │
      ▼                                               │
  UseCase Layer ◄── Annotation Parser                 │
      │                                               │
      ├── K8s Repository ──► Kubernetes API           │
      │                                               │
      └── VNGCloud Repository ──► VNGCloud VLB API    │
```

1. A Kubernetes event (Service, Ingress, Node change) triggers the controller.
2. The controller delegates all business logic to the UseCase layer.
3. The UseCase reads the current Kubernetes state and desired configuration from annotations/CRDs.
4. It reconciles the VNGCloud load balancer (create/update/delete pools, listeners, policies).
5. The Service or Ingress status is updated with the assigned load balancer address.

## Architecture Overview

The controller follows a clean **Controller → UseCase → Repository** layered architecture:

| Layer | Responsibility |
|---|---|
| Controller | Watches Kubernetes resources, enqueues reconcile requests |
| UseCase | All business logic — desired state computation and reconciliation |
| Repository | I/O abstraction — Kubernetes API and VNGCloud API calls |
| Domain | Shared constants, finalizers, error types |

## Quick Start

```bash
helm install vngcloud-load-balancer-controller \
  oci://vcr.vngcloud.vn/81-vks-public/vks-helm-charts/vngcloud-load-balancer-controller \
  --namespace kube-system \
  --set mysecret.global.clientID="<YOUR_CLIENT_ID>" \
  --set mysecret.global.clientSecret="<YOUR_CLIENT_SECRET>" \
  --set mysecret.global.vserverURL="https://hcm-3.api.vngcloud.vn/vserver"
```

See the [Installation guide](deploy/installation.md) for full details.
