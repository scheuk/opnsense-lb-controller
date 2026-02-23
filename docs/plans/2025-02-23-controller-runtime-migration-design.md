# Controller-Runtime Migration — Design

**Date:** 2025-02-23

## Goal

Replace custom controller machinery (client-go informers, workqueue, manual leader election) with controller-runtime Manager and Reconciler. Adopt SDK-style patterns: Manager’s event recorder, finalizer for reliable delete cleanup, and a status helper that clears `.status.loadBalancer.ingress` when reconciliation fails (“not ready”). No CRD.

## 1. Architecture

- **Single process:** `main` builds a controller-runtime **Manager** (with leader election), registers one **Reconciler** for `core/v1` Service, and runs `mgr.Start(ctx)`. No custom informers, workqueue, or client-go leader election.
- **Reconciler:** Receives `ctrl.Request{Namespace, Name}` (Service key). Uses Manager’s **cached client** to fetch Service, Endpoints, and Nodes as needed. Keeps existing domain logic: VIP allocation, `ComputeDesiredState`, OPNsense `EnsureVIP` / `ApplyNATRules` / `RemoveVIP`, and a **status helper** for “not ready” handling.
- **Watches:** Primary resource `Service` with a **predicate** so only Services with the configured `LoadBalancerClass` are reconciled. **Watches** on Endpoints and Nodes enqueue the corresponding Service(s) (by name for Endpoints; for Nodes, list Services with that class and enqueue them).
- **Finalizer:** A constant finalizer (e.g. `opnsense.org/opnsense-lb`) is added to Services we take ownership of. On delete, we run cleanup (NAT rules, VIP release, allocator release), then remove the finalizer so the Service can be deleted.

## 2. Components

| Component | Responsibility |
|-----------|----------------|
| **cmd/.../main.go** | Load config, create Manager (leader election on), build OPNsense client and VIPAllocator, create Reconciler, register with `ctrl.NewControllerManagedBy(mgr).For(...).Watches(...).Complete(reconciler)`, start Manager. |
| **Reconciler** (e.g. `internal/controller/reconciler.go`) | Holds Manager `Client`, `EventRecorder`, OPNsense client, VIPAllocator, loadBalancerClass, managedBy, finalizer name. Implements `Reconcile(ctx, ctrl.Request) (ctrl.Result, error)`. |
| **Predicate** | Filter: Service type LoadBalancer and `LoadBalancerClass == config.LoadBalancerClass`. |
| **Status helper** | `updateServiceStatus(ctx, client, svc, vip string)`: patch `.status.loadBalancer.ingress` to `[{ ip: vip }]` when `vip != ""`, or to `[]` when not ready. Single place for status writes. |
| **Domain logic** | `ComputeDesiredState`, `desiredStateToOPNsenseRules`, and OPNsense/VIP logic remain; called from `Reconcile`. |

## 3. Data Flow

- **Enqueue:** Service create/update (predicate) or Endpoints/Node change (watches) → request `{Namespace, Name}` (Service) into the controller’s queue.
- **Reconcile:** Get Service (if NotFound, run cleanup for that key and return). If not our Service, run cleanup and return. If deletion timestamp set and finalizer present → cleanup then remove finalizer and return. Otherwise ensure finalizer, allocate VIP, fetch Endpoints/Nodes, compute desired state, call OPNsense, then **status helper**: on success set ingress to VIP, on failure set ingress to `[]` and requeue with backoff.
- **Cleanup path:** Either “Service not found” or “Service has deletion timestamp and our finalizer”. Release NAT rules, remove VIP if allocated, release allocator key, remove finalizer (if delete), then return.

## 4. Error Handling and “Not Ready” Status

- **Transient errors** (e.g. OPNsense API failure, Get failure): Log, emit Warning event, call status helper to set `.status.loadBalancer.ingress = []`, return `ctrl.Result{Requeue: true}` or `RequeueAfter` so we retry.
- **Permanent / no VIP:** Emit event, set ingress to `[]`, return `ctrl.Result{}` (no requeue) or a long `RequeueAfter` so we don’t spin.
- **Status helper:** Always patch only `.status.loadBalancer` (e.g. `ingress: [{ ip: vip }]` or `ingress: []`). No custom conditions (Service is core type). Events remain the main observable for “why” (e.g. `NoVIP`, `EnsureVIPFailed`).

## 5. Testing

- **Unit tests:** `ComputeDesiredState` and `desiredStateToOPNsenseRules` unchanged; optional unit tests for the status helper (patch payload) and for the predicate (our Service vs not).
- **Integration tests:** Replace “start custom controller with `NewController` + `Run`” with “create Manager, register Reconciler with fake OPNsense and VIPAllocator, start Manager in a goroutine, run existing scenarios (create Service, check status/events/NAT), then cancel context”. Continue using envtest; Manager’s client and envtest work together.
- **E2E:** No structural change; same flows, controller is just running under Manager.
