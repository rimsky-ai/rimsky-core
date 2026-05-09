# Platform Extensions for Agent-Driven Consumers — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-08-platform-extensions-for-agent-consumers-design.md`

**Goal:** Implement all rimsky platform additions, reference-component changes, conformance suite extensions, and documentation updates from the 2026-05-08 platform-extensions spec, end-to-end, in one execution.

**Architecture:** The work spans rimsky's three Go modules (`protocols/`, `foundation/`, root `modeling/`) plus the TypeScript reference executor (`executors/claude-agent/`) and a new bundled MCP shim under `mcp-servers/control-api/`. The core additions are: (1) a generalized executor-protocol surface for declared userdata schemas, named events with payloads, and the `ParkRequested` terminal event; (2) a pluggable blob-spill mechanism in foundation persistence with four reference backends; (3) DSL extensions for `on_event` handlers and event-payload substitution; (4) new control-API admin and diagnostics endpoints; (5) Prometheus metrics export from each rimsky process; (6) reference-executor work on `claude-agent` (MCP catalog with four transports, schema-validated retry, auto rate-limit park); (7) a new bundled `mcp-servers/control-api/` MCP shim; (8) a new conformance suite `rimsky-blob-backend-conformance`; (9) per-component documentation surfaces.

**Tech Stack:** Go 1.22+ (foundation, protocols, modeling, mcp-servers); TypeScript / Node 20 (executors/claude-agent); Postgres + SQLite via the existing pluggable driver; gRPC + protobuf v3 for protocols; `go-chi/chi` for HTTP; `jackc/pgx/v5` for Postgres; `modernc.org/sqlite` for SQLite; `robfig/cron/v3` for cron; `log/slog` (stdlib) for logging.

---

## Critical path and dependencies

Tasks are organized in sections A–N. Dependencies:

- **A (protocol additions)** MUST land first. All other sections depend on the regenerated proto bindings.
- **C (schema migrations)** MUST land before D (BlobBackend impls), E (integration code), F (modeling code that reads the new columns and tables), and H (handler dispatch reads the events ledger).
- **B (state machine)** MUST land before E (terminal handlers that emit the new transitions).
- **D (BlobBackend impls)** MUST land before E (terminal handlers that may spill payloads).
- **F (modeling DSL extensions)** MUST land before H (handler dispatch wires DSL into runtime).
- **G (control-API endpoints)** MUST land before K (MCP shim wraps these endpoints).
- **H (event handler dispatch)** depends on F (DSL parsing) and on the event ledger from C.
- **I (Prometheus metrics)** depends on most other sections for instrumentation points; do it after E, F, G, H so all the metric hooks are in place to instrument.
- **J (claude-agent)** depends on A (Capabilities/Event/ParkRequested wire shapes), C (resume_context column), E (resume dispatch).
- **K (mcp-servers/control-api)** depends on G (admin endpoints exist).
- **L (conformance)** depends on A, D, J — exercises the new surfaces.
- **M (documentation)** is interleaveable but should not gate other tasks; do it as late as possible so the docs reflect what was actually built.
- **N (final verification)** is last.

Linear execution order respecting these dependencies: A → B → C → D → E → F → H → G → I → J → K → L → M → N.

If a task's verification fails, fix the failure before moving on. Do not commit anything; the plan produces working-tree edits only.

## Pre-resolved design decisions

These choices were left ambiguous in the spec or arose during planning. They are resolved here so the implementer does not need to re-litigate them:

- **JSON Schema validator (Go side):** `github.com/santhosh-tekuri/jsonschema/v5`. Used in F7 (userdata validation) and K3 (control-api MCP shim input validation). Justification: well-maintained, draft 2020-12 support, no transitive heavyweight deps.
- **JSON Schema validator (TS side, claude-agent):** `ajv` with `ajv-formats`. Justification: de facto standard for Node; supports draft 2020-12; already commonly available.
- **Prometheus client library (Go):** `github.com/prometheus/client_golang/prometheus` and `prometheus/promhttp`. Justification: de facto standard. The "resist heavier alternatives" rule in `rules.md` targets routing/logging frameworks, not metrics — Prometheus client is justified by direct use and ecosystem alignment.
- **Memory blob backend's multi-process rejection signal:** check the env var `RIMSKY_PROCESS_ROLE`. The unified `rimsky-entrypoint` sets this to `"unified"` (add this env-var setter to the entrypoint as part of D5). Per-process binaries (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) set their own role values. The memory backend startup validator rejects unless `RIMSKY_PROCESS_ROLE == "unified"`.
- **`module` transport in claude-agent:** implemented as an alias for `http-loopback`. Same lifecycle, same loader. The two names exist in userdata for documentation clarity (template authors can express intent — "this is in-process tooling" — even though the wire path is identical to `http-loopback`).
- **Go-side MCP server implementation in `mcp-servers/control-api/`:** implement the JSON-RPC 2.0 wire protocol directly (no third-party MCP SDK). The MCP wire surface needed (`initialize`, `tools/list`, `tools/call`) is small enough that a direct implementation is cleaner than pulling in a TypeScript-first SDK or a less-mature Go alternative. Use `go-chi/chi` for HTTP routing (already in the dependency set).
- **`AsyncCallbackBody` parser strategy:** the supervisor's HTTP callback handler tries the new shape (`{events: [...], terminal: {...}}`) first and falls back to the legacy shape (`{type: "complete"|"blocked"|"errored", ...}`) on a parse-as-new-shape error. Both shapes remain accepted indefinitely; the new shape is preferred and documented.
- **`ParkRequested.reason` validation:** non-empty is recommended but **not enforced**. Empty reason is accepted and logged at WARN level. Spec is explicit on this point.

---

## Conventions used in this plan

- Paths are relative to the repository root unless absolute.
- "Read X, then add Y" tasks: implementer first reads the named file(s) for current shape, then makes the prescribed change. This avoids embedding stale code in the plan.
- `make` targets are: `make proto-gen`, `make build-all`, `make test-all`, `make lint`, `make tidy` — all from the repo root.
- For tests that hit Postgres via testcontainers, Docker must be running; the implementer should `docker info` first if scenario tests fail.
- All new exported Go identifiers get standard godoc.
- New blessed invariants and updates to existing ones are annotated inline in source per the existing convention (see `foundation/cascade/state.go` for the pattern).
- All examples in code comments and docs use the generic illustrative names from `.claude/rules/rules.md` (`project-alpha`, `project-tracker`, `analytics_production`, `items`, `category`). No real consumer names.

---

## Section A — Protocol additions

The protocol layer changes land first; everything else depends on the regenerated bindings.

### A1. Add `userdata_schema` and `declared_events` to `Capabilities`

**Files:**
- `protocols/proto/v1/executor_observability.proto` (the `Capabilities` message lives here; confirmed by grep)

**Steps:**

1. Read `protocols/proto/v1/executor_observability.proto` and locate the `Capabilities` message.
2. Add two fields to `Capabilities`:
   - `bytes userdata_schema = <next>;` — JSON Schema (RFC 8259 + JSON Schema draft 2020-12) describing the executor's accepted userdata shape. Empty means "no schema; accept any userdata."
   - `repeated string declared_events = <next>;` — set of event names the executor may emit via the new `Event` wire type. Empty means "executor does not emit events."
3. Add field-level comments explaining the contract: rimsky validates incoming template userdata against `userdata_schema` at template registration and at dispatch (post-substitution); rimsky validates that any `on_event` handlers in templates referencing this executor name an event in `declared_events`.
4. Run `make proto-gen` from the repo root.

**Verify:** `go build ./protocols/...` succeeds. `git diff protocols/proto/v1/gen/` shows the new fields in the generated bindings.

### A2. Add `Event` non-terminal wire type to `ExecuteEvent`

**Files:**
- `protocols/proto/v1/executor.proto`

**Steps:**

1. Read `protocols/proto/v1/executor.proto`. Locate the `ExecuteEvent` message (it is a `oneof` containing `Heartbeat`, `Complete`, `Blocked`, `Errored`, `AsyncAccepted`).
2. Add a new variant to the `oneof`:
   ```proto
   message Event {
     string name = 1;        // domain-meaningful event name; must be in Capabilities.declared_events
     bytes payload = 2;      // opaque to rimsky; available to substitution via nodes.<emitter>.event.<name>.<path>
   }
   ```
3. Add the variant to the `oneof`: `Event event = <next>;`.
4. Comment: events are non-terminal. An executor may emit zero or more `Event` records during a run, in any order, and must still emit exactly one terminal event (`Complete`, `Blocked`, `Errored`, `AsyncAccepted`, or the new `ParkRequested` from A3).
5. Run `make proto-gen`.

**Verify:** `go build ./protocols/...` succeeds. The `Event` variant appears in `protocols/proto/v1/gen/`.

### A3. Add `ParkRequested` terminal wire type to `ExecuteEvent`

**Files:**
- `protocols/proto/v1/executor.proto`

**Steps:**

1. In `protocols/proto/v1/executor.proto`, add to the `ExecuteEvent` oneof:
   ```proto
   message ParkRequested {
     string reason = 1;                          // required; non-empty discouraged-empty
     bytes payload = 2;                          // opaque; passed back as resume_context.payload
     google.protobuf.Timestamp resume_at = 3;    // optional; absent means signal-based-only
     string session_token = 4;                   // optional; opaque; passed back as resume_context.session_token
   }
   ```
2. Add the variant: `ParkRequested park_requested = <next>;`.
3. Add comments: `ParkRequested` is a terminal event from the gRPC stream's perspective (closes the stream). The node transitions to the `parked` state. Resume re-dispatches with `resume_context` populated. At least one of `resume_at` or external invalidation must wake the node — there is no in-protocol enforcement of this (a `ParkRequested` with neither set is allowed and produces an indefinite park).
4. Import `google/protobuf/timestamp.proto` if not already imported.
5. Run `make proto-gen`.

**Verify:** `go build ./protocols/...` succeeds. The `ParkRequested` variant appears in generated bindings.

### A4. Add `ResumeContext` field to `ExecuteRequest`

**Files:**
- `protocols/proto/v1/executor.proto`

**Steps:**

1. In `protocols/proto/v1/executor.proto`, add a new message:
   ```proto
   message ResumeContext {
     bytes payload = 1;            // the original ParkRequested.payload
     string session_token = 2;     // the original session_token
     string resume_reason = 3;     // "deadline_elapsed" | "external_invalidate"
   }
   ```
2. Add to `ExecuteRequest` as an optional field: `ResumeContext resume_context = <next>;` (use field number after the highest existing one). When absent, this is a fresh dispatch; when present, this is a resume.
3. Run `make proto-gen`.

**Verify:** `go build ./protocols/...` succeeds.

### A5. Define async-callback body shape in protocols/

**Files:**
- `protocols/proto/v1/executor.proto` OR a new file `protocols/proto/v1/callback.proto`

**Steps:**

1. The async-callback body is HTTP+JSON, not gRPC, but the canonical schema for it should still live in `protocols/`. Add a new message describing the callback body. Either inline in `executor.proto` or as a new `callback.proto`. Choice: inline in `executor.proto` for proximity. Add:
   ```proto
   // Body shape POSTed by an executor to ${callback_url}/v1/callback/{async_ack_id}
   // after it emitted AsyncAccepted. The receiving supervisor parses this from JSON.
   message AsyncCallbackBody {
     repeated Event events = 1;            // optional; processed in order before terminal
     // Exactly one of the terminal fields must be set:
     Complete complete = 2;
     Blocked blocked = 3;
     Errored errored = 4;
     ParkRequested park_requested = 5;
   }
   ```
2. Add comment: legacy callback bodies (the existing `{type: "complete"|"blocked"|"errored", ...}` shape) remain accepted by the supervisor for transitional purposes; the new shape is preferred. The supervisor's parser tries the new shape first and falls back to the legacy on a parse error.
3. Run `make proto-gen`. The generated Go types are used as JSON-tagged structs in the supervisor's HTTP handler (next task).

**Verify:** `go build ./protocols/...` succeeds.

### A6. Update `Capabilities` Go-side caching to thread new fields

**Files:**
- `foundation/integration/remote/` (gRPC client; the `Capabilities` is fetched and cached here)
- Any direct callers of the cached `Capabilities` discoverable via grep

**Steps:**

1. Run `grep -rln 'GetCapabilities\|userdata_schema\|declared_events' --include='*.go' .` from the repo root.
2. Identify the `foundation/integration/remote/` file that calls `GetCapabilities()` and caches the result. Update its cache struct (or the type the cache holds) to include `UserdataSchema []byte` and `DeclaredEvents []string` fields, populated from the gRPC response.
3. Identify any consumer of the cached `Capabilities` in `modeling/` or `cmd/` — currently there should be at most one or two; update them to pass the new fields through their own data structures so F7 (validation) and F6 (handler validation) can read them. At this stage do not implement validation logic — just thread the fields.
4. If `grep` returns no consumers in `modeling/` or `cmd/`, no further changes are required beyond the `foundation/integration/remote/` cache update.

**Verify:** `go build ./...` from each module root succeeds. `make build-all` succeeds.

### A7. Tests for proto-gen output

**Files:**
- `protocols/` test files (the existing `_test.go` files near generated code)

**Steps:**

1. Add a small smoke test that constructs each new message, marshals to bytes, unmarshals, and asserts equality. Keep it minimal — just confirm the round-trip works.
2. Place in `protocols/proto/v1/gen/` if a test file exists there, or add `protocols/proto/v1/gen/proto_smoke_test.go`.

**Verify:** `go test ./protocols/...` passes.

---

## Section B — Foundation cascade: `parked` node state

### B1. Add `parked` to NodeState enum and update transition table

**Files:**
- `foundation/cascade/state.go`
- `foundation/cascade/state_test.go`

**Steps:**

1. Read `foundation/cascade/state.go` to find the existing `NodeState` constants and transition machinery.
2. Add a new constant: `NodeStateParked NodeState = "parked"`.
3. Update the transition validation table (or function) to permit:
   - `running → parked` with reason `dispatch_park_requested` (new reason constant: `ReasonHandlerPark` or similar — match the existing naming convention for reasons).
   - `parked → running` with reason `dispatch_resume` (new reason constant).
   - `parked → failed` with reason `park_timeout` (the watchdog path).
   - `parked → failed` with reason `external_failure` (if the spec calls for any other path; otherwise omit).
4. The transition table must reject all other transitions involving `parked`. In particular: `parked → fresh` is illegal (a parked node only leaves via resume or failure).

**Verify:** `go test ./foundation/cascade/...` passes (after B2 adds tests for the new transitions).

### B2. Tests for new state transitions

**Files:**
- `foundation/cascade/state_test.go`

**Steps:**

1. Read existing tests to follow the pattern.
2. Add table-driven tests covering:
   - `running → parked` succeeds with `ReasonHandlerPark`.
   - `parked → running` succeeds with `ReasonHandlerResume`.
   - `parked → failed` succeeds with `ReasonParkTimeout`.
   - All other `parked → X` transitions fail.
   - All other `Y → parked` transitions fail.
   - `parked → parked` (any reason) fails (matches invariant 1's "rejects illegal transitions" property).

**Verify:** `go test ./foundation/cascade/...` passes.

### B3. Update `@blessed-invariant 1` annotation

**Files:**
- `foundation/cascade/state.go`

**Steps:**

1. Locate the `@blessed-invariant 1` annotation block in `state.go`.
2. Update it to enumerate the five legitimate states: `fresh`, `stale`, `running`, `failed`, `parked`. Update the comment to mention the new `parked → running` and `running → parked` legal transitions and the rejected illegal ones.

**Verify:** `make lint` passes (revive will flag any annotation-format breakage).

### B4. Update CLAUDE.md state-list reference

**Files:**
- `CLAUDE.md`

**Steps:**

1. Read CLAUDE.md and find the line "Vocabulary: 1 graph-level message ... 4 node states (`fresh`, `stale`, `running`, `failed`) ...".
2. Update to "5 node states (`fresh`, `stale`, `running`, `failed`, `parked`)".
3. Add a brief "Held vs. failed states" note in the gotchas section explaining that parked is a non-terminal hold state distinct from failed; cascade does not propagate from parked.

**Verify:** No automated check; visually inspect with `git diff CLAUDE.md`.

---

## Section C — Foundation persistence schema additions

### C1. Migration: add `'parked'` value to the worker_request `phase` enum

**Files:**
- `foundation/persistence/postgres/migrations/<next-numbered>.sql`
- `foundation/persistence/sqlite/migrations/<next-numbered>.sql`

**Steps:**

1. Read both `migrations/` directories and identify the highest existing migration number; the next number is the existing max + 1, with the same numbering scheme (read the existing file naming pattern).
2. For Postgres: write a migration that alters the `phase` check constraint or enum on `rimsky_worker_request` to include `'parked'`. The existing values are `'pending' | 'active' | 'held' | 'completed'`. New value to add: `'parked'`. If `phase` is implemented as a `CHECK` constraint, drop and recreate it; if as a Postgres enum type, use `ALTER TYPE … ADD VALUE 'parked'`.
3. For SQLite: same additive change. SQLite typically uses CHECK constraints; recreate the table or use a less-disruptive migration depending on existing patterns.
4. Verify the migration is idempotent (uses `IF NOT EXISTS` or similar guards where SQL allows).

**Verify:** `cd foundation/persistence/postgres && go test -run TestMigrate -count=1 ./...` succeeds. Same for sqlite. (These tests boot a fresh DB, run all migrations, and check schema state.)

### C2. Migration: add park-state columns to `rimsky_worker_request`

**Files:**
- `foundation/persistence/postgres/migrations/<next-numbered>.sql`
- `foundation/persistence/sqlite/migrations/<next-numbered>.sql`

**Steps:**

1. Postgres migration:
   ```sql
   ALTER TABLE rimsky_worker_request
     ADD COLUMN IF NOT EXISTS parked_at TIMESTAMPTZ,
     ADD COLUMN IF NOT EXISTS resume_at TIMESTAMPTZ,
     ADD COLUMN IF NOT EXISTS parked_payload_inline BYTEA,
     ADD COLUMN IF NOT EXISTS parked_payload_handle TEXT,
     ADD COLUMN IF NOT EXISTS parked_payload_handle_backend TEXT,
     ADD COLUMN IF NOT EXISTS session_token TEXT,
     ADD COLUMN IF NOT EXISTS parked_reason TEXT;

   CREATE INDEX IF NOT EXISTS idx_worker_request_parked_resume
     ON rimsky_worker_request(resume_at) WHERE phase = 'parked' AND resume_at IS NOT NULL;
   ```
   The `parked_payload_inline` column stores small payloads inline; large payloads spill to the blob backend with `parked_payload_handle` + `parked_payload_handle_backend` set and `parked_payload_inline` NULL. Exactly one of `parked_payload_inline` and `parked_payload_handle` is non-NULL when `phase='parked'`.
2. SQLite migration: same shape, SQLite syntax (no `IF NOT EXISTS` for ADD COLUMN in older SQLite versions — the migration must check column existence via `PRAGMA table_info` or use a recreate-table pattern; follow what the existing migrations in this repo do).

**Verify:** Migration tests pass (same as C1).

### C3. Migration: add blob handle column to attribute storage

**Files:**
- `foundation/persistence/postgres/migrations/<next-numbered>.sql`
- `foundation/persistence/sqlite/migrations/<next-numbered>.sql`

**Steps:**

1. Read `foundation/persistence/postgres/node_attributes.go` and `foundation/persistence/sqlite/node_attributes.go` to find the current attribute-storage table name and shape.
2. Add a migration to the relevant table (likely `rimsky_node_attributes` or similar):
   ```sql
   ALTER TABLE rimsky_node_attributes
     ADD COLUMN IF NOT EXISTS value_handle TEXT,
     ADD COLUMN IF NOT EXISTS value_handle_backend TEXT;
   ```
   The semantics: when the inline `value` column is NULL and `value_handle` is non-NULL, the value lives in the named blob backend.
3. Add a CHECK constraint enforcing that exactly one of `value` and `value_handle` is non-NULL per row (or document this as an invariant the write path enforces).

**Verify:** Migration tests pass.

### C4. Migration: orphan-blob tracking

**Files:**
- `foundation/persistence/postgres/migrations/<next-numbered>.sql`
- `foundation/persistence/sqlite/migrations/<next-numbered>.sql`

**Steps:**

1. Add a new tracking table for unreferenced blobs awaiting reaping:
   ```sql
   CREATE TABLE IF NOT EXISTS rimsky_blob_orphans (
     handle TEXT PRIMARY KEY,
     backend TEXT NOT NULL,
     orphaned_at TIMESTAMPTZ NOT NULL,
     reap_after TIMESTAMPTZ NOT NULL
   );
   CREATE INDEX IF NOT EXISTS idx_blob_orphans_reap ON rimsky_blob_orphans(reap_after);
   ```
2. The semantics: when an attribute row is deleted or its `value_handle` is overwritten, the old handle goes into this table with `reap_after = now() + retention_window`. The `SweepOrphanedBlobs` sweep deletes rows where `reap_after <= now()` and calls `BlobBackend.Delete(handle)` for each.

**Verify:** Migration tests pass.

### C5. Migration: add `consecutive_retries_no_progress` column

**Files:**
- `foundation/persistence/postgres/migrations/<next-numbered>.sql`
- `foundation/persistence/sqlite/migrations/<next-numbered>.sql`

**Steps:**

1. Add a column to `rimsky_worker_request` to track consecutive retries with no `last_outcome` change (used by E5's max-retries-without-progress cap):
   ```sql
   ALTER TABLE rimsky_worker_request
     ADD COLUMN IF NOT EXISTS consecutive_retries_no_progress INTEGER NOT NULL DEFAULT 0;
   ```
2. SQLite parallel migration with column-existence check.

**Verify:** Migration tests pass.

### C6. Migration: create `rimsky_node_events` ledger table

**Files:**
- `foundation/persistence/postgres/migrations/<next-numbered>.sql`
- `foundation/persistence/sqlite/migrations/<next-numbered>.sql`

**Steps:**

1. Create the events ledger that stores executor-emitted named events for substitution (used by F4 and H1):
   ```sql
   CREATE TABLE IF NOT EXISTS rimsky_node_events (
     id BIGSERIAL PRIMARY KEY,
     instance_id UUID NOT NULL,
     emitter_node_id TEXT NOT NULL,
     event_name TEXT NOT NULL,
     payload_inline BYTEA,
     payload_handle TEXT,
     payload_handle_backend TEXT,
     emitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
     frame_id UUID
   );
   CREATE INDEX IF NOT EXISTS idx_node_events_lookup
     ON rimsky_node_events(instance_id, emitter_node_id, event_name, emitted_at DESC);
   ```
   Exactly one of `payload_inline` and `payload_handle` is non-NULL per row, mirroring the spill semantics of C2 and C3.
2. SQLite parallel migration. Use `INTEGER PRIMARY KEY AUTOINCREMENT` instead of `BIGSERIAL`; `BLOB` instead of `BYTEA`; `TEXT` for UUIDs.

**Verify:** Migration tests pass.

### C7. Migration: denormalize `max_retries_without_progress` and `max_park_duration` onto `rimsky_worker_request`

**Files:**
- `foundation/persistence/postgres/migrations/<next-numbered>.sql`
- `foundation/persistence/sqlite/migrations/<next-numbered>.sql`

**Steps:**

1. Add columns to `rimsky_worker_request` for per-node DSL fields used by sweeps (so the sweep doesn't need a join through templates on every tick):
   ```sql
   ALTER TABLE rimsky_worker_request
     ADD COLUMN IF NOT EXISTS max_park_duration_seconds INTEGER,
     ADD COLUMN IF NOT EXISTS max_retries_without_progress INTEGER;
   ```
   Both default NULL meaning "use deployment default."
2. These columns are populated at dispatch time from the resolved template DSL (F2/F3).

**Verify:** Migration tests pass.

---

## Section D — Foundation persistence: `BlobBackend` interface and impls

### D0. Define `BlobConfig` and thread it into the `Driver`

**Files:**
- `foundation/persistence/blob_config.go` (new)
- `foundation/persistence/driver.go` (extend the `Driver` constructor / config-loading path)
- `cmd/rimsky-migrate/main.go`, `cmd/rimsky-supervisor/main.go`, `cmd/rimsky-scheduler/main.go`, `cmd/rimsky-control-api/main.go`, `cmd/rimsky-entrypoint/main.go` (wire into rimsky.yml loading)

**Steps:**

1. Create `foundation/persistence/blob_config.go`:
   ```go
   package persistence

   import "time"

   // BlobConfig is parsed from rimsky.yml's persistence.blob block at startup.
   type BlobConfig struct {
       Backend              string             // "inline" | "pg-largeobject" | "filesystem" | "memory"
       SpillThresholdBytes  int                // default 65536 (64KB)
       Filesystem           FilesystemBlobConfig
       PgLargeObject        PgLargeObjectBlobConfig
       Retention            BlobRetentionConfig
   }

   type FilesystemBlobConfig struct {
       Root string
   }

   type PgLargeObjectBlobConfig struct {
       Schema string // optional namespacing; default "public"
   }

   type BlobRetentionConfig struct {
       OrphanSweepInterval         time.Duration // default 1h
       RetentionAfterUnreferenced  time.Duration // default 24h
   }

   func DefaultBlobConfig() BlobConfig {
       return BlobConfig{
           Backend:             "inline",
           SpillThresholdBytes: 65536,
           Retention: BlobRetentionConfig{
               OrphanSweepInterval:        time.Hour,
               RetentionAfterUnreferenced: 24 * time.Hour,
           },
       }
   }
   ```
2. Read `foundation/persistence/driver.go` for the existing `Driver` constructor signature. Add a `BlobConfig` field (or a `Configure(BlobConfig)` method) so the persistence layer holds the active config and can construct the right backend at startup.
3. Read each cmd binary's main.go to find the rimsky.yml-parsing path. Extend the YAML schema (the Go struct that maps the YAML) to include the `persistence.blob` block parsing into `BlobConfig`. The unified `rimsky-entrypoint` and the per-process binaries should all read this same block.
4. Validate at startup: if `Backend` is "memory" and `os.Getenv("RIMSKY_PROCESS_ROLE") != "unified"`, fail startup with a clear error. The `rimsky-entrypoint` binary sets `RIMSKY_PROCESS_ROLE=unified` in its environment before spawning the colocated process(es); the per-process binaries do not set it. (Add the env-set to `cmd/rimsky-entrypoint/main.go` as part of this task.)

**Verify:** `go build ./foundation/...` and each `cmd/` binary build succeeds. Add a `BlobConfig_test.go` covering the multi-process rejection: setting `RIMSKY_PROCESS_ROLE=""` with `Backend=memory` returns the expected error.

### D1. Define the `BlobBackend` Go interface

**Files:**
- `foundation/persistence/blob.go` (new)

**Steps:**

1. Create `foundation/persistence/blob.go`:
   ```go
   package persistence

   import (
       "context"
       "errors"
       "io"
   )

   // BlobBackend stores attribute values that exceed the inline-spill threshold.
   // Implementations are typically out-of-process (Postgres LOBs, filesystem on a
   // shared volume, or future S3/GCS/etc.); the in-process memory backend is
   // dev-only.
   //
   // @blessed-invariant: Blob content is inert in rimsky. Rimsky reads bytes
   // only via walkPath substitution; never logs, formats, validates beyond
   // schema gates, transforms, or attaches to traces or errors.
   type BlobBackend interface {
       // Write persists bytes and returns an opaque handle.
       Write(ctx context.Context, key BlobKey, bytes []byte) (Handle, error)
       // Read returns the bytes referenced by handle.
       Read(ctx context.Context, handle Handle) ([]byte, error)
       // ReadRange returns a byte range; backends that do not support range reads
       // may fall back to full Read and slice. Returns io.ErrUnexpectedEOF if
       // offset+length exceeds blob size.
       ReadRange(ctx context.Context, handle Handle, offset, length int64) ([]byte, error)
       // Delete removes the blob. Idempotent (deleting an absent handle returns nil).
       Delete(ctx context.Context, handle Handle) error
       // Name returns the backend's identifier (e.g. "pg-largeobject", "filesystem").
       Name() string
   }

   // BlobKey is a write-side hint for content addressing or namespacing.
   // Implementations may ignore it (e.g. memory) or use it for path derivation
   // (filesystem) or content hash deduplication (future S3 backend).
   type BlobKey struct {
       NodeID     string
       AttributeName string
   }

   // Handle is a backend-opaque identifier for a stored blob.
   type Handle string

   // ErrBlobNotFound is returned by Read/ReadRange/Delete when the handle is unknown.
   var ErrBlobNotFound = errors.New("blob: handle not found")

   var _ io.Closer = (*nopCloser)(nil) // forward declaration used by impls
   type nopCloser struct{}
   func (nopCloser) Close() error { return nil }
   ```
2. Add godoc on each method explaining the contract.

**Verify:** `go build ./foundation/...` succeeds.

### D2. Implement `inline` backend

**Files:**
- `foundation/persistence/blob_inline.go` (new)
- `foundation/persistence/blob_inline_test.go` (new)

**Steps:**

1. Create `blob_inline.go` with `InlineBackend` — a degenerate backend used when spill is disabled. All operations return `ErrBlobNotFound` because inline values never become handles:
   ```go
   type InlineBackend struct{}

   func (InlineBackend) Write(_ context.Context, _ BlobKey, _ []byte) (Handle, error) {
       return "", errors.New("inline backend does not produce handles")
   }
   func (InlineBackend) Read(_ context.Context, _ Handle) ([]byte, error) { return nil, ErrBlobNotFound }
   func (InlineBackend) ReadRange(_ context.Context, _ Handle, _, _ int64) ([]byte, error) { return nil, ErrBlobNotFound }
   func (InlineBackend) Delete(_ context.Context, _ Handle) error { return nil }
   func (InlineBackend) Name() string { return "inline" }
   ```
2. The inline backend is selected when `persistence.blob.backend = inline` (the default). The attribute-write path checks `if attr value <= spill_threshold || backend.Name() == "inline" { write inline; never call backend.Write }`.
3. Test: `_test.go` confirms the methods behave as documented.

**Verify:** `go test ./foundation/persistence/ -run TestInlineBackend -count=1` passes.

### D3. Implement `pg-largeobject` backend

**Files:**
- `foundation/persistence/postgres/blob_largeobject.go` (new)
- `foundation/persistence/postgres/blob_largeobject_test.go` (new)

**Steps:**

1. Read `foundation/persistence/postgres/driver.go` and `backend.go` to find the existing pgx connection and transaction patterns. The blob backend must use the same connection pool (or a peer pool) so it shares the operator's pg config.
2. Implement `PgLargeObjectBackend`:
   - `Write`: open a new LOB via `pg_largeobject` API (use `pgx`'s `LargeObjects.Create` and `Open` for write; write bytes; close; return the OID as the handle string).
   - `Read`: open the LOB by OID, read all bytes, close.
   - `ReadRange`: seek and read.
   - `Delete`: `LargeObjects.Unlink`.
3. The OID-as-handle is suitable; format as `"pglo:<oid>"` so handles are self-describing across backends.
4. Operator config plumbing: add a `pg_largeobject` block under `persistence.blob` in `rimsky.yml`. No fields strictly required (uses the same pg connection); optional `lob_schema: TEXT` for organizational separation.
5. Test: testcontainers-backed test that writes 1MB, reads back, verifies bytes match; deletes and confirms `ErrBlobNotFound` on subsequent read.

**Verify:** `go test ./foundation/persistence/postgres/ -run TestPgLargeObjectBackend -count=1` passes (Docker required for testcontainers).

### D4. Implement `filesystem` backend

**Files:**
- `foundation/persistence/blob_filesystem.go` (new)
- `foundation/persistence/blob_filesystem_test.go` (new)

**Steps:**

1. Implement `FilesystemBackend`:
   - Constructor takes a root directory (`/var/lib/rimsky/blobs` by default).
   - Handles are derived from `BlobKey` plus a content-hash component to avoid collisions, e.g. `<root>/<sha256(node_id)>/<attribute_name>-<unix_nano>-<short_hash>`. Format the handle as `"fs:<relative_path>"`.
   - `Write`: create parent directory; write bytes; fsync; return handle.
   - `Read` / `ReadRange`: open file, read.
   - `Delete`: `os.Remove`; if NotExist, return nil (idempotent).
2. Reject path-escape via `filepath.Clean` and a check that the resolved path stays within root.
3. Operator config: `filesystem.root: PATH` under `persistence.blob`.
4. Test: round-trip write/read/delete; range read; idempotent delete.

**Verify:** `go test ./foundation/persistence/ -run TestFilesystemBackend -count=1` passes.

### D5. Implement `memory` backend with multi-process rejection

**Files:**
- `foundation/persistence/blob_memory.go` (new)
- `foundation/persistence/blob_memory_test.go` (new)

**Steps:**

1. Implement `MemoryBackend` as a thread-safe `map[Handle][]byte` plus `sync.RWMutex`:
   ```go
   type MemoryBackend struct {
       mu  sync.RWMutex
       blobs map[Handle][]byte
       seq atomic.Uint64
   }
   ```
2. `Write` generates a sequential handle `"mem:<seq>"`, stores bytes, returns handle.
3. `Read`/`ReadRange`/`Delete`: standard map operations under the mutex.
4. **Multi-process rejection.** Implemented in D0 via the `RIMSKY_PROCESS_ROLE` env-var check. No additional logic needed in the backend itself; the gate runs at `Driver` config-validation time before any backend is constructed.
5. Document this prominently in the godoc and in operator-guide.md.

**Verify:** `go test ./foundation/persistence/ -run TestMemoryBackend -count=1` passes. The multi-process rejection test asserts the correct error class on conflicting topology.

### D6. Wire spill-write into attribute write path

**Files:**
- `foundation/persistence/node_attributes.go` (existing) — write path
- `foundation/persistence/postgres/node_attributes.go` and `sqlite/node_attributes.go` — backend-specific impls

**Steps:**

1. Read the existing attribute-write function in `node_attributes.go` (Postgres and SQLite each have one).
2. Refactor the write path to:
   ```go
   // pseudocode
   if len(value) <= spillThreshold || backend.Name() == "inline" {
       // store inline in the value column; null out value_handle
   } else {
       handle, err := backend.Write(ctx, BlobKey{NodeID: ..., AttributeName: ...}, value)
       if err != nil { return err }
       // store NULL in value column; store handle in value_handle column; backend.Name() in value_handle_backend
   }
   ```
3. The threshold and selected backend come from a `BlobConfig` struct loaded at startup from `rimsky.yml` and threaded into the `Driver`.
4. If the existing write path overwrites a row that already had a value_handle, capture the old handle and queue it for orphan reaping (insert a row in `rimsky_blob_orphans`).

**Verify:** `go test ./foundation/persistence/...` passes (existing tests still pass after the refactor; new tests exercise the spill path — see D9).

### D7. Wire spill-read into attribute read path / `walkPath`

**Files:**
- `foundation/persistence/node_attributes.go`
- `modeling/attribute/substitution.go`

**Steps:**

1. The read path checks `if value_handle is non-NULL { fetch via backend }`. Backends are looked up by name from the `Driver`'s configured backend (there is only one backend at a time per deployment in v1; future versions may support multiple).
2. `walkPath` (in `modeling/attribute/substitution.go`) is unaffected at the JSON-walking level — it operates on the materialized value bytes. The change is purely in how the bytes are sourced.
3. Add a small lazy-load optimization: if the substitution path only touches a top-level field of a large blob, the read can use `ReadRange` if the layout is known. For v1, eager-load on touch (simpler; no new optimizer logic). Document the future optimization point in the code comment.

**Verify:** Read-after-write tests in D9 pass.

### D8. `SweepOrphanedBlobs` foundation sweep

**Files:**
- `foundation/integration/orphan_reaper.go` (existing — extend) OR `foundation/integration/orphan_blobs.go` (new)
- `foundation/integration/orphan_blobs_test.go` (new)

**Steps:**

1. Read `foundation/integration/orphan_reaper.go` for the existing reaper pattern.
2. Add a new sweep `SweepOrphanedBlobs` that:
   - Queries `rimsky_blob_orphans WHERE reap_after <= now()`.
   - For each row, calls `backend.Delete(handle)`.
   - On success, deletes the row from `rimsky_blob_orphans`.
   - On error other than `ErrBlobNotFound`, logs and continues (will retry next tick).
3. Wire the sweep into the existing tick loop (the conductor or sweep-orchestrator). Cadence: `persistence.blob.retention.orphan_sweep_interval` (default 1h).

**Verify:** `go test ./foundation/integration/ -run TestSweepOrphanedBlobs -count=1` passes (uses an in-memory backend for fast deterministic test).

### D9. Cross-backend round-trip integration tests

**Files:**
- `foundation/persistence/blob_roundtrip_test.go` (new)

**Steps:**

1. Write a table-driven test that runs the same round-trip scenarios against each backend (`inline` excluded — it doesn't store handles), verifying:
   - Write 1KB inline (below threshold) → reads back inline.
   - Write 1MB above threshold → reads back from backend.
   - Range read returns expected slice.
   - Overwrite produces an orphan row in `rimsky_blob_orphans` for the old handle.
   - Delete removes the row and the bytes.
2. Use `pgtest` for the pg-largeobject case; use `t.TempDir()` for filesystem; use the in-memory backend directly for memory.

**Verify:** `go test ./foundation/persistence/ -run TestBlobRoundtrip -count=1` passes.

---

## Section E — Foundation integration: terminal handlers, sweeps, retry caps

### E1. `ParkRequested` terminal handler

**Files:**
- `foundation/integration/runner_terminal_handlers.go`
- `foundation/integration/runner_terminal.go`
- `foundation/integration/runner_terminal_release.go`

**Steps:**

1. Read the existing terminal-handler files to understand how `Complete`, `Blocked`, `Errored` are processed (each typically has an `applyTerminal*` function — see e.g. `applyTerminalPass`, `applyTerminalFail`, etc.).
2. Add `applyTerminalPark` that:
   - Logs a WARN if `ParkRequested.reason` is empty (per spec, empty is permitted but discouraged). Does not reject.
   - Persists the park metadata (`parked_at: now()`, `resume_at`, `parked_reason`, `session_token`) to the worker_request row.
   - If `payload` is non-empty and exceeds the spill threshold, writes through `BlobBackend.Write` and stores the handle in `parked_payload_handle` + `parked_payload_handle_backend`. Otherwise stores in `parked_payload_inline` (column added in C2). Exactly one of the two storage paths is used per row.
   - Updates `phase` from `'active'` to `'parked'`.
   - Updates the node's state from `running` to `parked` via the state machine (uses `ReasonHandlerPark`).
   - Releases the supervisor's in-memory dispatch slot (returns from the dispatch worker without releasing the held claim — claims are retained per the held-claim semantics).
3. The held claim handles are NOT released here. Existing held-claim semantics already handle this (claims auto-release on true terminal verdicts only).

**Verify:** Scenario test in E6 covers this.

### E2. Update orphan-claim reaper to skip `phase='parked'` rows

**Files:**
- `foundation/integration/orphan_reaper.go`
- `foundation/persistence/postgres/queue.go` and `sqlite/queue.go` (the queries the reaper uses)

**Steps:**

1. Read the existing orphan-reaper queries. The reaper currently selects rows where `phase='active'` and the heartbeat is stale (5× heartbeat interval per invariant 6).
2. Confirm `phase='parked'` rows are already excluded (the existing predicate is `WHERE phase = 'active' AND ...`). If yes, no change required; document with a comment that parked rows are intentionally excluded because heartbeating is paused during park.
3. Add a regression test: insert a fake parked row with a stale `parked_at`, run the reaper, assert the parked row is untouched.

**Verify:** `go test ./foundation/integration/ -run TestOrphanReaper -count=1` passes including the new regression test.

### E3. `SweepParkedNodes` sweep — time-based wake

**Files:**
- `foundation/integration/sweep_parked.go` (new)
- `foundation/integration/sweep_parked_test.go` (new)
- `foundation/integration/conductor.go` (wire into tick)

**Steps:**

1. Implement `SweepParkedNodes(ctx, persist, clock)`:
   - Selects `worker_request` rows where `phase='parked' AND resume_at IS NOT NULL AND resume_at <= clock.Now()`, ordered by `resume_at ASC`, limit 100 per tick.
   - For each row, transitions `phase` back to `'active'`, claims a fresh `claimed_by` (the running supervisor's id), constructs `ResumeContext{payload, session_token, resume_reason: "deadline_elapsed"}`, and enqueues a re-dispatch.
   - The transition happens in a single DB transaction with the verify-before-run discipline applied (re-read `phase` immediately before dispatch in case another supervisor races).
2. Add `max_park_duration` watchdog logic: same sweep also selects `worker_request` rows where `phase='parked' AND parked_at + max_park_duration <= now()` (with `max_park_duration` resolved from the node's template DSL — fetched from a sibling table or denormalized onto the worker_request). For overruns, transition to `phase='completed'` (with last_outcome=`failed`) and emit `Errored { error_class: "park_timeout" }` through the standard terminal pipeline.
3. Wire into `foundation/integration/conductor.go` next to the existing sweeps. Cadence: every 30s by default; configurable.

**Verify:** Scenario test in E6.

### E4. Resume dispatch: handle `parked → running` and populate `resume_context`

**Files:**
- `foundation/integration/runner_acquire.go`
- `foundation/integration/runner_dispatch.go`

**Steps:**

1. Read both files to find the existing dispatch flow.
2. The dispatch path checks the `worker_request.phase` and the node's `last_outcome` before dispatching. Add a branch: if the node's state is `parked` and the row is being acquired (either by sweep or by an external invalidate against a parked node), the dispatch path:
   - Reads the persisted `parked_payload_handle` / `parked_payload_inline` and `session_token` to construct `ResumeContext`.
   - Sets the new `resume_reason` field appropriately: `"deadline_elapsed"` when triggered by `SweepParkedNodes`, `"external_invalidate"` when triggered by an invalidate against a parked node.
   - Calls the executor with `ExecuteRequest{ ..., resume_context: <constructed> }`.
3. After successful dispatch, clears the parked metadata (`parked_at`, `resume_at`, `parked_payload_handle`, `parked_reason` set to NULL — keep `session_token` until terminal in case of re-park; or null it too if the executor's contract is "pass-through-once").

**Verify:** Scenario test in E6.

### E5. `max_retries_without_progress` cap

**Files:**
- `foundation/integration/on_error.go`
- `foundation/integration/runner_terminal_errors.go`

**Steps:**

1. Read the existing error-policy chain (`retry | invalidate(targets) | give_up`) in `on_error.go`.
2. Use the `consecutive_retries_no_progress` column added in migration C5. The retry handler increments the counter on retry; the terminal handler resets it on any `last_outcome` change.
3. When the counter exceeds the effective `max_retries_without_progress` value (resolved as: per-node value from `rimsky_worker_request.max_retries_without_progress` if non-NULL; else the deployment default from `scheduler.max_retries_without_progress` config, default 100; a per-node value of 0 disables the cap entirely), force `Errored { error_class: "retry_loop_no_progress" }` instead of retry.
4. Add deployment-level config: extend the rimsky.yml `scheduler` block with `max_retries_without_progress: <int>` (default 100). Read into a typed config struct at scheduler startup.

**Verify:** Scenario test in E6.

### E6. Scenario tests for parked-state lifecycle and retry cap

**Files:**
- `test/scenarios/parked_lifecycle_test.go` (new)
- `test/scenarios/retry_loop_cap_test.go` (new)

**Steps:**

1. Use the existing scenario-test pattern (testcontainers + `modeling/scenario.Start`).
2. `parked_lifecycle_test.go` covers:
   - Executor emits `ParkRequested` with `resume_at` 100ms in the future. Node transitions to parked. After resume_at, `SweepParkedNodes` wakes it. Executor receives `ResumeContext` with correct payload/session_token/resume_reason.
   - Executor emits `ParkRequested` with no `resume_at` (indefinite). External call to `POST /admin/instances/{instance}/nodes/{node_id}/invalidate` (G3) wakes it. Resume reason is `external_invalidate`.
   - Executor emits `ParkRequested` with `max_park_duration` set on the node. After overrun, watchdog transitions to failed with `error_class: "park_timeout"`.
   - Empty `reason` is permitted (logs WARN; node transitions to parked normally).
   - **Held-claim retention across park boundary:** node A acquires a held claim on first dispatch, parks, then resumes. Verify the `rimsky_claim_handle` row is unchanged across the park boundary and not affected by the orphan-claim reaper. After resume, the node still holds the same claim.
   - **Auto-terminal handling for parked-then-resumed:** node A is part of a holding subgraph. A parks mid-run, B and C complete normally, A resumes and completes. `auto_terminal.go::CheckAndFireResolution` should fire exactly once when A completes (not when A parks). Verify the held claim is committed at that point (success aggregate outcome).
   - **Intra-graph invalidate-against-parked:** node B emits a named event whose `on_event` handler invalidates a parked node A. Verify A wakes with `resume_reason: "external_invalidate"`. (Tests the unified-path requirement that handler-emitted invalidates resume parked nodes.)
3. `retry_loop_cap_test.go` covers:
   - Node retries 100 times with no `last_outcome` change → forced failure with `error_class: "retry_loop_no_progress"`.
   - Node retries 50 times then `last_outcome` changes (e.g., new error_class) → counter resets; another 100 retries proceed.
   - Per-node override `max_retries_without_progress: 0` → infinite retries permitted (no cap).

**Verify:** `go test ./test/scenarios/ -run TestParkedLifecycle -count=1`, `go test ./test/scenarios/ -run TestRetryLoopCap -count=1`. Both pass.

---

## Section F — Modeling: template DSL extensions

### F1. `on_event` parsing in template canonical schema

**Files:**
- `modeling/node/template.go` (the `Node` spec struct lives here; confirmed by grep)
- `modeling/node/template_validator.go` (validation helper)
- `modeling/template/canonical/jcs.go` (canonical-hash logic — verify the new fields are included in the canonical hash)

**Steps:**

1. Read `modeling/node/template.go`. Find the `Node` spec struct with the existing four lifecycle handler slots (`OnAcquireUnavailable`, `OnExecutorComplete`, `OnExecutorBlocked`, `OnExecutorErrored`).
2. Add a new field to the `Node` spec:
   ```go
   OnEvent map[string]EventHandler `json:"on_event,omitempty"`
   ```
   Where `EventHandler` is:
   ```go
   type EventHandler struct {
       Resolve     string         `json:"resolve,omitempty"`     // "pass" | "retry" | "error" | (default: do nothing)
       ErrorClass  string         `json:"error_class,omitempty"` // required if resolve=error
       Invalidate  *InvalidateSpec `json:"invalidate,omitempty"`
   }

   type InvalidateSpec struct {
       Targets []string `json:"targets"`             // node types
       Frame   string   `json:"frame,omitempty"`     // "in" | "next" (default: next)
   }
   ```
3. The parser handles both the existing four lifecycle slots (unchanged) and the new `on_event` map. Underlying machinery may unify them later; preserve the surface for now.

**Verify:** Add a parse-roundtrip test in the same package: a YAML template with `on_event` parses, JCS-canonicalizes deterministically, and round-trips through the canonical hash.

### F2. `max_park_duration` per-node DSL field

**Files:**
- Same node-spec file as F1.

**Steps:**

1. Add to the `Node` spec struct:
   ```go
   MaxParkDuration string `json:"max_park_duration,omitempty"` // duration string like "24h"; empty means unbounded
   ```
2. This is a top-level node field, sibling to `on_event` and the existing handler slots. Not nested inside any event entry.
3. Parse via `time.ParseDuration` at template-registration time; reject invalid values.

**Verify:** Roundtrip test in the same parse test as F1.

### F3. `max_retries_without_progress` per-node DSL field

**Files:**
- Same node-spec file.

**Steps:**

1. Add:
   ```go
   MaxRetriesWithoutProgress *int `json:"max_retries_without_progress,omitempty"` // pointer for tri-state: nil=use default, 0=disable cap, N>0=use N
   ```
2. Parsed; flowed into the worker_request at dispatch (added to a sibling field on `worker_request` denormalized for the sweep).

**Verify:** Same roundtrip test.

### F4. Event substitution source kind

**Files:**
- `modeling/attribute/substitution.go`
- `modeling/attribute/substitution_test.go`

**Steps:**

1. Read `modeling/attribute/substitution.go` to find the existing source-kind dispatch (`source: nodes.<dep>.value.<path>`, `source: params.<path>`, etc.).
2. Add a new source kind: `source: nodes.<emitter_node>.event.<event_name>.<json_path>`. Resolution:
   - Look up the most recent emission of `(emitter_node, event_name)` in the per-instance event ledger (the cascade ledger — see F5 for the storage shape).
   - Walk the JSON path through the payload bytes via the existing `walkPath` mechanism.
   - If no event has been emitted, return the substitution-default (typically nil; the schema's `default` directive governs the fallback).
3. Annotate the new dispatch branch with `@source: modeling/attribute/substitution.go::walkPath` to make the provenance explicit (per spec invariant-interaction note).

**Verify:** Add tests covering: simple field extraction, nested path, missing event returns default, multiple emissions return most recent.

### F5. Event ledger storage helpers

**Files:**
- `foundation/persistence/events.go` (existing — extend, or add a sibling `node_events.go` for clarity)
- `foundation/persistence/postgres/events.go` (existing — extend or add `node_events.go`)
- `foundation/persistence/sqlite/events.go` (existing — extend or add `node_events.go`)

**Steps:**

1. Read the existing event-ledger files. The new ledger table `rimsky_node_events` is created by migration C6; this task adds Go-side helpers.
2. Add helpers:
   ```go
   type NodeEventsStore interface {
       Insert(ctx context.Context, evt NodeEvent) error
       LatestByName(ctx context.Context, instanceID, emitterNodeID, eventName string) (*NodeEvent, error)
       DeleteByInstance(ctx context.Context, instanceID string) (int64, error) // for instance-termination cleanup
   }

   type NodeEvent struct {
       ID                   int64
       InstanceID           string
       EmitterNodeID        string
       EventName            string
       PayloadInline        []byte    // exactly one of inline / handle is set
       PayloadHandle        string
       PayloadHandleBackend string
       EmittedAt            time.Time
       FrameID              string
   }
   ```
3. The `Insert` helper applies blob spill: if `len(PayloadInline) > spillThreshold`, write through `BlobBackend` and populate the handle fields instead.
4. The `LatestByName` helper queries by `(instance_id, emitter_node_id, event_name) ORDER BY emitted_at DESC LIMIT 1` and resolves blob payloads via the configured backend.
5. The `DeleteByInstance` helper is called when an instance is terminated; it sweeps all event rows for that instance and queues blob handles for orphan reaping (insert into `rimsky_blob_orphans`).

**Verify:** Round-trip tests in `node_events_test.go` for both backends. Substitution tests in F4 exercise `LatestByName` end-to-end.

### F6. Cross-validate `on_event` handler names against `Capabilities.declared_events`

**Files:**
- Wherever templates are validated at registration (likely `modeling/controlapi/templates.go` or a sibling validator)

**Steps:**

1. At template registration:
   - Read the executor's cached `Capabilities` (fetched at peer-connection time per A7).
   - For each node referencing this executor, validate that every key in the node's `on_event` map appears in `Capabilities.declared_events`.
   - Reject the registration with a clear error: `"node <type>: on_event references undeclared event '<name>' (executor <name> declares: [...])"`.
2. The reserved names used by rimsky for the four lifecycle slots (`__rimsky.executor_complete`, etc.) are implicit; they are not subject to this validation because they are not in `on_event` (they remain in their named handler slots).

**Verify:** Add a validation test that asserts: a template with an unknown event name is rejected; a template with all known event names is accepted.

### F7. Cross-validate userdata against `Capabilities.userdata_schema`

**Files:**
- Same template-validation path as F6.
- A new helper `modeling/template/userdata_validation.go` containing the JSON Schema validation logic.

**Important context (recent commit 5f702ee):** rimsky now supports per-instance `userdata_overrides` on instance creation (`POST /instances` body field `userdata_overrides: {by_executor: {...}, by_node: {...}}`). At dispatch time, the existing pipeline does: template userdata → merge `by_executor` overrides → merge `by_node` overrides (most-specific wins) → run `{{...}}` substitution → dispatch. The merge logic lives in `foundation/integration/userdata_overrides.go` (`applyUserdataOverrides`) and uses `modeling/shared/jsonmerge.go` for deep-merge. This task's schema validation MUST run after both the merge and the substitution steps so the validated bytes are exactly what the executor will receive.

**Steps:**

1. Use `github.com/santhosh-tekuri/jsonschema/v5` per the pre-resolved decision in the plan header. Add to `go.mod`.
2. At template registration: parse `Capabilities.userdata_schema` (if non-empty); for each node referencing this executor, validate that node's *template-level* `userdata` (without yet running merge or substitution) against the schema. Reject with a clear error. This catches schema violations baked into the template even if no override would be applied later.
3. At dispatch: re-validate the *resolved-and-merged* userdata against the same schema. The validation point is **after** `applyUserdataOverrides` and **after** substitution resolves any `{{...}}` directives — i.e. validate the final bytes that will be marshaled into `ExecuteRequest.userdata`. Failures route through `Errored { error_class: "userdata_validation_failed" }` to the node's `on_executor_errored` handler.
4. The startup flag `--ignore-missing-refs` skips registration-time validation but never dispatch-time.
5. Override fragments themselves are opaque to rimsky per `@blessed-invariant 11` — the validation reads the merged result via the same standard substitution-leaf machinery, never inspecting fragment values outside the schema-validation pass.

**Verify:** Tests covering: schema-conformant template userdata accepts at registration; non-conformant template userdata rejects at registration; an instance with `userdata_overrides` that produce a schema-conformant final userdata succeeds at dispatch; an instance with `userdata_overrides` that produce a non-conformant final userdata fails at dispatch with `userdata_validation_failed`; substitution-introduced shape error rejects at dispatch.

---

## Section G — Modeling: control-API endpoints

### G1. `GET /admin/diagnostics/held-frames`

**Files:**
- `modeling/controlapi/admin_diagnostics.go` (new)
- `modeling/controlapi/admin_diagnostics_test.go` (new)
- `modeling/controlapi/app.go` (route wiring)

**Steps:**

1. Read `modeling/controlapi/app.go` to see how admin routes are wired.
2. Add the route:
   ```go
   r.Route("/admin/diagnostics", func(r chi.Router) {
       r.Get("/held-frames", a.heldFrames)
       r.Get("/parked-nodes", a.parkedNodes)
   })
   ```
3. Implement `heldFrames` to query frames currently in `held` state, returning:
   ```json
   {
     "frames": [
       {
         "frame_id": "...",
         "instance_id": "...",
         "node_ids": ["..."],
         "held_since": "2026-05-08T12:00:00Z",
         "node_states": [
           {"node_id": "...", "state": "parked", "reason": "human_review"}
         ]
       }
     ]
   }
   ```
4. Auth: standard admin perimeter (whatever the existing admin routes use — read `modeling/controlapi/auth.go`).

**Verify:** Test that creates an instance, parks a node, hits the endpoint, asserts the frame is present.

### G2. `GET /admin/diagnostics/parked-nodes`

**Files:**
- Same as G1.

**Steps:**

1. Implement `parkedNodes`:
   ```json
   {
     "parked_nodes": [
       {
         "instance_id": "...",
         "node_id": "...",
         "parked_at": "2026-05-08T12:00:00Z",
         "resume_at": "2026-05-08T12:30:00Z",
         "reason": "rate_limit"
       }
     ]
   }
   ```
2. Optional query param `?reason=<name>` filters by parked_reason.

**Verify:** Test parallel to G1.

### G3. `POST /admin/instances/{instance}/nodes/{node_id}/invalidate`

**Files:**
- `modeling/controlapi/admin_node_invalidate.go` (new)
- `modeling/controlapi/admin_node_invalidate_test.go` (new)
- `modeling/controlapi/app.go` (route wiring)
- `foundation/cascade/cascade_invalidate.go` (the existing invalidate handler — extend to handle parked state)

**Steps:**

1. Add route under the existing instance/admin routes:
   ```go
   r.Post("/admin/instances/{instance}/nodes/{node_id}/invalidate", a.adminInvalidate)
   ```
2. The handler delegates to a unified invalidate-handler function (next step) that all invalidate sources share — admin endpoint, on_event handler-emitted invalidates, scheduled-node force-fire, etc.
3. Modify (or wrap) the existing invalidate handler at `foundation/cascade/cascade_invalidate.go` so that it dispatches by node state:
   - If `parked`: trigger resume — read the persisted park metadata, construct `ResumeContext` with `resume_reason: "external_invalidate"`, transition `phase` from `'parked'` back to `'active'`, transition node state from `parked` to `running`, re-dispatch with the resume_context. This is the same code path `SweepParkedNodes` uses; extract a shared helper (e.g. `wakeParkedNode(ctx, persist, nodeID, reason)`) used by both.
   - If `fresh`: standard invalidate (state → stale; cascade engine schedules a fresh dispatch).
   - If `running` or `failed`: reject with 409 Conflict and a clear error message ("admin invalidate is valid only for parked or fresh states; node is in <state>").
4. Body shape: empty (no payload). The endpoint is a pure trigger; payload-bearing invalidates use the named-event mechanism instead (where the payload flows through the event ledger).
5. The unified handler is reused by the on_event handler dispatch in H2 — the same wakeup behavior applies whether the invalidate originates from the admin endpoint or from a handler.

**Verify:** Test covering all three state branches: parked → resumes; fresh → invalidates; running → 409.

### G4. Existing `force-fire` endpoint compatibility

**Files:**
- `modeling/controlapi/admin_force_fire.go`
- `modeling/controlapi/admin_routes_test.go` (the existing test file for admin routes)

**Steps:**

1. Read the existing `force-fire` endpoint and `admin_routes_test.go`. Document (in code comment + operator-guide.md) that `force-fire` remains scheduled-node-specific; the new `/admin/instances/{instance}/nodes/{node_id}/invalidate` is the general-purpose admin invalidation surface.
2. No code change required unless force-fire's behavior overlaps; verify they are distinct.

**Verify:** `go test ./modeling/controlapi/ -run TestAdminRoutes -count=1` passes (covers all admin routes including force-fire).

---

## Section H — Modeling: scheduler integration for events and on_event handlers

### H1. Event-emission processing in supervisor terminal pipeline

**Files:**
- `foundation/integration/runner.go` or `runner_dispatch.go` (the gRPC-stream consumer)
- `foundation/integration/callback.go` (the async-callback HTTP handler)

**Steps:**

1. In the gRPC-stream consumer, when an `Event` is received (non-terminal):
   - Persist a row to `rimsky_node_events` via the helper from F5, with the event's name, payload (spilling if needed via the configured BlobBackend), and `frame_id` taken from the current dispatch's frame context (the dispatch context already carries the frame id via existing machinery).
   - Process any matching `on_event` handlers on the emitting node — handler-emitted invalidates fire via the unified invalidate handler from G3 (which means they correctly resume parked targets, fresh-invalidate fresh targets, etc.), per the handler's `frame:` setting.
2. In the async-callback handler at `foundation/integration/callback.go`, update the JSON-body parser:
   - Try to parse the body as `AsyncCallbackBody` (the new shape from A5: `{events: [...], terminal: {...}}`).
   - If that parse fails (e.g. missing `terminal` field, presence of legacy `type` field), fall back to the legacy shape parser.
   - The fallback is determined by attempting the new-shape parse; the legacy parser is the existing code path. Keep both indefinitely.
3. After parsing, process events from the body's `events` array (if present) the same way as the gRPC streaming path (persist, fire handlers); then process the terminal verdict (`complete | blocked | errored | park_requested`) via the existing terminal-handler dispatch.

**Verify:** Scenario test that an executor emitting events both via streaming and via async callback results in the same persisted state and handler firings. Test both new-shape and legacy-shape callback bodies parse correctly.

### H2. `on_event` handler dispatch

**Files:**
- `foundation/integration/runner_terminal_handlers.go` (extend)

**Steps:**

1. The existing handler dispatch handles `on_executor_complete/blocked/errored` and `on_acquire_unavailable`. Add a parallel path for `on_event`:
   - When an event is processed (H1), look up the emitting node's template spec for an `on_event[<name>]` entry.
   - If present, apply the handler: emit any declared invalidates with the event payload made available to the target nodes' substitution via the new substitution source kind from F4.
2. The handler-emitted invalidates carry the event payload's identity (the event-ledger row id), not the bytes themselves. Substitution at the target's dispatch time reads from the ledger.

**Verify:** Scenario test in H3.

### H3. Scenario tests for on_event lifecycle

**Files:**
- `test/scenarios/on_event_test.go` (new)

**Steps:**

1. Test cases:
   - Node A (stub executor) emits a named event during run, then completes. Node B has `on_event.<name>: { invalidate: { targets: [B] } }`. Verify B is invalidated and the event payload is substituted into B's attributes via `nodes.A.event.<name>.<path>`. Verify the persisted `rimsky_node_events` row has the correct `frame_id` matching the dispatching frame.
   - Async-callback path (new shape): executor emits AsyncAccepted, then POSTs `{events: [...], terminal: {...}}` to the callback URL. Verify same outcome as gRPC path.
   - Async-callback path (legacy shape): executor POSTs `{type: "complete", ...}` (no events array). Verify legacy shape still parses and processes correctly.
   - Validation: a template with `on_event` referencing an undeclared event name (not in the executor's `Capabilities.declared_events`) fails registration.
   - Multiple emissions of the same event name on the same node return the most-recent payload via substitution.

**Verify:** `go test ./test/scenarios/ -run TestOnEvent -count=1` passes.

---

## Section I — Prometheus metrics

### I1. `/metrics` endpoint on each rimsky process

**Files:**
- `cmd/rimsky-scheduler/main.go`
- `cmd/rimsky-supervisor/main.go`
- `cmd/rimsky-control-api/main.go`
- `modeling/observability/metrics.go` (new) — shared registration helpers

**Steps:**

1. Create `modeling/observability/metrics.go` defining a small wrapper around `expvar` or `prometheus/client_golang` for the metric set. Prefer the official Prometheus client (`github.com/prometheus/client_golang/prometheus` and `prometheus/promhttp`) to minimize rolling-our-own — adding it is justified by direct use and Prometheus is the de facto standard.
2. Add to `go.mod`. Note: this is a new third-party dep; cross-check `.golangci.yml` `depguard` rules to ensure it's permitted in `modeling/`.
3. In each cmd binary's `main.go`, after the existing HTTP server starts (or, for the supervisor which has the callback HTTP server, alongside it), expose `/metrics` via `promhttp.Handler()` on a separate port (default 9090; configurable via `metrics.port` env var or YAML).

**Verify:** Build each binary; curl `/metrics` returns Prometheus text format.

### I2. Initial metric instrumentation set

**Files:**
- Throughout `foundation/integration/`, `modeling/scheduler/`, and `modeling/controlapi/`. Add metric increments at key code points.

**Steps:**

1. Define metrics in `modeling/observability/metrics.go`:
   ```go
   var (
       Dispatches = prometheus.NewCounterVec(...)            // labels: executor, terminal_class
       TerminalVerdicts = prometheus.NewCounterVec(...)      // labels: terminal_class, error_class
       Invalidates = prometheus.NewCounterVec(...)           // labels: source_kind ("scheduler", "handler", "admin", "lifecycle")
       ClaimAcquisitions = prometheus.NewCounterVec(...)     // labels: producer_name, intent
       NodesByState = prometheus.NewGaugeVec(...)            // labels: state
       ParkedByReason = prometheus.NewGaugeVec(...)          // labels: reason
       HeldFrames = prometheus.NewGauge(...)
       DispatchQueueDepth = prometheus.NewGauge(...)
       DispatchLatencySeconds = prometheus.NewHistogramVec(...) // labels: executor
       ClaimAcquisitionLatencySeconds = prometheus.NewHistogramVec(...)
       FrameDurationSeconds = prometheus.NewHistogram(...)
       ParkedDurationOnResumeSeconds = prometheus.NewHistogram(...)
   )
   ```
   All names follow Prometheus conventions: lowercase with underscores, `rimsky_` prefix on the metric name (not the variable name), units suffix where applicable (`_seconds`, `_total` for counters when registered with `MetricsName` getting `_total` automatically).
2. Increment counters and update gauges at the right code points: dispatch start/end, terminal verdict, invalidate processing, claim acquisition, parked-node sweep wake-up.
3. Gauges (`NodesByState`, `ParkedByReason`, `HeldFrames`, `DispatchQueueDepth`) are updated by a periodic refresher goroutine (every 5s) that queries the persistence layer.

**Verify:** Manually curl `/metrics` after running a small workload; expected counters and gauges present with non-zero values.

### I3. Metrics tests

**Files:**
- `modeling/observability/metrics_test.go` (new)

**Steps:**

1. Smoke test: register all metrics, increment each once, scrape via `httptest`, parse the response, assert all expected metric names are present.

**Verify:** `go test ./modeling/observability/ -count=1` passes.

---

## Section J — claude-agent reference executor

The TS executor lives at `executors/claude-agent/`. All work in this section is in TypeScript.

### J1. Userdata schema declaration via `Capabilities`

**Files:**
- `executors/claude-agent/src/main.ts` or wherever `GetCapabilities` is implemented (read the existing entry point)
- `executors/claude-agent/src/userdata-schema.ts` (new) — the JSON Schema definition

**Steps:**

1. Create `userdata-schema.ts` exporting the JSON Schema for claude-agent's userdata. The schema uses draft 2020-12 and covers:
   ```typescript
   export const userdataSchema = {
     $schema: "https://json-schema.org/draft/2020-12/schema",
     type: "object",
     properties: {
       cli: {
         type: "object",
         properties: {
           model: { type: "string" },
           system_prompt: { type: "string" },
           user_prompt_template: { type: "string" },
           allowedTools: { type: "array", items: { type: "string" } },
           disallowedTools: { type: "array", items: { type: "string" } },
           tools: { type: "array", items: { type: "string" } }, // friendlier alias for allowedTools
           permissionMode: { type: "string", enum: ["default", "ask", "deny"] },
           max_schema_corrections: { type: "integer", minimum: 0, default: 3 },
           handle_rate_limits: { type: "boolean", default: true },
           mcpServers: {
             type: "array",
             items: {
               oneOf: [
                 { type: "object", required: ["ref"], properties: { ref: { type: "string" }, config: { type: "object" } } },
                 { type: "object", required: ["name", "transport"], properties: {
                     name: { type: "string" },
                     transport: { type: "string", enum: ["http", "stdio", "module", "http-loopback"] },
                     url: { type: "string" },
                     headers: { type: "object" },
                     command: { type: "string" },
                     args: { type: "array", items: { type: "string" } },
                     env: { type: "object" },
                     module: { type: "string" },
                     config: { type: "object" },
                     lifetime: { type: "string", enum: ["persistent", "per-dispatch"] }
                 }}
               ]
             }
           }
         }
       }
     },
     additionalProperties: false
   } as const;
   ```
2. In the `Capabilities` handler, return this schema in the `userdata_schema` field as serialized JSON bytes.
3. Add `declared_events: []` initially (no events emitted by claude-agent today; rate-limit auto-park uses `ParkRequested`, not events). If any event names get added in J12, add them here.

**Verify:** `cd executors/claude-agent && npm run build && npm test`.

### J2. MCP catalog loader from startup config

**Files:**
- `executors/claude-agent/src/mcp-catalog.ts` (new)
- `executors/claude-agent/src/mcp-catalog.test.ts` (new)
- `executors/claude-agent/src/main.ts` (wire in)

**Steps:**

1. Define the catalog config shape:
   ```typescript
   export interface McpCatalogConfig {
     mcp_catalog: Record<string, CatalogEntry>;
     policy: {
       allow_inline: boolean;          // default false
       allow_modules_from: string[];   // glob patterns; empty = disable module/http-loopback
     };
   }

   export type CatalogEntry =
     | { transport: "http"; url: string; headers?: Record<string, string> }
     | { transport: "stdio"; command: string; args?: string[]; env?: Record<string, string>; lifetime?: "persistent" | "per-dispatch" }
     | { transport: "module"; module: string; lifetime: "per-dispatch" }
     | { transport: "http-loopback"; module: string; lifetime: "per-dispatch" };
   ```
2. Loader reads from `claude-agent`'s startup config. Read `executors/claude-agent/src/main.ts` and `cli-env.ts` first to find the existing config-loading pattern; extend it with the new `mcp_catalog` and `policy` blocks. If no startup-config-file mechanism exists today, introduce one driven by env var `CLAUDE_AGENT_CONFIG` (path to YAML/JSON) with default `/etc/claude-agent/config.yaml`. Document the choice in the per-executor docs (J12).
3. Resolve `${VAR}` env-var indirection in `headers` and `env` values at load time; never expose env-var references downstream.
4. Validate each entry against the schema (matches userdata schema's mcpServers shape conceptually).
5. Apply `policy.allow_inline`: if false and a userdata-time inline definition is provided (during dispatch in J3), reject the dispatch.
6. Apply `policy.allow_modules_from`: if a `module` or `http-loopback` entry's `module` field doesn't match any glob in the list, reject at startup.

**Verify:** Test loads a sample config, verifies catalog parsing, verifies inline rejection, verifies module-allowlist rejection.

### J3. Userdata-side MCP server resolution

**Files:**
- `executors/claude-agent/src/mcp-resolver.ts` (new)
- `executors/claude-agent/src/agent-run.ts` (wire into dispatch)

**Steps:**

1. At dispatch time, given the userdata's `cli.mcpServers` array:
   - For each entry: if it's a `{ ref: <name> }`, look up `<name>` in the loaded catalog. Reject with a clear error if absent. If `config:` is provided in the ref, merge into the catalog entry's effective config.
   - If it's an inline definition: check `policy.allow_inline`. If false, reject. Else use the inline definition directly.
2. Output: a list of resolved MCP server bindings ready for the four transport handlers (J4–J7).

**Verify:** Test that resolves named refs (with and without override config), inline (with policy on/off), and missing refs (correct rejection).

### J4. `http` transport handler

**Files:**
- `executors/claude-agent/src/mcp-transport-http.ts` (new)

**Steps:**

1. The HTTP transport is the simplest: claude-agent passes the URL and headers through to the Claude CLI's MCP server config (via `~/.claude/mcp.json` or `--mcp-server-config` flag — read existing claude-agent code to see how MCP is currently wired).
2. Generate a per-dispatch MCP config file (in a temp directory, deleted at run end) listing each resolved http-transport server with its URL and headers.

**Verify:** Test that produces correct mcp.json output for a sample http-transport entry.

### J5. `stdio` transport handler

**Files:**
- `executors/claude-agent/src/mcp-transport-stdio.ts` (new)

**Steps:**

1. For per-dispatch lifetime: the per-dispatch MCP config file lists `command` + `args` + `env` for each stdio entry. The Claude CLI spawns these as part of its own MCP machinery.
2. For persistent lifetime: claude-agent spawns the subprocess once per claude-agent process lifetime and exposes it on a known loopback port to all dispatches via http (effectively converting stdio-persistent to http internally). Document this mapping.
3. Manage child-process lifecycles: capture stderr to claude-agent's logs; reap on parent exit.

**Verify:** Test for both lifetimes.

### J6. `module` transport handler (alias for `http-loopback`)

**Files:**
- `executors/claude-agent/src/mcp-transport-module.ts` (new — thin shim)

**Steps:**

1. Per the pre-resolved decision in this plan's header, `module` is implemented as an alias for `http-loopback`. The shim's only role is to translate `transport: "module"` userdata entries into the same code path as `transport: "http-loopback"` entries. Same module loading, same loopback HTTP listener, same lifecycle.
2. Implement `mcp-transport-module.ts` as a 5-line wrapper: `export const handleModuleTransport = handleHttpLoopbackTransport;` (after J7 lands). The two-name surface is preserved in userdata for documentation clarity (template authors can express intent — "this is in-process tooling" — even when the wire path is identical).
3. Document this aliasing in `docs/executors/claude-agent/userdata.md` under the MCP-transports section (J12).

**Verify:** Test that asserts `module` and `http-loopback` userdata entries produce identical behavior given the same module reference.

### J7. `http-loopback` transport handler

**Files:**
- `executors/claude-agent/src/mcp-transport-loopback.ts` (new)

**Steps:**

1. At dispatch time, for each `http-loopback`-transport entry:
   - `import()` the module.
   - Create an MCP `Server` instance via `@modelcontextprotocol/sdk`.
   - Call `module.register(server, config)`.
   - Start a streamable-HTTP listener on a random local port (`127.0.0.1:0`) using `StreamableHTTPServerTransport`.
   - Write the loopback URL to the per-dispatch MCP config file as an `http`-transport entry from the Claude CLI's perspective.
   - Tear down the listener at dispatch end.

**Verify:** Test that spins up a sample module, dispatches, observes the agent calling tools through the loopback.

### J8. Validate-on-`report_complete` schema check

**Files:**
- `executors/claude-agent/src/internal-mcp-tools.ts` (existing — extend the `report_complete` handler)
- `executors/claude-agent/src/internal-mcp-tools.test.ts` (existing — extend tests)

**Steps:**

1. Read `internal-mcp-tools.ts` to find the `report_complete` handler.
2. After the handler receives `attributes_delta` and before it commits the terminal:
   - Validate `attributes_delta` against `attributes_schema` (which claude-agent receives in `ExecuteRequest`). Use the same JSON Schema library chosen for the rimsky-side validation (or the equivalent TS library — `ajv` is well-maintained and supports draft 2020-12).
   - If valid, proceed to terminal as today.
   - If invalid: do NOT call the terminal. Instead, return an error to the agent through the MCP tool result, and trigger the existing resume-with-prompt mechanism with a corrective prompt: `"Your report_complete call failed schema validation: <error>. Please correct the output and call report_complete again."`
3. Track the corrective-retry count per dispatch. When count exceeds `userdata.cli.max_schema_corrections` (default 3), commit `Errored { error_class: "schema_validation_failed" }`.

**Verify:** Test cases: schema-conformant `report_complete` succeeds; one validation failure followed by a corrected call succeeds; three failures in a row produce `Errored { error_class: "schema_validation_failed" }`.

### J9. Auto rate-limit handling — emit `ParkRequested`

**Files:**
- `executors/claude-agent/src/cli-runner.ts` (existing — extend rate-limit handling)
- `executors/claude-agent/src/agent-run.ts` (terminal emission)

**Steps:**

1. Read `cli-runner.ts` to find the existing 429 / rate-limit handling. The CLI typically reports rate-limit with a specific error pattern.
2. When `userdata.cli.handle_rate_limits` is true (default), on rate-limit detection:
   - Capture the CLI's session_id from its output (claude-agent already tracks this for resume-with-prompt; reuse).
   - Capture the rate-limit reset timestamp from the CLI's error output.
   - Emit `ParkRequested { reason: "rate_limit", resume_at: <reset_ts>, session_token: <session_id>, payload: {} }` instead of `Errored` or `Complete`.
3. The supervisor processes `ParkRequested`, persists, and the CLI session sticks around (claude-agent process exits cleanly after emitting; on resume, it gets `ResumeContext` and re-launches the CLI with `--resume <session_id>`).

**Verify:** Test that mocks a 429 response and asserts `ParkRequested` is emitted with the correct fields.

### J10. Resume with `ResumeContext`

**Files:**
- `executors/claude-agent/src/agent-run.ts`
- `executors/claude-agent/src/cli-runner.ts`

**Steps:**

1. When `ExecuteRequest.resume_context` is non-empty:
   - Extract `session_token` and `payload`.
   - If `session_token` is set: launch the Claude CLI with `--resume <session_token>`.
   - If `payload` is set: expose it to the prompt-template engine as `{{rimsky.resume_payload}}` (template authors can opt to use it; default behavior is to ignore).
   - The `resume_reason` field is exposed as `{{rimsky.resume_reason}}` similarly. Both are template-author-visible context, never auto-injected into prompts.

**Verify:** Test resumes after a `ParkRequested` and asserts the CLI is invoked with `--resume`.

### J11. End-to-end test for claude-agent's new lifecycle

**Files:**
- `executors/claude-agent/src/lifecycle.e2e.test.ts` (new)

**Steps:**

1. Test scenarios:
   - Dispatch with userdata that includes a stub MCP catalog entry (http transport pointing at a test server). Verify the agent makes calls.
   - Dispatch that hits a simulated rate limit; verify `ParkRequested` is emitted; resume; verify CLI is launched with `--resume`.
   - Dispatch that calls `report_complete` with malformed output; verify the corrective resume-prompt fires up to 3 times, then commits `Errored`.

**Verify:** `cd executors/claude-agent && npm test -- lifecycle.e2e.test.ts` passes.

### J12. Userdata schema documentation

**Files:**
- `docs/executors/claude-agent/userdata.md` (new)

**Steps:**

1. Document every userdata field, its semantics, defaults, and an example template.
2. Cover the four MCP transports with worked examples (using generic illustrative names — `project-tracker`, `workspace-files`, `@project-alpha/tools`).
3. Document `policy.allow_inline` and `policy.allow_modules_from` operator settings.
4. Cover the rate-limit auto-park behavior.
5. Document `max_schema_corrections` and the corrective-retry behavior.

(This is a docs task; verification is "the file exists and reads coherently." See section M for review of all docs.)

**Verify:** `test -f docs/executors/claude-agent/userdata.md` and a quick grep ensures the file mentions every userdata field listed in J1.

---

## Section K — `mcp-servers/control-api/` bundled MCP shim

### K1. New module layout

**Files:**
- `mcp-servers/control-api/` (new directory)
- `mcp-servers/control-api/go.mod` (new — own Go module)
- `mcp-servers/control-api/main.go` (new)
- `go.work` (extend to include the new module)

**Steps:**

1. Create the new directory.
2. Initialize: `cd mcp-servers/control-api && go mod init github.com/fallguy/rimsky/mcp-servers/control-api`.
3. Add to root `go.work`:
   ```
   use ./mcp-servers/control-api
   ```
4. Add a basic `main.go` that starts a streamable-HTTP MCP server on a port (default 8081) and registers the tool set defined below.

**Verify:** `make build-all` succeeds with the new module.

### K2. Streamable-HTTP MCP server skeleton

**Files:**
- `mcp-servers/control-api/server.go`
- `mcp-servers/control-api/server_test.go`

**Steps:**

1. Per the pre-resolved decision, implement the JSON-RPC 2.0 wire protocol directly using `go-chi/chi` for HTTP routing and stdlib `encoding/json`. No third-party MCP SDK.
2. Implement an HTTP server at `mcp-servers/control-api/server.go`:
   - Accepts POST requests at `/mcp` with JSON-RPC 2.0 bodies.
   - Dispatches by `method` field: `initialize` returns the server's capabilities (protocol version, server info); `tools/list` returns the registered tool catalog; `tools/call` dispatches to the named tool's handler.
   - Holds a `controlapi.Client` (a thin HTTP client wrapping the rimsky control-API endpoints) pointed at the operator-configured rimsky control-API URL.
3. The wire-format details are: each request is `{jsonrpc: "2.0", id: <num|str>, method: <str>, params: <obj>}`; each response is `{jsonrpc: "2.0", id: <same>, result: <obj>}` or `{jsonrpc: "2.0", id: <same>, error: {code, message, data?}}`. Streamable-HTTP transport supports SSE-style streamed responses for long-running tool calls — the control-api shim's tools are all synchronous, so plain JSON responses are sufficient.
4. Tool handlers in K3 are the substance.

**Verify:** Smoke test: send `initialize` and `tools/list` requests, assert correct responses. Use stdlib `httptest`.

### K3. Implement tool set wrapping control-API

**Files:**
- `mcp-servers/control-api/tools.go`
- `mcp-servers/control-api/tools_test.go`

**Steps:**

1. Implement each tool as a function `(ctx, args) → result`:
   - `template_list`, `template_get(hash)`, `template_register(spec_yaml)`, `template_deploy(hash)`, `template_undeploy(hash)`, `template_deregister(hash)`
   - `tag_list`, `tag_set(name, hash)`, `tag_delete(name)`
   - `instance_list`, `instance_get(id)`, `instance_create(template, instance_key?, params?, userdata_overrides?)`, `instance_terminate(id)`
   - `node_get(instance, node_id)`, `node_invalidate(instance, node_id)`
   - `force_fire_scheduled(node_id)`
   - `held_frames_list`, `parked_nodes_list(reason?)`

   The `userdata_overrides` argument on `instance_create` mirrors the body field landed in commit 5f702ee. Shape: `{by_executor: {<executor_name>: <obj>}, by_node: {<node_name>: <obj>}}`. The MCP shim passes it through unchanged to the control-API `POST /instances` body; the control-API validates routing keys and rejects unknowns.
2. Each handler:
   - Validates input against a JSON Schema using `github.com/santhosh-tekuri/jsonschema/v5` (same library as F7).
   - Calls the corresponding control-API endpoint via the `controlapi.Client`.
   - Returns results in MCP tool-result format (typically `{content: [{type: "text", text: <json>}]}`).
3. Auth: the shim does not introduce its own auth; it forwards a configured operator credential (env var `CONTROL_API_TOKEN`) to the control-API.

**Verify:** Test each tool against a stub control-API.

### K4. Configuration and documentation

**Files:**
- `mcp-servers/control-api/config.go`
- `docs/mcp-servers/control-api/README.md` (new)

**Steps:**

1. Config: YAML or env-var driven. Required: `control_api_url`, `control_api_token`. Optional: `port` (default 8081), `bind_addr` (default `0.0.0.0`).
2. Docs: tool reference (every tool, args, return shape, examples), config reference, deployment shape (sidecar or separate container), security considerations.

**Verify:** `test -f docs/mcp-servers/control-api/README.md`.

---

## Section L — Conformance suites

### L1. New `rimsky-blob-backend-conformance` binary

**Files:**
- `cmd/rimsky-blob-backend-conformance/` (new)
- `cmd/rimsky-blob-backend-conformance/main.go`
- `cmd/rimsky-blob-backend-conformance/checks.go`

**Steps:**

1. Read `cmd/rimsky-claim-producer-conformance/` for the existing pattern (a small CLI that points at a target endpoint and runs a battery of compliance checks).
2. The blob-backend conformance suite operates against the `BlobBackend` interface (not over the wire — backends are in-process Go libraries). The binary takes a backend-config flag and instantiates the backend in-process to test:
   - Round-trip: write 1KB, read back, bytes match.
   - Round-trip: write 10MB, read back, bytes match.
   - Range read returns expected slice.
   - Delete then read returns `ErrBlobNotFound`.
   - Idempotent delete (delete twice, second is no-op).
   - Concurrent writes to different keys complete without errors.
3. Output: pass/fail per check; exit code 0 on all pass.

**Verify:** `go run ./cmd/rimsky-blob-backend-conformance --backend filesystem --root /tmp/blob-test` passes all checks.

### L2. Extend `rimsky-conformance` for new executor surfaces

**Files:**
- `cmd/rimsky-conformance/` (existing — extend)

**Steps:**

1. Add checks for:
   - Executor returns valid `userdata_schema` from `Capabilities` (parses as JSON Schema; empty schema is permitted).
   - Executor returns `declared_events` array (may be empty).
   - Executor that emits `ParkRequested` produces a valid wire record (empty `reason` is permitted but logged WARN at the supervisor side).
   - Executor that emits an `Event` produces a valid wire record (name in `declared_events`).
   - Async-callback path (new shape): executor that emits AsyncAccepted and then POSTs `{events: [...], terminal: {...}}` is accepted; events are persisted and processed before the terminal.
   - Async-callback path (legacy shape): executor that POSTs `{type: "complete", ...}` is still accepted.
2. Update the stub executor in `executors/stub/` to optionally emit events and `ParkRequested` based on input flags so the conformance suite can exercise these paths.

**Verify:** `go run ./cmd/rimsky-conformance --endpoint <stub> --transport grpc` passes including the new checks. `go run ./cmd/rimsky-conformance --endpoint <stub> --transport http+json` (if the existing suite supports HTTP+JSON bridge) also passes the async-callback path checks.

### L3. Conformance test for the new ledger semantics

**Files:**
- `test/scenarios/conformance_events_test.go` (new)

**Steps:**

1. Asserts that:
   - Events emitted via gRPC stream are persisted in `rimsky_node_events`.
   - Events emitted via async-callback body are persisted identically.
   - Substitution from `nodes.<emitter>.event.<name>.<path>` returns the expected payload value.
   - Multiple emissions of the same event name return the most recent payload.

**Verify:** `go test ./test/scenarios/ -run TestConformanceEvents -count=1` passes.

---

## Section M — Documentation

The spec calls out specific doc files to create or update. This section batches them.

### M1. New orchestrator concept pages

**Files (all new):**
- `docs/concepts/parked.md`
- `docs/concepts/handlers.md` (check first with `test -f docs/concepts/handlers.md`; extend if present; create if absent)
- `docs/concepts/x-as-executor.md`
- `docs/concepts/domain-stores.md`
- `docs/concepts/deterministic-transformations.md`
- `docs/concepts/operational-health.md`

**Steps:**

1. `parked.md` — what the parked state is, how to enter it (`ParkRequested`), how to leave (time-based via `resume_at`, signal-based via admin invalidate or in-graph invalidate, watchdog via `max_park_duration`), interaction with frames (held), `ResumeContext` semantics. Cover the "human review = indefinite park with reason" pattern. Include a dedicated subsection titled **"Antipattern: mid-frame human review"** explaining: blocking a frame on review serializes parallel work in the same frame and creates long-lived held frames; the recommended idiom is **post-frame review** (the producing frame runs to completion; review happens externally; a follow-on graph or instance kicks off post-review for downstream effects). Frame-blocking review is supported and works correctly, but should be reserved for cases where downstream genuinely cannot proceed safely without approval.
2. `handlers.md` — the four lifecycle slots plus `on_event`. DSL examples. Resolve verdicts (`pass`, `retry`, `error`, `by_changed`, `always_propagate`, `never_propagate`). Handler-emitted invalidate semantics.
3. `x-as-executor.md` — the design idiom: cross-system integration via wrapping pipelines as executors; named events for non-terminal signals; `ParkRequested` for awaiting external decisions; async callback for webhook-driven flows.
4. `domain-stores.md` — pattern for prompt context, learnings, examples, corrections, and similar persistent project state via project-built MCP servers wired into executor catalogs.
5. `deterministic-transformations.md` — pattern for post-processors (downstream deterministic nodes), confidence-driven branching, and agent self-blocks.
6. `operational-health.md` — lifecycle-subscriber peers, watchdog graphs, control-API polling, and the platform-level diagnostic endpoints (held-frames, parked-nodes).

**Verify:** All files exist; each contains at least 200 words and one example using generic illustrative names.

### M2. Update existing orchestrator docs

**Files (existence-check then extend; create if absent):**
- `docs/protocols/executor.md`
- `docs/concepts/attributes.md`
- `docs/concepts/error-policy.md`
- `docs/operator-guide.md`
- `docs/concepts/frames.md`
- The doc page covering `Blocked` semantics — locate via `grep -rln 'Blocked' docs/concepts/ docs/protocols/`. Likely `docs/concepts/blocked.md` or a section within `docs/protocols/executor.md`.

**Steps:**

1. `executor.md` — `Capabilities.userdata_schema`, `Capabilities.declared_events`, `Event` wire type, `ParkRequested` wire type, `ResumeContext` field, async-callback body shape (both new and legacy parsers documented).
2. `attributes.md` — new `nodes.<emitter>.event.<name>.<path>` substitution source kind. Note the walkPath provenance and that event payloads inherit the same opacity discipline as attribute values.
3. `error-policy.md` — `max_retries_without_progress` cap; default 100; per-node override (0 disables); deployment-level config in `scheduler.max_retries_without_progress`.
4. `operator-guide.md` — `persistence.blob.*` config block, `mcp_catalog` for claude-agent, `metrics.port` for Prometheus, the new diagnostic endpoints (`/admin/diagnostics/held-frames`, `/admin/diagnostics/parked-nodes`), the new admin invalidate endpoint, the `RIMSKY_PROCESS_ROLE` env var and its memory-backend gate, the `rimsky-blob-backend-conformance` binary.
5. `frames.md` — the `held` frame state (parked or pending nodes), interaction with `serial_queue` and `coalesce`.
6. `Blocked`-semantics doc — extend with a section titled **"Using `Blocked` as a routing signal"** explaining: an executor may emit `Blocked { reason, payload }` when it produced output but explicitly chose not to claim success — for example, low-confidence outputs that should route to human review. This is distinct from `Errored` (which means the executor failed). Templates can wire `on_executor_blocked: { resolve: pass, invalidate: { targets: [routing_node] } }` to handle the routing.

**Verify:** Files updated; `make lint` (which includes the docs-lint command — see `cmd/rimsky-docs-lint/` if present) passes.

### M3. Per-component doc surface bootstrapping

**Files:**
- `docs/executors/claude-agent/README.md` (new — index)
- `docs/executors/claude-agent/userdata.md` (already created in J12)
- `docs/executors/http-node/README.md` (new — for symmetry)
- `docs/stores/postgres/README.md` (new — for symmetry; if absent, create minimal docs)
- `docs/stores/filesystem/README.md` (new)
- `docs/stores/stub/README.md` (new)
- `docs/blob-backends/inline.md`, `pg-largeobject.md`, `filesystem.md`, `memory.md` (all new)
- `docs/mcp-servers/control-api/README.md` (already created in K4)

**Steps:**

1. Each per-component doc starts with: what the component is, when to use it, configuration, a minimal example, security/safety notes if any.
2. The index files (`README.md` per directory) list the components in that surface and link to their docs.

**Verify:** All files exist.

### M4. Design-philosophy framing in front matter

**Files:**
- `docs/concepts/design-philosophy.md` (new)
- `docs/README.md` (extend to link the new page)

**Steps:**

1. Write the "rimsky stays domain-agnostic" framing covered in the spec's Design Philosophy section. ~600 words.
2. Link from the docs index.

**Verify:** File exists; `docs/README.md` links to it.

### M5. Update CHANGELOG

**Files:**
- `CHANGELOG.md`

**Steps:**

1. Append entries under `## Unreleased` summarizing the major adds: pluggable blob backend, `ParkRequested` and parked state, named events, `on_event` handlers, claude-agent userdata schema and MCP catalog, control-api MCP shim, blob-backend conformance, Prometheus metrics, new diagnostic endpoints.

**Verify:** `git diff CHANGELOG.md` shows new entries.

### M6. Update CLAUDE.md gotchas (selectively)

**Files:**
- `CLAUDE.md`

**Steps:**

1. Add gotcha entries under "Non-obvious gotchas":
   - "Memory blob backend is dev-only; rejected at startup unless `RIMSKY_PROCESS_ROLE=unified` (set by `rimsky-entrypoint`). Same caveat as SQLite-only-for-dev."
   - "Parked nodes do not heartbeat; the orphan-claim reaper skips `phase='parked'` rows. Held claims persist across the park boundary."
   - "Event payloads are inert in rimsky in the same sense as attribute values: read only via walkPath substitution; never logged or transformed."
2. Update the state-list reference to include `parked` (was done in B4; verify it's consistent here).
3. Add a new numbered blessed invariant to the "Blessed invariants" section of CLAUDE.md (after the existing 20). Format matches the existing style:
   - **21. Blob content is inert in Rimsky.** Bytes spilled to a configured `BlobBackend` are read by rimsky only via the `walkPath` substitution path (the same exception that applies to inline attribute values per invariant 11) and the persistence-layer fetch on attribute read. Rimsky never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, hashes, indexes, pattern-matches, attaches to traces, or includes blob bytes in error messages. The implementation annotates the relevant code paths with `@blessed-invariant 21`. (Annotation locations: `foundation/persistence/blob.go` interface comment; per-backend `Read` impls; `foundation/persistence/node_attributes.go` read path.)

**Verify:** `git diff CLAUDE.md` shows the additions.

---

## Section N — Final integration verification

### N1. Build + test + lint across all modules

**Steps:**

1. From the repo root, run:
   ```
   make tidy
   make proto-gen
   make build-all
   make lint
   make test-all
   ```
2. Each must succeed cleanly. Fix any issue before moving on.

**Verify:** All four commands exit 0.

### N2. Scenario tests

**Steps:**

1. Run the full scenario suite (Docker required):
   ```
   go test ./test/scenarios/... -count=1
   ```
2. Including the new tests from E6, H3, L3.

**Verify:** Suite passes.

### N3. Race-detector run on hot paths

**Steps:**

1. ```
   go test ./foundation/integration/... ./modeling/scheduler/... ./modeling/controlapi/... -race -count=3
   ```

**Verify:** No race-detector warnings.

### N4. Conformance smoke

**Steps:**

1. Bring up the docker-compose stack:
   ```
   docker compose -f deploy/docker-compose.yml up -d
   ```
2. Run conformance against the bundled stub executor:
   ```
   go run ./cmd/rimsky-conformance --endpoint stub --transport grpc
   ```
3. Run conformance against the bundled claim-producers:
   ```
   go run ./cmd/rimsky-claim-producer-conformance --endpoint <each producer>
   ```
4. Run blob-backend conformance against each backend:
   ```
   go run ./cmd/rimsky-blob-backend-conformance --backend pg-largeobject --pg-conn-string ...
   go run ./cmd/rimsky-blob-backend-conformance --backend filesystem --root /tmp/blob-conformance
   go run ./cmd/rimsky-blob-backend-conformance --backend memory
   ```
5. Take the stack down:
   ```
   docker compose -f deploy/docker-compose.yml down
   ```

**Verify:** All conformance binaries exit 0.

### N5. Claude-agent test suite

**Steps:**

1. ```
   cd executors/claude-agent && npm install && npm test && npm run build
   ```

**Verify:** Tests pass; build emits dist/ artifacts.

### N6. mcp-servers/control-api test

**Steps:**

1. ```
   cd mcp-servers/control-api && go test ./... -count=1
   ```

**Verify:** Tests pass.

### N7. Documentation lint

**Steps:**

1. If `cmd/rimsky-docs-lint/` exists, run it:
   ```
   go run ./cmd/rimsky-docs-lint
   ```
2. If it doesn't exist or doesn't cover the new doc files, manually verify each new doc file is present and non-empty:
   ```
   for f in \
     docs/concepts/parked.md \
     docs/concepts/handlers.md \
     docs/concepts/x-as-executor.md \
     docs/concepts/domain-stores.md \
     docs/concepts/deterministic-transformations.md \
     docs/concepts/operational-health.md \
     docs/concepts/design-philosophy.md \
     docs/executors/claude-agent/userdata.md \
     docs/executors/claude-agent/README.md \
     docs/blob-backends/inline.md \
     docs/blob-backends/pg-largeobject.md \
     docs/blob-backends/filesystem.md \
     docs/blob-backends/memory.md \
     docs/mcp-servers/control-api/README.md
   do
     test -s "$f" || echo "MISSING OR EMPTY: $f"
   done
   ```
3. The script outputs nothing if all files are present and non-empty.

**Verify:** Script output is empty.

---

## Manual checks after completion

These are not part of the automated execution. The user runs through them after the implementation and code review are done.

1. Spot-read the new `docs/concepts/design-philosophy.md` — does it set the "rimsky stays domain-agnostic" lens clearly?
2. Spot-read `docs/executors/claude-agent/userdata.md` — does the userdata schema documentation look complete and easy to follow?
3. Bring up the docker-compose stack with the unified image (`rimsky/all`) using the `memory` blob backend — confirm it works for a small test instance.
4. Bring up the multi-process stack with `pg-largeobject` blob backend — submit a template that produces a 5MB attribute value, confirm it spills correctly and downstream nodes can read it.
5. Hit `/metrics` on each rimsky process and confirm the metric values look sane after a small workload.
6. Drive the bundled `mcp-servers/control-api/` shim from a Claude Code instance — confirm `template_register`, `instance_create`, `node_invalidate`, `held_frames_list`, `parked_nodes_list` all work as expected.
