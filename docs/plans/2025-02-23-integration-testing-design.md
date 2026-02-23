# Integration Testing — Design

**Date:** 2025-02-23

## 1. Goal

Add integration tests that run against a real Kubernetes API server (envtest) and an in-memory mock OPNsense client, to gain confidence in the controller’s reconciliation, status updates, and cleanup before deployment. Tests run as part of the normal test suite (no build tag); CI runs them on every push/PR.

## 2. Scope (Layer 1)

- **Kubernetes:** envtest (controller-runtime) — API server + etcd in-process. No kind or real cluster.
- **OPNsense:** In-memory mock implementing `opnsense.Client`. No HTTP mock in Layer 1.
- **Scenarios:** (1) Create LoadBalancer Service → VIP + NAT + status. (2) Delete or change `loadBalancerClass` → cleanup (release VIP, remove NAT). (3) Update ports or backends → NAT and status updated.
- **Out of scope for Layer 1:** HTTP mock for OPNsense, kind-based E2E, leader-election testing, failure/retry behavior.

## 3. Test layout

- **Location:** Integration tests live in a dedicated package, e.g. `internal/controller/integration_test.go` or `test/integration` if envtest deps should stay out of `internal`.
- **No build tag:** Tests are normal Go tests; `go test ./...` includes them. CI runs `go test ./...` and thus always runs integration tests.
- **Dependencies:** Add `sigs.k8s.io/controller-runtime` for envtest in `go.mod`. Envtest downloads and starts API server and etcd; no Docker required in CI.

## 4. envtest setup and controller wiring

- **Lifecycle:** Start envtest once per test package (e.g. in a shared helper or `TestMain`), obtain `rest.Config`, create `kubernetes.Interface`. Start the controller in a goroutine (`ctrl.Run(ctx)`). Each test uses a dedicated namespace and creates Services, Endpoints, Nodes there.
- **Wiring:** Build controller with envtest’s `rest.Config`; inject a mock `opnsense.Client` and a real `config.VIPAllocator` (e.g. `config.NewVIPAllocator` with a small `VIPPool` or `SingleVIP`). Use the same `loadBalancerClass` (e.g. `opnsense.org/opnsense-lb`). No leader election in tests.
- **Synchronization:** After creating/updating resources, poll until the Service has `status.loadBalancer.ingress` set and/or the mock state matches expectations (with a timeout, e.g. 5–10s). No need to expose the workqueue or `reconcile`.
- **Isolation:** Each test creates its own namespace; resources are scoped to that namespace to avoid cross-test interference.

## 5. Mock OPNsense client and VIP allocator

- **Mock client:** Implement `opnsense.Client` in test code. In-memory state: set of VIPs and a list of NAT rules (with identity: port, protocol, target, description/serviceKey).
- **EnsureVIP:** Record VIP. **RemoveVIP:** Remove VIP. **ListNATRules:** Return current rules. **ApplyNATRules:** Replace rules for the given `managedBy`/`serviceKey`, add `desired` rules. All return nil.
- **Assertions:** Expose getters (e.g. `VIPs()`, `NATRulesFor(serviceKey)`) so tests can assert expected VIP and NAT state.
- **VIP allocator:** Use real `config.NewVIPAllocator` with test config (e.g. `VIPPool: ["192.0.2.1", "192.0.2.2"]` or `SingleVIP`). No mock.

## 6. Test scenarios

**Scenario 1 — Create (happy path)**  
Create namespace, Node (with InternalIP), LoadBalancer Service with our `loadBalancerClass` and ports, matching Endpoints. Wait until Service has `status.loadBalancer.ingress[0].ip`. Assert VIP and mock: EnsureVIP called, ApplyNATRules called with expected VIP:port → backend.

**Scenario 2 — Cleanup**  
**2a:** Delete the LoadBalancer Service. Wait until mock has no NAT rules for that service and VIP released (if pool). **2b:** Change `loadBalancerClass` (or clear it). Assert NAT removed and VIP released.

**Scenario 3 — Update**  
From a reconciled Service, add/change a port or change Endpoints. Wait for reconciliation. Assert mock NAT rules match new desired state and Service status still has the same VIP.

**Helpers:** Shared helpers for creating namespace, Node, Service, Endpoints, and for “wait for ingress IP” / “wait for mock rules” to keep scenarios short.

## 7. CI and error handling

- **CI:** Existing job runs `go test ./...`; integration tests are included and run every time. Same Go version as rest of project. Envtest may download API server/etcd on first run (cached thereafter). No separate job or tag.
- **Errors:** Tests use a clear timeout when waiting for reconciliation; on timeout, fail with a message stating what was expected. No error injection in the mock for Layer 1.

## 8. Optional future work (Layer 2)

- **HTTP mock OPNsense:** Optional test run using an in-process HTTP server implementing OPNsense endpoints; controller uses real `opnsense.Client`.
- **Real OPNsense:** Optional job or make target when URL and credentials are provided; not required for PRs.
- **Kind:** Optional job with kind cluster and deployed controller for full E2E; for rare validation.

---

**Next step:** Implementation plan via writing-plans skill.
