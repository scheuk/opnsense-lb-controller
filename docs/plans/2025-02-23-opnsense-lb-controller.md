# OPNsense LB Controller Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement a Kubernetes controller that watches LoadBalancer Services with a specific loadBalancerClass, allocates a VIP, syncs NAT/port-forward rules to OPNsense via REST API, and sets Service `.status.loadBalancer.ingress`. CI via GitHub Actions; container image stored on ghcr.io; **Helm chart** for deployment into Kubernetes.

**Architecture:** Go controller with client-go informers (Service, Endpoints, Node). Per-Service reconcile; leader election; OPNsense API client for firewall DNAT and virtual IP (alias). Status written back to Service. **Deployment:** raw manifests in `deploy/` and Helm chart in `helm/opnsense-lb-controller/`. GitHub Actions: test on push/PR, build and push image to ghcr.io on main/tag.

**Tech Stack:** Go 1.21+, client-go, controller-runtime (or raw client-go + workqueue), OPNsense Core API (firewall d_nat, interfaces for VIP), Docker, GitHub Actions, **Helm 3**.

**Design reference:** `docs/plans/2025-02-23-opnsense-lb-controller-design.md`

---

## Task 1: Go module and project layout

**Files:**
- Create: `go.mod`
- Create: `cmd/opnsense-lb-controller/main.go` (minimal, exit 0)
- Create: `README.md` (one paragraph: what this controller does, how to run)

**Step 1: Initialize Go module**

Run: `cd /Users/kevin.scheunemann/work/opnsense-lb-controller && go mod init github.com/<org>/opnsense-lb-controller`  
Replace `<org>` with your GitHub org or username.  
Expected: `go.mod` created.

**Step 2: Add minimal main**

In `cmd/opnsense-lb-controller/main.go`: `package main` with `func main() {}` that exits 0. Build with `go build -o /dev/null ./cmd/opnsense-lb-controller`.  
Expected: build succeeds.

**Step 3: Add README**

In `README.md`: one short paragraph describing the controller (watches LoadBalancer Services with loadBalancerClass, syncs to OPNsense NAT, sets status). No implementation details yet.

**Step 4: Commit**

```bash
git add go.mod cmd/opnsense-lb-controller/main.go README.md
git commit -m "chore: add Go module, minimal main, README"
```

---

## Task 2: Unit test for desired-state computation (failing)

**Files:**
- Create: `internal/controller/desired_state.go` (empty or stub)
- Create: `internal/controller/desired_state_test.go`

**Step 1: Define desired-state types**

In `desired_state.go`: define `DesiredState` struct with `VIP string` and `Rules []NATRule`. `NATRule` has at least: `ExternalPort`, `Protocol`, `Backends []Backend`. `Backend` has `IP`, `Port`. Add a function `ComputeDesiredState(svc *corev1.Service, endpoints *corev1.Endpoints, nodePort int32) (*DesiredState, error)` that returns `nil, nil` for now (or a placeholder that returns an error so the test can fail on "not implemented").

**Step 2: Write failing test**

In `desired_state_test.go`: test that given a LoadBalancer Service with one port (NodePort 30080) and Endpoints with one address, `ComputeDesiredState` returns a `DesiredState` with one rule, one backend, and the expected port. Use a shared VIP (e.g. "192.0.2.1") for the test.  
Expected: test fails (e.g. nil deref or "not implemented").

**Step 3: Run test**

Run: `go test ./internal/controller/... -v -run TestComputeDesiredState`  
Expected: FAIL.

**Step 4: Commit**

```bash
git add internal/controller/desired_state.go internal/controller/desired_state_test.go
git commit -m "test: add failing test for ComputeDesiredState"
```

---

## Task 3: Implement ComputeDesiredState (passing test)

**Files:**
- Modify: `internal/controller/desired_state.go`
- Test: `internal/controller/desired_state_test.go`

**Step 1: Implement ComputeDesiredState**

Implement logic: accept VIP from parameter (or from a config struct). For each `svc.Spec.Ports` (only LoadBalancer ports), find NodePort; from Endpoints take `.Subsets[].Addresses[].IP` (or use Node names to resolve Node IPs if you need node IPs — for a first pass, use Endpoint addresses as backend IPs and NodePort as port, i.e. assume endpoints are node IPs, or document that we use ClusterIP:port for now). Append one NATRule per port with backends. Handle nil Endpoints / empty subsets (return empty backends or error per design).

**Step 2: Run test**

Run: `go test ./internal/controller/... -v -run TestComputeDesiredState`  
Expected: PASS.

**Step 3: Commit**

```bash
git add internal/controller/desired_state.go
git commit -m "feat: implement ComputeDesiredState for Service+Endpoints"
```

---

## Task 4: OPNsense API client (interface + stub)

**Files:**
- Create: `internal/opnsense/client.go` (interface)
- Create: `internal/opnsense/client_test.go` (test that uses a mock or skips if no API)

**Step 1: Define client interface**

In `client.go`: define interface `Client` with methods: `ListNATRules(ctx) ([]NATRule, error)`, `ApplyNATRules(ctx, desired []NATRule, managedBy string) error` (or Create/Delete semantics: pass full desired list and managedBy label; implementation will diff and add/remove). Define `NATRule` struct matching what OPNsense needs (external port, protocol, target IP, target port, description/label). Add a struct `Config` with BaseURL, APIKey, APISecret. Add constructor `NewClient(cfg Config) Client` returning a real implementation that does HTTP calls (stub: return nil from List, no-op from Apply).

**Step 2: Add unit test**

In `client_test.go`: test that with a stub/mock implementation, Apply with one rule doesn’t panic. Or test parsing of OPNsense search_rule response if you add a parser.  
Run: `go test ./internal/opnsense/... -v`  
Expected: PASS.

**Step 3: Commit**

```bash
git add internal/opnsense/client.go internal/opnsense/client_test.go
git commit -m "feat: add OPNsense client interface and stub implementation"
```

---

## Task 5: OPNsense API client — real HTTP (d_nat)

**Files:**
- Modify: `internal/opnsense/client.go`
- Reference: OPNsense docs https://docs.opnsense.org/development/api/core/firewall.html (d_nat)

**Step 1: Implement ListNATRules**

Call OPNsense `GET /api/firewall/d_nat/search_rule` (or equivalent). Authenticate with HTTP Basic (API key : secret). Parse JSON response into `[]NATRule`. Filter or tag rules by description/label containing `managedBy` so we only return rules we manage. Handle HTTP errors and non-2xx (return error).

**Step 2: Implement ApplyNATRules**

Fetch current rules (ListNATRules). Diff desired vs current (by external port + protocol + target, or by our stored UUID/description). For each to delete: call `POST /api/firewall/d_nat/del_rule` with rule UUID. For each to add: call `POST /api/firewall/d_nat/add_rule` with rule payload. Use `managedBy` in description so we can list/delete our rules. Call apply/savepoint if the API requires it to commit firewall config. Do not add integration test against real OPNsense here unless you have a test instance; unit test with a mock or httptest server.

**Step 3: Run tests**

Run: `go test ./internal/opnsense/... -v`  
Expected: PASS (with mock or httptest).

**Step 4: Commit**

```bash
git add internal/opnsense/client.go
git commit -m "feat: implement OPNsense d_nat API client (list and apply rules)"
```

---

## Task 6: Virtual IP (alias) support

**Files:**
- Modify: `internal/opnsense/client.go`
- Reference: OPNsense API for interfaces/virtual IP (alias)

**Step 1: Add VIP methods to interface**

Add `EnsureVIP(ctx, vip string) error` and `RemoveVIP(ctx, vip string) error` (or a single `SetVIPs(ctx, vips []string) error` that ensures only those VIPs exist for our managed alias). Check OPNsense docs for interface/alias API (e.g. `/api/core/alias/` or similar).

**Step 2: Implement and wire**

Implement using OPNsense API to add/delete alias for the given VIP. Tag alias so we can find and remove it (e.g. alias name or description includes controller name). If the design uses a single shared VIP from config, EnsureVIP might be a no-op when VIP is pre-configured; document behavior.

**Step 3: Run tests**

Run: `go test ./internal/opnsense/... -v`  
Expected: PASS.

**Step 4: Commit**

```bash
git add internal/opnsense/client.go
git commit -m "feat: add OPNsense VIP/alias support"
```

---

## Task 7: Controller wiring — informers and workqueue

**Files:**
- Create: `internal/controller/controller.go`
- Modify: `cmd/opnsense-lb-controller/main.go`

**Step 1: Add dependencies**

Run: `go get k8s.io/client-go@v0.29.0 k8s.io/apimachinery@v0.29.0 k8s.io/api@v0.29.0` (or current stable). Add in the controller: rest.Config, clientset, informer factory for core v1 (Service, Endpoints). Add a workqueue (e.g. `workqueue.NewNamedRateLimitingQueue`).

**Step 2: Wire informers**

In `controller.go`: create SharedInformerFactory; add event handlers for Service and Endpoints that enqueue Service key (namespace/name). Filter: only enqueue if Service has `Type == LoadBalancer` and `Spec.LoadBalancerClass != nil` and matches our class (e.g. `opnsense.org/opnsense-lb`). Start informers and wait for cache sync in a Run(ctx) method. Process workqueue in a loop (single worker for now): get key, call reconcile(key).

**Step 3: Stub reconcile**

Reconcile(key): get Service from lister; if not found or not LoadBalancer with our class, return nil. Get Endpoints for Service. Call ComputeDesiredState(svc, endpoints, nodePort). Call OPNsense client EnsureVIP and ApplyNATRules. Update Service status: patch `.status.loadBalancer.ingress = [{ ip: vip }]`. Use typed client for status patch. On error, return error so workqueue retries with backoff.

**Step 4: Main**

In main: load kubeconfig (in-cluster or kubeconfig file from flag/env). Create OPNsense client from env (URL, secret name/namespace). Create controller with clientset and OPNsense client. Run controller (block until context cancelled). No leader election yet.

**Step 5: Build**

Run: `go build -o bin/opnsense-lb-controller ./cmd/opnsense-lb-controller`  
Expected: build succeeds.

**Step 6: Commit**

```bash
git add go.mod go.sum internal/controller/controller.go cmd/opnsense-lb-controller/main.go
git commit -m "feat: wire controller with informers, workqueue, and reconcile stub"
```

---

## Task 8: Leader election

**Files:**
- Modify: `cmd/opnsense-lb-controller/main.go`
- Add: `k8s.io/client-go/tools/leaderelection`

**Step 1: Add leader election**

In main: create LeaderElectionConfig (lease namespace/name, identity, callbacks). On `OnStartedLeading`, start the controller (informers + workqueue loop). On `OnStoppedLeading`, cancel context and exit. Run `leaderelection.RunOrDie(ctx, leaderElectionConfig)`.

**Step 2: Build and run (manual check)**

Build: `go build -o bin/opnsense-lb-controller ./cmd/opnsense-lb-controller`.  
Run in cluster or with kubeconfig: ensure only one replica acquires lease (if you run two processes, one should exit after losing lease).

**Step 3: Commit**

```bash
git add cmd/opnsense-lb-controller/main.go go.mod go.sum
git commit -m "feat: add leader election for controller"
```

---

## Task 9: Backend resolution (Node IP + NodePort)

**Files:**
- Modify: `internal/controller/desired_state.go`
- Modify: `internal/controller/controller.go`

**Step 1: Use Node list when resolving backends**

When computing desired state for NodePort Services: get Node list from lister; for each Endpoint address that is a NodeName, resolve to Node internal IP. Build backends as NodeInternalIP:NodePort. If Endpoint has IP only (pod IP), either skip for NodePort or document that we require Endpoint addresses to be node names (or add another resolution path). Prefer Node list + NodePort for external LB.

**Step 2: Wire Node informer**

In controller: add Node informer; when Node add/update/delete, enqueue all Services that have our loadBalancerClass (broad enqueue) or only Services that reference that node in their Endpoints. Simpler: enqueue all managed Services on Node change.

**Step 3: Run tests**

Run: `go test ./internal/controller/... -v`  
Expected: PASS. Update desired_state test if signature changed (e.g. pass node list or lister).

**Step 4: Commit**

```bash
git add internal/controller/desired_state.go internal/controller/controller.go
git commit -m "feat: resolve Node IP + NodePort for backends, add Node informer"
```

---

## Task 10: Configuration (VIP pool or single VIP, loadBalancerClass value)

**Files:**
- Create: `internal/config/config.go`
- Modify: `cmd/opnsense-lb-controller/main.go`

**Step 1: Add config struct**

In config.go: struct with LoadBalancerClass string (default `opnsense.org/opnsense-lb`), OPNsenseURL string, OPNsenseSecretName, OPNsenseSecretNamespace, and either SingleVIP string or VIPPool (e.g. CIDR or list of IPs). Load from flags or env (e.g. env OPNSENSE_URL, OPNSENSE_SECRET_NAME, VIP or VIP_POOL).

**Step 2: Wire in main and controller**

Main: read config; create OPNsense client from secret (read Secret, get key/secret from data). Controller: when allocating VIP for a Service, use SingleVIP or allocate from pool and store somewhere (e.g. in Service annotation or in-memory map keyed by Service key). If using pool, implement simple allocation (take first free IP) and release on Service delete.

**Step 3: Commit**

```bash
git add internal/config/config.go cmd/opnsense-lb-controller/main.go internal/controller/controller.go
git commit -m "feat: add config for loadBalancerClass, OPNsense, and VIP"
```

---

## Task 11: Deployment and RBAC manifests

**Files:**
- Create: `deploy/namespace.yaml`
- Create: `deploy/rbac.yaml` (ServiceAccount, Role, RoleBinding or ClusterRole/ClusterRoleBinding for Services, Endpoints, Nodes, and lease for leader election)
- Create: `deploy/deployment.yaml`
- Create: `deploy/secret-example.yaml` (example Secret for OPNsense API key, no real secrets)

**Step 1: RBAC**

ServiceAccount in deploy namespace. ClusterRole: get, list, watch on services, endpoints, nodes; patch on services/status; get, list, watch, create, update, patch, delete on leases (coordination.k8s.io) in the same namespace. ClusterRoleBinding binding the ServiceAccount to the ClusterRole.

**Step 2: Deployment**

Deployment: one replica, image placeholder `ghcr.io/<org>/opnsense-lb-controller:latest`, env from Secret for OPNsense URL and API key, readiness probe (optional: HTTP or exec that checks OPNsense connectivity). Command: `/opnsense-lb-controller` (or whatever binary path in image).

**Step 3: Commit**

```bash
git add deploy/
git commit -m "feat: add deploy manifests (RBAC, Deployment, example Secret)"
```

---

## Task 12: Helm chart for deployment

**Files:**
- Create: `helm/opnsense-lb-controller/Chart.yaml`
- Create: `helm/opnsense-lb-controller/values.yaml`
- Create: `helm/opnsense-lb-controller/templates/` (Deployment, ServiceAccount, ClusterRole, ClusterRoleBinding, Secret optional, _helpers.tpl, NOTES.txt)

**Step 1: Chart metadata**

In `Chart.yaml`: name `opnsense-lb-controller`, version `0.1.0`, appVersion matching controller version, description one line.  
In `values.yaml`: image (repository, tag, pullPolicy), replicaCount (default 1), opnsense (url, existingSecret name or create secret from values), loadBalancerClass (default `opnsense.org/opnsense-lb`), vip (single VIP or pool), resources, nodeSelector, tolerations, leaderElection (lease namespace/name).

**Step 2: Templates**

- `_helpers.tpl`: standard labels (app.kubernetes.io/name, chart, release, heritage).
- `serviceaccount.yaml`: ServiceAccount.
- `clusterrole.yaml` + `clusterrolebinding.yaml`: same permissions as deploy/rbac.yaml (services, endpoints, nodes, services/status, leases).
- `deployment.yaml`: Deployment with image from values, env from ConfigMap/Secret (OPNSENSE_URL, secret ref for key/secret), args/flags for loadBalancerClass and VIP. Leader election lease in same namespace as release.
- `secret.yaml`: optional — create Secret for OPNsense API key from values (opnsense.apiKey, opnsense.apiSecret) when existingSecret not set; otherwise use value opnsense.existingSecret.
- `NOTES.txt`: post-install instructions (create LoadBalancer Service with loadBalancerClass, link to docs).

**Step 3: Lint and template**

Run: `helm lint helm/opnsense-lb-controller` and `helm template opnsense-lb-controller helm/opnsense-lb-controller -f helm/opnsense-lb-controller/values.yaml`  
Expected: no errors, valid YAML.

**Step 4: Commit**

```bash
git add helm/
git commit -m "feat: add Helm chart for controller deployment"
```

---

## Task 13: Dockerfile

**Files:**
- Create: `Dockerfile`

**Step 1: Multi-stage Dockerfile**

Stage 1: FROM golang:1.21-alpine, COPY go.mod go.sum, RUN go mod download, COPY ., RUN CGO_ENABLED=0 go build -o /opnsense-lb-controller ./cmd/opnsense-lb-controller.  
Stage 2: FROM scratch (or distroless/static). COPY --from=0 /opnsense-lb-controller /opnsense-lb-controller. ENTRYPOINT ["/opnsense-lb-controller"].  
Optional: add ca-certificates if OPNsense uses HTTPS with a custom CA.

**Step 2: Build image locally**

Run: `docker build -t opnsense-lb-controller:local .`  
Expected: image builds. Run: `docker run --rm opnsense-lb-controller:local --help` or similar to see it starts (may exit quickly without kubeconfig).

**Step 3: Commit**

```bash
git add Dockerfile
git commit -m "feat: add multi-stage Dockerfile"
```

---

## Task 14: GitHub Actions — CI (test on push/PR)

**Files:**
- Create: `.github/workflows/ci.yml`

**Step 1: Workflow**

Name: CI. On: push, pull_request (branches: main, and optionally other branches). Jobs: test. Steps: checkout, set up Go (e.g. actions/setup-go@v5 with go-version-file or version '1.21'), run `go mod download`, run `go test ./... -v`. Optional: go vet ./..., golangci-lint.

**Step 2: Verify**

Push to a branch and open PR (or push to main) and confirm workflow runs and tests pass. Or run locally: `act` or push and check Actions tab.

**Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add GitHub Actions workflow for tests"
```

---

## Task 15: GitHub Actions — build and push image to ghcr.io

**Files:**
- Create or modify: `.github/workflows/build.yml` (or add job to ci.yml)

**Step 1: Workflow**

On: push to main (or on release tag). Jobs: build-and-push. Steps: checkout, log in to ghcr.io using GITHUB_TOKEN (e.g. docker login ghcr.io -u ${{ github.actor }} -p ${{ secrets.GITHUB_TOKEN }}), build Docker image, tag as ghcr.io/${{ github.repository }}:latest and optionally ghcr.io/${{ github.repository }}:${{ github.sha }}, push both. Use build-push-action or docker build and docker push. Ensure permissions: contents read, packages write.

**Step 2: Document**

In README: add section "Container image" with image location `ghcr.io/<org>/opnsense-lb-controller:latest` and how to pull/use.

**Step 3: Commit**

```bash
git add .github/workflows/build.yml README.md
git commit -m "ci: build and push Docker image to ghcr.io"
```

---

## Task 16: README and design doc link

**Files:**
- Modify: `README.md`

**Step 1: Expand README**

Sections: Description, Configuration (env/flags: OPNsense URL, Secret, loadBalancerClass, VIP), Deployment (Helm: `helm install`, or raw `kubectl apply -f deploy/`), Usage (create LoadBalancer Service with spec.loadBalancerClass set), Container image (ghcr.io), Development (build, test), Design (link to docs/plans/2025-02-23-opnsense-lb-controller-design.md).

**Step 2: Commit**

```bash
git add README.md
git commit -m "docs: expand README with config, deploy, and design link"
```

---

**Execution handoff**

Plan complete and saved to `docs/plans/2025-02-23-opnsense-lb-controller.md`.

**Two execution options:**

1. **Subagent-driven (this session)** — I dispatch a fresh subagent per task (or batch of small tasks), review between tasks, fast iteration.
2. **Parallel session (separate)** — Open a new session and use the executing-plans skill there for batch execution with checkpoints.

Which approach do you want?
