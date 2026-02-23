# opnsense-lb-controller

A Kubernetes controller that watches LoadBalancer Services with a specific `loadBalancerClass`, allocates a virtual IP (VIP), syncs NAT and port-forward rules to OPNsense via its REST API, and sets the Service `.status.loadBalancer.ingress` so the cluster can expose traffic through OPNsense. Run the controller in your cluster (see Deployment section) and create LoadBalancer Services with the configured class to use it.

## Container image

Images are published to GitHub Container Registry:

- **Latest:** `ghcr.io/scheuk/opnsense-lb-controller:latest`
- **By SHA:** `ghcr.io/scheuk/opnsense-lb-controller:<git-sha>`

Pull with: `docker pull ghcr.io/scheuk/opnsense-lb-controller:latest`
