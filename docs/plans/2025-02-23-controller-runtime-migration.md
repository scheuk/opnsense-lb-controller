# Controller-Runtime Migration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace custom client-go controller (informers, workqueue, manual leader election) with controller-runtime Manager and Reconciler; add finalizer for delete cleanup and status helper to clear `.status.loadBalancer.ingress` on failure.

**Architecture:** Single Reconciler for `core/v1` Service, registered with Manager. Predicate filters by LoadBalancerClass. Watches on Endpoints and Nodes enqueue the relevant Service(s). Finalizer ensures cleanup on delete. Status helper patches ingress to VIP or `[]`. Domain logic (ComputeDesiredState, OPNsense) unchanged.

**Tech Stack:** controller-runtime (Manager, Reconciler, Builder, predicate), client-go (only for types/scheme), existing internal/config and internal/opnsense.

**Design doc:** `docs/plans/2025-02-23-controller-runtime-migration-design.md`

---

### Task 1: Status helper

**Files:**
- Create: `internal/controller/status.go`
- Create: `internal/controller/status_test.go`
- Modify: (none)

**Step 1: Write the failing test**

In `internal/controller/status_test.go` add a test that calls `UpdateServiceLoadBalancerIngress` with a Service and a VIP and verifies the patch payload (or use a fake client / subresource client). Alternatively test the helper with envtest by getting a Service, calling the helper, then getting the Service and checking `.status.loadBalancer.ingress`. Prefer unit test with a controlled patch: e.g. function that returns the patch bytes or takes a writer, so we can assert content without a real API server.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/controller/ -run TestUpdateServiceLoadBalancerIngress -v`
Expected: FAIL (function/types not defined)

**Step 3: Implement status helper**

In `internal/controller/status.go`:
- Add `UpdateServiceLoadBalancerIngress(ctx context.Context, c client.Client, svc *corev1.Service, vip string) error`.
- If `vip != ""`, patch `status.loadBalancer.ingress = [{ ip: vip }]`; else patch to `[]`.
- Use `client.Status().Patch(ctx, svc, ...)` with MergeFrom(svc) or a strategic merge patch for status only. Ensure only `.status.loadBalancer` is changed.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/controller/ -run TestUpdateServiceLoadBalancerIngress -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/controller/status.go internal/controller/status_test.go
git commit -m "refactor: add status helper for Service loadBalancer ingress"
```

---

### Task 2: Predicate for LoadBalancerClass

**Files:**
- Create: `internal/controller/predicate.go`
- Create: `internal/controller/predicate_test.go`
- Modify: (none)

**Step 1: Write the failing test**

In `internal/controller/predicate_test.go` test `ServiceLoadBalancerClass(loadBalancerClass string) predicate.Predicate`: Service with type LoadBalancer and matching LoadBalancerClass → true; type ClusterIP → false; LoadBalancer with different class → false; nil → false.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/controller/ -run TestServiceLoadBalancerClass -v`
Expected: FAIL

**Step 3: Implement predicate**

In `internal/controller/predicate.go` implement a predicate that filters `corev1.Service`: `Spec.Type == LoadBalancer` and `Spec.LoadBalancerClass != nil && *Spec.LoadBalancerClass == loadBalancerClass`. Use `sigs.k8s.io/controller-runtime/pkg/predicate` and a custom `Predicate` or `Funcs` (Create, Update, Delete) that return true only when the object is a Service matching the filter.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/controller/ -run TestServiceLoadBalancerClass -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/controller/predicate.go internal/controller/predicate_test.go
git commit -m "refactor: add predicate for LoadBalancerClass"
```

---

### Task 3: Move desiredStateToOPNsenseRules to desired_state.go

**Files:**
- Modify: `internal/controller/desired_state.go` (add function and opnsense import)
- Modify: `internal/controller/controller.go` (remove function, or keep and call from reconciler later)

**Step 1:** Move `desiredStateToOPNsenseRules` from `internal/controller/controller.go` to `internal/controller/desired_state.go`. Add `import "github.com/scheuk/opnsense-lb-controller/internal/opnsense"` and ensure `DesiredState` and `opnsense.NATRule` are used. Export the function name to `DesiredStateToOPNsenseRules` if it will be used from another package (reconciler in same package, so lowercase is fine).

**Step 2:** Remove `desiredStateToOPNsenseRules` from `controller.go` and update any references to use the function from desired_state (same package, no change). Run `go build ./...` and `go test ./internal/controller/ -run DesiredState -v` to ensure existing tests still pass.

**Step 3: Commit**

```bash
git add internal/controller/desired_state.go internal/controller/controller.go
git commit -m "refactor: move DesiredStateToOPNsenseRules to desired_state.go"
```

---

### Task 4: Reconciler struct and Reconcile (no finalizer yet)

**Files:**
- Create: `internal/controller/reconciler.go`
- Modify: (none)

**Step 1:** In `internal/controller/reconciler.go` define `Reconciler` struct with: `Client client.Client`, `EventRecorder record.EventRecorder`, `OPNsense opnsense.Client`, `VIPAlloc config.VIPAllocator`, `LoadBalancerClass string`, `ManagedBy string`, `FinalizerName string`. Add constructor `NewReconciler(...) *Reconciler` if desired.

**Step 2:** Implement `Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error)`:
- Get Service by req.NamespacedName. If NotFound, call a private `cleanup(ctx, key)` (same key format `namespace/name`), return ctrl.Result{}.
- If Service exists but is not our Service (type != LoadBalancer or LoadBalancerClass != r.LoadBalancerClass), call cleanup and return.
- Allocate VIP with r.VIPAlloc.Allocate(key). If empty, emit Warning event "NoVIP", call status helper to set ingress `[]`, return ctrl.Result{}.
- Get Endpoints and build getNodeIP from Nodes (list or get by name). Call `ComputeDesiredState`, then `opnsense.EnsureVIP`, then `DesiredStateToOPNsenseRules` and `opnsense.ApplyNATRules`. On error: emit Warning, status helper set `[]`, return ctrl.Result{Requeue: true}.
- On success: call status helper with VIP, emit Normal "Synced", return ctrl.Result{}.

**Step 3:** Implement private `cleanup(ctx, key string)`: get VIP from allocator with GetVIP(key); call opnsense.ApplyNATRules(ctx, nil, ...) and if VIP != "" opnsense.RemoveVIP; Release(key). Do not add/remove finalizer in this task.

**Step 4:** Ensure `reconciler.go` compiles: `go build ./internal/controller/`. Run existing controller tests (integration may still use old controller): `go test ./internal/controller/ -run TestIntegration -count=0` can be skipped for now or run after wiring in main.

**Step 5: Commit**

```bash
git add internal/controller/reconciler.go
git commit -m "feat: add Service Reconciler (no finalizer yet)"
```

---

### Task 5: Add finalizer handling in Reconciler

**Files:**
- Modify: `internal/controller/reconciler.go`

**Step 1:** At start of Reconcile, if `svc.DeletionTimestamp != nil`: run cleanup(key), then remove finalizer from Service (patch to remove `r.FinalizerName` from `svc.Finalizers`), return ctrl.Result{}.

**Step 2:** After confirming Service is ours and before allocating VIP: ensure finalizer is present. If not, patch Service to add `r.FinalizerName` to `svc.Finalizers`, then return ctrl.Result{Requeue: true} so we re-run (optional: same reconcile can continue without requeue if you prefer).

**Step 3:** In cleanup, do not remove finalizer (caller removes it when handling delete). Ensure cleanup is idempotent.

**Step 4:** Run `go build ./...` and unit tests. No integration test change yet.

**Step 5: Commit**

```bash
git add internal/controller/reconciler.go
git commit -m "feat: add finalizer for delete cleanup in Reconciler"
```

---

### Task 6: Wire Manager and controller in main

**Files:**
- Modify: `cmd/opnsense-lb-controller/main.go`
- Modify: (none for this task)

**Step 1:** Add imports: `sigs.k8s.io/controller-runtime/pkg/controller`, `sigs.k8s.io/controller-runtime/pkg/manager`, `ctrl "sigs.k8s.io/controller-runtime"`, `"sigs.k8s.io/controller-runtime/pkg/client"`, corev1, and controller package.

**Step 2:** After building OPNsense client and VIPAllocator, create Manager: `mgr, err := ctrl.NewManager(restCfg, ctrl.Options{LeaderElection: true, LeaderElectionNamespace: cfg.LeaseNamespace, LeaderElectionID: cfg.LeaseName, ...})`. Use scheme that includes core v1 (e.g. `scheme.Scheme` from client-go or runtime.Scheme with core added).

**Step 3:** Build Reconciler: `rec := controller.NewReconciler(mgr.GetClient(), mgr.GetEventRecorderFor("opnsense-lb-controller"), oc, vipAlloc, cfg.LoadBalancerClass, "opnsense-lb-controller", "opnsense.org/opnsense-lb")`.

**Step 4:** Build controller: `ctrl.NewControllerManagedBy(mgr).For(&corev1.Service{}).WithEventFilter(controller.ServiceLoadBalancerClassPredicate(cfg.LoadBalancerClass)).Complete(rec)`. Then add Watches: for Endpoints, use a handler that enqueues the corresponding Service (req.Namespace, req.Name from Endpoints object). For Nodes, use a handler that lists all Services with our LoadBalancerClass and enqueues each (handler.EnqueueRequestsFromMapFunc). Ensure predicate is exported or same package.

**Step 5:** Replace `leaderelection.RunOrDie` and `controller.NewController`/`ctrl.Run` with `mgr.Start(ctx)`. Keep signal handling and context cancel. Remove client-go leaderelection and resourcelock imports.

**Step 6:** Run `go build ./cmd/opnsense-lb-controller` and fix any compile errors (scheme, builder API for Watches).

**Step 7: Commit**

```bash
git add cmd/opnsense-lb-controller/main.go
git commit -m "feat: run controller under Manager with leader election"
```

---

### Task 7: Integration tests — start Manager instead of custom controller

**Files:**
- Modify: `internal/controller/integration_test.go`

**Step 1:** Change `startController` to accept the same deps but create a Manager from `envtestCfg`, register the Reconciler (same as main: For Service, predicate, Watches for Endpoints/Nodes), then call `mgr.Start(ctx)` in a goroutine. Use the same VIPAllocator and FakeOPNsense. Ensure scheme includes core v1 (envtest typically provides this).

**Step 2:** In TestMain, where `startController(ctx, cfg, mock, vipAlloc)` is called, pass the rest.Config from envtest so the Manager is created with that config. No need to change test cases themselves if the behavior is the same.

**Step 3:** Run integration tests: `go test ./internal/controller/ -run TestIntegration -v` (with envtest assets). Fix any failures (e.g. finalizer might change delete behavior; ensure tests wait for cleanup and possibly for finalizer removal).

**Step 4: Commit**

```bash
git add internal/controller/integration_test.go
git commit -m "test: run integration tests with Manager and Reconciler"
```

---

### Task 8: Remove old controller code

**Files:**
- Modify: `internal/controller/controller.go` (delete file or reduce to minimal)
- Modify: `cmd/opnsense-lb-controller/main.go` (remove any remaining references to old controller)
- Modify: `internal/controller/integration_test.go` (ensure no reference to NewController)

**Step 1:** Delete `internal/controller/controller.go` entirely. All logic is now in reconciler.go (Reconcile, cleanup), desired_state.go (ComputeDesiredState, DesiredStateToOPNsenseRules), status.go, predicate.go.

**Step 2:** Search for `NewController`, `Controller`, `controller.Run` in the repo and remove or update. main.go should already use Manager only; integration_test.go should use Manager. Fix any remaining imports (e.g. remove `controller` package references to old type if needed).

**Step 3:** Run `go build ./...` and `make test` (and integration tests). Fix any broken references.

**Step 4: Commit**

```bash
git add internal/controller/controller.go cmd/opnsense-lb-controller/main.go internal/controller/integration_test.go
git commit -m "chore: remove legacy controller in favor of Reconciler"
```

---

### Task 9: Lint, test, and docs

**Files:**
- Modify: `Makefile` (if run target or deps need updating)
- Modify: (optional) `README` or `AGENTS.md` if they mention the old controller

**Step 1:** Run `make lint-fix` and `make test`. Fix any lint or test failures.

**Step 2:** If README or AGENTS.md describe the controller as "informers/workqueue", update to "controller-runtime Manager and Reconciler".

**Step 3: Commit**

```bash
git add -A
git commit -m "chore: lint and update docs for controller-runtime"
```

---

## Execution

Plan complete and saved to `docs/plans/2025-02-23-controller-runtime-migration.md`.

**Two execution options:**

1. **Subagent-Driven (this session)** — Dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Parallel Session (separate)** — Open a new session with executing-plans and run through the plan with checkpoints.

Which approach do you want?
