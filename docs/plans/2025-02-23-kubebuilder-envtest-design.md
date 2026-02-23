# Kubebuilder layout for envtest — Design

**Date:** 2025-02-23

## 1. Goal

Adopt the full Kubebuilder project layout so envtest is easily installed and run: one command to install (`make envtest`), one command to run all tests including integration (`make test`). The repo remains a controller that only watches built-in Services (no CRDs); we add PROJECT, Makefile, and optional config/ for standard layout.

## 2. Scope

- **In scope:** Add Kubebuilder project layout (PROJECT file, Makefile with envtest/test/build), keep existing binary (`cmd/opnsense-lb-controller`), packages (`internal/`), integration tests, and deployment (Helm, deploy/). Use `kubebuilder init` (user has Kubebuilder v4.12.0 via Homebrew).
- **Out of scope:** New CRDs/APIs, changing reconciliation logic, replacing Helm/deploy with Kustomize-only.

## 3. Approach

Use **kubebuilder init in place**, then reconcile:

1. Run `kubebuilder init --domain opnsense.org --repo github.com/scheuk/opnsense-lb-controller --plugins=go/v4` in the repo. (go/v4 is the default; specifying it keeps the plan explicit and reproducible.)
2. Keep `cmd/opnsense-lb-controller/main.go` as the single entrypoint; remove or ignore any generated root `main.go` and wire Makefile build/run to our cmd.
3. Retain Makefile targets for envtest, test, and build; drop or no-op manifests/generate if not needed.
4. Keep a minimal `config/` if generated (for standard layout); document that production deployment stays Helm/deploy.
5. Prefer CI to run `make envtest` then `make test` so local and CI match.

## 4. Layout and files

**PROJECT**

- At repo root; `domain: opnsense.org`, `repo: github.com/scheuk/opnsense-lb-controller`, layout/version per Kubebuilder (e.g. go.kubebuilder.io/v4). No `resources` (no CRDs).

**Makefile**

- **LOCALBIN:** e.g. `./bin` (already in .gitignore).
- **ENVTEST:** `$(LOCALBIN)/setup-envtest`.
- **ENVTEST_K8S_VERSION:** derived from go.mod `k8s.io/api` (e.g. 1.35) so envtest matches project k8s version.
- **envtest:** ensure setup-envtest is installed and (optionally) download envtest binaries; this is the single “install envtest” command.
- **test:** run `go test ./...` with `KUBEBUILDER_ASSETS=$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path ...)` so integration tests run without manual export.
- **build:** build existing binary from `./cmd/opnsense-lb-controller`.
- Omit or no-op `manifests`/`generate` so `make test` does not depend on them.

**Entrypoint**

- Keep `cmd/opnsense-lb-controller/main.go` as the only entrypoint. If init generates a root main or manager package, remove/ignore it and point Makefile build/run at `./cmd/opnsense-lb-controller`.

**config/**

- If init generates `config/`, keep a minimal set so the repo looks like a standard Kubebuilder project; document that production deployment is via Helm/deploy.

**CI**

- Use `make envtest` then `make test` in CI so the Makefile is the single source of truth for envtest.

**.gitignore**

- `bin/` already present; no change needed.

## 5. Implementation sequence (high level)

1. Ensure clean state (commit or stash).
2. Run `kubebuilder init --domain opnsense.org --repo github.com/scheuk/opnsense-lb-controller --plugins=go/v4` in repo root.
3. Remove or replace generated main/manager entrypoint; point Makefile build and run at `./cmd/opnsense-lb-controller`.
4. Adjust Makefile: ensure test sets KUBEBUILDER_ASSETS via ENVTEST, ensure build outputs to desired path, remove or no-op any manifests/generate dependency from test.
5. Update README: document `make envtest` and `make test` for local development.
6. Update CI workflow to run `make envtest` then `make test` (or keep current envtest setup if equivalent).
7. Run `make envtest` and `make test` to verify; fix any path or version issues.

---

**Next step:** Implementation plan via writing-plans skill (exact steps, file paths, commands, commits).
