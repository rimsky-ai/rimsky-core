# Rimsky Go Port — Plan C — Production Readiness

**Goal:** Deliver the `external-sql` reference resource implementation (commits to a consumer-owned SQL table via staging + atomic-swap), the full deployment toolkit (per-component Dockerfiles, a docker-compose reference, a Kubernetes Helm chart), and the full protocol conformance test suite (`rimsky-conformance`) that runs green against both v1 reference executors in stub mode. At the end of Plan C, rimsky is deployable at scale: `docker compose up -d` brings up a working stack in under 60 seconds; a Helm chart installs a working deployment on any Kubernetes cluster; and anyone writing a new executor can self-verify against a documented conformance suite.

**End state after this plan:** a developer can `docker compose -f deploy/docker-compose.yml up -d` and have a complete rimsky deployment running on a laptop. A Kubernetes operator can `helm install rimsky deploy/kubernetes/rimsky-chart` and get a functioning deployment. `rimsky-conformance --endpoint <url> --transport grpc|http --require-stub-mode` exits green against `http-node` and `claude-agent`. A template using `external-sql` for a consumer-owned table commits with quality-rule enforcement and rolls back correctly via `RestoreVersion("previous")`.

**Architecture:**
- `external-sql` resource uses staging-table + atomic-swap pattern per spec §8.4. Each committed version writes to a staging table; commit is an atomic swap (`current` → `previous`, `staging` → `current`). Rollback (`RestoreVersion("previous")`) swaps `previous` ↔ `current`.
- Docker images built per component: `rimsky/scheduler`, `rimsky/supervisor`, `rimsky/control-api`, `rimsky/executor-http-node`, `rimsky/executor-claude-agent`.
- Docker Compose reference wires one Postgres + all four rimsky components + both executors.
- Helm chart templates the same topology for Kubernetes, with StatefulSet for Postgres (or external), Deployments for the stateless components, and optional Ingress for the control API.
- Conformance suite is a Go binary with ~15 scenario tests it runs against any executor endpoint, covering every protocol shape (Execute happy path, Heartbeat, all terminal events, stream-close semantics, async handoff, malformed requests, cancel handling).

**Tech stack:** Go 1.22+ (external-sql, conformance suite; same module), Docker 24+, `docker compose` v2+, Helm 3+, Postgres 14+.

**Reference documents:**
- Spec: `docs/specs/2026-04-23-rimsky-go-port-design.md` (especially §8.4 external-sql, §14.4 conformance suite, §4 repo layout, §20 success criteria)
- Plan A (complete): `docs/plans/2026-04-23-rimsky-go-port-plan-a-foundation.md`
- Plan B (complete): `docs/plans/2026-04-23-rimsky-go-port-plan-b-executors.md`

---

## Phase 0 — Amendments to Plan A

**Outcome:** Two amendments. Plan A's `ResourceDataStore` row type needs `data_ref` reshaped for structured external-backed references, and Plan A's `SupervisorConfig.SQLConnections` type needs to evolve from string URLs to live pool handles.

### Task 0.1 — Reshape `data_ref` for structured external references

**Files edited (explicit enumeration):**
- `rimsky-go/core/storage/interfaces.go` — change `ResourceVersionRow.DataRef` from `string` to `[]byte`
- `rimsky-go/core/storage/postgres/resource_data.go` — update read/delete paths; inline-jsonb impl unchanged semantically (it leaves `data_ref` empty bytes)
- `rimsky-go/core/resource/interface.go` — change `Version.DataRef` from `string` to `[]byte`
- `rimsky-go/core/resource/inlinejsonb/resource.go` — update any `DataRef` usage (should be none; field unused by inline-jsonb, but verify)
- `rimsky-go/core/storage/postgres/postgres_test.go` — update integration tests that assert on the old `string` type
- NEW: `rimsky-go/core/migrations/002-data-ref-jsonb.sql` — migration to reshape column

**Steps:**

1. Add `core/migrations/002-data-ref-jsonb.sql`:
   ```sql
   ALTER TABLE rimsky_resource_versions
     ALTER COLUMN data_ref TYPE JSONB USING CASE
       WHEN data_ref IS NULL THEN NULL
       ELSE jsonb_build_object('legacy', data_ref)
     END;
   ```
2. Apply the type changes in each file enumerated above. Inline-jsonb leaves `data_ref` nil; external-sql (Task 1.2 below) sets it to a JSON-encoded byte slice matching `{"schema": ..., "table": ..., "row_count": N}`.
3. Verify no Plan B code touches `DataRef` (grep `rimsky-go/executors/` for `DataRef`). Plan B's `http-node` and `claude-agent` executors don't interact with `ResourceVersionRow`, so no Plan B edits expected.
4. Update Plan A's integration tests to reflect the new column type.

**Verification:** `go test ./core/storage/postgres/... ./core/resource/inlinejsonb/...` passes; migration 002 applies cleanly on a DB that already has 001.

---

### Task 0.2 — Evolve `SupervisorConfig.SQLConnections` from URLs to pool handles

**Files edited:**
- `rimsky-go/core/config/supervisor.go` — change `SQLConnections` type
- `rimsky-go/core/cmd/rimsky-supervisor/main.go` — update binary wiring to open pools before passing
- `rimsky-go/core/resource/externalsql/factory.go` — factory accepts `map[string]*pgxpool.Pool`

**Steps:**

1. Plan A Task 10.6 declared `SupervisorConfig.SQLConnections map[string]string` (URL map). External-sql (Task 1.1 below) needs live pool handles. Resolve by making the library API take pools and the reference binary open them.
2. Change `SupervisorConfig.SQLConnections` from `map[string]string` to `map[string]*pgxpool.Pool`. Library consumers pass pools.
3. In `cmd/rimsky-supervisor/main.go`, read the supervisor YAML config's `sql_connections` block (name → URL), open one `pgxpool.Pool` per entry with modest pool sizing (max 10 connections), register teardown for graceful shutdown, pass the resulting map to `StartSupervisor`.
4. `externalsql.Factory.Connections` is typed `map[string]*pgxpool.Pool`; factory does `pools[cfg["connection_ref"]]` lookup.

**Verification:** `go build ./...` exits 0; supervisor reference binary starts with a sample config that declares one connection and a test template referencing it.

---

## Phase 1 — `external-sql` resource

### Task 1.1 — Factory + config schema

**Files:**
- `rimsky-go/core/resource/externalsql/factory.go`
- `rimsky-go/core/resource/externalsql/config.go`

**Steps:**

1. Implement `resource.Factory`:
   ```go
   package externalsql

   var configSchema = []byte(`{
     "type": "object",
     "required": ["connection_ref", "schema", "table", "primary_key"],
     "properties": {
       "connection_ref":  { "type": "string" },
       "schema":          { "type": "string" },
       "table":           { "type": "string" },
       "staging_table":   { "type": "string" },
       "previous_table":  { "type": "string" },
       "primary_key":     { "type": "array", "items": { "type": "string" }, "minItems": 1 }
     },
     "additionalProperties": false
   }`)

   type Factory struct {
       // Connections is the named-connection map from supervisor config
       // (SQLConnections). At Create time, the factory looks up the
       // connection_ref and constructs a pool.
       Connections map[string]*pgxpool.Pool
   }
   func (f Factory) ConfigSchema() []byte { return configSchema }
   func (f Factory) Create(cfg resource.Config, rules []qualityrule.Spec, reg resource.Registry) (resource.Resource, error) { ... }
   ```
2. The factory is created by the consumer's `main()` (or by rimsky's reference `main` binaries when they wire it in). It does not self-register; the Plan C Docker images' entrypoints register it explicitly.
3. Config validation at Create: schema + table exist in the database (probe with a `SELECT 1 FROM <schema>.<table> LIMIT 0` check); primary_key columns exist.

**Verification:** `go build ./core/resource/externalsql/...` exits 0.

---

### Task 1.2 — Resource implementation

**Files:**
- `rimsky-go/core/resource/externalsql/resource.go`
- `rimsky-go/core/resource/externalsql/resource_test.go`

**Steps:**

1. Implement `resource.Resource`:
   ```go
   type sqlResource struct {
       path          []string
       ownerNodeID   shared.UUID
       pool          *pgxpool.Pool
       schema, table, staging, previous string
       pkCols        []string
       rules         []qualityrule.Spec
       reg           resource.Registry
   }
   ```
2. `Commit(ctx, req)` flow:
   a. Validate that `req.Result` is a `[]map[string]any` or a `[]SQL-row-shaped-struct`.
   b. Run quality rules against `req.Result` (load previous data from `previous` table if the rules need it).
   c. On error-severity failure → return `{Accepted: false, QualityErrors: ...}`.
   d. Open transaction on `pool`.
   e. `TRUNCATE <schema>.<staging>;`
   f. Bulk insert `req.Result` into staging.
   g. Atomic swap:
      ```sql
      ALTER TABLE <schema>.<table>    RENAME TO __rimsky_tmp_swap;
      ALTER TABLE <schema>.<staging>  RENAME TO <table>;
      DROP TABLE IF EXISTS <schema>.<previous>;
      ALTER TABLE __rimsky_tmp_swap   RENAME TO <previous>;
      CREATE TABLE <schema>.<staging> (LIKE <schema>.<table> INCLUDING ALL);
      ```
      (Exact DDL verified against Postgres 14 behavior in tests.)
   h. Insert new row into `rimsky_resource_versions` with `data: NULL`, `data_ref: {"schema": <>, "table": <table>, "row_count": N}` as JSONB, `change_summary`, etc.
   i. Update pointers via `reg.SetCurrentVersion`.
   j. Commit transaction. Log `commit` event (supervisor does this; resource returns the new version).
3. `RestoreVersion("previous")`:
   ```sql
   ALTER TABLE <schema>.<table>    RENAME TO __rimsky_tmp_swap;
   ALTER TABLE <schema>.<previous> RENAME TO <table>;
   ALTER TABLE __rimsky_tmp_swap   RENAME TO <previous>;
   ```
   Swap version pointers. Return the previously-current version as the new current.
4. `RestoreVersion("id", id)`:
   - If `id` is the current → no-op; return current.
   - If `id` is the previous → same as `"previous"`.
   - Otherwise → `resource.ErrRollbackUnsupported` (external-sql only keeps current + previous; older versions are physically dropped).
5. `NoOpCommit`: log `no_op_commit` event (via supervisor-injected event writer — actually the supervisor writes the event; the resource returns success).
6. `ListVersions(limit)`: read from `rimsky_resource_versions` filtered by `resource_id`.

Tests (use testcontainers Postgres):
- Commit happy path: row count matches.
- Commit with quality-rule rejection: staging truncated on rollback; current unchanged.
- Rollback via `"previous"`: current ↔ previous swap succeeds; subsequent rollback swaps back.
- Rollback to an arbitrary version ID fails with `ErrRollbackUnsupported`.
- Two concurrent commits serialize correctly (second waits for first's transaction).
- The swap atomicity: mid-swap failure leaves the schema in a consistent state (tests force a failure between the ALTER steps by injecting a failpoint or mock pool).

**Verification:** `go test ./core/resource/externalsql/...` passes.

---

### Task 1.3 — Scenario test: full template using `external-sql`

**Files:** `rimsky-go/test/scenarios/external_sql_rollback_test.go`

**Steps:**

1. In scenario harness: create a consumer schema + a target table + a staging table + a previous table (all empty).
2. Deploy a template with a node `executor: http-node`, `userdata: {url: <fake upstream returning JSON rows>}`, `owns_resources: [{path: ["demo", "rows"], implementation: "external-sql", config: {connection_ref: "test_conn", schema: "public", table: "demo_rows", staging_table: "demo_rows_staging", previous_table: "demo_rows_previous", primary_key: ["id"]}}]`.
3. Run the node; verify rows land in `demo_rows`.
4. Run the node again with a different upstream response; verify new rows replace old; old version is in `demo_rows_previous`.
5. Invoke operator rollback (`POST /nodes/<id>/invalidate` with `restore_version: "previous"`); verify current ↔ previous swap happened.

**Verification:** test passes.

---

## Phase 2 — Docker images

### Task 2.1 — Base `Dockerfile` for Go binaries

**Files:** `rimsky-go/deploy/Dockerfile.go-base`

**Steps:**

1. Multi-stage build:
   ```dockerfile
   # syntax=docker/dockerfile:1.7
   FROM golang:1.22-alpine AS builder
   WORKDIR /build
   COPY go.mod go.sum ./
   RUN go mod download
   COPY . .
   ARG BINARY
   RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./core/cmd/${BINARY}

   FROM gcr.io/distroless/static:nonroot
   COPY --from=builder /out/app /app
   USER nonroot
   ENTRYPOINT ["/app"]
   ```
2. Usage: `docker build -f deploy/Dockerfile.go-base --build-arg BINARY=rimsky-scheduler -t rimsky/scheduler:0.1 .`

**Verification:** build each of `rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`, `rimsky-migrate` against this base and confirm each image is <30 MB.

---

### Task 2.2 — `http-node` Dockerfile

**Files:** `rimsky-go/deploy/Dockerfile.http-node`

**Steps:**

1. Same multi-stage pattern; `BINARY=http-node` points at `./executors/http-node`.

**Verification:** `docker build -f deploy/Dockerfile.http-node -t rimsky/executor-http-node:0.1 .` succeeds.

---

### Task 2.3 — `claude-agent` Dockerfile

**Files:** `rimsky-go/deploy/Dockerfile.claude-agent`

**Steps:**

1. Multi-stage Node build:
   ```dockerfile
   FROM node:20-alpine AS builder
   WORKDIR /build
   COPY executors/claude-agent/package.json executors/claude-agent/package-lock.json ./
   RUN npm ci
   COPY executors/claude-agent/ ./
   RUN npm run build
   COPY proto/v1/ /build/proto/v1/

   FROM node:20-alpine
   RUN apk add --no-cache tini
   WORKDIR /app
   COPY --from=builder /build/dist ./dist
   COPY --from=builder /build/node_modules ./node_modules
   COPY --from=builder /build/proto ./proto
   ENV NODE_ENV=production
   USER node
   ENTRYPOINT ["/sbin/tini", "--", "node", "dist/main.js"]
   ```
2. Tini wrapper because claude-agent spawns subprocesses (Claude CLI) — proper signal handling matters.

**Verification:** `docker build -f deploy/Dockerfile.claude-agent -t rimsky/executor-claude-agent:0.1 .` succeeds; the image size is reasonable (<300 MB).

---

### Task 2.4 — Build-all script + versioning

**Files:**
- `rimsky-go/deploy/build-images.sh`

**Steps:**

1. Bash script that builds all six images with a consistent `RIMSKY_VERSION` tag (reads from `rimsky-go/CHANGELOG.md` or an env var).
2. Tags each as `rimsky/<component>:<version>` and `rimsky/<component>:latest`.

**Verification:** script runs to completion on a clean checkout.

---

## Phase 3 — Docker Compose reference

### Task 3.1 — `deploy/docker-compose.yml`

**Files:**
- `rimsky-go/deploy/docker-compose.yml`
- `rimsky-go/deploy/supervisor-config.yml` (mounted into the supervisor container)

**Steps:**

1. Compose services:
   ```yaml
   services:
     postgres:
       image: postgres:14-alpine
       environment:
         POSTGRES_USER: rimsky
         POSTGRES_PASSWORD: rimsky
         POSTGRES_DB: rimsky
       ports: ["5432:5432"]
       healthcheck:
         test: ["CMD-SHELL", "pg_isready -U rimsky"]
         interval: 5s
         retries: 10
       volumes:
         - rimsky_pg:/var/lib/postgresql/data

     rimsky-migrate:
       image: rimsky/migrate:0.1
       environment:
         RIMSKY_DB_URL: postgres://rimsky:rimsky@postgres:5432/rimsky
       depends_on: { postgres: { condition: service_healthy } }
       restart: "no"

     rimsky-scheduler:
       image: rimsky/scheduler:0.1
       environment:
         RIMSKY_DB_URL: postgres://rimsky:rimsky@postgres:5432/rimsky
       depends_on: { rimsky-migrate: { condition: service_completed_successfully } }

     rimsky-supervisor:
       image: rimsky/supervisor:0.1
       environment:
         RIMSKY_DB_URL: postgres://rimsky:rimsky@postgres:5432/rimsky
         RIMSKY_SUPERVISOR_CONFIG: /etc/rimsky/supervisor-config.yml
       volumes:
         - ./supervisor-config.yml:/etc/rimsky/supervisor-config.yml:ro
       depends_on:
         rimsky-migrate: { condition: service_completed_successfully }
         http-node: { condition: service_started }
         claude-agent: { condition: service_started }

     rimsky-control-api:
       image: rimsky/control-api:0.1
       environment:
         RIMSKY_DB_URL: postgres://rimsky:rimsky@postgres:5432/rimsky
         RIMSKY_CONTROL_API_HOST: 0.0.0.0
         RIMSKY_CONTROL_API_PORT: "8080"
       ports: ["8080:8080"]
       depends_on: { rimsky-migrate: { condition: service_completed_successfully } }

     http-node:
       image: rimsky/executor-http-node:0.1
       ports: ["9091:9091", "9092:9092"]

     claude-agent:
       image: rimsky/executor-claude-agent:0.1
       environment:
         ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY:-}"
         RIMSKY_EXECUTOR_STUB_MODE: "${RIMSKY_EXECUTOR_STUB_MODE:-1}"
       ports: ["9090:9090", "9190:9190"]

   volumes:
     rimsky_pg:
   ```
2. `supervisor-config.yml` wires executors:
   ```yaml
   supervisor_id: "${HOSTNAME}"
   postgres_url: "${RIMSKY_DB_URL}"
   concurrency: 8
   heartbeat_interval_ms: 5000
   claim_poll_interval_ms: 1000
   executors:
     claude-agent:
       transport: grpc
       endpoint: claude-agent:9090
     http-node:
       transport: grpc
       endpoint: http-node:9091
   concurrency_limits:
     agentic: 10
   sql_connections: {}
   callback:
     host: 0.0.0.0
     port: 9100
   ```

**Verification:**
- `docker compose -f deploy/docker-compose.yml config` validates.
- `docker compose up -d` brings everything online within 60 seconds.
- `docker compose ps` shows all services healthy.
- `curl http://localhost:8080/health` returns 200.

---

### Task 3.2 — Compose smoke test

**Files:** `rimsky-go/test/smoke/compose_smoke_test.go`

**Steps:**

1. A `// +build smoke` tagged Go test that runs `docker compose up -d`, waits for health, posts a trivial template via control API, creates an instance, waits for completion, tears down.
2. Not run in regular `go test ./...` (tagged `smoke`); run explicitly via `go test -tags=smoke ./test/smoke/...`.
3. Documented as the compose reference's "it works" check.

**Verification:** `go test -tags=smoke ./test/smoke/...` passes locally with Docker running.

---

## Phase 4 — Kubernetes Helm chart

### Task 4.1 — Chart skeleton

**Files:**
- `rimsky-go/deploy/kubernetes/rimsky-chart/Chart.yaml`
- `rimsky-go/deploy/kubernetes/rimsky-chart/values.yaml`
- `rimsky-go/deploy/kubernetes/rimsky-chart/templates/` (multiple)

**Steps:**

1. `Chart.yaml`:
   ```yaml
   apiVersion: v2
   name: rimsky
   description: Rimsky node-graph orchestrator
   type: application
   version: 0.1.0
   appVersion: "0.1"
   ```
2. `values.yaml`: defaults for image tags, replica counts, postgres DSN, resource limits, executor endpoints (externally resolvable or cluster-local DNS names).
3. Templates:
   - `serviceaccount.yaml`
   - `rbac.yaml` (minimal; no cluster-wide permissions)
   - `configmap-supervisor.yaml` (supervisor config from values)
   - `deployment-scheduler.yaml` (1 replica)
   - `deployment-supervisor.yaml` (N replicas; Deployment not StatefulSet)
   - `deployment-control-api.yaml` (N replicas)
   - `deployment-http-node.yaml` (N replicas)
   - `deployment-claude-agent.yaml` (N replicas)
   - `service-control-api.yaml` (ClusterIP; optional Ingress)
   - `service-http-node.yaml` (ClusterIP)
   - `service-claude-agent.yaml` (ClusterIP)
   - `job-migrate.yaml` (helm hook: `helm.sh/hook: pre-install,pre-upgrade`)
4. Postgres: reference an external Postgres via `postgres.dsn` value. Bundled Postgres is out of scope (use the Bitnami Postgres chart or a cloud DB).

**Verification:** `helm lint deploy/kubernetes/rimsky-chart` passes; `helm template deploy/kubernetes/rimsky-chart` renders valid YAML.

---

### Task 4.2 — kind-based smoke test for the chart

**Files:**
- `rimsky-go/deploy/kubernetes/smoke-test.sh`

**Steps:**

1. Bash script that uses `kind` (Kubernetes-in-Docker) to create a cluster, install a Postgres via Bitnami chart (or a one-liner Pod for simplicity), `helm install` the rimsky chart, probe `/health` via port-forward, tear down.
2. Not run in CI by default (needs `kind` binary); documented as the chart's "it works" check.

**Verification:** script runs end-to-end on a dev machine with `kind` installed.

---

## Phase 5 — Conformance suite

### Task 5.1 — Conformance scenarios framework

**Files:**
- `rimsky-go/conformance/scenario.go`
- `rimsky-go/conformance/runner.go`

**Steps:**

1. Define a `Scenario` struct:
   ```go
   type Scenario struct {
       Name         string
       RequiresAsync bool   // skipped if the executor doesn't advertise async
       RequiresStub bool
       Run          func(ctx context.Context, client executor.Client) error
   }
   ```
2. Runner iterates all registered scenarios, runs each against the provided endpoint, prints pass/fail. Returns non-zero exit on any failure.
3. Supports `--require-stub-mode` flag (spec §14.4): the runner issues a probe request at startup. If the probe response indicates a live LLM call (e.g. response time > 500ms, or result fields look real), fail fast with a clear message.

**Verification:** `go build ./conformance/...` exits 0.

---

### Task 5.2 — Scenario: Execute happy path

**Files:** `rimsky-go/conformance/scenarios/execute_happy_path.go`

**Steps:** call Execute with a minimal valid ExecuteRequest; assert stream emits at least one event, last event is a terminal (`Complete | Blocked | Errored | AsyncAccepted`), stream closes cleanly.

---

### Task 5.3 — Scenario: Heartbeats during long work

**Files:** `rimsky-go/conformance/scenarios/heartbeats.go`

**Steps:** call Execute with userdata that asks the stub for a delay before completion; assert at least one Heartbeat is observed; terminal event follows.

---

### Task 5.4 — Scenario: Malformed userdata rejected

**Files:** `rimsky-go/conformance/scenarios/malformed_userdata.go`

**Steps:** call Execute with userdata missing required fields; assert an `Errored` with a reasonable error class (e.g. `invalid_userdata`).

---

### Task 5.5 — Scenario: Terminal events are the last events

**Files:** `rimsky-go/conformance/scenarios/terminal_is_last.go`

**Steps:** after observing any terminal, `Recv()` returns `io.EOF`; no events follow.

---

### Task 5.6 — Scenario: Cancel via ctx

**Files:** `rimsky-go/conformance/scenarios/cancel.go`

**Steps:** call Execute with ctx; cancel ctx after a short delay; assert the stream terminates and the server respects the cancellation.

---

### Task 5.7 — Scenario: Async handoff + callback

**Files:** `rimsky-go/conformance/scenarios/async_handoff.go`

**Steps:** call Execute; if terminal is `AsyncAccepted`, spin up a temporary HTTP callback server and wait for the callback to arrive within 30 seconds; assert callback body is one of Complete/Blocked/Errored.

---

### Task 5.8 — Scenario: BigInt/circular-reference result handling

**Files:** `rimsky-go/conformance/scenarios/result_serialization.go`

**Steps:** call Execute with userdata instructing the stub to return a BigInt-equivalent or circular value (stub-mode protocol-level test that the executor correctly rejects or sanitizes).

---

### Task 5.9 — Scenario: Unknown async-ack-id rejected on callback

**Files:** `rimsky-go/conformance/scenarios/unknown_ack_id.go`

**Steps:** after an async handoff, post a callback with the wrong ack ID; assert 404.

Note: this tests the **orchestrator's** callback endpoint, not the executor. Skip this scenario for conformance runs that target only the executor; include it for a "full system" conformance run (optional future Phase).

For v1: include as a skippable scenario (marked `RequiresOrchestrator: true`).

---

### Task 5.10 — Scenario: Stream close without terminal

**Files:** `rimsky-go/conformance/scenarios/stream_close_without_terminal.go`

**Steps:** configure stub to close stream early (this is a behavior that SHOULD be impossible per spec §7.2 — but conformance verifies the executor doesn't accidentally do it). If the executor does close without a terminal, fail with a clear "spec §7.2 violated" message.

---

### Task 5.11 — Binary: `cmd/rimsky-conformance/main.go`

**Files:** `rimsky-go/core/cmd/rimsky-conformance/main.go`

**Steps:**

1. CLI flags:
   - `--endpoint <url>`
   - `--transport grpc|http`
   - `--require-stub-mode` (boolean)
   - `--scenarios <comma-list>` (subset of scenarios to run)
   - `--skip <comma-list>`
2. Connects to the endpoint; runs all applicable scenarios; prints a per-scenario pass/fail summary; exits 0 on all green.

**Verification:**
- `go build ./core/cmd/rimsky-conformance/` produces binary.
- Against `http-node` in stub mode: `rimsky-conformance --endpoint localhost:9091 --transport grpc --require-stub-mode` → all scenarios pass.
- Against `claude-agent` in stub mode: same, against port 9090 → all scenarios pass (except ones that don't apply to async-handoff executors, which should be auto-skipped).

---

### Task 5.12 — Docker image for conformance

**Files:** `rimsky-go/deploy/Dockerfile.conformance`

**Steps:** same pattern as other Go binaries; ship `rimsky/conformance:0.1`.

**Verification:** `docker run --network host rimsky/conformance:0.1 --endpoint localhost:9091 --transport grpc --require-stub-mode` passes.

---

## Phase 6 — Definition of Done

### Task 6.1 — Gate verification

**Steps:**

1. `cd rimsky-go && go build ./...` → exit 0.
2. `go test ./... -count=1` → all tests passing.
3. `go vet ./...` → exit 0.
4. `golangci-lint run` → exit 0.
5. `cd executors/claude-agent && npm test` → all tests passing.
6. Migration 002 applies cleanly on a DB that already has 001.
7. All six Docker images build via `deploy/build-images.sh`.
8. `docker compose -f deploy/docker-compose.yml up -d` brings up a healthy stack within 60 seconds. `curl http://localhost:8080/health` returns 200. `docker compose down -v` cleans up.
9. `helm lint deploy/kubernetes/rimsky-chart` passes.
10. `rimsky-conformance` passes against both `http-node` and `claude-agent` in stub mode, with `--require-stub-mode`.
11. The `external_sql_rollback_test.go` scenario passes.
12. Append a Plan C entry to `rimsky-go/CHANGELOG.md`.
13. Append `plan_completed` entry to the execution log.

**Verification:** all gates green.

---

## Appendix — Subagent dispatch notes

**Parallelizable groups:**
- Phase 1 (external-sql) and Phase 5 (conformance suite) share nothing; parallelize.
- Within Phase 2 (Docker), each of Tasks 2.1/2.2/2.3 is independent.
- Within Phase 5, scenarios 5.2–5.10 can be implemented in parallel.

**Critical-path tasks:**
- Task 1.2 (external-sql Commit) — atomic-swap DDL correctness, concurrent-commit serialization, mid-swap failure recovery. Reviewer should exercise the mid-swap failure path deliberately.
- Task 3.1 (docker-compose) — service dependency ordering and healthchecks. The stack must come up reliably; healthcheck timing issues are a common flakiness source.
- Task 5.11 (conformance binary) — `--require-stub-mode` probe correctness is the difference between "conformance is meaningful in CI" and "conformance passes against a half-broken executor."
