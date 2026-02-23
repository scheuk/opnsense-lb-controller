# opnsense-lb-controller

A Kubernetes controller that watches LoadBalancer Services with a specific `loadBalancerClass`, allocates a virtual IP (VIP), syncs NAT and port-forward rules to OPNsense via its REST API, and sets the Service `.status.loadBalancer.ingress` so the cluster can expose traffic through OPNsense.

## Configuration

Configuration is via environment variables (or flags in the future):

| Variable | Description |
|----------|-------------|
| `OPNSENSE_URL` | OPNsense API base URL (e.g. `https://firewall.example.com`) |
| `OPNSENSE_SECRET_NAME` | Name of the Kubernetes Secret containing API credentials |
| `OPNSENSE_SECRET_NAMESPACE` | Namespace of the Secret (default: `default`) |
| `OPNSENSE_API_KEY`, `OPNSENSE_API_SECRET` | API key/secret (optional if using Secret) |
| `LOAD_BALANCER_CLASS` | Value to match on `spec.loadBalancerClass` (default: `opnsense.org/opnsense-lb`) |
| `VIP` | Single VIP for all Services, or leave unset when using `VIP_POOL` |
| `VIP_POOL` | Comma-separated list of IPs for per-Service allocation |
| `LEASE_NAMESPACE`, `LEASE_NAME` | Leader election lease namespace and name |

## Deployment

### Helm (recommended)

```bash
helm install opnsense-lb-controller ./helm/opnsense-lb-controller \
  --namespace opnsense-lb-controller \
  --create-namespace \
  --set opnsense.existingSecret=opnsense-lb-controller-api \
  --set opnsense.url=https://opnsense.example.com
```

Create a Secret with your OPNsense API key and secret, then install with `opnsense.existingSecret` set to that Secret name.

### Raw manifests

```bash
kubectl apply -f deploy/namespace.yaml
kubectl apply -f deploy/rbac.yaml
kubectl apply -f deploy/secret-example.yaml   # edit with real credentials
kubectl apply -f deploy/deployment.yaml
```

## Usage

Create a LoadBalancer Service with the controller's `loadBalancerClass`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-app
spec:
  type: LoadBalancer
  loadBalancerClass: opnsense.org/opnsense-lb
  ports:
    - port: 80
      targetPort: 8080
      nodePort: 30080
```

The controller will allocate a VIP, create NAT rules on OPNsense, and set `status.loadBalancer.ingress[].ip` on the Service.

## Container image

Images are published to GitHub Container Registry:

- **Latest:** `ghcr.io/scheuk/opnsense-lb-controller:latest`
- **By SHA:** `ghcr.io/scheuk/opnsense-lb-controller:<git-sha>`

Pull with: `docker pull ghcr.io/scheuk/opnsense-lb-controller:latest`

## Development

```bash
go build -o bin/opnsense-lb-controller ./cmd/opnsense-lb-controller
go test ./...
```

Integration tests use envtest and run with `go test ./...` (see [Integration testing design](docs/plans/2025-02-23-integration-testing-design.md)). To run them locally, set `KUBEBUILDER_ASSETS` (e.g. `setup-envtest use -p path`); otherwise integration tests are skipped.

## Design

See [docs/plans/2025-02-23-opnsense-lb-controller-design.md](docs/plans/2025-02-23-opnsense-lb-controller-design.md) for architecture and design details.
