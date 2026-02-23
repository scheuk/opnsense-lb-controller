# Integration Testing Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add integration tests using envtest and an in-memory mock OPNsense client so the controller’s create/cleanup/update behavior is validated before deployment; tests run with `go test ./...` in CI (no build tag).

**Architecture:** controller-runtime envtest provides an in-process API server and etcd. A mock implementing `opnsense.Client` holds VIPs and NAT rules in memory. The real controller runs against envtest with this mock; tests create Services/Endpoints/Nodes, then poll until status and mock state match expectations.

**Tech Stack:** Go, k8s.io/client-go, sigs.k8s.io/controller-runtime (envtest), existing internal/controller and internal/opnsense packages.

**Design reference:** `docs/plans/2025-02-23-integration-testing-design.md`

---

### Task 1: Add envtest dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum` (will be updated by go mod)

**Step 1: Add controller-runtime with envtest**

Run:
```bash
cd /Users/kevin.scheunemann/work/opnsense-lb-controller && go get sigs.k8s.io/controller-runtime@v0.21.4
```
Use a version compatible with k8s.io 0.35 (e.g. v0.21.x or whatever `go get` resolves). If envtest is in a separate module, add `sigs.k8s.io/controller-runtime/pkg/envtest` and ensure the same controller-runtime version is used.

**Step 2: Tidy and verify**

Run:
```bash
go mod tidy
go build ./cmd/opnsense-lb-controller
go test ./... -count=1
```
Expected: build and existing tests pass.

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add controller-runtime for envtest"
```

---

### Task 2: Implement mock OPNsense client

**Files:**
- Create: `internal/controller/fake_opnsense.go`

**Step 1: Add fake client implementing opnsense.Client**

Create `internal/controller/fake_opnsense.go` with an in-memory implementation of `opnsense.Client`:

- Type `FakeOPNsense` (or `MockOPNsense`) with mutex-protected state: `vips map[string]struct{}` and `rules []fakeNATRule` where `fakeNATRule` has UUID, ExternalPort, Protocol, TargetIP, TargetPort, Description, and a field to store `serviceKey` (e.g. parsed from Description or stored when applying).
- `EnsureVIP(ctx, vip string) error`: add `vip` to the map; return nil.
- `RemoveVIP(ctx, vip string) error`: delete from map; return nil.
- `ListNATRules(ctx) ([]opnsense.NATRule, error)`: return current rules as `opnsense.NATRule` (set UUID for each so controller can reference; use a counter or uuid). Rules must be stored with enough info to filter by `managedBy`/`serviceKey` in `ApplyNATRules`.
- `ApplyNATRules(ctx, desired []opnsense.NATRule, managedBy, serviceKey string) error`: remove all rules that belong to this `managedBy`+`serviceKey` (e.g. match description prefix or a separate map keyed by serviceKey). Append `desired` rules, storing each with serviceKey. Return nil.

Helper: rule “belongs to” service if description has the form `managedBy + " " + serviceKey + " " + vip` (see `desiredStateToOPNsenseRules` in controller.go). You can store `serviceKey` in a wrapper or match by description prefix.

Expose for tests:
- `VIPs() []string` — return current VIP set (copy).
- `NATRulesFor(serviceKey string) []opnsense.NATRule` — return rules that were applied for that serviceKey (filter stored rules by serviceKey).

Use `sync.RWMutex` for all state access.

**Step 2: Run build and tests**

Run:
```bash
go build ./cmd/opnsense-lb-controller
go test ./internal/controller/... -count=1 -v
```
Expected: PASS (existing unit tests only; no integration test yet).

**Step 3: Commit**

```bash
git add internal/controller/fake_opnsense.go
git commit -m "test: add fake OPNsense client for integration tests"
```

---

### Task 3: envtest setup and shared test harness

**Files:**
- Create: `internal/controller/envtest_suite.go` (or integrate into `integration_test.go` in a way that starts envtest once)

**Step 1: Start envtest and controller in TestMain or helper**

Create a test file that uses build constraint so it only compiles when envtest is needed (optional: use a separate `integration_test.go` and a shared `envtest.go` in the same package with `//go:build !integration` to avoid pulling envtest in short tests — but we decided no tag, so all tests run always). So: single file `integration_test.go` that starts envtest once.

Approach: use a global or package-level `envtest.Environment`, start it in `TestMain`, and provide a function that returns `*rest.Config`, `kubernetes.Interface`, and a way to create a controller and run it in the background.

Example structure:

```go
// In integration_test.go (or envtest_suite_test.go)
var (
	testEnv    *envtest.Environment
	cfg        *rest.Config
	k8sClient  kubernetes.Interface
	startOnce  sync.Once
	startErr   error
)

func testStartEnvtest() (*rest.Config, kubernetes.Interface, error) {
	startOnce.Do(func() {
		testEnv = &envtest.Environment{}
		cfg, startErr = testEnv.Start()
		if startErr != nil {
			return
		}
		k8sClient, startErr = kubernetes.NewForConfig(cfg)
	})
	return cfg, k8sClient, startErr
}
```

In `TestMain`:
- Call `testStartEnvtest()`; if err != nil, os.Exit(1).
- Start the controller in a goroutine (see Task 4) with a cancel context.
- Run `m.Run()`.
- Stop envtest: `testEnv.Stop()`.
- os.Exit(code).

**Step 2: Add helper to create controller and run in background**

Helper `startController(ctx context.Context, cfg *rest.Config, mock opnsense.Client, vipAlloc config.VIPAllocator)`: build controller with `controller.NewController(cfg, mock, "opnsense.org/opnsense-lb", vipAlloc, "opnsense-lb-controller")`, then `go ctrl.Run(ctx)`. Return the controller (or just run; tests will use the same cfg and clientset to create resources and poll).

**Step 3: Verify envtest starts**

Run:
```bash
go test ./internal/controller/... -count=1 -v -run TestMain 2>&1 | head -30
```
Or run a minimal test that only calls `testStartEnvtest()` and then exits. Expected: envtest starts (may download binaries first run), then test exits. If TestMain doesn’t run as a test, add a trivial test like `func TestEnvtestStart(t *testing.T) { _, _, err := testStartEnvtest(); if err != nil { t.Fatal(err) } }` and run it.

**Step 4: Commit**

```bash
git add internal/controller/integration_test.go
git commit -m "test: add envtest harness for integration tests"
```

---

### Task 4: Helper to create Namespace, Node, Service, Endpoints

**Files:**
- Modify: `internal/controller/integration_test.go`

**Step 1: Create test namespace helper**

Add function:
```go
func createNamespace(ctx context.Context, t *testing.T, client kubernetes.Interface, name string) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	ns, err := client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	return ns
}
```

**Step 2: Create Node with InternalIP**

Add function that creates a Node with `Status.Addresses` containing one entry `Type: NodeInternalIP, Address: "192.0.2.10"` (or configurable). Use the same clientset and ctx.

**Step 3: Create LoadBalancer Service with loadBalancerClass**

Add function that creates a Service in the given namespace: `Type: LoadBalancer`, `LoadBalancerClass: ptr("opnsense.org/opnsense-lb")`, one port e.g. Port 80, TargetPort 8080, NodePort 30080. Return the created Service.

**Step 4: Create Endpoints**

Add function that creates Endpoints in the same namespace with the same name as the Service, one subset with one address (IP can be "10.0.0.1" and optionally NodeName set to the node name so that NodePort resolution uses the node’s InternalIP). Ensure the Endpoints name matches the Service name.

**Step 5: Run tests**

Run:
```bash
go test ./internal/controller/... -count=1 -v
```
Expected: existing tests pass; new helpers are not yet used by a scenario test.

**Step 6: Commit**

```bash
git add internal/controller/integration_test.go
git commit -m "test: add helpers to create namespace, node, service, endpoints"
```

---

### Task 5: Wait-for-status helper

**Files:**
- Modify: `internal/controller/integration_test.go`

**Step 1: Implement wait for ingress IP**

Add:
```go
func waitForIngressIP(ctx context.Context, t *testing.T, client kubernetes.Interface, ns, svcName string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		svc, err := client.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get service: %v", err)
		}
		if len(svc.Status.LoadBalancer.Ingress) > 0 && svc.Status.LoadBalancer.Ingress[0].IP != "" {
			return svc.Status.LoadBalancer.Ingress[0].IP
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for status.loadBalancer.ingress (within %v)", timeout)
	return ""
}
```

**Step 2: Commit**

```bash
git add internal/controller/integration_test.go
git commit -m "test: add waitForIngressIP helper"
```

---

### Task 6: Scenario 1 — Create (happy path)

**Files:**
- Modify: `internal/controller/integration_test.go`

**Step 1: Start controller in TestMain with mock and VIP allocator**

In TestMain (or before tests): ensure envtest is started, then create `FakeOPNsense`, `config.NewVIPAllocator` with e.g. `&config.Config{VIPPool: []string{"192.0.2.1", "192.0.2.2"}}` (you need a VIPAllocator; use a small pool). Start the controller in a goroutine with `startController(ctx, cfg, mock, vipAlloc)`. Use a context that is cancelled when m.Run() returns (e.g. pass a cancel into a helper that runs the controller).

**Step 2: Write TestIntegration_CreateService**

- Call `testStartEnvtest()` (or use package-level cfg/k8sClient from TestMain).
- Create context with timeout (e.g. 30s).
- Create namespace (e.g. "test-create-" + t.Name() or random suffix).
- Create Node with InternalIP 192.0.2.10, name e.g. "node-1".
- Create LoadBalancer Service with one port (80 -> 8080, NodePort 30080), our loadBalancerClass.
- Create Endpoints with one subset, one address (IP "10.0.0.1", NodeName "node-1" so backend becomes 192.0.2.10:30080).
- Call waitForIngressIP with e.g. 10s timeout. Assert returned IP is one of the pool (192.0.2.1 or 192.0.2.2).
- Assert mock: mock.VIPs() contains that IP; mock.NATRulesFor("default/svc-name") (or ns/name) has one rule with ExternalPort 80, TargetIP 192.0.2.10, TargetPort 30080.

Note: controller must be running before creating the Service; ensure TestMain (or a package init) starts the controller once with a shared mock and allocator. If each test needs a fresh mock, we need to refactor so each test gets its own controller + mock; design said “shared envtest,” so one controller instance with one mock is acceptable — then use unique namespaces per test and assert mock state for that namespace/service name (serviceKey = ns/name).

**Step 3: Run the test**

Run:
```bash
go test ./internal/controller/... -count=1 -v -run TestIntegration_CreateService
```
Expected: test runs, may need to fix small issues (e.g. Node not ready so Endpoints might not be used — ensure Node has Ready condition if controller filters; or ensure we don’t depend on that). Fix until PASS.

**Step 4: Commit**

```bash
git add internal/controller/integration_test.go
git commit -m "test: integration create LoadBalancer Service and assert VIP and NAT"
```

---

### Task 7: Scenario 2 — Cleanup (delete Service)

**Files:**
- Modify: `internal/controller/integration_test.go`

**Step 1: Write TestIntegration_DeleteService_Cleanup**

- Setup: create namespace, Node, Service (with our loadBalancerClass), Endpoints; wait for ingress IP and assert mock has NAT rules for that serviceKey.
- Delete the Service (clientset.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})).
- Wait until mock has no NAT rules for that serviceKey (poll mock.NATRulesFor(serviceKey) with timeout until len == 0).
- If using pool allocator, assert VIP was released (mock.VIPs() no longer contains the previously allocated IP, or we don’t call RemoveVIP for pool — check controller: for pool, GetVIP returns the assigned VIP and cleanup calls RemoveVIP). So assert mock’s VIP set no longer has that VIP after cleanup.

**Step 2: Run the test**

Run:
```bash
go test ./internal/controller/... -count=1 -v -run TestIntegration_DeleteService_Cleanup
```
Expected: PASS.

**Step 3: Commit**

```bash
git add internal/controller/integration_test.go
git commit -m "test: integration delete Service and assert NAT and VIP cleanup"
```

---

### Task 8: Scenario 2b — Cleanup when loadBalancerClass changed

**Files:**
- Modify: `internal/controller/integration_test.go`

**Step 1: Write TestIntegration_ChangeLoadBalancerClass_Cleanup**

- Setup: same as create (namespace, Node, Service with our class, Endpoints); wait for ingress.
- Patch Service to set LoadBalancerClass to something else (e.g. "other.org/lb") or set to nil. Use Patch with merge type.
- Poll until mock.NATRulesFor(serviceKey) is empty and VIP released (same as delete).
- Optionally assert Service status still exists but controller no longer owns it (no need to clear status in our controller when we “release” — design said “remove NAT and release VIP”). So just assert mock cleanup.

**Step 2: Run the test**

Run:
```bash
go test ./internal/controller/... -count=1 -v -run TestIntegration_ChangeLoadBalancerClass_Cleanup
```
Expected: PASS.

**Step 3: Commit**

```bash
git add internal/controller/integration_test.go
git commit -m "test: integration change loadBalancerClass and assert cleanup"
```

---

### Task 9: Scenario 3 — Update ports

**Files:**
- Modify: `internal/controller/integration_test.go`

**Step 1: Write TestIntegration_UpdatePorts**

- Setup: create namespace, Node, Service with one port (80->8080, NodePort 30080), Endpoints; wait for ingress. Record VIP.
- Patch Service to add a second port (e.g. 443->8443, NodePort 30443). Or use Update to replace spec.ports.
- Wait until mock.NATRulesFor(serviceKey) has two rules (or poll until stable): one for 80, one for 443, with correct target (NodeIP:NodePort).
- Assert Service status still has same VIP.

**Step 2: Run the test**

Run:
```bash
go test ./internal/controller/... -count=1 -v -run TestIntegration_UpdatePorts
```
Expected: PASS.

**Step 3: Commit**

```bash
git add internal/controller/integration_test.go
git commit -m "test: integration update Service ports and assert NAT updated"
```

---

### Task 10: CI runs integration tests

**Files:**
- Modify: `.github/workflows/ci.yml`

**Step 1: Ensure Go version and test command include integration**

CI already runs `go test ./... -v -count=1`. No change needed for “run all the time” — integration tests are in the same package and run with the rest. Only change if CI used a different Go version: align `go-version` with `go.mod` (e.g. 1.25 if that’s what the module uses). Check go.mod: it says 1.25.3; CI uses 1.21 — update CI to match:

```yaml
- name: Set up Go
  uses: actions/setup-go@v5
  with:
    go-version: "1.25"
```

(Use 1.25 or 1.25.x as needed so envtest and deps build.)

**Step 2: Optional — increase timeout for tests**

If integration tests are slow, set timeout for the test step:
```yaml
- name: Run tests
  run: go test ./... -v -count=1 -timeout 5m
```

**Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: align Go version with go.mod; optional test timeout for integration"
```

---

### Task 11: README and design doc link

**Files:**
- Modify: `README.md`

**Step 1: Document integration tests**

In the Development section, add one line:
```markdown
Integration tests use envtest and run with `go test ./...` (see [Integration testing design](docs/plans/2025-02-23-integration-testing-design.md)).
```

**Step 2: Commit**

```bash
git add README.md
git commit -m "docs: mention integration tests in README"
```

---

## Execution handoff

Plan complete and saved to `docs/plans/2025-02-23-integration-testing.md`.

**Two execution options:**

1. **Subagent-driven (this session)** — I run each task (or dispatch a subagent per task), you review between tasks and we iterate quickly.
2. **Parallel session (separate)** — You open a new session and use the executing-plans skill there to run the plan task-by-task with checkpoints.

Which approach do you want?
