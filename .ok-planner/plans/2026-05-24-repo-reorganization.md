# Repo reorganization implementation plan

**Spec:** `.ok-planner/specs/2026-05-24-repo-reorganization-design.md`
**Goal:** Carve rimsky's monorepo into five repos (rimsky-core, rimsky-services, rimsky-docs, crimefinder, rimsky-dashboard), introduce a new `pkg:sdk/go` peer Go module inside rimsky for implementer-facing wire scaffolding, and update the design docs to match.
**Architecture:** New peer Go module `sdk/go` at the rimsky repo root, alongside `protocols/`, `foundation/`. SDK is implementer-facing only (server scaffolding, publisher helpers, conformance library, testpg, ops glue); calling-side wire code stays rimsky-internal as `pkg:runtime/peer` (renamed from `pkg:runtime/remote`). Bundled production-side reference impls (`stores/{filesystem,postgres}/{cmd,server,store,lifecycle,...}`, `sensors/*`, `subscribers/openlineage`, `executors/{claude-agent,http-node,verifier-http,verifier-shape-checks}`) move to `../rimsky-services`. Test-infrastructure carve-outs (`pkg:executors/stub`, `pkg:stores/stub`, `pkg:stores/{filesystem,postgres}/testfixture`) stay in rimsky. Docs, apps/crimefinder, dashboards/rimsky-dashboard move to their respective sibling repos. Seven concept docs mutate; one new concept doc created (`concept:sdk`).
**Tech Stack:** Go modules + `go.work` workspace; `.golangci.yml` depguard for module boundaries; stdlib `log/slog`; `go-chi/chi`; `pgx/v5`; `testcontainers-go`; gRPC + protobuf via `pkg:protocols/proto/v1/gen`. TS workspaces under `executors/claude-agent` (npm) and `dashboards/rimsky-dashboard` (Vite + Tailwind); both move to sibling repos retaining their toolchains.

---

## Sibling repo paths (referenced throughout)

The user has pre-created four sibling repos at sibling paths to rimsky:
- `../rimsky-services` — destination for production-side bundled impls.
- `../rimsky-docs` — destination for `docs/` + docs-lint binaries + examples.
- `../crimefinder` — destination for `apps/crimefinder/`.
- `../rimsky-dashboard` — destination for `dashboards/rimsky-dashboard/`.

All paths in the plan that begin `../rimsky-services/`, `../rimsky-docs/`, `../crimefinder/`, `../rimsky-dashboard/` refer to these sibling repos. Cross-repo moves use plain `mv` (not `git mv`) — `git mv` cannot preserve file history across separate repos, and the user owns commits.

---

## Pass 1: P1 — In-repo audit prep (rimsky-only)

**Goal:** Land the five P1 fixes from the spec so the repo is ready for the SDK module to be born without inheriting any rimsky-internal leaks.
**Scope:** Tasks 1–5.
**End state:** working
**Verification:** `make lint && make test-all`

### Task 1: Swap `foundation/locks` → `protocols/claimproducer` imports in 11 `stores/` files

**Files:**
- `stores/filesystem/store/store.go`
- `stores/filesystem/store/pick_policy.go`
- `stores/postgres/cmd/main.go`
- `stores/postgres/server/server.go`
- `stores/postgres/server/executor_test.go`
- `stores/postgres/store/store.go`
- `stores/postgres/store/action_vocab_test.go`
- `stores/postgres/testfixture/testfixture.go`
- `stores/stub/cmd/main.go`
- `stores/stub/store/store.go`
- `stores/stub/store/store_test.go`

**Steps:**
1. In each file, change the import block: `corestore "github.com/fallguyconsulting/rimsky/foundation/locks"` → `claimproducer "github.com/fallguyconsulting/rimsky/protocols/claimproducer"`.
2. In each file, replace identifier references `corestore.X` → `claimproducer.X` for the six used symbols: `Capabilities`, `ClaimResult`, `OpenOutcome`, `WriteSemantics`, `WriteSemanticsSync`, `WriteSemanticsStagedAsync`. The four types are aliases at `foundation/locks/types.go:102,109,116,161`; the two constants at lines 129, 134 are re-declared because Go disallows const aliasing. All are semantic-preserving.
3. Verify no other identifiers from `corestore` are used. (`grep -n 'corestore\.' stores/` against each file should return no remaining matches after Step 2.)
4. Verify the package itself still owns its rimsky-internal types: `grep -n '^type\|^func\|^var\|^const' foundation/locks/*.go` should show `NamedLockSpec` and any other rimsky-internal types remain. Do not modify `foundation/locks/*.go`.

**Verification:** `cd /Users/patrick/Documents/projects/research/zonebase/submodules/rimsky && go build ./stores/... && go test ./stores/... && grep -rn 'foundation/locks' stores/ | grep -v _test.go | wc -l` returns 0.

### Task 2: Rewrite `subscribers/openlineage/subscriber_test.go` as docker-compose integration test

**Files:**
- `subscribers/openlineage/subscriber_test.go` (rewrite)
- `subscribers/openlineage/docker-compose.test.yml` (new — implementer's choice on mechanism)

**Context:** Current `subscriber_test.go` imports `foundation/persistence`, `foundation/shared`, `internal/pgtest`, and uses `seedInstanceWithMainScope` to INSERT into `rimsky_instances` and `rimsky_run_scopes` directly with raw SQL. This is white-box coupling rimsky-services-bound code cannot keep. Rewrite drives rimsky from outside via its public API.

**Steps:**
1. Read the current `subscribers/openlineage/subscriber_test.go` end-to-end. Note every assertion the test makes about OpenLineage event payloads — these are the correctness checks the new shape must preserve.
2. Add `subscribers/openlineage/docker-compose.test.yml` (or equivalent testcontainers-based bring-up) that stands up rimsky from a locally-built image (`./deploy/build-images.sh` builds the rimsky-control-api / rimsky-supervisor / rimsky-scheduler images) plus its Postgres dependency. The test must be self-contained: a single `go test ./subscribers/openlineage/...` invocation brings the stack up, runs assertions, tears the stack down.
3. Rewrite `subscriber_test.go` to:
   - Bring up the stack from Step 2.
   - Register a template via `POST /templates` (operator API) that emits at least one event the subscriber should pick up.
   - Create an instance via `POST /instances`.
   - Drive the instance to emit events (whatever interaction triggers the same OpenLineage signal the original test fixtures emitted).
   - Subscriber-under-test registers as a peer service (the existing subscriber binary). Receives callbacks from rimsky over its public callback API.
   - Assert on the OpenLineage event payloads the subscriber forwards downstream — same assertions as the original test, just driven from the public API.
4. Remove imports of `foundation/persistence`, `foundation/shared`, `internal/pgtest`. Remove `seedInstanceWithMainScope` and any other raw-SQL helpers.

**Verification:** `go test ./subscribers/openlineage/...` passes; `grep -E 'foundation/(persistence|shared)|internal/pgtest' subscribers/openlineage/subscriber_test.go` returns no matches.

### Task 3: Swap `internal/pgtest` → `StartFreshPostgresDSN` (no migrations) in three sensor tests

**Files:**
- `sensors/sensor-http/state_db_test.go`
- `sensors/sensor-webhook/state_db_test.go`
- `sensors/sensor-object-store/state_db_test.go`

**Context:** These tests test the sensor's *own* state-persistence (tables the sensor owns). They were using `pgtest.StartPostgres` which applies rimsky migrations as a side effect; they don't need rimsky's schema. `internal/pgtest/pgtest.go:81` declares `StartFreshPostgresDSN` (doc-comment opens at line 61) which spins up Postgres without migrations.

**Steps:**
1. In each file, replace the `pgtest.StartPostgres(ctx, t)` call with `pgtest.StartFreshPostgresDSN(ctx, t)`. Note the signature difference: `StartFreshPostgresDSN` returns a DSN string (no pool); the sensor's own `openStateDB` accepts the DSN via the `RIMSKY_SENSOR_*_STATE_DSN` env var that the test already sets. If the signature requires further adjustment, read `internal/pgtest/pgtest.go` to find the matching API.
2. Confirm by running each test individually.

**Verification:** `go test ./sensors/sensor-http/... ./sensors/sensor-webhook/... ./sensors/sensor-object-store/...` passes.

### Task 4: Delete empty `cmd/rimsky-verifier-*` directories

**Files:**
- `cmd/rimsky-verifier-http/` (empty directory)
- `cmd/rimsky-verifier-shape-checks/` (empty directory)

**Steps:**
1. Verify both directories are empty: `ls cmd/rimsky-verifier-http/ cmd/rimsky-verifier-shape-checks/` shows no files (verified during plan-write).
2. Delete: `rmdir cmd/rimsky-verifier-http cmd/rimsky-verifier-shape-checks`.

**Verification:** `test ! -d cmd/rimsky-verifier-http && test ! -d cmd/rimsky-verifier-shape-checks`.

### Task 5: Add `consumption-side-isolation` depguard rule + verify clean

**Files:**
- `.golangci.yml`

**Steps:**
1. Open `.golangci.yml`. Locate the `depguard.rules` block (sibling to the existing `pgx-isolation`, `foundation-internal-isolation`, etc.).
2. Add a new rule `consumption-side-isolation` matching this exact structure:

```yaml
      consumption-side-isolation:
        list-mode: lax
        files:
          - "**/stores/**"
          - "**/sensors/**"
          - "**/subscribers/**"
          - "**/executors/**"
        deny:
          - pkg: "github.com/fallguyconsulting/rimsky/foundation"
            desc: "Consumption-side binaries implement against protocols/ only. foundation/ is rimsky-internal."
          - pkg: "github.com/fallguyconsulting/rimsky/internal"
            desc: "internal/ is private to rimsky."
          - pkg: "github.com/fallguyconsulting/rimsky/graph"
          - pkg: "github.com/fallguyconsulting/rimsky/runtime"
          - pkg: "github.com/fallguyconsulting/rimsky/control"
          - pkg: "github.com/fallguyconsulting/rimsky/cmd"
```

3. Run `make lint`. Confirm no new violations. If the rule fires, the failure indicates a leak Tasks 1–3 didn't fully resolve; fix the leak and re-run.
4. Append a `CHANGELOG.md` entry under `## Unreleased` summarizing P1 (audit prep): the cosmetic store swaps, the openlineage rewrite, the sensor pgtest swaps, the deleted empty dirs, and the new depguard rule.

**Verification:** `make lint` clean; `grep -n 'consumption-side-isolation' .golangci.yml` returns a hit.

---

## Pass 2: P2a — SDK module scaffolding and content extraction

**Goal:** Create the `pkg:sdk/go` peer Go module and migrate the implementer-facing surfaces (server scaffolding, publisher helpers, testpg, ops glue) into it. Conformance reorganization and runtime/peer rename land in Pass 3.
**Scope:** Tasks 6–12.
**End state:** working
**Verification:** `make lint && make build-all && make test-all && (cd sdk/go && go build ./... && go test ./...)`

### Task 6: Scaffold the `sdk/go` Go module

**Files:**
- `sdk/go/go.mod` (new)
- `sdk/go/doc.go` (new)
- `sdk/go/README.md` (new)
- `go.work` (modify)

**Steps:**
1. Create directory `sdk/go/`.
2. Create `sdk/go/go.mod`:
```
module github.com/fallguyconsulting/rimsky/sdk/go

go 1.25.0

require (
	github.com/fallguyconsulting/rimsky/protocols v0.0.0
)

replace github.com/fallguyconsulting/rimsky/protocols => ../../protocols
```
3. Create `sdk/go/doc.go` with package-level doc:
```go
// Package sdk is the canonical Go-side implementer-facing surface for building
// services that rimsky talks to.
//
// Houses server scaffolding (claim-producer, executor, lifecycle-subscriber,
// blob-backend, publisher), publisher-side message-emit helpers, a conformance
// library invocable from service authors' own Go tests, a testcontainer helper,
// and operational glue (slog setup, healthcheck endpoint, DSN env-var parser).
//
// Does NOT contain calling-side wire code: rimsky-internal infrastructure
// (supervisor, terminal-resolution, discovery-cache) stays in rimsky's
// runtime/peer/. See concept:sdk in .ok-planner/design/concepts/sdk.md.
package sdk
```
4. Create `sdk/go/README.md` with a one-paragraph description matching `doc.go` and a link to `concept:sdk`.
5. Edit `go.work` (currently `go 1.25.0` + `use ( . ./foundation ./protocols )`) to add `./sdk/go`:
```
go 1.25.0

use (
	.
	./foundation
	./protocols
	./sdk/go
)
```
6. From the rimsky repo root, run `go work sync` to ensure the workspace resolves cleanly.

**Verification:** `go build ./sdk/go/...` succeeds (no source files yet beyond `doc.go`, so this just verifies the module resolves). `go work sync` exits 0.

### Task 7: Add `sdk-purity` depguard + update `foundation-purity` and `graph-purity`

**Files:**
- `.golangci.yml`

**Steps:**
1. In `.golangci.yml` under `depguard.rules`, add:
```yaml
      sdk-purity:
        list-mode: lax
        files:
          - "**/sdk/go/**"
        deny:
          - pkg: "github.com/fallguyconsulting/rimsky/foundation"
            desc: "sdk/go imports only protocols/ + stdlib + minimal third-party. foundation/ is rimsky-internal."
          - pkg: "github.com/fallguyconsulting/rimsky/internal"
            desc: "internal/ is private to rimsky."
          - pkg: "github.com/fallguyconsulting/rimsky/graph"
          - pkg: "github.com/fallguyconsulting/rimsky/runtime"
          - pkg: "github.com/fallguyconsulting/rimsky/control"
          - pkg: "github.com/fallguyconsulting/rimsky/cmd"
```
2. Locate the `foundation-purity` rule in `.golangci.yml` (it currently denies graph/, runtime/, control/, cmd/, stores/, executors/, dashboards/). Add `pkg: "github.com/fallguyconsulting/rimsky/sdk/go"` with desc `"foundation/ may not import sdk/go (sdk is implementer-facing surface)"`.
3. Locate the `graph-purity` rule. Add the same `pkg: "github.com/fallguyconsulting/rimsky/sdk/go"` deny entry with desc `"graph/ may not import sdk/go (sdk is implementer-facing surface)"`.
4. Run `make lint`. Should pass (the SDK module has no content yet to violate the rules).

**Verification:** `make lint` clean; `grep -n 'sdk-purity' .golangci.yml` returns a hit.

### Task 8: Extract Testpg — move `internal/pgtest` to `sdk/go/testpg`

**Files:**
- `internal/pgtest/pgtest.go` → `sdk/go/testpg/testpg.go`
- `internal/pgtest/pgtest_test.go` → `sdk/go/testpg/testpg_test.go`
- callers of `pgtest` (update import paths)

**Context:** `internal/pgtest` is rimsky-internal today. The spec moves it into `sdk/go/testpg` as a public testcontainer helper. Existing `StartPostgres` (applies rimsky migrations) and `StartFreshPostgresDSN` (no migrations) both move. The spec also calls for an optional `WithRimskyMigrations` variant — preserve the current API (rename if needed for clarity, but keep both modes accessible).

**Context (split-first plan):** `internal/pgtest/pgtest.go` today contains both a plain-Postgres entry point (`StartFreshPostgresDSN`) and a migrations-applying entry point (`StartPostgres`) that imports `pkg:foundation/persistence`, `pkg:foundation/persistence/postgres`, and `pkg:foundation/shared`. The `sdk-purity` depguard forbids `pkg:sdk/go` from importing `pkg:foundation/...`. So testpg cannot move as one piece — the plain-Postgres helper goes to `pkg:sdk/go/testpg` (public, sdk-purity-clean) and the migrations-applying helper stays rimsky-internal at a new home (e.g., `pkg:foundation/internal/testpg-with-migrations` or `pkg:test/pgmigrate`). Do the split before any moves.

**Steps:**
1. Read `internal/pgtest/pgtest.go` end-to-end. Identify which functions and helpers depend on `foundation/persistence` / `foundation/persistence/postgres` / `foundation/shared` (the migrations-applying surface) and which depend only on `testcontainers-go` / `pgx/v5` / stdlib (the plain-container surface).
2. Inside `internal/pgtest/`, refactor by splitting `pgtest.go` into two files:
   - `internal/pgtest/fresh.go` — contains `StartFreshPostgresDSN` and any other plain-container helpers; imports only `testcontainers-go`, `pgx/v5`, stdlib.
   - `internal/pgtest/migrate.go` — contains `StartPostgres` (the migrations-applying entry point) and helpers that import `foundation/*`.
   Each file has the same `package pgtest` declaration; the existing callers don't need changes yet.
3. Verify the split: `go build ./internal/pgtest/...` and `go test ./internal/pgtest/...` clean.
4. Create destination `sdk/go/testpg/`.
5. Move ONLY the plain-container surface to the SDK: `mv internal/pgtest/fresh.go sdk/go/testpg/testpg.go` (rename to match the destination package's convention). Also move the test cases that exercise plain-container functions: extract them from `internal/pgtest/pgtest_test.go` into `sdk/go/testpg/testpg_test.go`. Update both files' `package pgtest` → `package testpg`.
6. The migrations-applying surface stays where it is — but its directory should be renamed for clarity. Rename `internal/pgtest/` → `internal/pgmigrate/` (or another name that signals "rimsky-internal, migrations-aware"). Update its `package pgtest` → `package pgmigrate` (or matching). Update callers (rimsky-internal scenario harnesses, etc.) — `grep -rln 'fallguyconsulting/rimsky/internal/pgtest'` enumerates them.
7. Update `sdk/go/go.mod` to declare the third-party deps testpg needs (`testcontainers-go`, `pgx/v5`).
8. Find all SDK-side callers expected to use the plain-container surface. The three sensor tests from Task 3 (`sensors/sensor-{http,webhook,object-store}/state_db_test.go`) call `StartFreshPostgresDSN`; update their import from `github.com/fallguyconsulting/rimsky/internal/pgtest` → `github.com/fallguyconsulting/rimsky/sdk/go/testpg`. Adjust the call site (`pgtest.StartFreshPostgresDSN` → `testpg.StartFreshPostgresDSN`).
9. Any rimsky-internal callers using the migrations-applying surface update their import to the new internal location (`internal/pgmigrate` or chosen name).

**Verification:** `go build ./...` succeeds; `go test ./sdk/go/testpg/...` passes; `grep -rn '"github.com/fallguyconsulting/rimsky/internal/pgtest"' --include="*.go"` returns no matches anywhere (the old path no longer exists); `grep -rn '"github.com/fallguyconsulting/rimsky/sdk/go/testpg"' --include="*.go"` returns matches in the three sensor test files; rimsky-internal callers of the migrations-applying surface resolve to the new internal location and `go build ./... && go test ./...` clean.

### Task 9: Extract Publisher — move `sensors/internal/post` to `sdk/go/publisher`

**Files:**
- `sensors/internal/post/post.go` → `sdk/go/publisher/publisher.go`
- `sensors/internal/post/post_test.go` → `sdk/go/publisher/publisher_test.go`
- callers in `sensors/sensor-{cron,http,object-store,webhook}/sensor.go` (and elsewhere) — update import paths

**Context:** `sensors/internal/post` is the publisher-side message-emit helper (retry + backoff + idempotency-key header). Per the spec, this is the canonical publisher-side surface; moves to `sdk/go/publisher`.

**Steps:**
1. Create directory `sdk/go/publisher/`.
2. Move both files; update package declaration from `post` → `publisher`.
3. Verify the file has no rimsky-internal imports (the file's package comment at `sensors/internal/post/post.go` says "Single ~30-line helper... `sensors/internal/post` is invisible to non-sensor code by Go's internal-package visibility rules" — should be stdlib-only). Confirm via `grep -E '"github\.com/fallguyconsulting/rimsky/' sdk/go/publisher/publisher.go` returning no matches.
4. Find callers: `grep -rln 'fallguyconsulting/rimsky/sensors/internal/post' --include="*.go"`. Update each import to `github.com/fallguyconsulting/rimsky/sdk/go/publisher` and rename references `post.X` → `publisher.X`.
5. Delete the now-empty `sensors/internal/post/` directory: `rmdir sensors/internal/post && rmdir sensors/internal` (if `sensors/internal` is then empty).
6. Search for source-code comments referencing `sensors/internal/post` and update to `sdk/go/publisher` — earlier audit found references in sensor source files via `pkg:sensors/internal/post`. Update those.

**Verification:** `make build-all && make test-all` clean; `grep -rn 'sensors/internal/post' --include="*.go"` returns no matches.

### Task 10: Extract Server bridge — move `stores/internal/bridge` to `sdk/go/server` and extract inlined scaffolding from sensors/subscribers/executors

**Files:**
- `stores/internal/bridge/bridge.go` → `sdk/go/server/bridge.go`
- `stores/internal/bridge/bridge_test.go` → `sdk/go/server/bridge_test.go`
- `stores/internal/bridge/observability.go` → `sdk/go/server/observability.go`
- New: `sdk/go/server/sensor.go`, `sdk/go/server/subscriber.go`, `sdk/go/server/executor.go` (extracted from each impl's inlined server code — implementer's call on package boundaries)
- Update callers in `stores/{filesystem,postgres,stub}/server/*.go`, `sensors/sensor-*/sensor.go`, `subscribers/openlineage/*.go`, `executors/{claude-agent,http-node,verifier-http,verifier-shape-checks,stub}/server.go` to import from `sdk/go/server`

**Context:** Spec P2.2 Server row: only `stores/` has a dedicated `internal/bridge/` today; the equivalent code in sensors/subscribers/executors is inlined per impl. Extract the common scaffolding (gRPC server bring-up wrapping each protocol's generated stubs from `pkg:protocols/proto/v1/gen`).

**Steps:**
1. Read `stores/internal/bridge/bridge.go` end-to-end. Note the public functions, types, and what protocol they bridge (probably ClaimProducer). This is the most-developed bridge package and shapes the API for the rest.
2. Create `sdk/go/server/` directory. Move the three `stores/internal/bridge/` files. Update package declaration to `package server`.
3. For each of `sensors/sensor-{cron,http,object-store,webhook}/sensor.go`, `subscribers/openlineage/subscriber.go`, `executors/{claude-agent,http-node,verifier-http,verifier-shape-checks,stub}/*.go`, read the file and identify the gRPC server bring-up + connection-handling code that has the same shape as `stores/internal/bridge`. Extract these into appropriately-named files under `sdk/go/server/` (e.g. `sdk/go/server/publisher.go` for the publisher protocol, `sdk/go/server/executor.go` for the executor protocol, etc.). Each extracted helper takes the per-impl handler as a parameter and returns or runs the server.
4. Update each impl's `*.go` file to import `github.com/fallguyconsulting/rimsky/sdk/go/server` and call the extracted helper instead of its own inlined code.
5. Update `sdk/go/go.mod` to declare the third-party deps the server surface needs (`google.golang.org/grpc`, `go-chi/chi/v5`, plus anything else surfaced by the extraction).
6. Verify no remaining `internal/bridge/` references: `grep -rn 'stores/internal/bridge' --include="*.go"` returns no matches.
7. Delete `stores/internal/bridge/` and `stores/internal/` if empty: `rmdir stores/internal/bridge && rmdir stores/internal 2>/dev/null || true`.

**Verification:** `make build-all && make test-all` clean; `grep -rn 'stores/internal/bridge' --include="*.go"` returns no matches; `(cd sdk/go && go build ./server/... && go test ./server/...)` clean.

### Task 11: Extract Ops glue — `slog` setup, healthcheck, DSN env-var parser

**Files:**
- `sdk/go/ops/slog.go` (new)
- `sdk/go/ops/health.go` (new)
- `sdk/go/ops/dsn.go` (new)
- `sdk/go/ops/ops_test.go` (new)
- Callers: `stores/{filesystem,postgres,stub}/cmd/main.go`, `sensors/sensor-*/main.go`, `subscribers/openlineage/main.go`, `executors/*/main.go`

**Context:** Spec P2.2 Ops row: each bundled service has copy-pasted setup code in its `cmd/main.go` (slog initialization, /health HTTP endpoint, DSN env-var parsing). Extract the common shape.

**Steps:**
1. Read 2–3 bundled service `cmd/main.go` files (e.g., `sensors/sensor-http/main.go`, `stores/postgres/cmd/main.go`, `subscribers/openlineage/main.go`) and identify the common setup shape: how slog is initialized, how /health is served, how the DSN env var is read and parsed.
2. Create `sdk/go/ops/slog.go` with a `Setup(level slog.Level) *slog.Logger` (or matching the common shape) that returns the configured logger. Match what the existing services do — do not invent new conventions.
3. Create `sdk/go/ops/health.go` with a `HealthHandler() http.Handler` (or matching the common shape) that responds 200 OK on `/health`. Optionally accept a "readiness" callback.
4. Create `sdk/go/ops/dsn.go` with a `DSNFromEnv(envVar string) (string, error)` that reads and validates a Postgres DSN from an env var, matching the conventions in existing services.
5. Add basic tests in `sdk/go/ops/ops_test.go` covering the happy paths.
6. Update each bundled service's `cmd/main.go` to use the SDK helpers instead of its inlined setup code. Drop the inlined helpers.

**Verification:** `make build-all && make test-all` clean; `(cd sdk/go && go test ./ops/...)` clean.

### Task 12: Update `pgx-isolation` depguard to include `sdk/go` in allow list

**Files:**
- `.golangci.yml`

**Steps:**
1. Locate the `pgx-isolation` rule in `.golangci.yml`. Its `files:` block uses a "lax" list — currently allows `$all` minus the listed exclusions (i.e., the listed paths are allowed to use pgx). Add `"!**/sdk/go/**"` to the exclusions so `sdk/go/testpg/` can use pgx without violating the rule.
2. Run `make lint` to confirm clean.

**Verification:** `make lint` clean.

---

## Pass 3: P2b — Conformance reorg, `runtime/peer` rename, canary scenarios, P2 design-doc mutations

**Goal:** Complete P2 — extract conformance runners into `sdk/go/conformance`, rename `runtime/remote` → `runtime/peer`, add in-tree canary scenarios that replace crimefinder's drift signal, apply all P2-applicable concept-doc mutations.
**Scope:** Tasks 13–20.
**End state:** working
**Verification:** `make lint && make build-all && make test-all && (cd sdk/go && go build ./... && go test ./...)`

### Task 13: Extract conformance runners to `sdk/go/conformance` + convert CLI binaries to thin wrappers

**Files:**
- `sdk/go/conformance/claimproducer/runner.go` (new — extracted from `cmd/rimsky-claim-producer-conformance/main.go`)
- `sdk/go/conformance/executor/runner.go` (new — from `cmd/rimsky-executor-conformance/main.go` and `lifecycle_check.go` and `observability_check.go`)
- `sdk/go/conformance/blobbackend/runner.go` (new — from `cmd/rimsky-blob-backend-conformance/main.go`)
- `sdk/go/conformance/dataprocessing/runner.go` (new — from `cmd/rimsky-data-processing-conformance/main.go`)
- `sdk/go/conformance/publisher/runner.go` (new — from `cmd/rimsky-publisher-conformance/main.go`)
- `sdk/go/conformance/validation/runner.go` (new — from `cmd/rimsky-validation-conformance/main.go`)
- `cmd/rimsky-{claim-producer,executor,blob-backend,data-processing,publisher,validation}-conformance/main.go` — rewritten as thin wrappers
- Existing fixture packages under `conformance/claimproducer/`, `conformance/scenarios/` — relocated under `sdk/go/conformance/` (implementer's call on exact structure)

**Steps:**
1. For each of the six conformance binaries:
   - Read its current `main.go` (and any sibling files like `observability_check.go`, `lifecycle_check.go`).
   - Extract the runner logic — everything that's not flag parsing, output formatting, or CLI exit-code handling — into a package under `sdk/go/conformance/<protocol>/runner.go`. Export a `Run(opts Options) (Report, error)` (or similar) so it can be invoked from a Go test.
   - Rewrite the binary's `main.go` to: parse flags, instantiate `Options`, call `runner.Run`, format the `Report` for stdout, exit with appropriate code. Should be ~30 lines.
2. Relocate the existing fixture packages (`conformance/claimproducer/`, `conformance/scenarios/`) under `sdk/go/conformance/` if they're imported by the extracted runners. Keep the public API surface so existing call sites still work.
3. Update `Makefile` build targets if any reference the old locations (probably not — `make build-all` builds via `go build ./cmd/...`).
4. Run each conformance binary's tests: the existing `cmd/rimsky-claim-producer-conformance/main_test.go` and `cmd/rimsky-data-processing-conformance/main_test.go` should still pass (they invoke the binary's `main.go`; after the rewrite, the runner library is what they exercise via the thin wrapper).

**Verification:** `make build-all && make test-all` clean; `(cd sdk/go && go test ./conformance/...)` clean; each conformance CLI binary builds (`go build ./cmd/rimsky-claim-producer-conformance/...` etc.).

### Task 14: Rename `runtime/remote` → `runtime/peer`

**Files:**
- `runtime/remote/` → `runtime/peer/` (rename directory)
- All callers of `runtime/remote` — update imports

**Context:** Spec P2.3 — mandatory rename. The package is rimsky-internal infrastructure tightly coupled to supervisor / terminal-resolution / discovery-cache; "peer" matches the `concept:service` vocabulary better than "remote."

**Steps:**
1. Move the directory: `git mv runtime/remote runtime/peer`. (`git mv` is fine here — same repo.)
2. Update package declaration in each file in the renamed directory: `package remote` → `package peer`. Files include `client.go`, `data_processing_client.go`, `dial.go`, `doc.go`, `lifecycle_client.go`, `publisher_client.go`, `validation_client.go`.
3. Find all callers: `grep -rln 'fallguyconsulting/rimsky/runtime/remote' --include="*.go"`. Update each import to `github.com/fallguyconsulting/rimsky/runtime/peer` and rename `remote.X` references → `peer.X`.
4. Update any references in non-Go files (depguard rules, documentation, comments) — `grep -rln 'runtime/remote'` will surface them. Notable: the `pgx-isolation` rule's allow-list (if it references runtime/remote, update to runtime/peer; check during execution).

**Verification:** `make lint && make build-all && make test-all` clean; `grep -rn 'runtime/remote' --include="*.go"` returns no matches; `grep -rn 'runtime/remote' .golangci.yml Makefile` returns no matches.

### Task 15: Add in-tree canary scenario — template-registration + run-a-pass

**Files:**
- `test/scenarios/canary/template_registration_run_a_pass_test.go` (new)

**Context:** Spec P2.5 — this scenario replaces crimefinder's role as the drift signal for control-api YAML grammar and template registration. Before writing, audit `apps/crimefinder/test/` to understand crimefinder's e2e test scope, so the canary catches what crimefinder catches today.

**Steps:**
1. Read `apps/crimefinder/test/integration/` and `apps/crimefinder/test/e2e/` to identify what crimefinder's tests assert about control-api shape and YAML grammar.
2. Read an existing scenario test (e.g., `test/scenarios/fanout_callback_determinism_e2e_test.go`) to learn the `graph/scenario.Start` harness pattern.
3. Write `test/scenarios/canary/template_registration_run_a_pass_test.go` that:
   - Uses the scenario harness to bring up rimsky in-process.
   - Registers a non-trivial template via the operator API (`POST /templates`).
   - Creates an instance via `POST /instances`.
   - Triggers a pass (the same shape crimefinder triggers).
   - Asserts on the run outcome — completion, expected outputs, etc. The assertions should be tight enough to catch silent YAML-grammar drift.
4. Verify the test runs and passes.

**Verification:** `go test ./test/scenarios/canary/...` passes.

### Task 16: Add in-tree canary scenario — lifecycle-subscriber callback contract

**Files:**
- `test/scenarios/canary/lifecycle_subscriber_callback_test.go` (new)

**Context:** Spec P2.5 — this scenario exercises the `concept:lifecycle-subscriber` callback API end-to-end. Replaces openlineage white-box's catch beyond what its peer-driven rewrite in Task 2 covers.

**Steps:**
1. Read `subscribers/openlineage/subscriber.go` and `subscribers/openlineage/emitter.go` to understand which lifecycle events the subscriber registers for and how it acks them.
2. Read `concept:lifecycle-subscriber` in `.ok-planner/design/concepts/lifecycle-subscriber.md` to verify the contract.
3. Write `test/scenarios/canary/lifecycle_subscriber_callback_test.go` that:
   - Brings up rimsky via the scenario harness.
   - Registers a fake lifecycle-subscriber peer (a test-double receiving callbacks).
   - Drives rimsky through state transitions that should fire the six lifecycle events.
   - Asserts the fake subscriber received each event with the expected envelope shape + idempotency behavior.
4. Verify the test runs and passes.

**Verification:** `go test ./test/scenarios/canary/...` passes (both canary tests now exist and pass).

### Task 17: Create `concept:sdk` doc

**Files:**
- `.ok-planner/design/concepts/sdk.md` (new)
- `.ok-planner/design/concepts.md` (regenerated TOC)

**Steps:**
1. Create `.ok-planner/design/concepts/sdk.md` with this exact content (from spec lines 456–498):

```markdown
---
concept: sdk
status: as-is
aliases:
  - rimsky-sdk
  - sdk/go
---

# SDK

## What it is

`pkg:github.com/fallguyconsulting/rimsky/sdk/go` is the canonical Go-side implementer-facing surface for building services that rimsky talks to. A peer Go module within the rimsky repo, alongside `pkg:protocols/` and `pkg:foundation/`. Houses:

- Server scaffolding for claim-producer / executor / lifecycle-subscriber / blob-backend / publisher protocols
- Publisher-side helpers (message-emit retry+backoff, idempotency-key header, callback POST handling)
- Conformance library (`pkg:sdk/go/conformance`) — invocable from service authors' Go tests in addition to the thin CLI wrappers in `pkg:cmd/rimsky-*-conformance`
- Testcontainer helpers (`pkg:sdk/go/testpg`) — plain Postgres + optional `WithRimskyMigrations` variant
- Ops glue — `slog` setup, healthcheck HTTP endpoint, DSN env-var parser

## Purpose

Remove footguns from third-party and bundled service authors (canonical example: the TS `kind`-vs-`type` body-key bug documented in `file:CLAUDE.md`'s gotcha list). Provide one paved path to "implement a service rimsky calls."

## Boundaries

Owns: the implementer-facing surface listed above. Does NOT own: the calling-side wire code (rimsky-internal infrastructure tightly coupled to `concept:supervisor`, `concept:terminal-resolution`, `concept:discovery-cache` — stays in rimsky's `pkg:runtime/peer`). Does NOT own: non-Go languages (a future `pkg:sdk/ts` would be a separate concept if/when it lands).

## Invariants

- `sdk-purity` depguard rule: `pkg:sdk/go` imports only `pkg:protocols/` + stdlib + minimal third-party. No imports from `foundation/`, `graph/`, `runtime/`, `control/`, or `cmd/`.
- Lockstep tagging with rimsky-core: root module tagged `v0.X.0`, sub-module tagged `sdk/go/v0.X.0`, both cut by the same release script.
- Break-freely pre-v1 license per `file:.claude/rules/rules.md`. No deprecation-alias discipline; CHANGELOG entries are the visibility surface for breaks.

## Aliases and historical names

`rimsky-sdk` informally; `sdk/go` in path-form. Created in this reorganization (spec `2026-05-24-repo-reorganization-design`).

## Notes

- 2026-05-24: created as part of the repo reorganization. SDK birth covered in spec `2026-05-24-repo-reorganization-design` phase P2.
```

2. Regenerate `.ok-planner/design/concepts.md` so the new `sdk` concept appears in the TOC. The current concepts.md is auto-generated; per its header comment, it's "refreshed by `execute-plan` when a plan touches `concepts/`." Add a one-line bullet for `sdk` matching the format of existing TOC entries: `- \`sdk\` (aliases: rimsky-sdk, sdk/go) — Canonical Go-side implementer-facing surface for building services rimsky talks to.` Insert in alphabetical order.

**Verification:** `test -f .ok-planner/design/concepts/sdk.md` and the file matches the template; `grep -n '^- \`sdk\`' .ok-planner/design/concepts.md` returns a hit.

### Task 18: Mutate `concept:module-layout`

**Files:**
- `.ok-planner/design/concepts/module-layout.md`

**Context:** This is the major concept-doc mutation. The current doc describes a 3-module workspace (protocols, foundation, root) but mistakenly mentions a 4th "MCP-server module" at `mcp-servers/control-api/` that does not exist (`find . -name go.mod` confirms 3 modules pre-reorg). The mutation adds `sdk/go` as the genuine 4th module, removes the stale MCP-server-module claim, and updates the invariants for the new depguard landscape.

**Steps:**
1. Read the current `concept:module-layout` at `.ok-planner/design/concepts/module-layout.md` end-to-end.
2. **"What it is" section:** Replace the current paragraph (which describes "Three Go modules into one workspace plus the MCP-server module" and a four-way split inside root) with text describing the new four-Go-module workspace: `pkg:protocols`, `pkg:foundation`, `pkg:sdk/go` (NEW), and the root module containing `graph/`, `runtime/`, `control/`, `cmd/`. Clarify that the operator MCP shim at `pkg:control/controlapi/mcp` is in-tree as part of the root module (NOT a separate module — corrects the pre-existing concept-doc error; `file:go.work` lists only three modules pre-reorg, four post-reorg). Describe `pkg:sdk/go`'s dependency budget (protocols + stdlib + minimal third-party) and its purpose (canonical implementer-facing surface — link to `concept:sdk`). Remove references to `stores/`, `executors/`, `sensors/`, `subscribers/`, `dashboards/`, `examples/`, `apps/` — those directories live in `pkg:github.com/fallguyconsulting/rimsky-services` and sibling repos post-reorg (test-infrastructure carve-outs `stores/stub`, `executors/stub`, `stores/{filesystem,postgres}/testfixture` remain in rimsky).
3. **"Boundaries" section:** Add a sentence: `pkg:sdk/go` owns the implementer-facing surface; does NOT own the calling-side wire code (rimsky-internal, stays at `pkg:runtime/peer`).
4. **"Invariants" section:**
   - Add `sdk-purity` depguard rule entry.
   - Add `consumption-side-isolation` depguard rule entry (transitional during P1-P3; stays as defensive guard against re-bundling).
   - Update `foundation-purity` entry: deny list adds `pkg:sdk/go`.
   - Update `graph-purity` entry: deny list adds `pkg:sdk/go`.
   - Remove `stores/`, `executors/`, `dashboards/` from `foundation-purity`, `graph-purity`, `runtime-purity` deny lists (those directories no longer exist in rimsky post-P3+P6; the `consumption-side-isolation` rule covers any future re-introduction).
5. **"Notes" section:** Append two entries verbatim from the spec (lines 513–514):
   - `2026-05-24: in-repo audit prep. P1 of spec 2026-05-24-repo-reorganization-design: 11 cosmetic foundation/locks → protocols/claimproducer swaps in stores/*, white-box openlineage subscriber test rewritten as peer-driven integration, 4 sensor/store tests dropped their internal/pgtest dependency, new consumption-side-isolation depguard rule, two empty cmd/rimsky-verifier-* directories deleted.`
   - `2026-05-24: SDK birth + bundled-deliverables migration. P2–P6 of spec 2026-05-24-repo-reorganization-design: new pkg:sdk/go peer Go module (server scaffolding, publisher helpers, conformance library, testcontainer helpers, ops glue); rimsky's calling-side renamed pkg:runtime/remote → pkg:runtime/peer; conformance CLI binaries become thin wrappers over pkg:sdk/go/conformance; production-side bundled stores (filesystem, postgres), sensors, subscribers/openlineage, and production-side executors (claude-agent, http-node, verifier-http, verifier-shape-checks) moved to ../rimsky-services. Test-infrastructure carve-outs (stores/stub, executors/stub, stores/{filesystem,postgres}/testfixture) stayed in rimsky. Docs + docs-tooling + atomic-staging-fs-producer example + four of its scenario tests moved to ../rimsky-docs; apps/crimefinder moved to ../crimefinder; dashboards/rimsky-dashboard moved to ../rimsky-dashboard; in-tree pkg:test/scenarios/ canaries added to replace crimefinder + openlineage in-tree drift signals.`

**Verification:** `grep -c 'sdk-purity' .ok-planner/design/concepts/module-layout.md` ≥ 1; `grep -c 'consumption-side-isolation' .ok-planner/design/concepts/module-layout.md` ≥ 1; `grep -c '2026-05-24' .ok-planner/design/concepts/module-layout.md` ≥ 2.

### Task 19: Mutate `concept:claim-producer` (P2 portion) + `concept:publisher` (P2 portion)

**Files:**
- `.ok-planner/design/concepts/claim-producer.md`
- `.ok-planner/design/concepts/publisher.md`

**Context:** Both concept docs reference `runtime/remote/` paths that change in P2. The full mutations of these docs span P2 (the rename) and P3 (the bundled-impl relocation). Apply only the P2 portion here.

**Steps:**

For `claim-producer.md`:
1. Read line 19 of the current `claim-producer.md`. It references the gRPC client at `runtime/remote/`.
2. Replace `runtime/remote/` with `runtime/peer/` at line 19.

For `publisher.md`:
3. Read lines 23 and 42 of `publisher.md`. Both reference `code:runtime/remote/publisher_client.go`.
4. At each line, replace `runtime/remote/publisher_client.go` with `runtime/peer/publisher_client.go`.
5. Sweep the rest of `publisher.md` for any other `runtime/remote/` references and update accordingly.

Notes-section appends will be added in their full form during the P3 portion of these mutations (Tasks 34 for `claim-producer.md` and 35 for `publisher.md`) — do not append Notes entries in this task to avoid double-stamping.

**Verification:** `grep -n 'runtime/remote' .ok-planner/design/concepts/claim-producer.md .ok-planner/design/concepts/publisher.md` returns no matches.

### Task 20: Mutate `concept:conformance` + CHANGELOG entry for P2

**Files:**
- `.ok-planner/design/concepts/conformance.md`
- `CHANGELOG.md`

**Steps:**
1. Read the current `conformance.md`. The "What it is" section opens with text mentioning "Four standalone binaries" — stale, the actual count is six (`rimsky-{claim-producer,executor,blob-backend,data-processing,publisher,validation}-conformance`).
2. **"What it is" section:** Replace the intro paragraph with: `Six thin CLI wrappers in pkg:cmd/rimsky-*-conformance over a shared library at pkg:sdk/go/conformance (one sub-package per protocol).` Update the per-binary bullet list that follows: current list enumerates only four binaries; add bullets for `rimsky-data-processing-conformance`, `rimsky-publisher-conformance`, `rimsky-validation-conformance` mirroring the structure of the existing entries.
3. **"Boundaries" section:** Replace `Owns: the standalone binaries, the two shared fixture packages, the stub-mode probe.` with `Owns: the conformance library (pkg:sdk/go/conformance), the thin CLI wrappers (pkg:cmd/rimsky-*-conformance), the two shared fixture packages, and the stub-mode probe (pkg:cmd/rimsky-conformance-probe).`
4. **"Notes" section:** Append the following entry verbatim (spec line 555):
   `2026-05-24: conformance runner logic extracted from pkg:cmd/rimsky-*-conformance/main.go into pkg:sdk/go/conformance as a library. CLI binaries kept as thin wrappers calling the library. External Go authors can now invoke conformance from a Go test. Also corrected pre-existing stale binary count (four → six) in the "What it is" section. See spec 2026-05-24-repo-reorganization-design phase P2.`
5. **CHANGELOG.md:** Append to `## Unreleased`:
   - SDK birth: new `pkg:sdk/go` peer module with server scaffolding, publisher helpers, conformance library, testpg, ops glue.
   - `pkg:runtime/remote` → `pkg:runtime/peer` rename (breaking change for any external consumer; pre-v1 license applies).
   - Conformance CLI binaries become thin wrappers over `pkg:sdk/go/conformance`.

**Verification:** `grep -n '2026-05-24' .ok-planner/design/concepts/conformance.md` returns a hit at the appended Notes entry; `grep -n 'sdk/go' CHANGELOG.md` returns hits.

---

## Pass 4: P3a — Scenario test audit + testfixture refactor

**Goal:** Categorize the ~30 in-tree scenario tests that currently import `pkg:stores/{filesystem,postgres}/`, rewrite the default-rule subset to use `pkg:stores/stub`, identify the move-to-rimsky-services subset, and refactor `pkg:stores/{filesystem,postgres}/testfixture/` packages so they no longer import the to-be-moved production code.
**Scope:** Tasks 21–24.
**End state:** working
**Verification:** `make lint && make test-all`

### Task 21: Inventory and categorize the in-tree tests importing bundled production stores

**Files:**
- `.ok-planner/plans/2026-05-24-repo-reorganization-test-audit.md` (new — implementer's working notes; not a deliverable, but enumeration goes here for Tasks 22–23)

**Context:** Spec P3.4 specifies the default rule (rewrite to stub-store), Exception 1 (file-name conventions `stores/fs_*`, `pg_verifier_*` move to rimsky-services), Exception 2 (delete redundant tests). The audit categorizes each test for Tasks 22 (rewrite) and 23 (move).

**Steps:**
1. Generate the full list: `grep -rln 'fallguyconsulting/rimsky/stores' --include="*.go" . | grep -v '^./stores/'` (any imports outside the `stores/` tree itself). Then also enumerate `test/scenarios/stores/` directly: `ls test/scenarios/stores/*.go` — every file in that directory needs classification (verified at plan-write: includes `fs_*`, `scope_*`, `open_rollback_test.go`, `placeholder_test.go`, possibly others). For files whose name doesn't match an obvious convention (e.g., `open_rollback_test.go`, `placeholder_test.go`, `scope_claim_test.go`, `scope_envelope_test.go`), read the file's contents — if the test exercises a store's specific behavior (filesystem-only, postgres-only) it's MOVE-TO-RIMSKY-SERVICES; if it exercises rimsky's behavior against any store, it's REWRITE-TO-STUB.
2. For each file, classify into one of three categories:
   - **REWRITE-TO-STUB:** scenarios that exercise rimsky's behavior generically with any backing store (the test is about cascade / scheduler / supervisor / lifecycle behavior; the store is incidental). Default category. Includes most tests under `test/scenarios/locks/`, `test/scenarios/fanout_*`, `test/scenarios/held_claim_*`, `test/scenarios/lifecycle/`, `test/scenarios/acquire_*`, `test/scenarios/asset/`, `test/scenarios/claim_stores/`, `test/scenarios/run_tree/`, `test/scenarios/parked_lifecycle_test.go`, `test/scenarios/attribute_overrides_match_overlay_fanout_e2e_test.go`, `test/scenarios/bundled_executor_vocab_test.go`.
   - **MOVE-TO-RIMSKY-SERVICES:** store-specific tests — name conventions indicate intent:
     - `test/scenarios/stores/fs_*` — filesystem-specific (pick policies, queue concurrency).
     - `test/scenarios/atomic_staging/pg_verifier_*` (three files: `pg_verifier_commit_abandon_test.go`, `pg_verifier_conformance_test.go`, `pg_verifier_test.go`) — postgres-specific.
     - `test/scenarios/stores/scope_*` — may or may not be store-specific; read each to decide. If they test fs/postgres specifics, MOVE; if they test rimsky's claim-scope behavior with any store, REWRITE.
     - `test/smoke/setup.go` and `test/smoke/data_platform_smoke_test.go` — read to decide.
   - **MOVE-TO-RIMSKY-DOCS:** four atomic-staging example tests (handled by P4, not P3):
     - `test/scenarios/atomic_staging/abandon_on_any_failure_test.go`
     - `test/scenarios/atomic_staging/commit_on_all_success_test.go`
     - `test/scenarios/atomic_staging/concurrent_staging_test.go`
     - `test/scenarios/atomic_staging/sub_stage_verifier_failure_test.go`
3. Also include the two conformance binary tests that import `stores/stub`: `cmd/rimsky-claim-producer-conformance/main_test.go`, `cmd/rimsky-data-processing-conformance/main_test.go`. These stay in rimsky (stub-store remains in rimsky) — they need no changes here.
4. Write the categorization into `.ok-planner/plans/2026-05-24-repo-reorganization-test-audit.md` as a checklist with three sections (REWRITE-TO-STUB, MOVE-TO-RIMSKY-SERVICES, MOVE-TO-RIMSKY-DOCS), each listing the files. This file is execute-plan's working notes and gets archived with the plan.

**Verification:** `test -f .ok-planner/plans/2026-05-24-repo-reorganization-test-audit.md`; the file enumerates every test file that came back from the `grep` in Step 1.

### Task 22: Rewrite REWRITE-TO-STUB tests to use `pkg:stores/stub`

**Files:** Every file in the REWRITE-TO-STUB category from Task 21's audit.

**Steps:**
1. For each REWRITE-TO-STUB test, examine the import block. Identify which `stores/filesystem/...` or `stores/postgres/...` packages are imported. Common imports include `stores/postgres/testfixture`, `stores/filesystem/testfixture`, `stores/postgres/store`, `stores/filesystem/store`, `stores/common/action`.
2. For each, swap to the equivalent `pkg:stores/stub` package. Specifically:
   - `stores/{filesystem,postgres}/testfixture` → `stores/stub/testfixture` (if it exists; if not, the testfixture refactor in Task 24 may need to add a stub-store testfixture sibling first — flag during execution).
   - `stores/{filesystem,postgres}/store` → `stores/stub/store`.
   - `stores/common/action` stays as-is (it's a shared package, not store-specific; if `common/action` moves out, separate handling).
3. Adjust the test setup to use the stub-store fixture's API. The semantics should match: stub-store is deterministic in-memory, which is fine for tests exercising cascade/scheduler/supervisor behavior.
4. Run the rewritten test individually to confirm it still passes.
5. After all rewrites in this task, run `go test ./test/scenarios/...` to verify no regressions across the rewrite set.

**Verification:** `go test ./test/scenarios/...` clean; `grep -rln 'fallguyconsulting/rimsky/stores/filesystem\|fallguyconsulting/rimsky/stores/postgres' test/scenarios/ --include="*.go"` returns no matches outside files categorized MOVE-TO-* in the audit.

### Task 23: Identify exact MOVE-TO-RIMSKY-SERVICES test set + leave-in-place markers

**Files:** Confirm the list from Task 21's audit; no file changes in this task — the actual move happens in Pass 5 (Task 28). This task firms up the list and verifies the move-set compiles standalone before the move.

**Steps:**
1. Re-read the MOVE-TO-RIMSKY-SERVICES set from Task 21. For each file, confirm by reading it that the test genuinely depends on filesystem-specific or postgres-specific behavior (file naming is the heuristic; the file content is the truth).
2. For any file that turns out to be generic-store-behavior despite a misleading filename, reclassify to REWRITE-TO-STUB and add it to Task 22's set (do Task 22 again for those). For any file genuinely store-specific, leave in the move set.
3. Update `.ok-planner/plans/2026-05-24-repo-reorganization-test-audit.md` with the firmed-up list.

**Verification:** The audit file's MOVE-TO-RIMSKY-SERVICES set is finalized; each file in it has a one-line note explaining why it's store-specific.

### Task 24: Refactor `stores/{filesystem,postgres}/testfixture/` packages to not import production code

**Files:**
- `stores/filesystem/testfixture/testfixture.go` (refactor)
- `stores/postgres/testfixture/testfixture.go` (refactor)

**Context:** Spec P3.1 — after P3 moves `stores/{filesystem,postgres}/{cmd,server,store,lifecycle,...}` to `../rimsky-services`, the testfixture packages (staying in rimsky) can no longer import their sibling production packages. Two options per spec:
- **(preferred)** refactor to spin up the rimsky-services-published image of the store (no Go import of production code — shell out to docker), or
- swap testfixture's backing store from postgres/filesystem to stub-store while preserving the same test-facing API.

**Steps:**
1. Read each testfixture file end-to-end. Note its public API — what callers expect to call.
2. Choose the refactor approach. The preferred approach (docker image) requires the rimsky-services image to be built and reachable; this only works after Pass 5 (Task 28+) publishes the image. **For this task, use the stub-store approach** so the testfixture refactor lands in this pass (before the move) and the in-tree tests stay green throughout P3. Implementation: rewrite each testfixture to wrap `pkg:stores/stub` while preserving the public function signatures.
3. Verify the testfixture's public API is unchanged (same function names, same return types) so call-site tests don't need changes.
4. Update any imports in the testfixture file that referenced sibling production packages.
5. Confirm tests that use the refactored testfixture still pass: `go test ./test/scenarios/... ./cmd/rimsky-claim-producer-conformance/... ./cmd/rimsky-data-processing-conformance/...`.

**Verification:** `make test-all` clean; `grep -E '"github\.com/fallguyconsulting/rimsky/stores/(filesystem|postgres)/(cmd|server|store|lifecycle)' stores/filesystem/testfixture/testfixture.go stores/postgres/testfixture/testfixture.go` returns no matches.

---

## Pass 5: P3b — Move production-side directories to `../rimsky-services`

**Goal:** Physically move the production-side bundled impls from rimsky to `../rimsky-services`, set up the rimsky-services Go module against `pkg:sdk/go`, and move the MOVE-TO-RIMSKY-SERVICES tests from Task 23.
**Scope:** Tasks 25–31.
**End state:** working
**Verification:** `cd ../rimsky-services && go build ./... && go test ./...` clean AND `cd /Users/patrick/Documents/projects/research/zonebase/submodules/rimsky && make lint && make build-all && make test-all` clean.

### Task 25: Set up `../rimsky-services/go.mod` + workspace scaffolding

**Files:**
- `../rimsky-services/go.mod` (new)
- `../rimsky-services/README.md` (new)
- `../rimsky-services/CHANGELOG.md` (new)
- `../rimsky-services/CLAUDE.md` (new)
- `../rimsky-services/.gitignore` (new)
- `../rimsky-services/deploy/` (new directory)
- `../rimsky-services/.github/workflows/ci.yml` (new — basic CI)

**Steps:**
1. Verify `../rimsky-services` exists and is empty or contains only initial scaffolding (the user pre-created it).
2. Create `../rimsky-services/go.mod`:
```
module github.com/fallguyconsulting/rimsky-services

go 1.25.0

require (
	github.com/fallguyconsulting/rimsky/sdk/go v0.0.0
)
```
   The version `v0.0.0` is a placeholder; for local dev, the per-developer `go.work` resolves it locally; for CI, the user pins a tagged version. The plan does not commit a specific version.
3. Create `../rimsky-services/README.md` with a one-paragraph description: this repo houses the production-side bundled implementations of rimsky's protocols — stores, sensors, subscribers, executors. Built against `pkg:github.com/fallguyconsulting/rimsky/sdk/go`. Link back to rimsky.
4. Create `../rimsky-services/CHANGELOG.md` with an initial `## Unreleased` section noting the repo's creation per the reorganization spec.
5. Create `../rimsky-services/CLAUDE.md` (terse — point at concept catalog in rimsky via `RIMSKY_REPO` if useful for agents).
6. Create `../rimsky-services/.gitignore` matching rimsky's pattern (binary outputs, .DS_Store, vendor, etc.).
7. Create `../rimsky-services/deploy/` (placeholder; service-side compose fragments land here as services move).
8. Create `../rimsky-services/.github/workflows/ci.yml` with a basic Go CI workflow: `go build ./... && go test ./... && golangci-lint run` against the pinned rimsky/sdk/go version. Copy the workflow shape from rimsky's existing `.github/workflows/` files.

**Verification:** `cd ../rimsky-services && go mod verify` exits 0 (the module file is well-formed).

### Task 26: Move `stores/filesystem/{cmd,server,store,lifecycle,Dockerfile.filesystem}` to rimsky-services

**Files:**
- Source: `stores/filesystem/{cmd,server,store,lifecycle}/` + `stores/filesystem/Dockerfile.filesystem`
- Destination: `../rimsky-services/stores/filesystem/{cmd,server,store,lifecycle}/` + `../rimsky-services/stores/filesystem/Dockerfile.filesystem`
- Stays in rimsky: `stores/filesystem/testfixture/` (already refactored in Task 24)

**Steps:**
1. `mkdir -p ../rimsky-services/stores/filesystem`.
2. For each sub-directory `cmd`, `server`, `store`, `lifecycle`:
   - `mv stores/filesystem/<subdir> ../rimsky-services/stores/filesystem/<subdir>`.
3. `mv stores/filesystem/Dockerfile.filesystem ../rimsky-services/stores/filesystem/Dockerfile.filesystem`.
4. Update import paths in every moved `.go` file: replace `github.com/fallguyconsulting/rimsky/stores/filesystem` with `github.com/fallguyconsulting/rimsky-services/stores/filesystem`. Sites include sibling-package imports (e.g., `stores/filesystem/store` importing `stores/filesystem/lifecycle`).
5. Update import paths to common/shared packages: if moved code imports `stores/common/action` or `stores/shared/sql-checks`, those packages must also move (decision: move them with the first store that needs them — likely to be `../rimsky-services/stores/common/action` and `../rimsky-services/stores/shared/sql-checks`; do the move in this task if first needed, or in Task 27).
6. Verify the moved code builds in its new location: `cd ../rimsky-services && go build ./stores/filesystem/...`.
7. Verify rimsky still builds (testfixture's call sites should resolve since testfixture was refactored in Task 24): `cd /Users/patrick/Documents/projects/research/zonebase/submodules/rimsky && go build ./...`.

**Verification:** Both `cd ../rimsky-services && go build ./stores/filesystem/...` and rimsky's `go build ./...` exit 0.

### Task 27: Move `stores/postgres/{cmd,server,store,lifecycle,Dockerfile.postgres}` to rimsky-services

**Files:**
- Source: `stores/postgres/{cmd,server,store,lifecycle}/` + `stores/postgres/Dockerfile.postgres`
- Destination: `../rimsky-services/stores/postgres/{cmd,server,store,lifecycle}/` + `../rimsky-services/stores/postgres/Dockerfile.postgres`
- Stays in rimsky: `stores/postgres/testfixture/` (already refactored in Task 24)

**Steps:**
1. Mirror Task 26's steps for the postgres store. Same shape, different store.
2. If Task 26 didn't already move `stores/common/` or `stores/shared/`, do it here. Confirm via `grep -rln 'fallguyconsulting/rimsky/stores/common\|fallguyconsulting/rimsky/stores/shared' --include="*.go" ../rimsky-services/` returning hits.
3. Update import paths in moved files: `github.com/fallguyconsulting/rimsky/stores/postgres` → `github.com/fallguyconsulting/rimsky-services/stores/postgres`; same for any common/shared imports.
4. Move `stores/common/` and `stores/shared/` to `../rimsky-services/stores/common/` and `../rimsky-services/stores/shared/` if not already done. Update imports of those packages everywhere.

**Verification:** `cd ../rimsky-services && go build ./stores/...` and rimsky's `go build ./...` both clean.

### Task 28: Move sensors, subscribers/openlineage, and production-side executors

**Files:**
- Source: `sensors/sensor-{cron,http,object-store,webhook}/`, `sensors/internal/` (if anything remains there after Task 9), `subscribers/openlineage/`, `executors/{claude-agent,http-node,verifier-http,verifier-shape-checks}/`
- Destination: `../rimsky-services/{sensors,subscribers/openlineage,executors}/...`

**Steps:**
1. `mkdir -p ../rimsky-services/{sensors,subscribers,executors}`.
2. For each `sensors/sensor-*` directory: `mv sensors/sensor-XXX ../rimsky-services/sensors/sensor-XXX`. Repeat for cron, http, object-store, webhook.
3. If `sensors/internal/` has any leftover content not moved by Task 9, move it to `../rimsky-services/sensors/internal/`. Otherwise: `rmdir sensors/internal && rmdir sensors`.
4. `mv subscribers/openlineage ../rimsky-services/subscribers/openlineage`. The `subscribers/openlineage/docker-compose.test.yml` from Task 2 moves with it.
5. `rmdir subscribers` (rimsky has no other subscribers).
6. For each production-side executor: `mv executors/<name> ../rimsky-services/executors/<name>` (where name is claude-agent, http-node, verifier-http, verifier-shape-checks). DO NOT move `executors/stub/` — it stays in rimsky.
7. Update all moved Go files' imports: `github.com/fallguyconsulting/rimsky/{sensors,subscribers,executors}/...` → `github.com/fallguyconsulting/rimsky-services/{sensors,subscribers,executors}/...`.
8. Update SDK consumer paths if any moved code imported the SDK with internal paths instead of `github.com/fallguyconsulting/rimsky/sdk/go`.
9. For `executors/claude-agent` — verify the TS workspace moves cleanly with the directory (npm package.json, node_modules ignored via .gitignore). The TS toolchain inside `executors/claude-agent` is self-contained.
10. Verify each moved tree builds: `cd ../rimsky-services && go build ./sensors/... ./subscribers/... ./executors/...`.

**Verification:** `cd ../rimsky-services && go build ./... && go test ./...` clean; rimsky's `go build ./...` and `go test ./...` clean.

### Task 29: Move MOVE-TO-RIMSKY-SERVICES tests from Task 23

**Files:** The MOVE-TO-RIMSKY-SERVICES file list from `.ok-planner/plans/2026-05-24-repo-reorganization-test-audit.md`. Includes (verified during audit):
- `test/scenarios/stores/fs_cross_queue_concurrency_test.go`
- `test/scenarios/stores/fs_pick_policy_basic_test.go`
- `test/scenarios/stores/fs_pick_vs_scope_concurrency_test.go`
- `test/scenarios/atomic_staging/pg_verifier_commit_abandon_test.go`
- `test/scenarios/atomic_staging/pg_verifier_conformance_test.go`
- `test/scenarios/atomic_staging/pg_verifier_test.go`
- Possibly: `test/scenarios/stores/scope_claim_test.go`, `test/scenarios/stores/scope_envelope_test.go`, `test/smoke/setup.go`, `test/smoke/data_platform_smoke_test.go` (per audit conclusions).

**Steps:**
1. For each file in the audit's MOVE-TO-RIMSKY-SERVICES list, identify the appropriate destination in `../rimsky-services/test/`. The natural layout mirrors rimsky's `test/scenarios/` and `test/smoke/`:
   - `test/scenarios/stores/fs_*` → `../rimsky-services/test/scenarios/stores/fs_*`.
   - `test/scenarios/atomic_staging/pg_verifier_*` → `../rimsky-services/test/scenarios/atomic_staging/pg_verifier_*` (only the pg_verifier_* subset; the four non-pg_verifier atomic-staging tests move to rimsky-docs in P4, not here).
   - `test/smoke/*` → `../rimsky-services/test/smoke/*` if categorized to move.
2. `mkdir -p` the destination directories as needed.
3. Move each file individually: `mv test/scenarios/stores/fs_cross_queue_concurrency_test.go ../rimsky-services/test/scenarios/stores/fs_cross_queue_concurrency_test.go` etc.
4. Update import paths in each moved file: rimsky-internal imports (e.g., `pkg:graph/scenario`) probably need to be replaced with equivalent SDK calls or moved-in-rimsky-services helpers. Read each test and update accordingly. If a test depends on `graph/scenario.Start` (an in-process rimsky harness that won't be reachable from rimsky-services), rewrite the test to bring up rimsky from the published image and drive it via public API (similar shape to Task 2).
5. Verify each moved test builds and runs in rimsky-services: `cd ../rimsky-services && go test ./test/...`.
6. Clean up empty directories in rimsky: `rmdir test/scenarios/stores 2>/dev/null || true` if empty; same for `test/scenarios/atomic_staging` (it won't be empty until P4 moves the four atomic-staging tests).

**Verification:** `cd ../rimsky-services && go test ./test/...` clean.

### Task 30: Set up rimsky-services CI workflow

**Files:**
- `../rimsky-services/.github/workflows/ci.yml` (refine from Task 25's initial version)
- `../rimsky-services/deploy/build-images.sh` (new)

**Steps:**
1. Refine `../rimsky-services/.github/workflows/ci.yml` to:
   - Trigger on push and pull_request.
   - Run `go build ./...`, `go test ./...`, and `golangci-lint run` (if a `.golangci.yml` is needed in rimsky-services, copy a minimal version from rimsky and adjust for the new module).
   - Build per-service Docker images via `./deploy/build-images.sh` (creating it in the next step).
   - Push images to a registry on tagged commits (placeholder — actual registry name is operator's choice per spec; the workflow includes a comment noting where to plug in the registry).
2. Create `../rimsky-services/deploy/build-images.sh` modeled after rimsky's `deploy/build-images.sh`. It builds Docker images for each bundled service: `docker build -f stores/filesystem/Dockerfile.filesystem -t rimsky-services-store-filesystem .`, repeated for each. Adjust paths for the new module layout.
3. Make the script executable: `chmod +x ../rimsky-services/deploy/build-images.sh`.
4. Run the script locally to verify all images build: `cd ../rimsky-services && ./deploy/build-images.sh`.

**Verification:** `cd ../rimsky-services && ./deploy/build-images.sh` exits 0; the workflow file is valid YAML (`yamllint` or visual inspection).

### Task 31: Append CHANGELOG entries — rimsky-services bootstrap; rimsky services-removed

**Files:**
- `../rimsky-services/CHANGELOG.md`
- `CHANGELOG.md` (rimsky)

**Steps:**
1. In `../rimsky-services/CHANGELOG.md` under `## Unreleased`, note:
   - Repo bootstrapped from rimsky's bundled-services trees per reorganization spec `2026-05-24-repo-reorganization-design`.
   - Contains production-side stores (filesystem, postgres), sensors (cron, http, object-store, webhook), subscribers (openlineage), and executors (claude-agent, http-node, verifier-http, verifier-shape-checks).
   - Depends on `github.com/fallguyconsulting/rimsky/sdk/go` for the implementer-facing surface.
2. In rimsky's `CHANGELOG.md` under `## Unreleased`, append the P3 entry:
   - Bundled production-side reference impls (stores/filesystem, stores/postgres, all sensors, subscribers/openlineage, executors/{claude-agent,http-node,verifier-http,verifier-shape-checks}) moved to `github.com/fallguyconsulting/rimsky-services`.
   - Test-infrastructure carve-outs (`stores/stub`, `executors/stub`, `stores/{filesystem,postgres}/testfixture`) remain in rimsky.
   - Operators upgrading past this point must pull bundled services as separate images from rimsky-services.

**Verification:** `grep -n '2026-05-24-repo-reorganization' ../rimsky-services/CHANGELOG.md CHANGELOG.md` returns hits in both files.

---

## Pass 6: P3c — Rimsky deploy/build updates + P3 design-doc mutations

**Goal:** Update rimsky's deploy/ to reference rimsky-services images, restrict rimsky's build-images.sh to rimsky-core images only, and apply the P3 portions of concept-doc mutations (claim-producer, publisher, sensor, executor, replica).
**Scope:** Tasks 32–36.
**End state:** working
**Verification:** `make lint && make test-all && (cd rimsky && docker compose -f deploy/docker-compose.yml up -d --wait)` cleanly reaches `/health` against rimsky-services images (the `up -d --wait` part is the implementer's verification; if `docker compose` isn't available in the sandbox, fall back to `make build-all && make test-all`).

### Task 32: Update rimsky `deploy/` to reference rimsky-services images

**Files:**
- `deploy/docker-compose.yml` (modify)
- `deploy/store-postgres.yml` (modify)
- `deploy/store-filesystem.yml` (modify)
- Any other `deploy/*.yml` referencing bundled services

**Steps:**
1. Read each YAML file in `deploy/`. For every `image: rimsky-...` or `build: ...` entry pointing at a bundled service (stores/filesystem, stores/postgres, sensors, subscribers/openlineage, executors), update to point at a published rimsky-services image. The image-name convention is `ghcr.io/fallguyconsulting/rimsky-services/<service>:<tag>` per spec P3.6 — placeholder tag `latest` for the plan; the user pins to a specific tag during deployment.
2. Verify the reference deployment still composes cleanly (`docker compose config -f deploy/docker-compose.yml` exits 0).
3. The reference deployment may still reference the in-tree `stores/stub` binary as a no-op fallback per `quickstart/store-stub.yml`. Leave those references unchanged.

**Verification:** `docker compose config -f deploy/docker-compose.yml` exits 0; `grep -n 'rimsky-services' deploy/docker-compose.yml deploy/store-*.yml` returns hits.

### Task 33: Restrict rimsky `deploy/build-images.sh` to rimsky-core images only

**Files:**
- `deploy/build-images.sh`

**Steps:**
1. Read `deploy/build-images.sh` end-to-end. Identify build steps for bundled services (stores, sensors, subscribers, executors).
2. Remove the bundled-service build steps. Keep build steps for rimsky-core (rimsky-control-api, rimsky-supervisor, rimsky-scheduler, rimsky-migrate, etc.).
3. Add a comment near the top noting that bundled-service images are built separately in `../rimsky-services/deploy/build-images.sh`.

**Verification:** `bash -n deploy/build-images.sh` (syntax check) passes; the script no longer references `stores/filesystem`, `stores/postgres`, `sensors/`, `subscribers/openlineage`, or production-side `executors/`.

### Task 34: Mutate `concept:claim-producer` (P3 portion) + finalize Notes append

**Files:**
- `.ok-planner/design/concepts/claim-producer.md`

**Steps:**
1. **"Boundaries" section:** Replace `The bundled SQL-based store stores/postgres/ additionally registers proto:executor.proto::Executor to support verification of its own staged content` with `The bundled SQL-based store pkg:github.com/fallguyconsulting/rimsky-services/stores/postgres additionally registers proto:executor.proto::Executor to support verification of its own staged content`.
2. **"Aliases and historical names" section:** Replace `the directory name (stores/)` with `the directory name (stores/ in pkg:github.com/fallguyconsulting/rimsky-services for production-side reference impls; pkg:stores/stub stays in rimsky as test infrastructure)`.
3. **Sweep for other in-tree store path references:** Scan the rest of `claim-producer.md` for `stores/filesystem/`, `stores/postgres/` references. Update each to `pkg:github.com/fallguyconsulting/rimsky-services/stores/filesystem/` and `pkg:github.com/fallguyconsulting/rimsky-services/stores/postgres/` respectively (notably the reference-implementation enumeration around line 50). Leave `stores/stub/` references rimsky-local.
4. **"Notes" section:** Append entry verbatim from spec line 524:
   `2026-05-24: production-side bundled claim-producer reference impls (stores/{filesystem,postgres}) moved out of rimsky to pkg:github.com/fallguyconsulting/rimsky-services. Test-infrastructure carve-outs (stores/stub for test-double + quickstart, stores/{filesystem,postgres}/testfixture as test-fixture packages) stay in rimsky. Boundary statement updated to reflect new home. Also: calling-side gRPC client path updated runtime/remote/ → runtime/peer/ per P2 rename. See spec 2026-05-24-repo-reorganization-design phases P2 and P3.`

**Verification:** `grep -n 'rimsky-services/stores/postgres' .ok-planner/design/concepts/claim-producer.md` returns ≥1 hit; `grep -n 'stores/postgres/' .ok-planner/design/concepts/claim-producer.md` returns no hits without the `rimsky-services/` prefix (except possibly in historical Notes entries — those stay as written).

### Task 35: Mutate `concept:publisher` (P3 portion), `concept:sensor`, `concept:executor`, `concept:replica`

**Files:**
- `.ok-planner/design/concepts/publisher.md`
- `.ok-planner/design/concepts/sensor.md`
- `.ok-planner/design/concepts/executor.md`
- `.ok-planner/design/concepts/replica.md`

**Steps:**

For `publisher.md` (P3 portion):
1. **Update bundled-sensor path reference at line 51:** Replace `pkg:sensors/sensor-*/` with `pkg:github.com/fallguyconsulting/rimsky-services/sensors/sensor-*/`. Sweep for any other in-tree sensor-bundled-impl references.
2. **"Notes" section:** Append verbatim from spec line 532: `2026-05-24: calling-side gRPC client path updated runtime/remote/ → runtime/peer/ per P2 rename; bundled-sensor path references retargeted to pkg:github.com/fallguyconsulting/rimsky-services/sensors/* per P3 move. See spec 2026-05-24-repo-reorganization-design.`

For `sensor.md`:
3. Replace `pkg:sensors/sensor-*/` (line 17 of current sensor.md) with `pkg:github.com/fallguyconsulting/rimsky-services/sensors/sensor-*/`. Sweep the file for any other `sensors/` path references and update to the new location.
4. **"Notes" section:** Append verbatim from spec line 539: `2026-05-24: bundled sensor reference impls moved to pkg:github.com/fallguyconsulting/rimsky-services. Path references updated. See spec 2026-05-24-repo-reorganization-design phase P3.`

For `executor.md`:
5. Read lines 17 and 27. Line 17 references `executors/claude-agent`; line 27 references `stores/postgres/`.
6. Replace `executors/claude-agent` (line 17) with `pkg:github.com/fallguyconsulting/rimsky-services/executors/claude-agent`.
7. Replace `stores/postgres/` (line 27) references with `pkg:github.com/fallguyconsulting/rimsky-services/stores/postgres`.
8. If the doc references `pkg:executors/http-node`, `pkg:executors/verifier-http`, or `pkg:executors/verifier-shape-checks`, update to their `pkg:github.com/fallguyconsulting/rimsky-services/executors/...` locations.
9. **Preserve test-infrastructure references unchanged:** `pkg:executors/stub` stays in rimsky; references to its role (test double, conformance target, stubtest in-process wrapper) keep their current paths.
10. **"Notes" section:** Append verbatim from spec line 547: `2026-05-24: production-side bundled executor reference impls (claude-agent, http-node, verifier-http, verifier-shape-checks) moved to pkg:github.com/fallguyconsulting/rimsky-services/executors/. executors/stub stays in rimsky as test infrastructure. Cross-reference to stores/postgres also retargeted. See spec 2026-05-24-repo-reorganization-design phase P3.`

For `replica.md`:
11. Read lines 31, 32, 33. Each references in-tree paths (sensors, executors, stores).
12. **Update path references** (production-side only; `pkg:stores/stub` and `pkg:executors/stub` references, if present, stay rimsky-local):
    - Replace `pkg:sensors/sensor-*/` with `pkg:github.com/fallguyconsulting/rimsky-services/sensors/sensor-*/`.
    - Replace `pkg:executors/*` (production-side: claude-agent, http-node, verifier-http, verifier-shape-checks) with `pkg:github.com/fallguyconsulting/rimsky-services/executors/*`.
    - Replace `pkg:stores/*` (production-side: filesystem, postgres) with `pkg:github.com/fallguyconsulting/rimsky-services/stores/*`.
13. **"Notes" section:** Append verbatim from spec line 562: `2026-05-24: path references retargeted from in-tree bundled-impl locations to pkg:github.com/fallguyconsulting/rimsky-services. See spec 2026-05-24-repo-reorganization-design phase P3.`

**Verification:** For each of the four concept docs, `grep -n '2026-05-24' <file>` returns a hit at the appended Notes entry; `grep -n 'rimsky-services' <file>` returns the expected number of post-mutation references.

### Task 36: Regenerate `concepts.md` TOC reflecting all mutated concepts

**Files:**
- `.ok-planner/design/concepts.md`

**Steps:**
1. The `concepts.md` file is auto-generated per its own header comment: "Read first... Generated by `discover-design` and refreshed by `execute-plan` when a plan touches `concepts/`. Do not edit by hand — changes will be overwritten."
2. Each concept's one-sentence definition in `concepts.md` should reflect any boundary-relevant changes from the mutations. Specifically: `module-layout` (now four modules + carve-outs), `claim-producer` (production-side at rimsky-services), `sensor`, `executor`, `replica`, `conformance` (lib + thin CLIs), and the new `sdk`.
3. Update each affected concept's TOC line to match the new boundary/definition.
4. Verify the TOC stays sorted alphabetically by concept slug.

**Verification:** Active-concept TOC entry count matches the number of active concept files; retired-concept TOC entry count matches the number of files under `_retired/`. The TOC has two sections (`## Concepts` and `## Retired concepts`); confirm both halves line up against `.ok-planner/design/concepts/*.md` and `.ok-planner/design/concepts/_retired/*.md` respectively. Implementer chooses the exact tooling (awk on section headers, two grep passes, manual inspection).

---

## Pass 7: P4 — Docs migration to `../rimsky-docs`

**Goal:** Move `docs/`, the docs-lint binaries, `docs/.vocabulary-lint.yml`, the atomic-staging example (and its four scenario tests), and set up the `env:RIMSKY_REPO` convention plus pre-release reconciliation gate.
**Scope:** Tasks 37–42.
**End state:** working
**Verification:** `cd ../rimsky-docs/cmd && go build ./... && go test ./...` clean; `cd ../rimsky-docs/examples && go build ./... && go test ./...` clean; rimsky's `make lint && make test-all` clean.

### Task 37: Move `docs/` and `docs/.vocabulary-lint.yml` to `../rimsky-docs`

**Files:**
- Source: `docs/` (49 markdown files + `.vocabulary-lint.yml`)
- Destination: `../rimsky-docs/docs/` + `../rimsky-docs/.vocabulary-lint.yml`

**Steps:**
1. `mkdir -p ../rimsky-docs/docs`.
2. `mv docs ../rimsky-docs/docs` (or copy + delete if mv refuses across filesystems). Treat `../rimsky-docs/docs/.vocabulary-lint.yml` as the per-spec moved location.
3. Verify all 49 markdown files moved: `find ../rimsky-docs/docs -name '*.md' | wc -l` returns 49.

**Verification:** `test -d ../rimsky-docs/docs` and `test -f ../rimsky-docs/docs/.vocabulary-lint.yml` (or wherever the .vocabulary-lint.yml ended up — spec layout puts it at `../rimsky-docs/.vocabulary-lint.yml` repo root; pick one location consistently).

### Task 38: Move docs-lint Go binaries to `../rimsky-docs/cmd/`

**Files:**
- Source: `cmd/rimsky-docs-lint/`, `cmd/rimsky-docs-llms-full/`, `cmd/rimsky-docs-glossary/`
- Destination: `../rimsky-docs/cmd/rimsky-docs-lint/`, `../rimsky-docs/cmd/rimsky-docs-llms-full/`, `../rimsky-docs/cmd/rimsky-docs-glossary/`
- New: `../rimsky-docs/cmd/go.mod`

**Steps:**
1. `mkdir -p ../rimsky-docs/cmd`.
2. `mv cmd/rimsky-docs-lint ../rimsky-docs/cmd/`. Same for the other two.
3. Create `../rimsky-docs/cmd/go.mod` declaring `module github.com/fallguyconsulting/rimsky-docs/cmd` (Go module path TBD by the operator; this is a placeholder). Dependencies: stdlib + yaml package (probably `gopkg.in/yaml.v3`).
4. Update import paths in the moved binaries: any rimsky-internal imports (e.g., `github.com/fallguyconsulting/rimsky/docs`) need handling. The docs-lint tools read rimsky source from `env:RIMSKY_REPO` at runtime, not via compile-time imports. Verify by reading each binary's main.go; if any compile-time imports remain, replace with runtime file reads anchored at `env:RIMSKY_REPO`.
5. Verify the binaries build in their new location: `cd ../rimsky-docs/cmd && go build ./...`.

**Verification:** `cd ../rimsky-docs/cmd && go build ./...` clean.

### Task 39: Move `examples/atomic-staging-fs-producer` and four scenario tests to `../rimsky-docs/examples/`

**Files:**
- Source: `examples/atomic-staging-fs-producer/` + 4 scenario tests
- Destination: `../rimsky-docs/examples/atomic-staging-fs-producer/` + `../rimsky-docs/examples/atomic-staging-fs-producer/scenarios/`
- New: `../rimsky-docs/examples/go.mod`

**Steps:**
1. `mkdir -p ../rimsky-docs/examples`.
2. `mv examples/atomic-staging-fs-producer ../rimsky-docs/examples/atomic-staging-fs-producer`.
3. Move the four scenario tests (from spec lines 341–344):
   - `mv test/scenarios/atomic_staging/abandon_on_any_failure_test.go ../rimsky-docs/examples/atomic-staging-fs-producer/scenarios/abandon_on_any_failure_test.go`
   - `mv test/scenarios/atomic_staging/commit_on_all_success_test.go ../rimsky-docs/examples/atomic-staging-fs-producer/scenarios/commit_on_all_success_test.go`
   - `mv test/scenarios/atomic_staging/concurrent_staging_test.go ../rimsky-docs/examples/atomic-staging-fs-producer/scenarios/concurrent_staging_test.go`
   - `mv test/scenarios/atomic_staging/sub_stage_verifier_failure_test.go ../rimsky-docs/examples/atomic-staging-fs-producer/scenarios/sub_stage_verifier_failure_test.go`
4. Create `../rimsky-docs/examples/go.mod` declaring `module github.com/fallguyconsulting/rimsky-docs/examples` with `require github.com/fallguyconsulting/rimsky/sdk/go vX.Y.Z`.
5. Update import paths in moved files:
   - `github.com/fallguyconsulting/rimsky/examples/atomic-staging-fs-producer/...` → `github.com/fallguyconsulting/rimsky-docs/examples/atomic-staging-fs-producer/...`.
   - Imports of `github.com/fallguyconsulting/rimsky/protocols/...` and `github.com/fallguyconsulting/rimsky/sdk/go/...` stay unchanged (cross-repo deps via go.mod).
   - The four scenario tests previously used `graph/scenario.Start` — rewrite them to drive rimsky from a published image (same pattern as Task 2 and Task 29) if they need to be runnable in the new repo; OR if they can be unit tests of the example's logic without needing rimsky's harness, rewrite as unit tests.
6. Verify the example builds: `cd ../rimsky-docs/examples && go build ./... && go test ./...`.
7. Clean up empty rimsky directories: `rmdir test/scenarios/atomic_staging 2>/dev/null || true` (it should be empty now — pg_verifier_* moved to rimsky-services in Task 29, atomic-staging examples moved here); `rmdir examples 2>/dev/null || true`.

**Verification:** `cd ../rimsky-docs/examples && go build ./... && go test ./...` clean; `test ! -d examples` (in rimsky) and `test ! -d test/scenarios/atomic_staging` (in rimsky).

### Task 40: Handle root-level `llms.txt` and `llms-full.txt`

**Files:**
- `llms.txt` (verify origin)
- `llms-full.txt` (verify origin)

**Steps:**
1. Read `llms.txt` and `llms-full.txt` at rimsky repo root. Determine whether they are generated (by `cmd:rimsky-docs-llms-full`) or hand-written.
2. Also check whether `docs/agents/llms-full.txt` (the generated location per `cmd/rimsky-docs-llms-full/main.go`) exists or moved with the docs in Task 37.
3. If the root files are generated from docs content, move them to `../rimsky-docs/` and update `../rimsky-docs/cmd/rimsky-docs-llms-full/main.go` to write to the new location.
4. If the root files are hand-written or generated by other tooling, leave them in rimsky.
5. Document the resolution in the rimsky `CHANGELOG.md` `## Unreleased` section.

**Verification:** Either the root files are gone from rimsky (moved with docs), or they're documented to stay; no ambiguity.

### Task 41: Set up `env:RIMSKY_REPO` convention and pre-release reconciliation gate

**Files:**
- `../rimsky-docs/cmd/rimsky-docs-lint/main.go` (modify)
- `../rimsky-docs/cmd/rimsky-docs-llms-full/main.go` (modify)
- `../rimsky-docs/cmd/rimsky-docs-glossary/main.go` (modify)
- `../rimsky-docs/README.md` (document the convention)
- rimsky-side: a release script that invokes docs-lint as a gate (location: `scripts/release.sh` or wherever rimsky's release process lives; if no release script exists yet, create a stub at `scripts/release.sh`)

**Steps:**
1. In each of the three docs-lint binaries' `main.go`:
   - Read `env:RIMSKY_REPO` at startup.
   - If unset, log a clear error and exit non-zero with a helpful message ("set RIMSKY_REPO to a local rimsky checkout path").
   - Anchor file reads at `env:RIMSKY_REPO` (e.g., concept catalog at `${RIMSKY_REPO}/.ok-planner/design/concepts/`, source annotations under `${RIMSKY_REPO}/`).
2. Document the convention in `../rimsky-docs/README.md`.
3. Create or modify rimsky's release script at `scripts/release.sh` (or its actual location) to:
   - Accept a `--skip-docs-reconciliation` flag.
   - Default: invoke `cd ../rimsky-docs/cmd && RIMSKY_REPO=$(pwd) go run ./rimsky-docs-lint/...` (or equivalent). If lint reports drift, the release blocks until docs are reconciled (manual user step).
   - With `--skip-docs-reconciliation`: skip the gate (for emergency releases).
4. Verify the docs-lint binary runs against the rimsky checkout: `RIMSKY_REPO=/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky go run ../rimsky-docs/cmd/rimsky-docs-lint/...`.

**Verification:** `RIMSKY_REPO=$(pwd) go run ../rimsky-docs/cmd/rimsky-docs-lint/...` from rimsky root exits 0 (or reports legitimate drift to be addressed); rimsky's release script blocks if docs-lint reports drift.

### Task 42: CHANGELOG entries — rimsky-docs bootstrap; rimsky docs-removed

**Files:**
- `../rimsky-docs/CHANGELOG.md`
- `CHANGELOG.md` (rimsky)

**Steps:**
1. In `../rimsky-docs/CHANGELOG.md` under `## Unreleased`, note:
   - Repo bootstrapped per reorganization spec.
   - Contains 49 markdown docs, three lint binaries (docs-lint, docs-llms-full, docs-glossary), and the atomic-staging-fs-producer example with four scenario tests.
   - `env:RIMSKY_REPO` convention documented in README.
2. In rimsky's `CHANGELOG.md` under `## Unreleased`, append the P4 entry:
   - `docs/` moved to `github.com/fallguyconsulting/rimsky-docs`.
   - `examples/atomic-staging-fs-producer/` moved to `rimsky-docs/examples/` with its four scenario tests.
   - `cmd/rimsky-docs-{lint,llms-full,glossary}` moved to rimsky-docs.
   - Pre-release reconciliation gate added to rimsky's release script.

**Verification:** Both CHANGELOG files updated; `grep -n 'rimsky-docs' ../rimsky-docs/CHANGELOG.md CHANGELOG.md` returns hits in both.

---

## Pass 8: P5 — Crimefinder migration to `../crimefinder`

**Goal:** Move `apps/crimefinder/` to `../crimefinder` and remove the now-empty `apps/` directory from rimsky.
**Scope:** Tasks 43–45.
**End state:** working
**Verification:** rimsky's `make lint && make test-all` clean; `cd ../crimefinder && npm install && npm test && npm run build` clean.

### Task 43: Move `apps/crimefinder/` to `../crimefinder`

**Files:**
- Source: `apps/crimefinder/` (257 TS files + CHANGELOG, CLAUDE.md, feature-index.md, README, cold-read/, deploy/)
- Destination: `../crimefinder/`

**Steps:**
1. Verify `../crimefinder` exists and is empty or contains only initial scaffolding (the user pre-created it).
2. Copy the contents of `apps/crimefinder/` (excluding `node_modules/` if present) to `../crimefinder/`. Use `rsync -av --exclude=node_modules apps/crimefinder/ ../crimefinder/` or `cp -r` followed by `rm -rf ../crimefinder/node_modules`.
3. Delete `apps/crimefinder/` from rimsky: `rm -rf apps/crimefinder`.
4. Delete the now-empty `apps/` directory: `rmdir apps`.
5. Verify the move: `cd ../crimefinder && ls` shows the project layout (cli/, executor/, producer/, shared/, test/, templates/, deploy/, cold-read/, package.json, etc.).

**Verification:** `cd ../crimefinder && npm install && npm test && npm run build` clean; `test ! -d apps` (in rimsky).

### Task 44: Verify crimefinder's deploy fragments reference rimsky-as-image (not in-tree paths)

**Files:**
- `../crimefinder/deploy/docker-compose.fragment.yml`
- `../crimefinder/deploy/rimsky.yml.fragment`

**Steps:**
1. Read `../crimefinder/deploy/docker-compose.fragment.yml`. Confirm it references `image: crimefinder/producer:latest` (or similar — the image must be published from crimefinder's CI; pin to a tag if the operator has set one).
2. Read `../crimefinder/deploy/rimsky.yml.fragment`. Confirm any rimsky references use image tags (e.g., `image: ghcr.io/fallguyconsulting/rimsky:vX.Y.Z`) rather than relative paths into rimsky's source tree.
3. If any references need adjustment to match the post-split image conventions, update them.

**Verification:** `grep -n 'image:' ../crimefinder/deploy/*.yml` returns image references; no `build: ../rimsky/...` or similar source-tree references.

### Task 45: CHANGELOG entries — crimefinder; rimsky crimefinder-removed

**Files:**
- `../crimefinder/CHANGELOG.md`
- `CHANGELOG.md` (rimsky)

**Steps:**
1. In `../crimefinder/CHANGELOG.md`, the project's CHANGELOG already exists; append to `## Unreleased`:
   - Repo bootstrapped from rimsky's `apps/crimefinder/` per reorganization spec.
2. In rimsky's `CHANGELOG.md` under `## Unreleased`, append:
   - `apps/crimefinder/` moved to `github.com/fallguyconsulting/crimefinder`.
   - The `apps/` directory in rimsky is removed.
   - Drift signal previously provided by crimefinder's e2e tests is replaced by in-tree canary scenarios at `test/scenarios/canary/` (added in Pass 3).

**Verification:** Both CHANGELOG files updated; `grep -n 'crimefinder' CHANGELOG.md ../crimefinder/CHANGELOG.md` returns hits in both.

---

## Pass 9: P6 — Dashboard migration to `../rimsky-dashboard`

**Goal:** Move `dashboards/rimsky-dashboard/` to `../rimsky-dashboard` and remove the now-empty `dashboards/` directory from rimsky.
**Scope:** Tasks 46–48.
**End state:** working
**Verification:** rimsky's `make lint && make test-all` clean; `cd ../rimsky-dashboard && npm install && npm test && npm run build` clean.

### Task 46: Move `dashboards/rimsky-dashboard/` to `../rimsky-dashboard`

**Files:**
- Source: `dashboards/rimsky-dashboard/` (TS web app — Vite + Tailwind + postcss)
- Destination: `../rimsky-dashboard/`

**Steps:**
1. Verify `../rimsky-dashboard` exists and is empty or contains only initial scaffolding.
2. Copy contents excluding `node_modules` and `dist`: `rsync -av --exclude=node_modules --exclude=dist dashboards/rimsky-dashboard/ ../rimsky-dashboard/`.
3. Delete `dashboards/rimsky-dashboard/` from rimsky: `rm -rf dashboards/rimsky-dashboard`.
4. Delete the now-empty `dashboards/` directory: `rmdir dashboards`.
5. Verify the move: `cd ../rimsky-dashboard && ls` shows index.html, src/, package.json, tailwind.config.js, postcss.config.js, Dockerfile, tests/, etc.

**Verification:** `cd ../rimsky-dashboard && npm install && npm test && npm run build` clean; `test ! -d dashboards` (in rimsky).

### Task 47: Update rimsky's reference deployment to reference rimsky-dashboard image

**Files:**
- `deploy/docker-compose.yml` or whichever compose file references the dashboard

**Steps:**
1. Find references to the in-tree dashboard build: `grep -rn 'rimsky-dashboard\|dashboards/' deploy/`.
2. Replace `build: ./dashboards/rimsky-dashboard` (or similar) with `image: ghcr.io/fallguyconsulting/rimsky-dashboard:<tag>` (or matching the post-split image convention; placeholder tag `latest`).

**Verification:** `grep -n 'dashboards/' deploy/*.yml` returns no matches (or only references to the published image).

### Task 48: CHANGELOG entries — dashboard; rimsky dashboard-removed

**Files:**
- `../rimsky-dashboard/CHANGELOG.md` (create if missing)
- `CHANGELOG.md` (rimsky)

**Steps:**
1. In `../rimsky-dashboard/CHANGELOG.md` under `## Unreleased`, note:
   - Repo bootstrapped from rimsky's `dashboards/rimsky-dashboard/` per reorganization spec.
2. In rimsky's `CHANGELOG.md` under `## Unreleased`, append:
   - `dashboards/rimsky-dashboard/` moved to `github.com/fallguyconsulting/rimsky-dashboard`.
   - The `dashboards/` directory in rimsky is removed.
   - Reference deployment now pulls the dashboard from `ghcr.io/fallguyconsulting/rimsky-dashboard:<tag>`.

**Verification:** Both CHANGELOG files updated; `grep -n 'rimsky-dashboard' CHANGELOG.md ../rimsky-dashboard/CHANGELOG.md` returns hits in both.

---

## Manual checks after completion

These items cannot be automated and require human verification after the plan finishes:

- **Image publishing & tagging:** confirm that the four downstream repos' CI workflows correctly publish Docker images to the chosen registries (`ghcr.io/fallguyconsulting/rimsky-services/<service>`, `ghcr.io/fallguyconsulting/crimefinder/producer`, `ghcr.io/fallguyconsulting/rimsky-dashboard`). The plan creates the workflow shapes but does not publish first images.
- **Reference deployment smoke test:** after the user pulls rimsky-services / rimsky-dashboard images, run `docker compose -f deploy/docker-compose.yml up -d --wait` and visually confirm `/health` endpoints respond and the dashboard renders.
- **Crimefinder end-to-end pass:** run a code-review pass via crimefinder against a real repo and verify it completes successfully against the rimsky image.
- **Docs-lint reconciliation:** after the docs move, run the docs-lint binaries against rimsky and confirm any reported drift is real (and reconcile via PR to rimsky-docs).
- **Pre-release gate dry-run:** invoke the rimsky release script (without actually tagging) to confirm the docs-reconciliation gate fires correctly. Then run with `--skip-docs-reconciliation` to confirm bypass works.
- **Per-developer `go.work` documentation:** confirm the team has a documented convention for cross-repo local dev (per-developer `go.work` listing both rimsky and rimsky-services module paths, not committed to either repo).
