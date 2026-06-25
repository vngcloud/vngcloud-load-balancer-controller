# Configuration

The controller is configured via a YAML file mounted at `/etc/vngcloud-load-balancer-controller/config.yaml` inside the pod. When deploying via Helm, this is automatically created from the values you provide.

## Configuration File Reference

```yaml
chartVersion: "0.0.1"

global:
  # VNGCloud IAM endpoint (required)
  identityURL: "https://iamapis.vngcloud.vn/accounts-api"

  # VNGCloud vServer API endpoint (required)
  vserverURL: "https://hcm-3.api.vngcloud.vn/vserver"

  # OAuth2 credentials (required)
  clientID: ""
  clientSecret: ""

  # Optional: override project/user ID (skips metadata service lookup)
  projectID: ""
  userID: 0

  # Optional: super-client credentials for InterVPC load balancers
  superClientID: ""
  superClientSecret: ""

cluster:
  # Kubernetes cluster ID (auto-detected from node labels if not set)
  clusterID: ""

  # Namespace where this controller is deployed
  namespace: "kube-system"

  # VNGCloud region
  region: "hcm-3"

  # Enable remote cluster mode (ClusterAPI)
  isRunRemote: false

loadBalancerOpts:
  # Default package for Network (L4) Load Balancers
  defaultL4PackageName: "NLB_Small"

  # Default package for Application (L7) Load Balancers
  defaultL7PackageName: "ALB_Small"

  # Default scheme: Internal | Internet | InterVPC
  defaultScheme: "Internet"

  # Default pool algorithm: ROUND_ROBIN | LEAST_CONNECTIONS | SOURCE_IP
  defaultPoolAlgorithm: "ROUND_ROBIN"

  # Default health check thresholds
  defaultHealthyThreshold: 3
  defaultUnhealthyThreshold: 3
  defaultInterval: 30
  defaultTimeout: 5

  # Default listener timeouts (seconds)
  defaultTimeoutClient: 50
  defaultTimeoutMember: 50
  defaultTimeoutConnection: 5

  # Default allowed CIDRs for listeners
  defaultAllowedCidrs: "0.0.0.0/0"

globalLoadBalancerOpts:
  # Default package for Global Load Balancers
  defaultL4PackageName: ""

  # Default pool algorithm
  defaultPoolAlgorithm: "ROUND_ROBIN"

  # Default health check thresholds
  defaultHealthyThreshold: 3
  defaultUnhealthyThreshold: 3
  defaultInterval: 30
  defaultTimeout: 5

  # Default listener timeouts (seconds)
  defaultTimeoutClient: 50
  defaultTimeoutMember: 50
  defaultTimeoutConnection: 5

  # Default allowed CIDRs
  defaultAllowedCidrs: ""

# Maximum number of parallel reconcile loops per controller
maxConcurrentReconciles: 5
```

## Helm Values

The Helm chart exposes the most common configuration as values. See all available values:

```bash
helm show values oci://vcr.vngcloud.vn/81-vks-public/vks-helm-charts/vngcloud-load-balancer-controller
```

Key values:

| Value | Description | Default |
|---|---|---|
| `mysecret.global.clientID` | VNGCloud client ID | `""` |
| `mysecret.global.clientSecret` | VNGCloud client secret | `""` |
| `mysecret.global.vserverURL` | vServer API endpoint | `""` |
| `manager.manager.image.repository` | Controller image repository | `vcr.vngcloud.vn/81-vks-public/vngcloud-load-balancer-controller` |
| `manager.manager.image.tag` | Controller image tag | chart's appVersion |
| `manager.replicaCount` | Number of controller replicas | `1` |
| `gatewayApi.alb.enabled` | Enable the [Gateway API (ALB)](../guide/gateway-api.md) controller and install the `vngcloud-alb` GatewayClass. Requires the Gateway-API CRDs (Gateway/HTTPRoute) to be installed first | `false` |
| `gatewayApi.nlb.enabled` | Enable the [Gateway API (NLB / L4)](../guide/gateway-nlb.md) controller and install the `vngcloud-nlb` GatewayClass. Requires the Gateway-API **experimental-channel** CRDs (TCPRoute/UDPRoute) | `false` |

## CLI Flags

The controller binary supports the following flags:

| Flag | Default | Description |
|---|---|---|
| `--metrics-bind-address` | `0` | Address for Prometheus metrics endpoint |
| `--health-probe-bind-address` | `:8081` | Address for liveness/readiness probes |
| `--leader-elect` | `false` | Enable leader election for HA deployments |
| `--metrics-secure` | `true` | Serve metrics over HTTPS |
| `--enable-http2` | `false` | Enable HTTP/2 on the metrics and webhook servers |
| `--log-level` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `--sync-period` | `5m` | Resync period for the informer cache |
| `--disable-service-controller` | `false` | Disable the Service reconciler |
| `--disable-ingress-controller` | `false` | Disable the Ingress reconciler |
| `--disable-load-balancer-config-controller` | `false` | Disable the LoadBalancerConfig reconciler |
| `--disable-global-load-balancer-config-controller` | `false` | Disable the GlobalLoadBalancerConfig reconciler |
| `--disable-node-security-group-controller` | `false` | Disable the NodeSecurityGroup reconciler |
| `--disable-vngcloud-global-load-balancer-controller` | `false` | Disable the VngcloudGlobalLoadBalancer reconciler |
| `--disable-service-glb-controller` | `false` | Disable the Service GLB reconciler |
| `--disable-alb-gateway-controller` | `true` | Disable the Gateway API (ALB) reconcilers. Disabled by default; needs the Gateway-API CRDs (Gateway/HTTPRoute) |
| `--disable-nlb-gateway-controller` | `true` | Disable the Gateway API (NLB / L4) reconcilers. Disabled by default; needs the experimental TCPRoute/UDPRoute CRDs |

## Environment Variables

All config file fields can be overridden by environment variables (via `viper.AutomaticEnv()`). For example:

```bash
GLOBAL_CLIENTID=xxx
GLOBAL_CLIENTSECRET=yyy
GLOBAL_VSERVERURL=https://hcm-3.api.vngcloud.vn/vserver
```
