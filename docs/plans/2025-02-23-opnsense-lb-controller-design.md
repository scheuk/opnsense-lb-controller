# OPNsense LB Controller — Design

**Date:** 2025-02-23

## 1. Goal

A Kubernetes controller that watches LoadBalancer Services with a specific `loadBalancerClass`, allocates a virtual IP (VIP) and syncs NAT/port-forward rules to OPNsense via its REST API, so traffic can be exposed to the internet through OPNsense. The cluster uses Cilium for internal LB; this controller is for external exposure only.

## 2. Architecture & Deployment

- **Controller:** Single Go binary, runs as a Kubernetes Deployment (default one replica). Uses leader election so only the elected replica runs reconciliation and talks to OPNsense.
- **Scope:** Cluster-scoped watch of Services (and Endpoints / Nodes as needed). Only Services with `type: LoadBalancer` and `spec.loadBalancerClass` matching this implementation are reconciled.
- **Deployment:** Can run in-cluster (service account + RBAC) or out-of-cluster with kubeconfig.
- **Deployment options:** Provide raw Kubernetes manifests (`deploy/`) and a **Helm chart** for parameterized installation (image, OPNsense URL/Secret, loadBalancerClass, VIP, replica count, resources, etc.). Chart is the recommended way to install into a cluster.
- **Credentials:** OPNsense API key (or user/password) in a Kubernetes Secret; OPNsense base URL and Secret reference via env or ConfigMap/CLI.

## 3. Source of Desired State (LoadBalancer + loadBalancerClass)

- **Primary source:** Services with `type: LoadBalancer` and `spec.loadBalancerClass` set to this implementation (e.g. `opnsense.org/opnsense-lb`).
- **Per Service:** Allocate or assign a VIP; write back `.status.loadBalancer.ingress[].ip`; create NAT/port-forward rules on OPNsense for each `spec.ports[]` entry, mapping VIP:port → backends (NodePort or ClusterIP).
- **Backends:** Resolved via Service (NodePort + Endpoints/Nodes) or ClusterIP. NodePort by default.
- **Ingress:** Out of scope for v1.

## 4. Components

- **Watched resources:** Service (with our loadBalancerClass), Endpoints, optionally Node for NodePort.
- **Computed state:** VIP(s), set of NAT rules (VIP:port → backend IP:port). Rules tagged so the controller can identify and remove its own rules (e.g. label/comment with `managed-by=opnsense-lb-controller`, Service namespace/name).
- **OPNsense API:** Create/update/delete virtual IPs and NAT port-forward rules. No CRDs.

## 5. Data Flow

- **Trigger:** Informers on Service, Endpoints (and Node if needed). Reconcile key = Service namespace/name. Leader-only reconciliation.
- **Reconcile:** Resolve Service → ports and backends; build desired VIP + NAT rules; fetch current state from OPNsense; diff; apply (create/update/delete). Idempotent; no incremental state stored in cluster beyond `.status.loadBalancer.ingress`.
- **Ordering:** Stable ordering when building desired state (e.g. by namespace/name).

## 6. Error Handling & Robustness

- Reconcile per Service; re-queue on failure with exponential backoff (capped). Optional Kubernetes Event on Service for sync failures.
- Do not set `.status.loadBalancer.ingress` until OPNsense accept; on VIP allocation failure re-queue and optionally emit Event.
- Identify and delete stale rules by label/comment when a Service is removed or no longer matches.
- Startup: if OPNsense unreachable, retry with backoff; optional readiness probe on OPNsense connectivity.

## 7. Testing

- **Unit:** Logic that maps Service + Endpoints → desired OPNsense state (VIP + rules); diff logic if separate. No API calls.
- **Integration (optional):** kind/minikube + mock or real OPNsense API; create LoadBalancer Service with our class; assert status and NAT rules.
- **E2E:** Manual or later; real cluster + OPNsense.

## 8. CI/CD (GitHub Actions + Container Image)

- **CI:** On push/PR: checkout, build Go binary, run unit tests (`go test ./...`). Optional: go vet / golangci-lint. Image build can be on main or on tag.
- **Image:** Build on merge to main (or on release tag). Push to **GitHub Container Registry (ghcr.io)** as `ghcr.io/<org>/opnsense-lb-controller:latest` and `ghcr.io/<org>/opnsense-lb-controller:<tag>`. Use `GITHUB_TOKEN` for push.
- **Dockerfile:** Multi-stage: build stage (Go), minimal runtime image (scratch or distroless), non-root when supported.
- **Helm:** Chart under `helm/opnsense-lb-controller/` (or `charts/opnsense-lb-controller/`) for deployment; values for image, OPNsense config, loadBalancerClass, VIP, and common deployment knobs (replicas, resources, nodeSelector, tolerations).

---

**Next step:** Implementation plan via writing-plans skill.
