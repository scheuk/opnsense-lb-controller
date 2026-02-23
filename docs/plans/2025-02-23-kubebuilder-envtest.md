# Kubebuilder envtest layout — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add Kubebuilder project layout so envtest is installed with `make envtest` and all tests (including integration) run with `make test`.

**Architecture:** Run `kubebuilder init` in repo; keep existing `cmd/opnsense-lb-controller` as entrypoint; reconcile Makefile (build, test, envtest) and remove/redirect generated entrypoint; update README and CI.

**Tech Stack:** Kubebuilder v4, Go, controller-runtime envtest, existing integration tests in `internal/controller`.

---

### Task 1: Clean state and record pre-init state

**Files:**
- None (repo state only)

**Step 1: Ensure working tree is clean**

Run: `cd /Users/kevin.scheunemann/work/opnsense-lb-controller && git status`
Expected: clean working tree (or stash/uncommit so we can see init changes clearly).

**Step 2: Record current go.mod module path**

Run: `head -5 go.mod`
Expected: `module github.com/scheuk/opnsense-lb-controller`. Note this; we may need to restore it if init changes it.

---

### Task 2: Run kubebuilder init

**Files:**
- Created by init: `PROJECT`, `Makefile`, possibly `main.go`, `config/`, `Dockerfile` (may overwrite), `go.mod`/`go.sum` (may change)

**Step 1: Run init**

Run:
```bash
cd /Users/kevin.scheunemann/work/opnsense-lb-controller
kubebuilder init --domain opnsense.org --repo github.com/scheuk/opnsense-lb-controller --plugins=go/v4
```
Expected: Success; new PROJECT and Makefile; possibly new or changed main (e.g. `cmd/main.go` in v4), config/, and Dockerfile. (go/v4 is the default; specifying it keeps the command reproducible.)

**Step 2: Inspect what was added or changed**

Run: `git status` and `git diff go.mod` (if changed)
Note: Which of these exist: PROJECT, Makefile, cmd/main.go (or root main.go), config/, Dockerfile. We will keep PROJECT and Makefile; we will remove or replace generated main and reconcile Dockerfile/build.

---

### Task 3: Remove generated entrypoint and keep our cmd

**Files:**
- Delete: generated manager entrypoint (e.g. `cmd/main.go` if present, or root `main.go`; do not delete `cmd/opnsense-lb-controller/`)
- Modify: `Makefile` — set build target to use `./cmd/opnsense-lb-controller`

**Step 1: Remove generated main**

If init created `cmd/main.go` (and we have `cmd/opnsense-lb-controller/`), delete the generated one:
```bash
rm -f cmd/main.go
```
If the only content of `cmd/` was the generated main, remove that file only; keep `cmd/opnsense-lb-controller/` intact. If init created root `main.go`, remove it: `rm -f main.go`.

**Step 2: Point Makefile build at our binary**

Open `Makefile`. Find the target that builds the binary (e.g. `build` or `build:`). Change the build command so the output is our controller and the package is `./cmd/opnsense-lb-controller`. Example:
```makefile
build: ## Build the controller binary.
	go build -o $(LOCALBIN)/opnsense-lb-controller ./cmd/opnsense-lb-controller
```
Ensure `run` (if present) runs the same binary, e.g. `$(LOCALBIN)/opnsense-lb-controller` or `go run ./cmd/opnsense-lb-controller`.

**Step 3: Restore go.mod module path if needed**

If `go.mod` was changed to a different module path, restore the first line to:
```
module github.com/scheuk/opnsense-lb-controller
```
Run `go mod tidy`.

---

### Task 4: Wire Makefile test and envtest

**Files:**
- Modify: `Makefile`

**Step 1: Ensure envtest variables and targets exist**

In `Makefile`, confirm or add:
- `LOCALBIN` (e.g. `./bin`)
- `ENVTEST ?= $(LOCALBIN)/setup-envtest`
- `ENVTEST_K8S_VERSION` derived from go.mod (e.g. `$(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')`)
- Target `envtest: $(ENVTEST)` that installs setup-envtest into LOCALBIN (see Kubebuilder book: go-install-tool for setup-envtest)
- Target that downloads envtest binaries (e.g. `setup-envtest` or `envtest` target that runs `$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path`)

**Step 2: Ensure test target sets KUBEBUILDER_ASSETS**

Find the `test` target. It must run `go test ./...` with `KUBEBUILDER_ASSETS` set so integration tests see envtest binaries. Example:
```makefile
test: ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./... -coverprofile cover.out
```
Remove any dependency of `test` on `manifests` or `generate` (this project has no CRDs to generate).

**Step 3: Verify envtest target is runnable**

Run: `make envtest`
Expected: setup-envtest binary in LOCALBIN; optionally envtest k8s binaries downloaded. Fix any missing variables or targets using the Kubebuilder envtest reference.

---

### Task 5: Reconcile Dockerfile and config/

**Files:**
- Modify or restore: `Dockerfile`
- Keep or trim: `config/`

**Step 1: Restore Dockerfile if overwritten**

If `kubebuilder init` overwrote `Dockerfile`, restore the previous Dockerfile that builds `./cmd/opnsense-lb-controller` (copy from git: `git show HEAD:Dockerfile` or reapply the multi-stage build that uses the same entrypoint).

**Step 2: Keep config/ minimal**

If `config/` was created, keep it for standard layout. No need to add CRDs; we can leave default/kustomization as-is or trim unused parts. Do not remove config/ so the repo stays Kubebuilder-style.

---

### Task 6: Update README

**Files:**
- Modify: `README.md`

**Step 1: Document make envtest and make test**

In the Development section, replace or add:
- To install envtest binaries locally: `make envtest`
- To run all tests (including integration): `make test`
- Optionally: if readers run `go test ./...` directly, they must set `KUBEBUILDER_ASSETS` (e.g. `make envtest` first, then `export KUBEBUILDER_ASSETS=$(setup-envtest use -p path)`).

---

### Task 7: Update CI to use Makefile

**Files:**
- Modify: `.github/workflows/ci.yml`

**Step 1: Use make envtest and make test**

Replace the steps that install setup-envtest and run tests with:
- Run `make envtest` (ensures PATH includes LOCALBIN so setup-envtest is found, and downloads envtest binaries if needed)
- Run `make test`

Ensure PATH includes `$(go env GOPATH)/bin` and/or LOCALBIN so that `make envtest` and the Makefile’s use of `$(ENVTEST)` work. Example:
```yaml
- name: Run tests
  run: |
    export PATH="$(go env GOPATH)/bin:$(pwd)/bin:$PATH"
    make envtest
    make test
```
Adjust if the Makefile’s `test` target already sets KUBEBUILDER_ASSETS via ENVTEST (no separate export needed in that case).

---

### Task 8: Verify and commit

**Files:**
- None (verification only)

**Step 1: Run full test flow**

Run:
```bash
cd /Users/kevin.scheunemann/work/opnsense-lb-controller
make envtest
make test
```
Expected: envtest installs; `go test ./...` runs and integration tests run (no skip). Fix any path or version errors (e.g. ENVTEST_K8S_VERSION vs installed binaries).

**Step 2: Build**

Run: `make build`
Expected: binary at `./bin/opnsense-lb-controller` (or whatever LOCALBIN is).

**Step 3: Commit**

```bash
git add PROJECT Makefile config/ README.md .github/workflows/ci.yml Dockerfile go.mod go.sum
git add -u
git status   # ensure no unintended files
git commit -m "chore: add Kubebuilder layout for envtest (make envtest, make test)"
```

---

**Execution:** Use @superpowers:executing-plans to run this plan task-by-task. After the plan is done, offer: (1) Subagent-driven execution in this session, or (2) Parallel session with executing-plans in a worktree.
