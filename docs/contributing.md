# Contributing

We welcome contributions to the VNGCloud Load Balancer Controller!

## Development Setup

### Prerequisites

- Go 1.25.11+
- Docker
- `kubectl` and access to a Kubernetes cluster
- `make`

### Clone the repository

```bash
git clone https://github.com/vngcloud/vngcloud-load-balancer-controller.git
cd vngcloud-load-balancer-controller
```

### Local configuration

Create the config file at `/etc/vngcloud-load-balancer-controller/config.yaml`:

```yaml
chartVersion: 0.0.0
global:
  clientID: "your-client-id"
  clientSecret: "your-client-secret"
  identityURL: "https://iamapis.vngcloud.vn/accounts-api"
  vserverURL: "https://hcm-3.api.vngcloud.vn/vserver"
  projectID: "pro-your-project-id"
```

### Run locally

```bash
make install run
```

## Running Tests

```bash
make test
```

For focused integration tests:

```bash
go clean -testcache && go test -v ./internal/controller/networking/... \
  -ginkgo.focus="When node status changes from not ready to ready"
```

The Gateway API e2e suite provisions **real** VNGCloud ALBs and is opt-in; it needs a real cluster with the controller running:

```bash
RUN_GATEWAY_E2E=true KUBECONFIG=/path/to/cluster.yaml go test ./test/e2e/gateway/ -v
```

## Code Generation

After modifying CRD types (`api/v1alpha1/*.go`), regenerate:

```bash
make generate
make manifests kustomize helm
```

After modifying mockery config (`.mockery.yml`):

```bash
mockery
```

## Building

```bash
# Build and push image
make docker-build docker-push IMG=<registry>/vngcloud-load-balancer-controller:<tag>

# Deploy to cluster
make deploy IMG=<registry>/vngcloud-load-balancer-controller:<tag>
```

## Working on Documentation

```bash
pip install pipenv
pipenv install

# Preview docs locally
pipenv run mkdocs serve

# Build static site
pipenv run mkdocs build
```

## Creating a New Controller

```bash
kubebuilder create api --group core --version v1 --kind Service --resource=false --controller=true
go mod tidy
make generate
make manifests kustomize helm
```

## Linting

```bash
make lint
make lint-fix
```

## Submitting a Pull Request

1. Fork the repository and create a feature branch
2. Make your changes with tests
3. Run `make test lint`
4. Open a PR against the `main` branch

For more information see the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html).
