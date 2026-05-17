# Data Platform Extensions — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`
**Goal:** Implement the full set of rimsky platform additions from the 2026-05-15 spec, end-to-end, in one execution: new `DataProcessing` / `Validation` / `Sensor` protocols; run-tree extension; recursive scope partitioning; unified message layer; first-class sub-graphs; claim co-holdership + lifetime + asset pattern; content lineage; parked-state taxonomy; backfills; bundled stores/executors/sensors/subscribers; conformance suites; scenario tests; design-log mutations.
**Architecture:** The work spans rimsky's three Go modules (`protocols/`, `foundation/`, root with `graph/` + `runtime/` + `control/`) plus new top-level directories `sensors/` and `subscribers/` and `examples/`. The core additions are: (1) three new protobuf services and three extended ones; (2) ~8 schema migrations (tables added, columns added, columns removed, columns renamed); (3) the run-tree data model on `rimsky_node_runs` with state aggregation; (4) a recursive claim-tree resolution mechanism extending `ResolveClaimHandleTerminal`; (5) a unified message queue + frame-boundary delivery; (6) sub-graph canonicalization (entry-node absorption, exit-node writeback carry-rule); (7) a `Validation` registration pipeline; (8) `Sensor` lifecycle (StartWatch/StopWatch/resync) and a sensor observation endpoint; (9) lineage projection from runs + claim commits; (10) bundled stores (`parquet-store`, `geo-parquet-store`, `geo-postgis-store`), verifier executors (`verifier-shape-checks`, `verifier-http`), sensors (`sensor-cron`, `sensor-http`, `sensor-object-store`, `sensor-webhook`), subscribers (`openlineage`), and one example (`atomic-staging-fs-producer`); (11) three new conformance binaries and extensions to two existing; (12) extensive scenario coverage; (13) retirement of `graph/qualityrule/`, the per-node `schedule:` field, `rimsky_schedules` table, and the `node-state` / `quality-rule` / `schedule` concept entries.
**Tech Stack:** Go 1.22+ (three modules; layout per `CLAUDE.md`); Postgres (`pgx/v5`) + SQLite (`modernc.org/sqlite`) via the existing pluggable driver in `foundation/persistence/`; gRPC + protobuf v3 for protocols; `go-chi/chi` for HTTP; `robfig/cron/v3` for cron parsing inside `sensor-cron`; `log/slog` (stdlib) for logging; `github.com/santhosh-tekuri/jsonschema/v5` for JSON-Schema static checks; testcontainers-go for Postgres + LocalStack scenario tests; Apache Arrow / Parquet libraries for the parquet stores; PostGIS Go drivers for `geo-postgis-store`; TypeScript / React / Vite for the dashboard reframe under `dashboards/`.

---

## Reference materials

The implementer MUST read these before starting:

- **Spec:** `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md` — authoritative requirements. Sections cited below by their `## …` headings (e.g. `spec §Persistence schema`).
- **Existing layered contracts** the spec extends:
  - `.ok-planner/specs/2026-05-04-foundation-contract.md`
  - `.ok-planner/specs/2026-05-04-modeling-layer-contract.md`
  - `.ok-planner/specs/2026-05-04-service-protocol-contract.md`
- **Repo orientation:** `CLAUDE.md` at repo root — module structure, depguard rules, blessed invariants, build commands, gotchas.
- **Cold-read conventions:** `.claude/rules/cold-read-cheatsheet.md` — one feature per file; ≤2 levels of dir nesting; ~500-line file / ~100-line function guidelines; max 3 nesting depth via early returns; `@source` / `@diverged` for tracked duplication; `@agent-contract` / `@blessed-invariant` blocks for shared infrastructure.
- **Project rules:** `.claude/rules/rules.md` — pre-v1 break-freely (drop-and-recreate migrations OK; no compat shims); fix every bug discovered; verify the build after every code change.
- **Citation grammar:** `.claude/rules/citation-grammar.md` — `code:` / `concept:` / `proto:` / `table:` / `invariant:` prefixes for prose. Used in implementation-notes file; NOT used inside the source code or in this plan's task instructions.
- **Concept catalog (live):** `.ok-planner/design/concepts/` — read entries the spec marks for mutation before editing them. Spec §Concept catalog impacts is the canonical list.

Existing blessed invariants the implementation MUST preserve through the refactor (numbering keeps; text updates per spec §Blessed-invariant updates):

- `invariant:1` — state machine rejects illegal transitions; new transitions for parents listed in spec §State machine.
- `invariant:2` — node-run claim brackets the running window (extended for sub-claims).
- `invariant:3` — multi-lock acquisition uses deterministic sorted order.
- `invariant:4` — claimant-guarded release on every claim_handle deletion and `claimed_by` nullification.
- `invariant:4b` — text update per spec.
- `invariant:5` — verify-before-run.
- `invariant:6` — orphan-claim cutoff `5 × heartbeat_interval`.
- `invariant:7` — advisory lock on scheduler tick.
- `invariant:8` — session advisory lock on migrations.
- `invariant:9a` / `invariant:9b` — lock state lives in persistence; producers don't internally serialize on lock-shaped predicates.
- `invariant:10` — text update per spec (acquisition atomic with parent + sub-claim inserts).
- `invariant:11` / `invariant:20` / `invariant:21` — userdata / claim-content / blob-content inert in rimsky.
- `invariant:12` — attributes validate twice.
- `invariant:13` — held-claim resolution single, locked, aggregate-outcome-driven.
- `invariant:15` — `Open` fires inside acquisition tx.
- New invariants added (spec §Blessed-invariant updates): held-durable persistence; exit-node-writeback carry; messages inert.

---

## Pre-resolved design decisions

Spec leaves a few choices unspecified or open. Resolved here so the implementer doesn't re-litigate:

- **JSON-Schema validator (Go):** `github.com/santhosh-tekuri/jsonschema/v5`. Used for static userdata-schema check at template registration and for Validation request validation. Same choice as the prior platform-extensions plan; consistency across the codebase.
- **S3 SDK:** `github.com/aws/aws-sdk-go-v2/service/s3`. Used by `sensor-object-store`.
- **OpenLineage emission:** HTTP POST to a configurable endpoint matching the OpenLineage 1.x JSON schema; we ship our own emitter rather than depending on `github.com/OpenLineage/openlineage-go`.
- **Migration ordering:** Each migration is its own numbered SQL file under `foundation/persistence/{postgres,sqlite}/migrations/`. The numbering picks up from the current highest. Pre-v1: drop-and-recreate where cleaner per `rules.md`.
- **Sub-graph encapsulation enforcement:** at canonicalization (rejection at registration), not at runtime.
- **`held_durable` orphan-reaper skip:** the existing orphan-claim reaper (`runtime/orphan_reaper.go`) adds a `WHERE held_durable = FALSE` clause; this is part of the new invariant (held-durable persistence).
- **Cancellation semantics for `strict.cancel_siblings: true`:** recursive Abandon walks through descendant claim-trees, bounded by claim-tree depth. The implementation re-uses `ResolveClaimHandleTerminal` with a forced `outcome: failed` short-circuit.
- **Frame-delivery mode default:** `coalesce` per spec; persisted on `rimsky_instances.frame_delivery_mode`.
- **`sensor-cron` advisory lock:** if operator deploys multi-replica, the bundled cron sensor uses its own Postgres advisory lock (`pg_try_advisory_lock(SENSOR_CRON_KEY)`) per watch. Single-replica is the documented default.
- **`producer_candidate_handle` placement:** on the sub-claim rows (rows with `parent_claim_handle_id NOT NULL`), not on the parent row. Spec is explicit; this resolves a question implicit in the schema.
- **Run-tree retention default:** `retention.recent_frames_kept = 100`; `retention.lineage_trailing = 30d`. Configurable in `rimsky.yml`.
- **`main` graph reservation:** rejected at canonicalization if `main` is declared with `entry:` or `exit:`. Rejection class `subgraph_main_has_entry_or_exit`.
- **Dashboard work is JS/TS only** — no Go backing API changes beyond what the asset / lineage / messages / backfill endpoints already provide.
- **Asset URL identity form:** the control-api uses the dotted form `{template_node_alias}.{claim_alias}` for the `{alias}` path parameter in `GET/DELETE/POST /instances/{id}/assets/{alias}` and adjacent routes. Per-instance namespacing per spec §Lifetime and the asset pattern / Asset section ("`{instance_id}.{asset_alias}` is the asset's canonical identity"); the URL form within an instance scope is the dotted `node_alias.claim_alias`.
- **OpenLineage subscriber transport:** polls `rimsky_lineage` for new records since a stored cursor. The alternative shape (subscribing to lifecycle events directly) is rejected for V1; polling decouples the subscriber from the lifecycle event surface and keeps it as a passive reader of the projection.
- **`sensors/` and `subscribers/` pgx access:** `sensor-cron` and `openlineage` both maintain their own state DBs (cursor + watch next-fire-at) and may use Postgres via pgx. The `.golangci.yml` `pgx-isolation` rule allowlist must be extended to cover `sensors/` and `subscribers/`. (Tasked in T2.)
- **Bundled reference stores cut (parquet / geo-parquet / geo-postgis):** Section H is cut in full. Rationale: a reference store that's worth shipping (predicate pushdown, schema evolution, partition discovery, row-group sizing, CRS handling, spatial-index strategy) is a meaningful engineering effort in its own right, and a naive one misleads users who copy it; specialized format stores belong with the users who need them, not bundled with the project-agnostic core. DataProcessing-aware self-test for M1/M2 conformance binaries uses an extension to the existing stub store; details in Section H. No follow-up dispatches will revive H.
- **`candidate_handle` wire path to the leaf executor:** the supervisor passes `producer_candidate_handle` to the leaf as one of the per-claim address fields inside `ExecuteRequest`. The existing `ExecuteRequest` carries a per-claim address structure (the `ClaimResult.address` from `ClaimProducer.Open`); extend that structure (proto-side, in `executor.proto`) with an optional `bytes candidate_handle` field rather than introducing a sibling top-level field — keeps the wire surface coherent with how the leaf already routes per-claim metadata.
- **Co-holder dispatch wiring:** at the co-holder's dispatch (a run whose template node declares `holds:`), the supervisor (a) INSERTs a `rimsky_claim_holders` row for each held claim with `holder_run_id = <this run>` + `state = active`, and (b) reads the upstream `rimsky_claim_handles.address` for each held claim and includes it in the leaf's `ExecuteRequest` alongside addresses for any `claims:`-acquired handles. Same wire shape as for acquired claims — the leaf cannot tell from `ExecuteRequest` whether a given claim was acquired or co-held.

---

## Conventions used in this plan

- Paths are relative to the repo root (the rimsky repository root, not the parent zonebase repo).
- "Read X, then add Y" tasks: implementer reads the current shape first; avoids embedding stale code in the plan.
- Make targets are `make proto-gen`, `make build-all`, `make test-all`, `make lint`, `make tidy` — all from repo root.
- For tests via testcontainers (scenario + storage-integration), Docker must be running.
- All new exported Go identifiers get standard godoc.
- All examples use generic illustrative names per `rules.md` (`project-alpha`, `analytics_production`, `items`, `category`); no real consumer terminology.
- Cold-read annotations (`@blessed-invariant`, `@agent-contract`, `@concept`, `@source`, `@diverged`) added inline at code sites the spec marks load-bearing.
- A running implementation notes file lives at `.ok-planner/plans/2026-05-15-data-platform-extensions-plan-notes.md`. The implementer appends discoveries, deviations, and items for post-run discussion there (use the citation grammar in that file).

---

## Critical path and dependencies

Tasks are organized in lettered sections. Sections may execute sequentially; within a section, tasks are ordered. Dependencies between sections:

- **A (protocol additions)** lands first; everything downstream consumes the regenerated bindings.
- **B (schema migrations)** lands second; foundation/persistence + runtime read the new columns.
- **C (foundation primitive types)** updates `foundation/spec/`, `foundation/cascade/`, `foundation/locks/`, `foundation/persistence/` Go-side types to match A + B.
- **D (template canonicalization)** picks up A + B + C; introduces the `graphs:` / `sensors:` / `delegate:` / `holds:` / `fan_out:` / `lifetime:` / `data:` DSL surfaces.
- **E (runtime — orchestration core)** picks up A + B + C + D; introduces run-tree state propagation, recursive claim-tree resolution, message delivery, sub-graph dispatch, fan-out dispatch, validation pipeline at registration, lineage writer.
- **F (control-API endpoints)** picks up E; the surface that operators / sensors / SDK adapters call.
- **G (CLI subcommands)** picks up F.
- **H (bundled stores)** **cut.** See Section H block + pre-resolved decisions. The DataProcessing surface is exercised via a stub-store extension.
- **I (bundled verifier executors)** independent after A.
- **J (bundled sensors)** independent after A; `sensor-cron` replaces the retired internal cron path, so it must land before the scheduler-tick cron path is removed in section P.
- **K (bundled openlineage subscriber)** picks up E (lineage writer fires the same events the subscriber consumes).
- **L (atomic-staging example)** picks up A + claim-handle extensions.
- **M (conformance binaries)** picks up A + E + I + J + K, plus the stub-store DataProcessing extension noted in Section H.
- **N (scenario tests)** picks up E + I + J + K + L (plus stub-store DataProcessing).
- **O (smoke test extension)** picks up M + N.
- **P (retirements)** picks up J (sensor-cron must exist) + I (verifier executors must exist) — the qualityrule package is removed only after verifier-shape-checks is operational.
- **Q (concept catalog mutations)** lands incrementally alongside the code per spec §Concept catalog impacts ("application timing"); a final pass at the end reconciles.
- **R (blessed-invariant updates)** mostly inline in source as code changes; a final pass updates the catalog under `CLAUDE.md`.
- **S (dashboard reframe)** picks up F (asset / lineage / messages / backfill endpoints exist).
- **T (documentation + cleanup)** last: `CHANGELOG.md`, `CLAUDE.md` gotchas, module-layout doc, dead-code sweep.

Linear execution order respecting these dependencies: A → B → C → D → E → F → G → I → J → K → L → M → N → O → P → Q → R → S → T. (H cut.)

If a task's verification fails, fix it before continuing. Do not commit; the user owns git.

---

# Section A — Protocol additions

The protocol layer changes land first; downstream sections depend on the regenerated bindings.

### A1. Extend `claim_producer.proto` with `SplitScope`, `ScopesConflict`, and `version_id` on `CommitResponse`

**Files:**
- `protocols/proto/v1/claim_producer.proto`

**Steps:**

1. Read the current file. Locate the `ClaimProducer` service definition and the `CommitResponse` message.
2. Add two new RPCs to the `ClaimProducer` service:

   ```proto
   rpc SplitScope(SplitScopeRequest) returns (SplitScopeResponse);
   rpc ScopesConflict(ScopesConflictRequest) returns (ScopesConflictResponse);
   ```

3. Add the corresponding messages:

   ```proto
   message SplitScopeRequest {
     string claim_handle_id = 1;       // rimsky-side claim_handle id
     bytes partition_request = 2;      // opaque-to-rimsky, producer-interpreted
   }

   message SubScopeDescriptor {
     bytes scope_data = 1;             // producer-canonicalized scope bytes
     string partition_key = 2;         // human-readable child_key for run-tree bookkeeping
     bytes producer_metadata = 3;      // opaque; per-sub-scope info producer wants persisted on the row
   }

   message SplitScopeResponse {
     repeated SubScopeDescriptor sub_scopes = 1;
   }

   message ScopesConflictRequest {
     bytes scope_a = 1;
     bytes scope_b = 2;
   }

   message ScopesConflictResponse {
     bool conflicts = 1;
   }
   ```

4. Extend `CommitResponse` (or whichever message the current `Commit` RPC returns) with an optional `string version_id = <next>;` field. Add a comment: "Set by DataProcessing-capable producers; opaque to rimsky; persisted in `rimsky_claim_handles.version_id` and `rimsky_lineage` for `record_kind: claim_commit`."
5. Extend the `Capabilities` message returned by the existing `Capabilities()` RPC with `bool supports_split_scope = <next>;` and `bool supports_scopes_conflict = <next>;`. Comments: rimsky reads these at startup; templates that fan out against a producer with `supports_split_scope == false` are rejected at registration.
6. Add a `repeated string protocols = <next>;` field to `Capabilities` if not already present (it should be — verify). Note: the spec extends advertisement of mix-ins (`data_processing`, `validation`) through this field. If not present, add it.
7. Add a `repeated string validation_supported_roles = <next>;` field to `Capabilities` for services that advertise the `validation` protocol — set of role discriminators (`executor` | `claim_producer` | `lifecycle_subscriber` | `sensor`) the service is willing to validate.

**Verify:** `make proto-gen` from repo root succeeds. `go build ./protocols/...` is clean.

---

### A2. Create `protocols/proto/v1/data_processing.proto`

**Files:**
- `protocols/proto/v1/data_processing.proto` (new)

**Steps:**

1. Create the file with `syntax = "proto3";` and `package rimsky.v1;` matching the other proto files in this directory. Generated Go package option matches the others.
2. Define the `DataProcessing` service with these RPCs (mirror spec §Protocol surfaces / DataProcessing exactly):

   ```proto
   service DataProcessing {
     rpc Capabilities(google.protobuf.Empty) returns (DataProcessingCapabilities);
     rpc BeginCandidate(BeginCandidateRequest) returns (BeginCandidateResponse);
     rpc CommitCandidate(CommitCandidateRequest) returns (CommitCandidateResponse);
     rpc AbandonCandidate(AbandonCandidateRequest) returns (google.protobuf.Empty);
     rpc ListVersions(ListVersionsRequest) returns (ListVersionsResponse);
     rpc ListPartitions(ListPartitionsRequest) returns (ListPartitionsResponse);
     rpc GetVersionSchema(GetVersionSchemaRequest) returns (GetVersionSchemaResponse);
   }
   ```

3. Define the request/response messages. Key shapes:

   ```proto
   message DataProcessingCapabilities {
     repeated string data_shapes = 1;          // e.g. "parquet", "geo-parquet", "postgis-table"
     repeated string materializations = 2;     // e.g. "full", "partitioned", "iceberg"
     repeated string partition_kinds = 3;      // e.g. "date_range", "region_list", "hash"
     repeated string aggregators = 4;          // e.g. "map_partitioned", "union", "merge", "reduce", "collect", "first"
   }

   message BeginCandidateRequest {
     string claim_handle_id = 1;           // sub-claim's rimsky-side id
     bytes sub_scope_descriptor = 2;       // the descriptor returned by SplitScope
     string idempotency_key = 3;           // rimsky-side run_id-derived key for dedup
   }

   message BeginCandidateResponse {
     bytes candidate_handle = 1;           // opaque producer-side identifier; persisted on rimsky_claim_handles.producer_candidate_handle
   }

   message CommitCandidateRequest {
     bytes candidate_handle = 1;
   }

   message CommitCandidateResponse {
     bytes candidate_metadata = 1;         // opaque-to-rimsky; producer-side metadata about the candidate
   }

   message AbandonCandidateRequest {
     bytes candidate_handle = 1;
   }

   message ListVersionsRequest { string claim_handle_id = 1; }
   message ListVersionsResponse { repeated VersionMetadata versions = 1; }

   message VersionMetadata {
     string version_id = 1;
     google.protobuf.Timestamp committed_at = 2;
     bytes producer_metadata = 3;
   }

   message ListPartitionsRequest { string claim_handle_id = 1; string version_id = 2; }
   message ListPartitionsResponse { repeated PartitionDescriptor partitions = 1; }

   message PartitionDescriptor {
     string partition_key = 1;
     bytes partition_metadata = 2;
   }

   message GetVersionSchemaRequest { string claim_handle_id = 1; string version_id = 2; }
   message GetVersionSchemaResponse { bytes schema = 1; }    // typically JSON schema or Arrow schema bytes
   ```

4. Run `make proto-gen` from repo root.

**Verify:** `go build ./protocols/...` is clean. The generated `DataProcessingClient` and `DataProcessingServer` interfaces exist in `protocols/proto/v1/gen/`.

---

### A3. Create `protocols/proto/v1/validation.proto`

**Files:**
- `protocols/proto/v1/validation.proto` (new)

**Steps:**

1. Create the file. Define the `Validation` service per spec §Protocol surfaces / Validation. Method name is `Validate` (singular; request is self-describing per `role`).
2. Define the messages. Use the schema in spec §Protocol surfaces / Validation verbatim (single `Validate` RPC; request with `oneof context`; role-specific context messages `ExecutorContext`, `ClaimProducerContext`, `LifecycleSubscriberContext`, `SensorContext`; `ValidateResponse` with `valid`, `errors`, `warnings`).
3. Run `make proto-gen`.

**Verify:** `go build ./protocols/...` is clean.

---

### A4. Create `protocols/proto/v1/sensor.proto`

**Files:**
- `protocols/proto/v1/sensor.proto` (new)

**Steps:**

1. Create the file. Define the `Sensor` service per spec §Sensors as a service kind:

   ```proto
   service Sensor {
     rpc Capabilities(google.protobuf.Empty) returns (SensorCapabilities);
     rpc StartWatch(StartWatchRequest) returns (StartWatchResponse);
     rpc StopWatch(StopWatchRequest) returns (StopWatchResponse);
     rpc ListWatches(google.protobuf.Empty) returns (ListWatchesResponse);
   }

   message SensorCapabilities {
     repeated SensorKindCapability supported_kinds = 1;
     repeated string protocols = 2;                 // e.g. "sensor", "validation"
     repeated string validation_supported_roles = 3;
   }

   message SensorKindCapability {
     string kind = 1;                               // e.g. "cron", "http", "object-store", "webhook"
     bytes config_schema = 2;                       // JSON schema for resolved_config per kind
   }

   message StartWatchRequest {
     string watch_id = 1;
     string instance_id = 2;
     string kind = 3;
     bytes resolved_config = 4;                     // post-substitution config
   }

   message StartWatchResponse {}

   message StopWatchRequest { string watch_id = 1; }
   message StopWatchResponse {}

   message ListWatchesResponse { repeated WatchDescriptor watches = 1; }

   message WatchDescriptor {
     string watch_id = 1;
     string instance_id = 2;
     string kind = 3;
     google.protobuf.Timestamp started_at = 4;
   }
   ```

2. Run `make proto-gen`.

**Verify:** `go build ./protocols/...` is clean.

---

### A5. Extend `executor.proto`: `Snooze` carries `ParkReason` + `reason_label`; per-claim address gains `candidate_handle`

**Files:**
- `protocols/proto/v1/executor.proto`

**Steps:**

1. Read the current file. Locate the `Snooze` message (the parked-terminal payload).
2. Add the `ParkReason` enum per spec §Parked-state taxonomy / Proto change verbatim.
3. Add `optional ParkReason reason = <next>;` and `optional string reason_label = <next>;` to `Snooze`.
4. Add a doc comment on `ParkReason`: "Maps to `rimsky_node_runs.parked_reason` after lifecycle-handler resolution; `OTHER` requires `reason_label` non-empty (validated at the supervisor's terminal handler)."
5. Locate the per-claim address structure on `ExecuteRequest` (whatever the existing message is — e.g. `ClaimAddress` or a field on a `Claim` sub-message; confirm by grep). Add `optional bytes candidate_handle = <next>;` to it. Doc comment: "Set by the supervisor for DataProcessing-capable claims at fan-out leaf dispatch; opaque to rimsky; the executor passes this back to its own writes against the producer (see `proto:data_processing.proto::CommitCandidate`). Empty for non-DataProcessing claims and non-fan-out claims."
6. Run `make proto-gen`.

**Verify:** `go build ./protocols/...` is clean. `grep -A2 candidate_handle protocols/proto/v1/gen/executor*.go` shows the generated field.

---

### A6. Extend `lifecycle.proto`: no spec-required changes

**Steps:**

1. Confirm by re-reading spec §Protocol surfaces — `lifecycle.proto` is **unchanged** for this spec. No-op task; included to be explicit.

**Verify:** `git diff protocols/proto/v1/lifecycle.proto` shows no changes.

---

### A7. Update protocol Go-side wrappers if any

**Files:**
- `protocols/` Go-side types (if any wrappers exist beyond the generated bindings)

**Steps:**

1. Grep for non-generated Go files under `protocols/` (`find protocols -name '*.go' -not -path '*gen*'`). For each, scan for usages of the modified protos (notably `Capabilities`, `Commit`, `Snooze`).
2. If a wrapper exists for `Capabilities`, extend the Go struct's field set to include the new protobuf fields.
3. Skip Go-side wrappers for the new services (`DataProcessing`, `Validation`, `Sensor`) — the generated bindings are the contract; root-module callers consume them directly.

**Verify:** `go build ./protocols/...` is clean; `cd protocols && go mod tidy` is clean.

---

# Section B — Persistence schema migrations

Each migration is a new numbered SQL file under `foundation/persistence/postgres/migrations/` and `foundation/persistence/sqlite/migrations/`. Pre-v1: drop-and-recreate where cleaner; no compat shims. SQLite migrations must mirror Postgres in semantics (use `BLOB` for `BYTEA`, omit `TIMESTAMPTZ` precision details, etc.).

### B1. Determine current migration numbering

**Steps:**

1. List `foundation/persistence/postgres/migrations/` and `foundation/persistence/sqlite/migrations/`. Find the highest existing migration number (`NNNN`). Subsequent migrations are `NNNN+1`, `NNNN+2`, ….
2. Record the numbering plan in notes (B2..B11 are sequential).

**Verify:** numbering noted.

---

### B2. Migration — extend `rimsky_node_runs` with parent/child/aggregation columns + lift state columns from `rimsky_nodes`

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_node_runs_tree_and_state.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_node_runs_tree_and_state.sql` (new)

**Steps:**

1. Postgres migration content:
   - `ALTER TABLE rimsky_node_runs ADD COLUMN parent_run_id UUID NULL REFERENCES rimsky_node_runs(id) ON DELETE SET NULL;`
   - `ALTER TABLE rimsky_node_runs ADD COLUMN child_key TEXT NULL;`
   - `ALTER TABLE rimsky_node_runs ADD COLUMN aggregation_policy JSONB NULL;`
   - `ALTER TABLE rimsky_node_runs ADD COLUMN state TEXT NOT NULL DEFAULT 'stale' CHECK (state IN ('fresh','stale','running','failed','parked'));`
   - `ALTER TABLE rimsky_node_runs ADD COLUMN last_outcome TEXT NOT NULL DEFAULT 'fresh_unchanged' CHECK (last_outcome IN ('fresh_changed','fresh_unchanged','passed','pure_cascade','failed'));`
   - `ALTER TABLE rimsky_node_runs ADD COLUMN parked_reason TEXT NULL CHECK (parked_reason IN ('TIME_WAIT','CALLBACK_WAIT','RETRY_BACKOFF','OTHER') OR parked_reason IS NULL);`
   - `ALTER TABLE rimsky_node_runs ADD COLUMN parked_reason_label TEXT NULL;`
   - `ALTER TABLE rimsky_node_runs ADD COLUMN parked_resume_at TIMESTAMPTZ NULL;`
   - `CREATE INDEX idx_node_runs_parent_run_id ON rimsky_node_runs(parent_run_id);`
   - `CREATE INDEX idx_node_runs_node_frame ON rimsky_node_runs(node_id, frame_id);`
   - (Confirm the existing `claimed_by` partial index `(claimed_by) WHERE claimed_by IS NOT NULL` is present; preserve it.)
2. SQLite migration mirrors the Postgres semantics (`BLOB` not used here; `TEXT` and `INTEGER` for booleans where applicable; SQLite `CHECK` constraints work the same). Use `TIMESTAMP` instead of `TIMESTAMPTZ`.
3. Update `rimsky_nodes` deletion in a separate migration (B3 below); this migration does NOT yet drop state columns from `rimsky_nodes`. Reason: we need both columns temporarily during the Go-side cutover, but the Go-side change in C lands in the same execution run, so the column drop can immediately follow.

**Verify:** `go test ./foundation/persistence/... -run TestMigrations -count=1` is clean.

---

### B3. Migration — drop state columns from `rimsky_nodes`

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_nodes_drop_state.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_nodes_drop_state.sql` (new)

**Steps:**

1. Postgres:
   - `ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS state;`
   - `ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS last_outcome;`
   - `ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS parked_reason;`
   - `ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS frame_id;`
   - `ALTER TABLE rimsky_nodes DROP COLUMN IF EXISTS parked_resume_at;`
2. SQLite: same column drops. (SQLite supports `ALTER TABLE … DROP COLUMN` since 3.35; confirm the project's minimum SQLite version supports it; otherwise use the `CREATE TABLE new_…; INSERT INTO new_… SELECT…; DROP TABLE old; RENAME` pattern.)
3. Update any indexes that referenced those columns (drop them first if present).

**Verify:** `go test ./foundation/persistence/... -run TestMigrations -count=1` is clean.

---

### B4. Migration — extend `rimsky_claim_handles`

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_claim_handles_extensions.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_claim_handles_extensions.sql` (new)

**Steps:**

1. Postgres:
   - `ALTER TABLE rimsky_claim_handles ADD COLUMN parent_claim_handle_id UUID NULL REFERENCES rimsky_claim_handles(id) ON DELETE SET NULL;`
   - `ALTER TABLE rimsky_claim_handles ADD COLUMN lifetime TEXT NOT NULL DEFAULT 'subgraph' CHECK (lifetime IN ('subgraph','durable'));`
   - `ALTER TABLE rimsky_claim_handles ADD COLUMN held_durable BOOLEAN NOT NULL DEFAULT FALSE;`
   - `ALTER TABLE rimsky_claim_handles ADD COLUMN version_id TEXT NULL;`
   - `ALTER TABLE rimsky_claim_handles ADD COLUMN producer_candidate_handle BYTEA NULL;`
   - `CREATE INDEX idx_claim_handles_parent ON rimsky_claim_handles(parent_claim_handle_id) WHERE parent_claim_handle_id IS NOT NULL;`
   - `CREATE INDEX idx_claim_handles_held_durable ON rimsky_claim_handles(held_durable) WHERE held_durable = TRUE;`
2. SQLite mirror. Use `BLOB` for `BYTEA`. SQLite `BOOLEAN` is stored as INTEGER.

**Verify:** migrations test passes.

---

### B5. Migration — rename `rimsky_claim_holders.holder_node` → `holder_run_id`

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_claim_holders_rename.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_claim_holders_rename.sql` (new)

**Steps:**

1. Postgres pre-v1 clean break (drop-and-recreate; per `rules.md` and spec §What this does NOT cover):
   - `DROP TABLE rimsky_claim_holders;`
   - `CREATE TABLE rimsky_claim_holders (claim_handle_id UUID NOT NULL REFERENCES rimsky_claim_handles(id) ON DELETE CASCADE, holder_run_id UUID NOT NULL REFERENCES rimsky_node_runs(id) ON DELETE CASCADE, state TEXT NOT NULL CHECK (state IN ('active','completed','failed')), PRIMARY KEY (claim_handle_id, holder_run_id));`
   - `CREATE INDEX idx_claim_holders_run ON rimsky_claim_holders(holder_run_id);`
2. SQLite mirror.

**Verify:** migrations test passes; FK shape exercised in scenario test that follows.

---

### B6. Migration — `rimsky_wait_set` to run-level

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_wait_set_run_level.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_wait_set_run_level.sql` (new)

**Steps:**

1. Postgres clean break:
   - `DROP TABLE rimsky_wait_set;`
   - `CREATE TABLE rimsky_wait_set (sender_run_id UUID NOT NULL REFERENCES rimsky_node_runs(id) ON DELETE CASCADE, receiver_run_id UUID NOT NULL REFERENCES rimsky_node_runs(id) ON DELETE CASCADE, frame_id UUID NOT NULL, PRIMARY KEY (sender_run_id, receiver_run_id, frame_id));`
   - `CREATE INDEX idx_wait_set_receiver ON rimsky_wait_set(receiver_run_id);`
2. SQLite mirror.

**Verify:** migrations test passes.

---

### B7. Migration — create `rimsky_messages`

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_messages.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_messages.sql` (new)

**Steps:**

1. Postgres:
   - `CREATE TABLE rimsky_messages (id UUID PRIMARY KEY, instance_id UUID NOT NULL, kind TEXT NOT NULL, sender TEXT NOT NULL, sender_kind TEXT NOT NULL CHECK (sender_kind IN ('operator','sensor','instance')), target TEXT NULL, payload BYTEA NULL, backfill_operation_id UUID NULL, received_at TIMESTAMPTZ NOT NULL, delivered_at TIMESTAMPTZ NULL, frame_id UUID NULL);`
   - `CREATE INDEX idx_messages_instance_received ON rimsky_messages(instance_id, received_at);`
   - `CREATE INDEX idx_messages_backfill ON rimsky_messages(backfill_operation_id) WHERE backfill_operation_id IS NOT NULL;`
   - `CREATE INDEX idx_messages_pending ON rimsky_messages(instance_id, delivered_at) WHERE delivered_at IS NULL;`
2. SQLite mirror; `BLOB` for `BYTEA`; `TIMESTAMP` for `TIMESTAMPTZ`.

**Verify:** migrations test passes.

---

### B8. Migration — create `rimsky_lineage`

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_lineage.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_lineage.sql` (new)

**Steps:**

1. Postgres:
   - `CREATE TABLE rimsky_lineage (id UUID PRIMARY KEY, record_kind TEXT NOT NULL CHECK (record_kind IN ('leaf_run','claim_commit')), instance_id UUID NOT NULL, frame_id UUID NOT NULL, observed_at TIMESTAMPTZ NOT NULL, record JSONB NOT NULL);`
   - `CREATE INDEX idx_lineage_run ON rimsky_lineage(record_kind, (record->>'run_id'));`
   - `CREATE INDEX idx_lineage_claim ON rimsky_lineage(record_kind, (record->>'claim_handle_id'));`
   - `CREATE INDEX idx_lineage_substitution_refs ON rimsky_lineage USING GIN (record);`
2. SQLite: `JSONB` becomes `TEXT` storing JSON; functional indexes via `json_extract(record, '$.run_id')`. Drop the GIN index; use a `record` text index for full-scan grep-based queries.

**Verify:** migrations test passes; basic insert + select scenario test added in section N exercises the GIN path on Postgres.

---

### B9. Migration — create `rimsky_sensor_watches`

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_sensor_watches.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_sensor_watches.sql` (new)

**Steps:**

1. Postgres:
   - `CREATE TABLE rimsky_sensor_watches (id UUID PRIMARY KEY, instance_id UUID NOT NULL, sensor_name TEXT NOT NULL, kind TEXT NOT NULL, resolved_config JSONB NOT NULL, on_observation JSONB NOT NULL, started_at TIMESTAMPTZ NULL, last_observed_at TIMESTAMPTZ NULL, state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','failed','stopped')));`
   - `CREATE INDEX idx_sensor_watches_instance ON rimsky_sensor_watches(instance_id);`
   - `CREATE INDEX idx_sensor_watches_state ON rimsky_sensor_watches(state) WHERE state = 'active';`
2. SQLite mirror.

**Verify:** migrations test passes.

---

### B10. Migration — drop `rimsky_schedules` — LANDED (dispatch 13)

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_drop_schedules.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_drop_schedules.sql` (new)

**Steps:**

1. Both: `DROP TABLE IF EXISTS rimsky_schedules;`. State moves to bundled `sensor-cron`'s own persistence; per-instance watch identity carried in `rimsky_sensor_watches.resolved_config`.

**Verify:** migrations test passes.

---

### B11. Migration — add `rimsky_instances.frame_delivery_mode`

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_instances_frame_delivery.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_instances_frame_delivery.sql` (new)

**Steps:**

1. Postgres:
   - `ALTER TABLE rimsky_instances ADD COLUMN frame_delivery_mode TEXT NOT NULL DEFAULT 'coalesce' CHECK (frame_delivery_mode IN ('serial_queue','coalesce'));`
2. SQLite mirror.

**Verify:** migrations test passes.

---

### B12. Re-tidy and rerun the migration-runner integration test

**Steps:**

1. `cd foundation && go mod tidy && go build ./... && go test ./persistence/...`
2. Confirm both Postgres and SQLite drivers cleanly apply all new migrations against fresh DBs.

**Verify:** clean.

---

# Section C — Foundation primitive type updates

### C1. Extend `foundation/cascade/` state machine

**Files:**
- `foundation/cascade/state.go`
- `foundation/cascade/transition_reason.go` (or wherever transition reasons live)
- `foundation/cascade/state_test.go`

**Steps:**

1. Read `state.go`. Confirm existing transitions and `ErrIllegalTransition`.
2. Allow the parent-run-only transitions described in spec §State machine: `terminal → stale`, `terminal → running`, `running → running` (the last one with the new transition reason `ReasonChildTransitioned` or `ReasonSubGraphInternalCascadeFired`). Encode them in the transition table guarded by a `IsParentRun bool` flag on the transition context, OR by gating on the transition reason (preferred — same shape as existing `dispatch_claimed` gating).
3. Add `ReasonChildTransitioned` and `ReasonSubGraphInternalCascadeFired` to the transition-reason enum.
4. Add unit tests in `state_test.go` covering each new transition (allowed) and the still-illegal cases (e.g. `running → running` under `dispatch_claimed` still rejected; the leaf-run transitions unchanged).
5. Re-annotate `@blessed-invariant 1` if its text needs to mention "for leaf runs and parent runs respectively." Keep the invariant number.

**Verify:** `go test ./foundation/cascade/... -count=1`.

---

### C2. Extend `foundation/spec/` row-type primitives

**Files:**
- `foundation/spec/template.go` (or wherever `TemplateSpec` / `TemplateNodeDef` live)
- `foundation/spec/node_run.go` (or equivalent)
- `foundation/spec/claim_handle.go` (or equivalent)
- `foundation/spec/parked_reason.go` (new — small enum file)
- `foundation/spec/sensor.go` (new — `SensorSpec`, `OnObservationSpec`)

**Steps:**

1. `TemplateSpec` / `TemplateNodeDef`:
   - Add `Graphs []GraphSpec` (replaces existing `Nodes []TemplateNodeDef` at the top level). The reserved name `main` per spec §Sub-graphs.
   - Add `Sensors []SensorSpec`.
   - `TemplateNodeDef` gains: `Delegate string` (sub-graph name; mutually exclusive with `Executor`), `Holds map[string]HoldsBinding`, `FanOut *FanOutSpec`, `Claims` gains `Lifetime string` ("subgraph" | "durable", default "subgraph") and `Data json.RawMessage` (opaque-to-rimsky producer-targeted bytes).
2. `GraphSpec`:

   ```go
   type GraphSpec struct {
     Name string  `json:"name"`             // "main" is reserved for the top-level graph
     Entry string `json:"entry,omitempty"`  // sub-graphs only
     Exit  string `json:"exit,omitempty"`   // sub-graphs only
     Nodes []TemplateNodeDef `json:"nodes"`
   }
   ```

3. `FanOutSpec` per spec §Fan-out template DSL verbatim.
4. `HoldsBinding`: `{From string, As string}` mapping `holds: { <claim_alias>: { from: <node_alias> } }`.
5. `SensorSpec`: `{Name, Kind, Config json.RawMessage, OnObservation OnObservationSpec}` per spec §Sensors / Per-instance parameterization.
6. `OnObservationSpec`: `{TargetNode string, MessageKind string, PayloadTemplate map[string]any}`.
7. `ParkReason` enum: `TIME_WAIT | CALLBACK_WAIT | RETRY_BACKOFF | OTHER` (string-typed; mirror proto enum names).
8. `NodeRun` struct (the persisted row-type primitive): add `ParentRunID *uuid.UUID`, `ChildKey *string`, `AggregationPolicy json.RawMessage`, `State NodeState`, `LastOutcome LastOutcome`, `ParkedReason *ParkReason`, `ParkedReasonLabel *string`, `ParkedResumeAt *time.Time`. Confirm the existing fields preserved.
9. `ClaimHandle` struct: add `ParentClaimHandleID *uuid.UUID`, `Lifetime string` (default `"subgraph"`), `HeldDurable bool`, `VersionID *string`, `ProducerCandidateHandle []byte`.
10. Validate that none of these new types breach the `foundation-purity` depguard rule (they're all pure data with stdlib types and `uuid`).

**Verify:** `cd foundation && go build ./... && go mod tidy && go test ./spec/...`.

---

### C3. Update `foundation/locks/` for sub-claim and held-durable semantics

**Files:**
- `foundation/locks/interface.go` (or wherever `ClaimProducer` Go interface lives)
- `foundation/locks/types.go`

**Steps:**

1. Add Go method signatures to the `ClaimProducer` interface for `SplitScope` and `ScopesConflict`, mirroring the proto:

   ```go
   SplitScope(ctx context.Context, req SplitScopeRequest) (SplitScopeResponse, error)
   ScopesConflict(ctx context.Context, a, b []byte) (bool, error)
   ```

   Mark both as optional in the godoc; implementations advertise via `Capabilities`.
2. Extend `ClaimResult` / `ClaimSpec` if needed to carry `producer_candidate_handle` from sub-claim opens. Concretely: `ClaimResult` gains optional `ProducerCandidateHandle []byte`.
3. Update `@blessed-invariant 4b` annotation per spec text update: "single-writer-per-scope; overlap is producer-defined, byte-equal as the trivial default."
4. Update `@blessed-invariant 9a/b` annotations: text unchanged in intent; add a note that the byte-equal default is now explicit when `ScopesConflict` is unsupported.
5. Update the storetest fake (`foundation/locks/storetest/`) to implement the two new methods (default behavior: byte-equal `ScopesConflict`; `SplitScope` returns an error indicating "test fake does not support split"; scenarios that need split can register custom behavior).

**Verify:** `cd foundation && go build ./... && go test ./locks/...`.

---

### C4. Update `foundation/persistence/` driver interfaces

**Files:**
- `foundation/persistence/database.go` (the umbrella `Database` interface or equivalent)
- `foundation/persistence/postgres/*.go`
- `foundation/persistence/sqlite/*.go`
- `foundation/persistence/migrations.go`
- Test files

**Steps:**

1. Re-read the `Tables` / `<Row>Table` umbrella. Add per-row-type interfaces for the new tables:
   - `MessagesTable` — `Insert`, `MarkDelivered`, `ListPendingForInstance`, `Get`, `ListByInstance(filters)`, `ListByBackfill`.
   - `LineageTable` — `InsertLeafRun`, `InsertClaimCommit`, `GetByRunID`, `GetByClaimHandleID`, `AncestorsByRun(run_id, depth)`, `DescendantsByRun(run_id, depth)`, `BySource(kind, id)`, `ByProducer(producer, version)`.
   - `SensorWatchesTable` — `Insert`, `Update`, `Delete`, `ListByInstance`, `ListByState`.
2. Extend `NodeRunsTable` with methods for the run-tree:
   - `InsertParentRun(...)`
   - `InsertChildRun(parent_id, child_key, ...)`
   - `GetChildren(parent_id)`
   - `WalkAncestors(run_id)` — used by state propagation.
3. Extend `ClaimHandlesTable`:
   - `InsertSubClaim(parent_handle_id, ...)`
   - `GetSubClaims(parent_handle_id)`
   - `SetVersionID(handle_id, version_id)`
   - `SetHeldDurable(handle_id, true)` and `ListHeldDurableByInstance(instance_id)`.
4. Extend `ClaimHoldersTable` for the `holder_run_id` rename. Method names track Go naming; behavior unchanged.
5. Extend `WaitSetTable` for the run-level granularity. Eligibility predicate method updated.
6. Postgres impl: implement each new method. Use prepared statements where the existing pattern uses them.
7. SQLite impl: mirror Postgres semantics; SQL adjusted for SQLite syntax.
8. Memory blob backend stays unchanged.
9. Add Go-side migration registration: each new SQL file registered in the migration runner per the existing convention.

**Verify:** `cd foundation && go test ./persistence/... -count=1` is clean (testcontainers Postgres + SQLite in-memory both pass).

---

### C5. Frame-end predicate re-roots from `rimsky_nodes` to `rimsky_node_runs`

**Files:**
- `runtime/frame_end.go` (or `foundation/cascade/frame_end.go` — locate via grep for the predicate)

**Steps:**

1. Locate the predicate. The current shape: "no `rimsky_nodes` rows in state `stale` or `running` for this instance."
2. Rewrite to: "no `rimsky_node_runs` rows for this `frame_id` with `state IN ('stale','running')`."
3. Update the corresponding SQL.
4. Add a `@concept:frame` annotation if absent.
5. Update the unit test for this predicate (locate via grep for the existing test name) to seed run-level state rather than node-level.

**Verify:** `go test ./runtime/... -run FrameEnd -count=1`. `go test ./graph/scenario/... -count=1` is clean (sanity check).

---

### C6. Extend `foundation/spec/` for transition reasons and aggregation policy snapshot

**Files:**
- `foundation/spec/transition_reason.go` (or co-located with cascade)
- `foundation/spec/aggregation_policy.go` (new)

**Steps:**

1. Add the new transition reasons (already added in C1; re-verify they're also exported via `foundation/spec/` if that's the contract pattern).
2. Add `AggregationPolicy` type:

   ```go
   type AggregationPolicy struct {
     Kind            string `json:"kind"`              // "strict" | "threshold" | "best_effort" | "first"
     CancelSiblings  bool   `json:"cancel_siblings,omitempty"`
     MaxFailures     int    `json:"max_failures,omitempty"`
   }
   ```

3. Validation: `Kind` must be one of the four; `CancelSiblings` only meaningful for `strict`; `MaxFailures` only for `threshold`. Validator returns an error usable at template registration.

**Verify:** `go test ./foundation/spec/... -count=1`.

---

# Section D — Template canonicalization (the `graph/` layer)

### D1. Top-level `graphs:` + `sensors:` parsing

**Files:**
- `graph/template/canonical/parser.go` (and adjacent)
- `graph/template/canonical/parser_test.go`

**Steps:**

1. Re-read the existing YAML parser entry point. Currently it consumes a top-level `nodes:` list.
2. Update the parser to consume `graphs:` (a list of `GraphSpec`) and `sensors:` (a list of `SensorSpec`). The single-graph `nodes:` legacy shape is **removed** pre-v1 (per `rules.md` break-freely). Templates must declare a `main` graph explicitly.
3. Validate that exactly one graph is named `main`; all others must declare both `entry:` and `exit:`. Reject otherwise with classes from spec §Edge-case rejections at registration: `subgraph_missing_entry`, `subgraph_missing_exit`, `subgraph_main_has_entry_or_exit`.
4. Reject `subgraph_entry_equals_exit` if `entry == exit` for any non-main graph.
5. Reject disconnected internal nodes: every internal node must be reachable from `entry` along edges (`subscribes` + `dependencies`) AND reach `exit`. Use BFS over the edge set. Rejection class: `subgraph_disconnected_internal_node`.
6. Reject recursive sub-graphs: a sub-graph delegating to itself (directly or via a cycle). Build a delegation graph from `delegate:` references; check for cycles. Rejection class: `subgraph_recursion_unsupported`.
7. Reject internal nodes referencing outer-graph nodes (via `subscribes:` / `dependencies:` / `holds:` `from:` pointers to outer aliases). Rejection class: `subgraph_internal_references_outer`.
8. Reject `main` containing `entry:` or `exit:` (already covered by step 3).
9. Tests: each rejection class gets at least one positive (rejected) and one near-miss (accepted) test case.

**Verify:** `go test ./graph/template/canonical/... -count=1`.

---

### D2. `delegate:` / `executor:` mutual exclusivity + node-level DSL extensions

**Files:**
- `graph/template/canonical/node.go`
- `graph/template/canonical/node_test.go`

**Steps:**

1. Per-node validation: a node has either `executor:` or `delegate:`, not both, not neither (unless the executor field is allowed to default in some niche — confirm by re-reading the current `TemplateNodeDef` shape).
2. Add canonicalization for `delegate:`: at canonicalization, the calling node's executor field gets populated from the referenced sub-graph's entry node's executor; the entry node's sub-graph-internal claims/holds/userdata are merged into the calling node's external declarations (calling-node-wins on conflict; document this in a comment on the canonicalizer). The entry node does NOT produce its own `rimsky_nodes` row in the canonical instance.
3. Non-entry internal nodes (including exit) get **one** `rimsky_nodes` row per instance, declaratively shared across all invocations of the sub-graph within that instance (spec §Sub-graphs / Multiple invocations is explicit). The canonicalizer emits one row per `(instance_id, internal_node_alias)`, regardless of how many outer-graph nodes delegate to this sub-graph. Run-tree distinguishes per-invocation execution via `parent_run_id`; the declarative node row is shared.
4. Subscription edges from internal nodes that reference the entry alias get rewritten to reference the calling node's alias **conceptually**, but because "the calling node" is dynamic per-invocation, the canonicalizer doesn't bake a specific calling node into the edge. Instead, mark the edge with a `resolves_via_calling_node: true` flag; runtime resolution happens in the cascade walker (E6 step 2b).
5. Tests cover: entry absorption (executor inherited; merge behavior); exit identity (own row exists); non-entry internal nodes shared across two delegating callers; subscription-edge marker present.

**Verify:** `go test ./graph/template/canonical/... -count=1`.

---

### D3. `holds:` template directive

**Files:**
- `graph/template/canonical/holds.go` (new)
- `graph/template/canonical/holds_test.go` (new)

**Steps:**

1. Parse the `holds: { <alias>: { from: <upstream-node-alias> } }` block.
2. Validate that every `from:` points to an upstream dependency of the node (the `holds:` graph must be a subset of the cell graph). Rejection class: `holds_from_not_dependency`.
3. Validate that the upstream node declares the referenced claim alias in its `claims:` block. Rejection: `holds_unknown_claim_alias`.
4. Canonicalize into a `claim_holder_specs` list on the node-spec for runtime consumption.

**Verify:** `go test ./graph/template/canonical/... -count=1`.

---

### D4. `fan_out:` template DSL

**Files:**
- `graph/template/canonical/fanout.go` (new)
- `graph/template/canonical/fanout_test.go` (new)

**Steps:**

1. Parse `fan_out: { claim, partition_request, parallelism, error_policy }` per spec §Fan-out template DSL.
2. Validate that `claim` references a claim alias declared on the node (in `claims:` or `holds:`).
3. Validate the producer of the referenced claim advertises `supports_split_scope: true` in its `Capabilities` snapshot (the canonicalizer consults the discovery cache — `control/observability/discovery-cache.go` — for this).
4. Parse `error_policy` per the four shapes (`strict`, `threshold`, `best_effort`, `first`). Default `strict.cancel_siblings: true`.
5. Allow `partition_request` to be a substitution string (`"{{trigger.message.payload.X | default: Y}}"`) — defer substitution to runtime; at canonicalization, just record the literal/template string.
6. If the producer advertises `validation` for role `claim_producer`, the validation pipeline (D8 / F9) is responsible for forwarding the canonicalized `data:` block and the fan-out node's `partition_request` literal (or its `default` clause when it's a substitution template) to the producer's `Validate` RPC inside a `ClaimProducerContext.ClaimBinding`. The producer may reject malformed partition_request shapes at registration. Pure rimsky-side validation does not parse partition_request bytes (the bytes are opaque to rimsky); only the producer-side `Validate` does the shape check.

**Verify:** `go test ./graph/template/canonical/... -count=1`.

---

### D5. Claim `lifetime:` + `data:` block

**Files:**
- `graph/template/canonical/claims.go`
- `graph/template/canonical/claims_test.go`

**Steps:**

1. Extend the `claims:` block parser. New optional fields: `lifetime: subgraph | durable` (default `subgraph`), `data: { ... }` (opaque to rimsky; producer-targeted bytes — parsed as `json.RawMessage` and stored verbatim).
2. Per-claim validation: if `lifetime: durable`, validate that the producer's `Capabilities` advertises `protocols: [..., data_processing]` (assets MUST be DataProcessing-capable). Rejection: `lifetime_durable_requires_data_processing`.

**Verify:** `go test ./graph/template/canonical/... -count=1`.

---

### D6. `subscribes:` topic kind `message`

**Files:**
- `graph/template/canonical/subscriptions.go`
- `graph/template/canonical/subscriptions_test.go`

**Steps:**

1. Extend the subscription parser to accept `{ on: message, kind?, sender?, sender_kind?, target? }`.
2. Validate: at least one filter field present (otherwise an "any message" subscription is allowed iff explicitly specified as `{ on: message }` with no filters — accept this; document the broad-fan-out implication).
3. Reject unknown `sender_kind` values (must be `operator | sensor | instance`).
4. Add a `target: self` shorthand that the canonicalizer resolves to the node's own alias.

**Verify:** `go test ./graph/template/canonical/... -count=1`.

---

### D7. Retire `schedule:` field from `TemplateNodeDef` — LANDED (dispatch 13)

**Files:**
- `graph/template/canonical/schedule.go` (delete)
- `graph/template/canonical/parser.go` (remove `schedule:` parsing)
- `foundation/spec/template.go` (remove `Schedule` field from `TemplateNodeDef`)
- Tests for cron-fire that lived in `graph/scheduler/`

**Steps:**

1. Delete the `schedule:` parsing path.
2. Reject any template at registration that still declares `schedule:` on a node, with class `schedule_field_retired` and remediation text pointing to the bundled `sensor-cron`.
3. Update tests that previously exercised the per-node schedule path; convert them to use `sensor-cron` indirectly (or move them to the `sensor-cron` package).

**Verify:** `go test ./graph/template/... -count=1`.

---

### D8. Validation pipeline at registration

**Files:**
- `runtime/validation_pipeline.go` (new — but lives in `runtime/` because the pipeline is the runtime's; alternative path `graph/template/canonical/validation_pipeline.go` is acceptable, decide by where the Validate RPC call site naturally fits — likely runtime, since the discovery cache and remote client live in runtime).
- `runtime/validation_pipeline_test.go` (new)

**Steps:**

1. Build a pipeline ordering:
   - `userdata_schema` static check first (executor's advertised JSON Schema vs. node userdata) — pure rimsky-side, no RPC.
   - For each service the node references that advertises `validation`, call `Validation.Validate` over gRPC with the role-specific context built from the canonicalized spec. Use `runtime/remote/` for the client (same shape as the existing remote-ClaimProducer client). Add a `runtime/remote/validation_client.go`.
2. Failure modes:
   - Static-schema failure: hard reject, no `Validate` RPC.
   - `Validate` returns `valid: false` errors → hard reject; warnings → soft.
   - Service unreachable: behavior governed by `rimsky.yml`'s `registration.unreachable_validator: permissive_warn | strict`; default `permissive_warn`.
3. Wire into the existing template registration endpoint (`control/controlapi/templates.go` or equivalent).
4. Preserve `@blessed-invariant 11` — rimsky never inspects userdata bytes locally beyond the JSON-Schema validator's structural pass. Annotate.

**Verify:** `go test ./runtime/... -run TestValidationPipeline -count=1`. The conformance binaries (M2 below) exercise this end-to-end against a real `validation`-advertising fixture.

---

# Section E — Runtime: orchestration core

This section is the biggest. Each sub-task introduces one mechanism; verification follows immediately.

### E1. Run-tree creation + Go-side wrappers

**Files:**
- `runtime/run_tree.go` (new)
- `runtime/run_tree_test.go` (new)

**Steps:**

1. Add functions `CreateRootRun(tx, frame_id, node_id, aggregation_policy)`, `CreateChildRun(tx, parent_run_id, child_key, node_id)`, `GetRunTree(run_id) → tree`.
2. `aggregation_policy` is snapshotted from the template-node spec at creation time. Persisted on `rimsky_node_runs.aggregation_policy`.
3. `child_key` is the partition_key (for fan-out children) or the internal node's alias (for sub-graph internal nodes).
4. Idempotency: re-creating a child with the same `(parent_run_id, child_key)` returns the existing row.

**Verify:** `go test ./runtime/... -run RunTree -count=1`.

---

### E2. State propagation transaction (run-tree)

**Files:**
- `runtime/state_propagation.go` (new)
- `runtime/state_propagation_test.go` (new)

**Steps:**

1. Function `PropagateChildState(tx, child_run_id, new_state, transition_reason)`:
   - Updates the child's row.
   - Looks up the parent via `parent_run_id`.
   - If parent NULL, done.
   - Else: `SELECT ... FOR UPDATE` the parent row.
   - Re-aggregates state across all children of the parent (one SELECT joined on `parent_run_id`).
   - Computes new parent state per the rule table (spec §State aggregation rules). Honor `aggregation_policy.kind`.
   - If parent's state changed, write the new state with reason `ReasonChildTransitioned`, then recurse to grandparent.
   - All locks taken in tree order (upward); no deadlocks because tree.
2. Encapsulate the aggregation rule table in a pure function `Aggregate(children []ChildState, policy AggregationPolicy) (NodeState, LastOutcome, *cancel.Action)`. Returns an optional cancel action (e.g., for `strict.cancel_siblings: true`, returns "abandon siblings"; for `first`, "cancel non-winners").
3. Apply cancel actions after the propagation completes (in the same transaction): walk the affected siblings' claim_handles and call `AbandonCandidate` + transition siblings to `failed{error_class: "sibling_failed"}`. Recursive — for siblings with their own claim-trees, the auto-terminal walks descendant claims via `ResolveClaimHandleTerminal` (see E4).
4. Use existing `runtime/terminal_decision.go::ResolveClaimHandleTerminal` for the auto-terminal verb-fire path (single, locked, claimant-guarded).

**Verify:** `go test ./runtime/... -run StatePropagation -count=1`. Race-sensitive — also run `-race`.

---

### E3. Recursive claim-tree resolution / auto-terminal

**Files:**
- `runtime/auto_terminal.go` (extend the existing `CheckAndFireResolution`)
- `runtime/auto_terminal_test.go`

**Steps:**

1. The existing `CheckAndFireResolution` fires at every node-terminal in a held subgraph; current shape walks one level. Extend to recurse:
   - When a leaf-level claim auto-terminals (Commit / Abandon), if it has a `parent_claim_handle_id`, check whether the parent's other sub-claims are all non-active. If so, fire the parent's auto-terminal.
   - Recurse up the claim-tree until reaching a root claim with no parent.
   - Bound by claim-tree depth (= run-tree depth in practice).
2. Aggregate-outcome decision at each level: any sub-claim `Abandon` → parent `Abandon`; all sub-claims `Commit` → parent `Commit`. Hooks back to `ResolveClaimHandleTerminal` (the unified terminal-decision engine).
3. For DataProcessing-capable producers: at parent `Commit`, the call to `ClaimProducer.Commit(parent_handle_id)` returns `version_id`; rimsky persists it on `rimsky_claim_handles.version_id` AND in the lineage `claim_commit` record.
4. For `lifetime: durable` claim handles: on `Commit` (or `Abandon`), set `held_durable = TRUE` (or `FALSE`) per the lifetime. After commit on `durable`, the row persists past holding-subgraph completion. (Annotate the new "held-durable persistence" invariant here.)

**Verify:** `go test ./runtime/... -run AutoTerminalRecursive -count=1`.

---

### E4. Atomic acquisition extended for sub-claims + BeginCandidate + candidate_handle wired into ExecuteRequest

**Files:**
- `runtime/runner_acquire.go` (extend)
- `runtime/runner_dispatch.go` (extend — or wherever the leaf-dispatch ExecuteRequest is constructed; locate by grep for `ExecuteRequest{`)

**Steps:**

1. Re-read the current `handleClaimAcquisition`. The current flow: claim node-run, INSERT claim-handle rows, RPC `Open`, persist addresses, COMMIT.
2. For a fan-out node, after the parent claim handle is acquired (Open + address), but **inside the same transaction**:
   - Call `ClaimProducer.SplitScope(parent_handle_id, partition_request)` → list of sub-scope descriptors.
   - For each sub-scope: INSERT a sub-claim row with `parent_claim_handle_id` set; call `ClaimProducer.Open` on the sub-claim; persist the returned address.
   - If the producer advertises `data_processing`: call `DataProcessing.BeginCandidate(sub_claim_id, sub_scope_descriptor, idempotency_key)` → persist `producer_candidate_handle` on the sub-claim row.
3. At leaf dispatch (`runtime/runner_dispatch.go`), extend the per-claim address construction in `ExecuteRequest` to populate the new `candidate_handle` field from the sub-claim row's `producer_candidate_handle` column. Empty for non-DataProcessing claims and non-fan-out claims. Annotate `@blessed-invariant 20` adjacent — the bytes are inert; rimsky passes them verbatim from row to wire.
4. Atomicity discipline: rimsky-side row inserts + producer's `Open` calls bundled per `@blessed-invariant 10` (parent claim + sub-claim handles + addresses all together or none). The producer's own state mutations remain decoupled (producer's own tx). Annotate the updated invariant.
5. Update the existing test that exercises atomic acquisition to cover the sub-claim shape; add a scenario test in N for the cross-process behavior.

**Verify:** `go test ./runtime/... -run AcquireAtomic -count=1 -race`. Also `go test ./runtime/... -run DispatchCandidateHandle -count=1` for the leaf dispatch wiring.

---

### E4b. Co-holder dispatch wiring (`holds:` runtime)

**Files:**
- `runtime/runner_acquire.go` (extend — co-holdership rows are inserted in the same transaction that records the holder run's claim, alongside the existing claim-handle inserts)
- `runtime/runner_dispatch.go` (extend the leaf-dispatch `ExecuteRequest` builder)
- `runtime/runner_acquire_test.go`, `runtime/runner_dispatch_test.go`

**Steps:**

1. At dispatch of a run whose template node declares `holds:` (the canonicalized `claim_holder_specs` list from D3), for each declared `(alias, from_node)` pair:
   - Resolve `from_node`'s upstream claim handle: query `rimsky_claim_handles` for the most recent active row produced by `from_node` for this instance + claim alias.
   - INSERT a row into `rimsky_claim_holders` with `claim_handle_id = <upstream>`, `holder_run_id = <this run>`, `state = 'active'`. Annotate `@blessed-invariant 13` adjacent — holders set is the auto-terminal's input.
   - Read `rimsky_claim_handles.address` for the upstream and bind it to the leaf's `ExecuteRequest` per-claim address slot using the local alias from `holds: { <alias>: ... }`. Same wire shape as for `claims:`-acquired addresses; the leaf cannot distinguish acquired vs co-held in `ExecuteRequest`.
2. Atomicity: the `rimsky_claim_holders` INSERTs land in the same transaction as the run's own claim acquisition (so the run is either fully bound — own claims acquired AND co-held claims registered — or not bound at all).
3. At terminal: the existing auto-terminal flow already updates `rimsky_claim_holders.state` per the row's own run terminal (see E3's recursive resolution). No new code path needed at terminal — the existing mechanism already walks `rimsky_claim_holders` for the auto-terminal check (E3 step 1).
4. Strict-cancel-siblings interaction: if a sibling fails and triggers strict-cancel, the cancellation walks `rimsky_claim_holders` rows for each affected sibling and transitions them to `'failed'` (the existing E2 step 3 cancel path).

**Verify:** `go test ./runtime/... -run CoHolder -count=1 -race`. Scenario test in N10 (verifier pattern, which is co-holdership in practice) exercises end-to-end.

---

### E5. Message queue + delivery at frame boundary

**Files:**
- `runtime/message_delivery.go` (new)
- `runtime/message_delivery_test.go` (new)

**Steps:**

1. Function `EnqueueMessage(tx, envelope)`: INSERTs into `rimsky_messages` with `delivered_at NULL`. Validates envelope (kind in `{invalidate}`; sender / sender_kind set; target if provided is a valid node alias in the instance template).
2. Function `DeliverPendingMessages(tx, instance_id, frame_id)`: at frame-boundary creation, run this:
   - SELECT pending messages for this instance (`delivered_at IS NULL`).
   - Per the instance's `frame_delivery_mode`:
     - `serial_queue`: pick the oldest one message, deliver, leave the rest pending.
     - `coalesce` (default): deliver all pending messages into this frame.
   - For each delivered message: walk subscriptions in the target instance matching envelope fields (`kind`, `sender`, `sender_kind`, `target`); stale-mark matched receivers' runs in the new frame; UPDATE `rimsky_messages.delivered_at = now()` and `frame_id = <new_frame>`.
3. Dead-lettering: if no subscribers match, set `delivered_at` and `frame_id = NULL`; surface via the messages list endpoint.
4. Frame creation: messages-delivered-at-boundary is a new frame-creation site. The existing frame-creation code (locate via grep for `INSERT INTO rimsky_frames`) gets a new entry point `CreateFrameFromMessages(tx, instance_id) → frame_id`.

**Verify:** `go test ./runtime/... -run MessageDelivery -count=1`.

---

### E6. Sub-graph dispatch: entry absorption + internal cascade + exit carry-rule — LANDED (dispatch 15: runner-tx integration + canonicalizer markers + N3 scenarios)

**Files:**
- `runtime/subgraph_dispatch.go` (new)
- `runtime/subgraph_dispatch_test.go` (new)
- `runtime/runner.go` (extend to call into the new path)

**Steps:**

1. On a calling-node dispatch:
   - Parent run is created (root run if outer-graph; child run if nested). The parent's executor is the absorbed entry's executor (set at canonicalization).
   - Standard executor dispatch runs the entry's executor against the parent run.
2. On the parent run's executor terminal:
   - Failure / parked → parent transitions directly to that terminal. No internal cascade. Sub-claims that the entry absorbed acquired Abandon per the standard path. (Entry-failure short-circuit; parent's writeback stays empty because exit never runs.)
   - Success (`fresh_changed` / `fresh_unchanged`) → parent stays `running` with transition reason `ReasonSubGraphInternalCascadeFired`. Fire internal cascade: stale-mark non-entry internal nodes that subscribe to the entry alias.
   - **2b. Per-invocation subscription-edge resolution** — the cascade walker extends to recognize the canonicalizer's `resolves_via_calling_node: true` marker (see D2 step 4). When stale-marking a non-entry internal node whose subscription targets the entry alias, the walker resolves that edge by binding to the current parent run (the calling node's run for this invocation). The wait-set row inserted at stale-mark time points to the specific parent run id, not to an abstract entry-alias key. This makes multiple in-flight invocations of the same sub-graph independent: each invocation's internal children block on their own parent's terminals.
3. Dispatch internal child runs (one per non-entry internal node stale-marked in this frame) as children of the parent run (`parent_run_id = <this parent>`, `child_key = <internal_node_alias>`). The internal-node `rimsky_nodes` row is the declarative-shared row (per D2 step 3); the per-invocation differentiation is via `parent_run_id`.
4. Internal children run per their own templates; subscribe to each other; eventually exit is dispatched.
5. Exit-node terminal: carry-rule fires. Copy exit's writeback to the parent run's writeback row in the same transaction as exit's terminal write. Annotate `@blessed-invariant: exit-node-writeback flows to parent run writeback`.
   - **Exit-never-runs edge case:** if exit never runs (e.g., an internal node failed and `strict.cancel_siblings: true` cancelled exit before it dispatched), the carry-rule never fires; the parent's writeback row remains empty (NULL writeback bytes). The aggregation engine in E2 still produces a terminal state for the parent per the rule table; only the writeback content is absent. Annotate this case in `subgraph_dispatch.go` with a comment referencing spec §Aggregation for sub-graphs.
6. Standard run-tree state aggregation evaluates the parent's terminal once all children are non-active.

**Verify:** `go test ./runtime/... -run SubGraphDispatch -count=1`. Scenario tests in N cover end-to-end.

---

### E7. Fan-out dispatch: SplitScope → N sub-claims → leaves — LANDED (dispatch 15: dispatcher-side child-run loop + parent-terminal rendezvous + N2 scenarios)

**Files:**
- `runtime/fanout_dispatch.go` (new)
- `runtime/fanout_dispatch_test.go` (new)

**Steps:**

1. On a fan-out node's parent-run creation: post-acquisition, supervisor reads the spec's `fan_out` block from the snapshot.
2. Resolve `partition_request` substitution (in particular, `{{trigger.message.payload.partition_request_override}}` against the trigger message bound to this frame, falling back to the literal `default` clause).
3. For each sub-scope returned by `SplitScope`: dispatch a child leaf run with `child_key = sub_scope.partition_key`. The leaf's `ExecuteRequest` carries the sub-claim's address.
4. Honor `parallelism`: if set, limit concurrent in-flight leaves; the remainder stays `stale` until a slot opens. Implemented via a counting semaphore in `runtime/dispatcher.go` keyed by parent run id.
5. On leaf terminal: call `CommitCandidate(producer_candidate_handle)` on success; `AbandonCandidate` on failure or strict-cancel.
6. On parent-run terminal (per aggregation): call `ClaimProducer.Commit(parent_handle_id)` for promote, or `ClaimProducer.Abandon` for abandon. On success: persist `version_id` on `rimsky_claim_handles.version_id`, AND write the producer's full `Commit` response (the version_id alongside any producer-supplied metadata bytes the response carries) into the parent run's writeback row. Spec §Output aggregation: "the parent's writeback comes from the producer's `Commit` response (carries version_id and producer-supplied metadata)." Producer metadata bytes are inert per `@blessed-invariant 20`; rimsky stores and forwards verbatim.

**Verify:** `go test ./runtime/... -run FanOutDispatch -count=1 -race`.

---

### E8. Lineage writer

**Files:**
- `runtime/lineage_writer.go` (new)
- `runtime/lineage_writer_test.go` (new)

**Steps:**

1. Subscribe (Go-side, in-process) to `rimsky_events` writes at the supervisor's terminal handler:
   - On every **leaf-run terminal**: build a `leaf_run` lineage record per spec §Content lineage / Leaf-run record shape and INSERT into `rimsky_lineage`.
   - On every **`Commit`** of a claim handle: build a `claim_commit` lineage record and INSERT.
2. Records carry hashes (params snapshot hash, userdata hash, scope_data hash) — compute via SHA-256 over a canonical-JSON representation (use the existing `graph/template/canonical/CanonicalSpecHash` helper as the hash convention).
3. `substitution_refs` populated from the attribute-substitution layer's resolved paths (extend `graph/attribute/substitution.go` to record what got read and from where, so the writer can populate this field). The substitution layer is `concept:inertness`-disciplined; the path is recorded, not the bytes — annotate that this preserves `@blessed-invariant 11/20/21`.
4. Idempotency: lineage writes are within the supervisor's terminal transaction; if the transaction rolls back, no lineage row is persisted. Re-execution of a terminal (e.g., retry path) writes a new lineage row — lineage is append-only, multiple records per logical event are acceptable (the projection accommodates).

**Verify:** `go test ./runtime/... -run LineageWriter -count=1`.

---

### E9. Held-durable claim lifecycle: persist past auto-terminal; instance termination cleanup

**Files:**
- `runtime/auto_terminal.go` (extend)
- `runtime/instance_termination.go` (new — or extend the existing instance-termination path)
- `runtime/orphan_reaper.go` (extend — skip held-durable rows)

**Steps:**

1. In auto-terminal: after `Commit` for a `lifetime: durable` claim, do NOT delete the `rimsky_claim_handles` row; instead set `held_durable = TRUE`. On `Abandon` for `lifetime: durable`, the row IS deleted (the durable artifact was never promoted).
2. Instance termination flow: walk all `held_durable = TRUE` rows for the instance; call `ClaimProducer.Release(claim_id)` on each (sequentially); log per-claim outcome; do not block instance-termination completion on `Release` failures.
3. Orphan-claim reaper: add `WHERE held_durable = FALSE` to the existing reaper SELECT. Annotate new "held-durable persistence" invariant here.

**Verify:** `go test ./runtime/... -run HeldDurable -count=1 -race`.

---

### E10. Watchdog: run-tree retention sweep + lineage retention sweep

**Files:**
- `runtime/watchdog/retention_sweeps.go` (new)
- Tests adjacent.

**Steps:**

1. Add `SweepRunTreeRetention(ctx, instance_id, recent_frames_kept)`:
   - SELECT frame IDs older than the `recent_frames_kept`-th most-recent frame per instance.
   - For each old frame: DELETE `rimsky_node_runs` rows in that frame and their descendants (CASCADE via `parent_run_id`). Skip runs whose `rimsky_claim_handles.held_durable = TRUE` (i.e., don't delete a run that owns a held-durable claim).
2. Add `SweepLineageRetention(ctx, lineage_trailing)`:
   - DELETE `rimsky_lineage` rows where the corresponding artifact (run_id or claim_handle_id) is no longer present AND `observed_at < now() - lineage_trailing`.
3. Wire both into the existing watchdog tick (the runtime's scheduler-tick sweep coordinator).

**Verify:** `go test ./runtime/watchdog/... -count=1`.

---

### E11. Per-reason `max_park_duration` + `SweepParkedNodes` extension

**Files:**
- `runtime/watchdog/parked.go` (extend the existing `SweepParkedNodes`)
- `control/config/config.go` (extend `RimskyConfig` with `max_park_duration` per spec)

**Steps:**

1. Read existing `SweepParkedNodes`. It currently checks `max_park_duration` flat. Extend to consult per-reason caps (`time_wait`, `callback_wait`, `retry_backoff`, `other`).
2. When a per-reason cap trips: transition the run to `failed` with `error_class: "park_timeout"` and a reason-specific message.
3. Config shape per spec §Parked-state taxonomy / Per-reason `max_park_duration` config. Defaults: `time_wait: 1h`, `callback_wait: 7d`, `retry_backoff: 1h`, `other: 1h`.

**Verify:** `go test ./runtime/watchdog/... -run Parked -count=1`.

---

### E12. Supervisor handling of `Snooze.reason`

**Files:**
- `runtime/runner_terminal.go` (or wherever the existing `Snooze` handler lives)

**Steps:**

1. Re-read the existing `Snooze`-terminal handler. Currently it sets `state = parked`, persists `parked_resume_at`, `session_token`, `payload`.
2. Extend: persist `parked_reason` from `Snooze.reason` (mapped from the proto enum to the string form `TIME_WAIT | CALLBACK_WAIT | RETRY_BACKOFF | OTHER`); persist `parked_reason_label` from `Snooze.reason_label`.
3. Validate `reason_label` required when `reason = OTHER` (reject the terminal with a runtime error if `OTHER` without label).
4. Audit the transition reason in `rimsky_events` per existing pattern.

**Verify:** `go test ./runtime/... -run SnoozeReason -count=1`.

---

### E13. Userdata-overrides merge (extend per-instance overrides if needed)

**Steps:**

1. Re-read `runtime/applyUserdataOverrides` (per CLAUDE.md). The existing behavior covers `by_executor` and `by_node`. Confirm spec doesn't change this — it doesn't. No-op task; explicit to confirm the merge order survives the run-tree extension (sub-graph children inherit the calling node's userdata after merge per the canonicalizer's absorption rules).

**Verify:** existing tests pass.

---

### E14. Substitution-layer extensions: `{{trigger.message.payload.X}}` + `{{child.partition_key}}` + `subscribes: on: message` matching

**Files:**
- `graph/attribute/substitution.go` (extend `walkPath` and the substitution-context resolver)
- `graph/attribute/substitution_test.go`
- `graph/scheduler/` or wherever cascade-walker handles subscription matching (locate via grep for "subscribes" handling)
- `runtime/message_delivery.go` (extend if subscription matching lives there)

**Steps:**

1. Extend the subscription matcher to handle `on: message` subscriptions. At message delivery (E5), walk subscriptions in the target instance; match envelope fields (`kind`, `sender`, `sender_kind`, `target`) against subscription filters.
2. Extend the substitution-context resolver in `graph/attribute/substitution.go` to recognize two new top-level namespaces:
   - `{{trigger.message.payload.X}}` — resolves to the trigger message's payload field `X` for the frame the dispatched executor belongs to. Bind from the frame's trigger message (looked up via `rimsky_messages.frame_id`).
   - `{{child.partition_key}}` — resolves to the child run's `child_key` value for fan-out leaf dispatches. Bind at child dispatch in E7. The substitution-layer accepts the binding as part of the per-dispatch substitution context.
3. Both new namespaces obey the inertness discipline (`@blessed-invariant 11/20/21`): rimsky reads the named field via `walkPath` only; never logs, formats with `%v`, or otherwise inspects.
4. Add unit tests covering both new namespaces.

**Verify:** `go test ./graph/attribute/... -count=1 && go test ./runtime/... -run MessageSubscriptionMatch -count=1`.

---

### E15. Backfill operation handling

**Files:**
- `runtime/backfill.go` (new)
- `runtime/backfill_test.go` (new)

**Steps:**

1. Function `CreateBackfill(tx, instance_id, target_node, partition_request_override, reason) → message_id`:
   - Validate `target_node` exists in the template and has `fan_out.partition_request` referencing trigger substitution (warning if not — surfaced to the operator).
   - Generate `backfill_operation_id`. Construct an `invalidate`-kind message envelope with payload `{partition_request_override, backfill_operation_id, reason}`, `target = target_node`, `sender = "operator"`, `sender_kind = "operator"`. ENQUEUE via `EnqueueMessage`.
   - Return message id + backfill_operation_id.
2. Function `CancelBackfill(tx, op_id)`: marks the operation cancelled in a new `rimsky_backfill_status` table OR sets a sentinel on the messages (`cancelled: true` column added to `rimsky_messages` — preferred since spec frames backfills as "messages with a backfill_operation_id"). For pending messages with this op_id and `delivered_at IS NULL`, set `delivered_at = now()` and `cancelled = TRUE` to prevent delivery. In-flight frames complete normally (no preemption in V1).
3. Function `GetBackfillStatus(op_id)`: joins messages + parent-run + children states (aggregated) per spec §Backfills / Control-api `GET /backfills/{op_id}`.
4. Decision point: add a `cancelled BOOLEAN NOT NULL DEFAULT FALSE` column to `rimsky_messages` in a new migration (call this B13 — slot into section B numbering, or land alongside this task as a follow-on migration). Update spec-time tasks accordingly.

**Verify:** `go test ./runtime/... -run Backfill -count=1`.

---

### B13. Migration — add `cancelled` to `rimsky_messages`

**Files:**
- `foundation/persistence/postgres/migrations/NNNN_messages_cancelled.sql` (new)
- `foundation/persistence/sqlite/migrations/NNNN_messages_cancelled.sql` (new)

(Out-of-order numbering; this is a Section B migration but I number it B13 because it's discovered during E15.)

**Steps:**

1. `ALTER TABLE rimsky_messages ADD COLUMN cancelled BOOLEAN NOT NULL DEFAULT FALSE;` (both drivers).

**Verify:** migrations test passes.

---

### E16. `rimsky-scheduler` cron-fire path removed — LANDED (dispatch 13)

**Files:**
- `graph/scheduler/schedule_ticker.go` (delete; or remove just the cron-fire body and keep the file if it has other sweep responsibilities)
- `graph/scheduler/scheduler.go` (locate cron-fire invocation; remove)

**Steps:**

1. Re-read `graph/scheduler/`. Identify the cron-fire path (the code that consults `rimsky_schedules` and stale-marks nodes per cron). Remove.
2. Keep the rest of the scheduler tick (orphan reapers, parked timeouts, frame-progress watchdog, retention sweeps from E10).
3. `rimsky_schedules` is dropped in B10; the Go-side code that wrote to it is removed here.

**Verify:** `go test ./graph/scheduler/... -count=1`.

---

# Section F — Control-API endpoints

### F1. `POST /instances/{id}/messages`

**Files:**
- `control/controlapi/instances.go` (extend) or `control/controlapi/messages.go` (new)
- Adjacent test file.

**Steps:**

1. Handler: validate the body shape `{kind, target?, payload?}`. `sender` derived from caller identity (`operator` for V1 since cross-instance is V2).
2. Call `EnqueueMessage` via the runtime API (transactional).
3. Return `201 Created` with the message id.

**Verify:** `go test ./control/controlapi/... -run Messages -count=1`.

---

### F2. `GET /instances/{id}/messages` and `GET /messages/{id}`

**Steps:**

1. List handler: query params `kind`, `sender_kind`, `target`, `delivered_at` (ISO time range), `backfill_operation_id`. Paginated (limit + after cursor).
2. Detail handler: fetch single message by id; 404 if not found.

**Verify:** test pass.

---

### F3. `POST /sensors/{watch_id}/observations`

**Files:**
- `control/controlapi/sensors.go` (new)

**Steps:**

1. Resolve `watch_id` to a row in `rimsky_sensor_watches`. 404 if not found.
2. Apply `on_observation.payload_template` substitution against the observation body.
3. Construct an envelope: `sender = watch.sensor_name`, `sender_kind = "sensor"`, `target = watch.on_observation.target_node`, `kind = watch.on_observation.message_kind`.
4. Call `EnqueueMessage`.
5. Update `rimsky_sensor_watches.last_observed_at = now()`.

**Verify:** test pass.

---

### F4. `POST /instances/{id}/backfills` and adjacent GETs / cancel

**Files:**
- `control/controlapi/backfills.go` (new)

**Steps:**

1. `POST /instances/{id}/backfills` body `{target_node, partition_request_override, reason}` → calls `runtime.CreateBackfill`.
2. `GET /instances/{id}/backfills` lists ops.
3. `GET /backfills/{op_id}` returns the status object per spec §Backfills.
4. `GET /backfills/{op_id}/partitions` returns per-child-run drill-down.
5. `POST /backfills/{op_id}/cancel` calls `runtime.CancelBackfill`.

**Verify:** test pass.

---

### F5. Asset endpoints

**Files:**
- `control/controlapi/assets.go` (new)

**Steps:**

1. `GET /instances/{id}/assets` — list `rimsky_claim_handles` rows for this instance filtered to `held_durable = TRUE` AND producer advertises `data_processing`.
2. `GET /instances/{id}/assets/{alias}` — single asset's full state. The `{alias}` path parameter is the dotted form `{template_node_alias}.{claim_alias}` (pinned in Pre-resolved design decisions). Reject malformed aliases with `400 Bad Request`.
3. `GET /instances/{id}/assets/{alias}/versions` — calls `DataProcessing.ListVersions` on the corresponding producer.
4. `GET /instances/{id}/assets/{alias}/materialization-history` — joins `rimsky_lineage` records of `claim_commit` for this claim_handle_id with their parent runs and frames.
5. `POST /instances/{id}/assets/{alias}/materialize` — alias for `POST /instances/{id}/messages` constructing an `invalidate` envelope targeting the producer node.
6. `DELETE /instances/{id}/assets/{alias}` — explicit operator deletion. Refuse (`409 Conflict`) if any in-flight run holds the claim. Otherwise: call `ClaimProducer.Release(claim_id)`; DELETE the claim_handle row; audit in `rimsky_events`.

**Verify:** test pass.

---

### F6. Lineage endpoints

**Files:**
- `control/controlapi/lineage.go` (new)

**Steps:**

1. `GET /lineage/runs/{run_id}` — fetch one `leaf_run` record.
2. `GET /lineage/runs/{run_id}/ancestors?depth=N` — recursive backward walk. Resolve via `substitution_refs` (matching `(source_node_alias, source_kind, source_version_or_id)` against forward records) AND `held_claims` (joining to `claim_commit` records via `claim_handle_id`). Bounded by `depth` (max 50).
3. `GET /lineage/runs/{run_id}/descendants?depth=N` — recursive forward walk. Mirror; resolve via the GIN index on `record->'substitution_refs'`.
4. `GET /lineage/claims/{claim_handle_id}` — fetch one `claim_commit` record.
5. `GET /lineage/claims/{claim_handle_id}/ancestors?depth=N` — walk via the `sub_claim_handle_ids` chain and the runs that wrote each.
6. `GET /lineage/by-source/{source_type}/{source_id}` — reverse lookup; query `(record_kind = 'leaf_run' AND record @> '{"substitution_refs":[{"source_kind":..., "source_version_or_id":...}]}')` (Postgres JSONB containment + GIN index).
7. `GET /lineage/by-producer/{executor_name}?version=...` — by-producer; lookup on `record->>'executor_name'` plus optional `record->>'executor_version'`.

**Verify:** test pass.

---

### F7. `GET /diagnostics/parked?reason=` filter

**Files:**
- `control/controlapi/diagnostics.go` (extend the existing parked endpoint)

**Steps:**

1. Extend the existing `GET /diagnostics/parked` to accept `reason` query param.
2. Filter `rimsky_node_runs` rows where `state = 'parked' AND parked_reason = <reason>` if provided.

**Verify:** test pass.

---

### F8. Sensor lifecycle on instance create / terminate

**Files:**
- `control/controlapi/instances.go` (extend the create/terminate handlers)
- `runtime/sensors.go` (new — calls `Sensor.StartWatch` / `StopWatch`)
- `runtime/remote/sensor_client.go` (new — gRPC client for Sensor protocol)

**Steps:**

1. Instance create flow: after canonicalization succeeds, walk the template's `sensors:` block. For each:
   - Generate a fresh `watch_id` (rimsky-side UUIDv7) — this is the `rimsky_sensor_watches.id` and the identifier the sensor service binds internally.
   - Resolve `config` substitution against instance params.
   - INSERT a `rimsky_sensor_watches` row with `id = <watch_id>`, the resolved config, `state = active`, `started_at = now()`.
   - Call `Sensor.StartWatch(watch_id, instance_id, kind, resolved_config)` via remote client.
   - On failure: leave `state = failed`, log; do not block instance creation.
2. Instance terminate flow: walk active `rimsky_sensor_watches` rows for the instance; call `Sensor.StopWatch(watch_id)` on each; set `state = stopped`.
3. Resync after rimsky restart: at supervisor startup, walk `rimsky_sensor_watches` rows in `state = active` (grouped by `sensor_name`); for each sensor, call `Sensor.ListWatches()` on the sensor service; reconcile two directions:
   - Watches rimsky expects but the sensor doesn't report → re-issue `StartWatch` to restore.
   - Watches the sensor reports but rimsky doesn't expect (orphans, e.g., from a deleted instance whose `StopWatch` never landed) → issue `StopWatch` on the sensor and log per-orphan-watch at WARN.
   - Add a startup hook in the supervisor's process init (locate via grep for the existing supervisor startup wiring).

**Verify:** `go test ./runtime/... -run Sensor -count=1`. Scenario in N covers the resync.

---

### F8b. Extend `POST /instances` body to accept `frame_delivery_mode`

**Files:**
- `control/controlapi/instances.go` (the create handler)
- Adjacent test file.

**Steps:**

1. Re-read the existing `POST /instances` body parser. Currently it accepts `{template, instance_key?, params, userdata_overrides?}`.
2. Add optional `frame_delivery_mode string` to the request body. Validate `frame_delivery_mode ∈ {"serial_queue", "coalesce"}`; default `"coalesce"` per spec §Messages / Delivery.
3. Persist the resolved value on `rimsky_instances.frame_delivery_mode` (column added in B11).
4. Document the field in the handler's godoc and any OpenAPI / endpoint catalog.

**Verify:** `go test ./control/controlapi/... -run InstanceCreate -count=1`. Round-trip test asserts the persisted value matches the request (or defaults correctly when omitted).

---

### F9. Validation pipeline integration with template registration

**Files:**
- `control/controlapi/templates.go` (extend)

**Steps:**

1. Existing template-register handler currently runs canonicalization. After canonicalization succeeds but before persistence (the content-addressed template row), run the validation pipeline (E1's `runtime/validation_pipeline.go`):
   - userdata_schema static check
   - `Validate` RPCs to advertising services
2. On hard reject: return `400 Bad Request` with error list.
3. On warnings: persist the template + return `200 OK` with warning list; if `--warnings-as-errors` (operator flag — CLI-only; the API surface accepts a query param `warnings_as_errors: true` for parity), treat warnings as errors.

**Verify:** test pass.

---

# Section G — CLI subcommands

### G1. `rimsky-cli asset {list,show,materialize,versions,delete,lineage}`

**Files:**
- `control/cli/asset.go` (new)
- Adjacent test file (existing CLI tests use a httptest backend).

**Steps:**

1. Subcommand group `asset` with the listed verbs. Each calls the F5 endpoints.
2. `lineage <id>:<alias> --version <v>` calls F6 `GET /lineage/claims/{claim_handle_id}/ancestors`.

**Verify:** `go test ./control/cli/... -run Asset -count=1`.

---

### G2. `rimsky-cli backfill {create,list,show,cancel}`

**Steps:**

1. Subcommand group `backfill`. `create --instance --node --range --reason` builds a `partition_request_override` payload from the `--range` shorthand (e.g., `2024-01-01..2024-09-30` → `{date_range: {start, end}}`) and calls F4.

**Verify:** test pass.

---

### G3. `rimsky-cli messages tail` + filter subcommands

**Files:**
- `control/cli/messages.go` (new)

**Steps:**

1. `tail --instance X --kind invalidate --sender-kind operator` — long-polls or repeatedly hits `GET /instances/X/messages?delivered_at_after=...`.
2. `show <id>` — fetches one message.

**Verify:** test pass.

---

### G4. `rimsky-cli lineage prune`

**Files:**
- `control/cli/lineage.go` (new)

**Steps:**

1. `prune --before <date>` — calls a new control-api `POST /admin/lineage/prune` endpoint (add this to F as an extension if not already present). Or implements prune locally via direct DB access — decide based on whether the CLI has direct DB access elsewhere (it doesn't, per the thin-client pattern). Add the control-api endpoint here.

**Verify:** test pass.

---

### G5. `rimsky-cli parked list --reason=`

**Files:**
- `control/cli/parked.go` (likely exists; extend)

**Steps:**

1. Add `--reason` flag; forwards to F7.

**Verify:** test pass.

---

### G6. `rimsky-cli template register --warnings-as-errors`

**Files:**
- `control/cli/template.go` (the existing `template register` subcommand; locate via grep)
- Adjacent test file.

**Steps:**

1. Add a `--warnings-as-errors` boolean flag to the existing `template register` subcommand.
2. When set, forward as `?warnings_as_errors=true` query param to the control-api `POST /templates` endpoint (the query param surface added in F9 step 3).
3. CLI exit code: non-zero when warnings escalated to errors and the registration is rejected.
4. Document the flag in the subcommand's help text per spec §Validation pipeline integration: "Errors at either step reject the template registration; warnings surface to the operator (`rimsky-cli template register --warnings-as-errors` to escalate)."

**Verify:** `go test ./control/cli/... -run TemplateRegister -count=1`.

---

# Section H — Bundled stores (CUT)

Section H is cut from this plan and is not intended to land later. The three bundled stores originally listed (`stores/parquet-store/`, `stores/geo-parquet-store/`, `stores/geo-postgis-store/`) are not delivered.

**Rationale.** A bundled reference store worth shipping requires meaningful engineering in its own right: row-group sizing, schema evolution, partition discovery and pruning, predicate pushdown, S3 lifecycle, CRS handling, spatial-index strategy, transactional version promotion. A naive impl is misleading — users copy it and inherit the limitations. Rimsky is project-agnostic per `.claude/rules/rules.md`; specialized format stores belong with the users who need them, not bundled with the project-agnostic core. The DataProcessing / Validation / ClaimProducer protocols and their conformance suites (Section M) ship without an opinionated reference impl for any specialized format.

**What replaces H for the rest of the plan.**

- **Stub-store DataProcessing extension.** The existing `stores/stub/` store gains a thin DataProcessing surface: `BeginCandidate` / `CommitCandidate` / `AbandonCandidate` round-trip cleanly in-memory; `ListVersions` / `ListPartitions` / `GetVersionSchema` return fixture data; `Capabilities` advertises `protocols: [claim_producer, data_processing]`, `supports_split_scope: true`, `supports_scopes_conflict: true`, and a small `data_shapes: [stub]`. This is the self-test target for M1 (data-processing conformance) and is invoked by N6/N7/N10 wherever a DataProcessing target is needed. Implementation is small (estimate: one file, one test) and lives inside the stub-store directory; no new top-level directory created.
- **No LocalStack / Parquet / PostGIS dependencies** are added to the smoke fixture or scenario tests. References in O1 and elsewhere drop accordingly.

**Downstream touchpoints updated by this cut:**

- Pre-resolved design decisions: Parquet library, PostGIS driver, parquet-store advertised aggregator set entries removed; S3 SDK entry narrowed to `sensor-object-store`.
- Critical-path / Linear execution order: H removed from the sequence; dependents (M, N, O) re-stated to omit H.
- M1, M2: self-test targets flip from parquet-store to the stub-store DataProcessing extension.
- O1: parquet-store + LocalStack boot removed from the smoke fixture; the smoke end-to-end exercises DataProcessing through the stub.
- O2: integration tests for bundled stores cut entirely.
- T1: CHANGELOG note acknowledging the cut.
- Q: no concept doc changes — `data-processing`, `asset`, `claim-lifetime`, etc. are protocol-level concepts and don't hinge on a bundled impl.

---

# Section I — Bundled verifier executors

### I1. `executors/verifier-shape-checks/`

**Files:**
- `executors/verifier-shape-checks/main.go` (new)
- `executors/verifier-shape-checks/checks/no_nulls.go` (new)
- `executors/verifier-shape-checks/checks/nullable_fields_present.go` (new)
- `executors/verifier-shape-checks/checks/pk_unique.go` (new)
- `executors/verifier-shape-checks/checks/row_count_ratio.go` (new)
- `executors/verifier-shape-checks/checks/row_count_absolute.go` (new)
- `executors/verifier-shape-checks/checks/value_in_set.go` (new)
- `executors/verifier-shape-checks/checks/regex_match.go` (new)
- `executors/verifier-shape-checks/checks/numeric_range.go` (new)
- `executors/verifier-shape-checks/registry.go` (new — registers each check by name)
- `executors/verifier-shape-checks/*_test.go` (new)
- `cmd/rimsky-verifier-shape-checks/main.go` (new)

**Steps:**

1. Implement `Executor` (the existing service protocol). Userdata schema: `{checks: [{kind: "no_nulls", config: {...}}, ...]}`. Co-holds upstream claims declared in `holds:`.
2. Each check reads the upstream claim's address (Parquet file or PostGIS table), runs its predicate, returns pass/fail/warn.
3. Aggregated terminal: `Complete{changed: false}` on all pass; `Error{error_class: "verifier_failed"}` on any fail.
4. Apache-licensed: file `LICENSE-APACHE` in the package directory and SPDX headers on each source file.

**Verify:** `go test ./executors/verifier-shape-checks/... -count=1`.

---

### I2. `executors/verifier-http/`

**Files:**
- `executors/verifier-http/main.go`
- `executors/verifier-http/executor.go`
- `executors/verifier-http/*_test.go`
- `cmd/rimsky-verifier-http/main.go`

**Steps:**

1. Implement `Executor`. Userdata schema: `{url, body_template, expected_status, timeout}`.
2. At dispatch: substitute `body_template` against the claim's address and userdata; POST to `url`; check response status; terminal: `Complete` on expected, `Error` on mismatch.

**Verify:** `go test ./executors/verifier-http/... -count=1`.

---

### I3. Update existing bundled executors to populate `Snooze.reason`

**Files:**
- `executors/claude-agent/src/` (TypeScript; locate Snooze-emitting paths)
- `executors/claude-agent/src/*.test.ts`
- `executors/http-node/` (locate Snooze-emitting paths)
- `executors/http-node/*_test.go`
- `executors/stub/` (locate Snooze-emitting paths; test fixtures only)
- `executors/stub/*_test.go`
- `verifier-shape-checks/` and `verifier-http/` from I1/I2 — confirm Snooze isn't emitted; if so, ignore.

**Steps:**

1. Grep each existing bundled executor for `Snooze` emission (`grep -rn 'Snooze' executors/`). For each emit site, classify per spec §Parked-state taxonomy / Bundled emitter updates:
   - Rate-limit-aware wait (e.g., `claude-agent` backing off on API rate limits) → set `reason: PARK_REASON_RETRY_BACKOFF`.
   - Long-running-job callback wait (e.g., `claude-agent` parking pending an async callback; `http-node` parking on an async HTTP webhook) → set `reason: PARK_REASON_CALLBACK_WAIT`.
   - Time-based polling wait (e.g., `http-node` polling on `Retry-After`) → set `reason: PARK_REASON_TIME_WAIT`.
   - If a Snooze site doesn't fit any of the three categories → set `reason: PARK_REASON_OTHER` and populate `reason_label` with a freeform tag describing the wait.
2. Update each `Snooze` construction to set `reason` (and `reason_label` if applicable). For the TS `claude-agent`, add `reason` to the typed snooze payload and pass through to the wire envelope.
3. Update each executor's existing tests that assert on Snooze emission to also assert the new `reason` field is populated.
4. `executors/stub/` test fixtures may emit Snooze for synthetic scenarios; pick a sensible default (`OTHER` with `reason_label: "stub_test_fixture"`).

**Verify:** `go test ./executors/http-node/... ./executors/stub/... -count=1` and `cd executors/claude-agent && npm test`. `grep -A3 'Snooze' executors/*/` shows every emit site sets `reason`.

---

# Section J — Bundled sensors

New top-level directory `sensors/`. Each sensor a standalone Go binary at `sensors/<kind>/` + `cmd/rimsky-sensor-<kind>/main.go`.

### J1. `sensors/sensor-cron/`

**Files:**
- `sensors/sensor-cron/main.go` (new)
- `sensors/sensor-cron/sensor.go` (new)
- `sensors/sensor-cron/cron_state.go` (new — next-fire-at bookkeeping)
- `sensors/sensor-cron/*_test.go` (new)
- `cmd/rimsky-sensor-cron/main.go` (new)

**Steps:**

1. Implement `Sensor` protocol:
   - `Capabilities` — `supported_kinds: [{kind: "cron", config_schema: {...}}]`; `protocols: [sensor]`.
   - `StartWatch(watch_id, instance_id, kind: "cron", resolved_config: {cron, missed_fires})` — parse cron expression (robfig/cron/v3); register watch in internal state with next-fire-at; persist to sensor's own SQLite (or Postgres if configured for multi-replica).
   - `StopWatch` — remove watch.
   - `ListWatches` — list active watches.
2. Fire loop: tick every second; for each watch where `next_fire_at <= now`: compute next fire from current next-fire-at (not now — missed fires NOT backfilled, per spec); POST observation to rimsky's `/sensors/{watch_id}/observations` endpoint.
3. Multi-replica safety: optional Postgres advisory lock per watch_id if configured (`mode: multi-replica`). Default single-replica.
4. YAML config (sensor-side): `{rimsky_endpoint: http://..., advisory_lock: bool, state_db: postgres://... | sqlite://...}`.

**Verify:** `go test ./sensors/sensor-cron/... -count=1`.

---

### J2. `sensors/sensor-http/` — LANDED (dispatch 14)

**Files:**
- `sensors/sensor-http/main.go`
- `sensors/sensor-http/sensor.go`
- `sensors/sensor-http/poller.go`
- `sensors/sensor-http/*_test.go`
- `cmd/rimsky-sensor-http/main.go`

**Steps:**

1. `Capabilities` — `supported_kinds: [{kind: "http", config_schema: {url, poll_interval, match: {status?, jsonpath?}}}]`.
2. Poll loop: per watch, GET the URL on `poll_interval`; match against `status` / `jsonpath`; on match, push observation with the matched data as payload.
3. Watermark: store last-observed (response body hash) per watch; only push on change.

**Verify:** `go test ./sensors/sensor-http/... -count=1`.

---

### J3. `sensors/sensor-object-store/` — LANDED (dispatch 14)

**Files:**
- `sensors/sensor-object-store/main.go`
- `sensors/sensor-object-store/sensor.go`
- `sensors/sensor-object-store/s3_watcher.go`
- `sensors/sensor-object-store/gcs_watcher.go`
- `sensors/sensor-object-store/azure_watcher.go`
- `sensors/sensor-object-store/*_test.go`
- `cmd/rimsky-sensor-object-store/main.go`

**Steps:**

1. `Capabilities` — `supported_kinds: [{kind: "object-store", config_schema: {backend: s3|gcs|azure, bucket, prefix, poll_interval, watermark_field}}]`.
2. Per watch: poll the bucket+prefix on `poll_interval`; track high-watermark by object name or LastModified; emit one observation per new object.
3. Idempotency: dedup against the watermark.

**Verify:** `go test ./sensors/sensor-object-store/... -count=1`.

---

### J4. `sensors/sensor-webhook/` — LANDED (dispatch 14)

**Files:**
- `sensors/sensor-webhook/main.go`
- `sensors/sensor-webhook/sensor.go`
- `sensors/sensor-webhook/server.go`
- `sensors/sensor-webhook/*_test.go`
- `cmd/rimsky-sensor-webhook/main.go`

**Steps:**

1. `Capabilities` — `supported_kinds: [{kind: "webhook", config_schema: {path_prefix, idempotency_header}}]`.
2. Run a chi HTTP server. Each `StartWatch` registers a route under `path_prefix`. Inbound POSTs → push observation to rimsky.
3. Idempotency-key header support: dedup by header value.

**Verify:** `go test ./sensors/sensor-webhook/... -count=1`.

---

# Section K — Bundled openlineage subscriber

### K1. `subscribers/openlineage/` — LANDED (dispatch 13)

**Files:**
- `subscribers/openlineage/main.go` (new)
- `subscribers/openlineage/poller.go` (new — polling loop over `rimsky_lineage`)
- `subscribers/openlineage/cursor.go` (new — cursor persistence in the state DB)
- `subscribers/openlineage/emitter.go` (new — builds OpenLineage events)
- `subscribers/openlineage/transport.go` (new — HTTP POST to backend)
- `subscribers/openlineage/*_test.go` (new)
- `cmd/rimsky-openlineage/main.go` (new)

**Steps:**

1. Implementation shape (pinned in Pre-resolved design decisions): the subscriber **polls** `rimsky_lineage` for new records since a stored cursor. Do NOT implement the alternative LifecycleSubscriber-events path; polling decouples the subscriber from the lifecycle event surface and treats it as a passive reader of the projection.
2. Maintain the cursor in a small state DB (Postgres or SQLite, configured per deployment). The cursor is the last `rimsky_lineage.id` (or `observed_at`) read.
3. For each new `leaf_run` record, build an OpenLineage event and POST to the configured backend per spec §Content lineage / OpenLineage emitter.
4. For each new `claim_commit` record, build a complementary OpenLineage event (`COMPLETE` job-run with output dataset namespace+name derived from `(producer_name, scope_data_hash)`).
5. Configuration: `{backend_url, namespace, polling_interval, cursor_state_db}`.

**Verify:** `go test ./subscribers/openlineage/... -count=1`. Smoke test in O against Marquez.

---

# Section L — Atomic-staging example

### L1. `examples/atomic-staging-fs-producer/` — LANDED (dispatch 14; verified pre-existing)

**Files:**
- `examples/atomic-staging-fs-producer/main.go` (new)
- `examples/atomic-staging-fs-producer/producer.go` (new)
- `examples/atomic-staging-fs-producer/template.yaml` (new — sample template)
- `examples/atomic-staging-fs-producer/README.md` (new — pointer to `docs/agents/examples/atomic-staging.md`)
- `docs/agents/examples/atomic-staging.md` (new — public-facing doc)

**Steps:**

1. Implement `ClaimProducer` using POSIX directory rename for atomic Commit. Subdir per scope; staging via `Open` writes to `<scope>/staging/<run_id>/`; `Commit` renames `staging/<run_id>` to `<scope>/canonical/<version_id>/` (atomic within filesystem).
2. Sample template demonstrates the staging-then-swap pattern: producer acquires a `lifetime: subgraph` claim; verifier executors co-hold via `holds:`; aggregation drives `Commit` vs `Abandon`.
3. `docs/agents/examples/atomic-staging.md` documents the pattern and substrate caveats per spec §Atomic-staging worked example.

**Verify:** `go test ./examples/atomic-staging-fs-producer/... -count=1`. Sample template registers cleanly against a real rimsky instance (smoke).

---

# Section M — Conformance binaries

### M1. `cmd/rimsky-data-processing-conformance/` — LANDED (dispatch 16)

**Files:**
- `cmd/rimsky-data-processing-conformance/main.go` (new)
- `cmd/rimsky-data-processing-conformance/tests.go` (new — the test battery)

**Steps:**

1. CLI: `--endpoint <addr> --transport grpc`.
2. Run test battery:
   - Capabilities advertisement parses cleanly.
   - `BeginCandidate` → `CommitCandidate` round-trip per declared materialization.
   - `BeginCandidate` → `AbandonCandidate` round-trip.
   - `ListVersions`, `ListPartitions`, `GetVersionSchema` smoke tests.
   - Idempotency: re-calling `BeginCandidate` with the same idempotency_key returns the same candidate_handle.
   - Concurrent writes per materialization (light fuzz).
3. Output a pass/fail report.

**Verify:** `go test ./cmd/rimsky-data-processing-conformance/... -count=1`. Integration: runs cleanly against the stub-store DataProcessing extension (see Section H for the stub-store extension, and Section O for the smoke).

---

### M2. `cmd/rimsky-validation-conformance/` — LANDED (dispatch 16)

**Files:**
- `cmd/rimsky-validation-conformance/main.go` (new)
- `cmd/rimsky-validation-conformance/tests.go` (new)

**Steps:**

1. CLI: `--endpoint <addr> --transport grpc --role <role>`.
2. Run battery: Capabilities advertises `validation` + the role; `Validate` request/response shape conformance; error/warning semantics.

**Verify:** test pass; runs cleanly against any service that advertises the Validation protocol (the stub-store DataProcessing extension may add a minimal Validation surface for self-test, or M2 self-tests against `executors/verifier-shape-checks/` which already implements Validation).

---

### M3. `cmd/rimsky-sensor-conformance/` — LANDED (dispatch 16)

**Files:**
- `cmd/rimsky-sensor-conformance/main.go` (new)
- `cmd/rimsky-sensor-conformance/tests.go` (new)

**Steps:**

1. CLI: `--endpoint <addr> --transport grpc --kind <kind>`.
2. Run battery: `StartWatch` / `StopWatch` / `ListWatches` lifecycle; observation push integration (stand up a fake rimsky receiver on a free port and assert observations land); resync after restart (kill the sensor's state DB, restart, call `ListWatches` from rimsky's perspective, assert StartWatch is re-issued).

**Verify:** test pass; runs cleanly against `sensors/sensor-cron/`.

---

### M4. Extend `rimsky-claim-producer-conformance` — LANDED (dispatch 16)

**Files:**
- `cmd/rimsky-claim-producer-conformance/tests.go` (extend)

**Steps:**

1. Add tests for the new optional methods: `SplitScope` (if advertised), `ScopesConflict` (if advertised). Test battery skipped gracefully if the producer doesn't advertise.

**Verify:** test pass.

---

### M5. Extend `rimsky-executor-conformance` — LANDED (dispatch 16)

**Files:**
- `cmd/rimsky-executor-conformance/tests.go` (extend)

**Steps:**

1. Add tests for `Snooze.reason` emission. Provide a test harness that asserts executors emitting `Snooze` populate `reason` (warning if absent; required when `reason = OTHER` → label must be set).

**Verify:** test pass.

---

# Section N — Scenario tests

All scenario tests live under `test/scenarios/`. Each scenario follows the existing convention: bootstrap via `graph/scenario.Start` against pre-launched producer-services on ephemeral ports; use `internal/pgtest/` for the Postgres harness. Add per-scenario directories where the count of files justifies (e.g. `test/scenarios/run_tree/`, `test/scenarios/messages/`).

### N1. Run-tree scenarios — LANDED (dispatch 16)

**Files:**
- `test/scenarios/run_tree/fanout_aggregation_test.go`
- `test/scenarios/run_tree/state_propagation_test.go`
- `test/scenarios/run_tree/strict_cancel_siblings_test.go`
- `test/scenarios/run_tree/error_policy_threshold_test.go`
- `test/scenarios/run_tree/error_policy_best_effort_test.go`
- `test/scenarios/run_tree/error_policy_first_test.go`
- `test/scenarios/run_tree/deep_tree_subgraph_of_fanout_test.go`
- `test/scenarios/run_tree/deep_tree_fanout_of_subgraph_test.go`
- `test/scenarios/run_tree/candidate_handle_threaded_test.go` (asserts: at fan-out dispatch, `producer_candidate_handle` is persisted on each sub-claim row; leaf `ExecuteRequest` carries the bytes via the per-claim address slot; on leaf success, supervisor reads back from the row and calls `CommitCandidate` with matching bytes; on parent terminal, `Commit` returns version_id and producer-supplied metadata, both surfaced in the parent's writeback)

**Steps:**

1. For each: assemble a template exercising the scenario; create an instance; drive it through a frame; assert run-tree shape + state transitions + aggregation outcome.
2. Use the stub claim-producer (extended in section H if needed to support `SplitScope` for tests) for scope partitioning.

**Verify:** `go test ./test/scenarios/run_tree/... -count=1 -race`.

---

### N2. Recursive scope scenarios

**Files:**
- `test/scenarios/scope/recursive_sub_claims_test.go`
- `test/scenarios/scope/producer_aware_conflict_test.go`
- `test/scenarios/scope/auto_terminal_recursive_test.go`
- `test/scenarios/scope/mixed_atomicity_test.go`

**Verify:** test pass.

---

### N3. Sub-graph scenarios

**Files:**
- `test/scenarios/subgraph/entry_absorption_test.go`
- `test/scenarios/subgraph/exit_writeback_carry_test.go`
- `test/scenarios/subgraph/exit_never_runs_test.go` (internal failure with strict-cancel cancels exit before dispatch; parent's writeback stays empty; parent's terminal state still computed correctly)
- `test/scenarios/subgraph/multiple_invocations_test.go` (two callers delegating to the same sub-graph; declarative-shared internal nodes; distinct run-trees; subscription edges resolve to per-invocation parent runs)
- `test/scenarios/subgraph/encapsulation_rejection_test.go`
- `test/scenarios/subgraph/cascade_boundary_opacity_test.go`
- `test/scenarios/subgraph/aggregation_over_internal_children_test.go`
- `test/scenarios/subgraph/entry_failure_short_circuit_test.go`

**Verify:** test pass.

---

### N4. Message scenarios — LANDED (dispatch 16)

**Files:**
- `test/scenarios/messages/operator_invalidate_to_cascade_test.go`
- `test/scenarios/messages/sensor_invalidate_to_cascade_test.go`
- `test/scenarios/messages/multi_receiver_match_test.go`
- `test/scenarios/messages/dead_letter_test.go`
- `test/scenarios/messages/frame_delivery_mode_serial_queue_test.go`
- `test/scenarios/messages/frame_delivery_mode_coalesce_test.go`

**Verify:** test pass.

---

### N5. Sensor scenarios — LANDED (dispatch 16)

**Files:**
- `test/scenarios/sensor/lifecycle_start_stop_test.go`
- `test/scenarios/sensor/observation_routing_test.go`
- `test/scenarios/sensor/sensor_cron_missed_fires_drop_test.go`
- `test/scenarios/sensor/multi_instance_cron_test.go`
- `test/scenarios/sensor/resync_after_restart_test.go`

**Verify:** test pass.

---

### N6. Asset-pattern scenarios — LANDED (dispatch 16)

**Files:**
- `test/scenarios/asset/durable_lifetime_persistence_test.go`
- `test/scenarios/asset/held_durable_across_run_completion_test.go`
- `test/scenarios/asset/instance_termination_cleanup_test.go`
- `test/scenarios/asset/staging_then_swap_with_co_holders_test.go`

**Verify:** test pass.

---

### N7. Lineage scenarios — LANDED (dispatch 16)

**Files:**
- `test/scenarios/lineage/leaf_run_record_creation_test.go`
- `test/scenarios/lineage/claim_commit_record_creation_test.go`
- `test/scenarios/lineage/recursive_ancestor_walk_test.go`
- `test/scenarios/lineage/openlineage_emission_test.go`

**Verify:** test pass. OpenLineage test boots a fake Marquez-shaped HTTP receiver.

---

### N8. Atomic-staging scenarios — LANDED (dispatch 16)

**Files:**
- `test/scenarios/atomic_staging/commit_on_all_success_test.go`
- `test/scenarios/atomic_staging/abandon_on_any_failure_test.go`
- `test/scenarios/atomic_staging/concurrent_staging_test.go`
- `test/scenarios/atomic_staging/sub_stage_verifier_failure_test.go`

**Verify:** test pass. Uses `examples/atomic-staging-fs-producer/`.

---

### N9. Backfill scenarios — LANDED (dispatch 16)

**Files:**
- `test/scenarios/backfill/partition_selector_override_test.go`
- `test/scenarios/backfill/status_rollup_test.go`
- `test/scenarios/backfill/cancellation_policy_test.go`
- `test/scenarios/backfill/lineage_chain_test.go`

**Verify:** test pass.

---

### N10. Verifier-pattern + co-holder-dispatch scenarios — LANDED (dispatch 16)

**Files:**
- `test/scenarios/verifier/co_holding_drives_promotion_test.go`
- `test/scenarios/verifier/cross_table_verifier_test.go`
- `test/scenarios/verifier/mixed_outcomes_test.go`
- `test/scenarios/verifier/co_holder_inherits_address_test.go` (asserts the co-holder's `ExecuteRequest` carries the upstream claim's address per `@blessed-invariant 20`, and that the `rimsky_claim_holders` row is INSERTed at co-holder dispatch with `holder_run_id` set)
- `test/scenarios/verifier/co_holder_strict_cancel_test.go` (verifier failure with `strict.cancel_siblings: true` transitions co-holder rows to `'failed'` and walks the producer Abandon recursively)

**Verify:** test pass. Uses `executors/verifier-shape-checks/` and `executors/verifier-http/`.

---

# Section O — Smoke test extension

### O1. Extend `test/smoke/setup.go` — LANDED (dispatch 17)

**Files:**
- `test/smoke/setup.go` (extend)
- `test/smoke/data_platform_smoke_test.go` (new)

**Steps:**

1. Existing smoke fixture boots Postgres + scheduler + supervisor + control-api + stub producer + stub executor on ephemeral ports.
2. Extend to also boot:
   - The stub-store DataProcessing extension (no extra binary — same `stores/stub/` already booted; gains the DataProcessing surface per Section H).
   - `sensors/sensor-http/` against a fake HTTP service in-process.
   - `subscribers/openlineage/` against a fake Marquez receiver in-process.
3. Smoke test exercises end-to-end: template registers → instance creates → sensor watches → operator backfill → fan-out → DataProcessing commits (through the stub-store extension) → lineage rows projected → openlineage events emitted → 100 sequential force-fires drive the loop.

**Verify:** `go test ./test/smoke/... -count=1`.

---

### O2. Integration tests for bundled stores — CUT

Cut alongside Section H. The DataProcessing protocol is exercised end-to-end via the stub-store extension in O1 (smoke); the per-format integration tests originally listed here are not delivered. See Section H for the rationale.

---

# Section P — Retirements

### P1. Retire `graph/qualityrule/` package — LANDED (dispatch 13)

**Files:**
- `graph/qualityrule/` (delete the entire directory)
- `foundation/spec/quality_rule.go` (delete; `Spec`, `Failure`, `EvalInput`, `Severity` for quality-rules removed)
- `foundation/spec/template.go` (remove `TemplateNodeDef.QualityRules` field)
- Tests that import these (delete references)

**Steps:**

1. Grep for imports of `graph/qualityrule/` and `foundation/spec.QualityRule*`. Remove all references.
2. Migrate the eight bundled checks to `executors/verifier-shape-checks/checks/` (already done in I1).
3. Update `quality_rule_failed` event references: rolling into `executor_errored` with `error_class: "verifier_failed"` per spec.
4. Remove the AGPL constraint on `graph/qualityrule/` (the package is gone; no constraint to apply).
5. Update `CLAUDE.md`'s `graph/qualityrule/` reference (it appears in the package list); remove or annotate as retired.

**Verify:** `make build-all && make lint && make test-all` clean.

---

### P2. Retire `rimsky_schedules` table — LANDED (dispatch 13)

(Already covered by migration B10.)

**Steps:**

1. Confirm no Go-side code references the table after the cron-fire path is removed (E16).

**Verify:** `grep -r rimsky_schedules .` returns nothing in active code (matches in `.ok-planner/` are OK).

---

### P3. Retire per-node `schedule:` field — LANDED (dispatch 13)

(Already covered by D7.)

---

### P4. Remove the per-node `on_event:` map path (per spec) — LANDED (dispatch 13)

**Files:**
- `graph/template/canonical/` (locate `on_event:` parsing)
- Tests

**Steps:**

1. Per spec §Concept catalog impacts — Updated concept entries — `concept:event` clarifying note: "The retired `on_event:` map path is fully retired per `tension:_resolved/send-vs-subscribe-asymmetry`; consumption is via `subscribes: [{on: event, ...}]` only."
2. Remove the `on_event:` parsing path. Templates that reference it get reject class `on_event_map_retired_use_subscribes`.

**Verify:** `go test ./graph/template/canonical/... -count=1`.

---

# Section Q — Concept catalog mutations

The concept catalog lives at `.ok-planner/design/concepts/`. Per spec §Concept catalog impacts, mutations land **alongside the corresponding implementation**, not all at the end. This section enumerates the mutations; the implementer applies each as the corresponding code lands.

### Q1. New concept files — LANDED (dispatch 17)

For each of the entries below: create `.ok-planner/design/concepts/<slug>.md`. Each file follows the existing concept-doc shape (frontmatter with `kind:`, `surface:`, `state:`; body with `## Definition`, `## Boundaries`, `## Invariants`, `## Annotation sites`). Derive content from the spec body — the spec has detailed coverage of each.

| Slug | Source section in spec |
|---|---|
| `graph` | §Vocabulary / New nouns |
| `sub-graph` | §Sub-graphs |
| `delegation` | §Sub-graphs / Identity and absorption |
| `fan-out` | §Fan-out template DSL |
| `asset` | §Lifetime and the asset pattern |
| `claim-lifetime` | §Lifetime and the asset pattern |
| `claim-co-holdership` | §Claim co-holdership |
| `data-processing` | §Protocol surfaces / DataProcessing |
| `validation` | §Protocol surfaces / Validation |
| `sensor` | §Sensors as a service kind |
| `message` | §Messages |
| `lineage` | §Content lineage |
| `lineage-record` | §Content lineage / Records |
| `atomic-staging` | §Atomic-staging worked example |
| `backfill` | §Backfills |

**Verify:** each file is well-formed (the catalog has a TOC at `concepts.md` — regenerate or update it after additions).

---

### Q2. Updated concept files — LANDED (dispatch 17)

Edit each existing `.ok-planner/design/concepts/<slug>.md` per spec §Concept catalog impacts — Updated concept entries:

| Slug | Update |
|---|---|
| `attribute` | Clarifying note about its relationship to assets (assets are claims, not attributes). |
| `claim` | Gains lifetime, may have parent (sub-claim), may have co-holders. |
| `claim-handle` | Gains `parent_claim_handle_id`, `lifetime`, `held_durable`, `version_id`, `producer_candidate_handle`. |
| `claim-holders` | `holder_run_id` instead of `holder_node`. |
| `claim-producer` | Gains three optional methods (SplitScope, ScopesConflict, Validation as mix-in). |
| `cascade` | Clarifying note about sub-graph encapsulation. |
| `node-run` | Expanded to run-tree structure; carries all state-bearing columns lifted from `rimsky_nodes`. |
| `frame` | Gains message-delivery as a frame-creation site. |
| `parked-state` | Gains 4-reason taxonomy + freeform label. |
| `invalidate` | One `kind` of message (the V1 kind). |
| `subscription` | Gains `message` topic kind with filter fields. |
| `service` | Gains `sensor` service kind. |
| `named-event` (or `event`) | Clarifying note that events are internal-to-rimsky and frame-synchronous; distinct from `message` (external, frame-bounded). The retired `on_event:` map path is fully retired. |
| `event-log` | Clarifying note that `rimsky_events` remains the audit log for events; messages have their own audit table `rimsky_messages`. |
| `inertness` | Add the new "messages inert in rimsky" invariant alongside userdata/claim-content/blob-content. |

---

### Q3. Retire concept files — LANDED (dispatch 17)

Move each retired entry to `.ok-planner/design/concepts/_retired/<slug>.md` (mirror the existing `_retired/on-event-handler.md` shape).

| Slug | Successor concept |
|---|---|
| `node-state` | `node-run` (state lives on runs). |
| `quality-rule` | None — pattern is `executor` + `holds:`; documented in `docs/concepts/verifier-pattern.md` rather than as a concept. |
| `schedule` | `sensor` (cron is a sensor-kind via bundled `sensor-cron`). |

---

### Q4. Update the concept catalog TOC — LANDED (dispatch 17)

**Files:**
- `.ok-planner/design/concepts.md`

**Steps:**

1. First determine whether `concepts.md` is auto-generated. Check the top of the file for a generator banner (e.g. `<!-- generated by ... -->`) and `grep -r 'concepts.md' --include='*.sh' --include='Makefile' .` for a generator script.
2. If auto-generated: run the generator command discovered in step 1 (likely `make docs` or a script under `cmd/` or `scripts/`).
3. If manually maintained: edit by hand. Add one line per Q1 new entry (sorted alphabetically into the existing list); remove lines for Q3 retirements; preserve the format of surrounding entries.
4. Re-list `_retired/` entries in the appropriate sub-section of the TOC if the existing TOC distinguishes active from retired.

**Verify:** `concepts.md` reflects current state of `concepts/`. `diff <(ls .ok-planner/design/concepts/*.md | xargs -n1 basename | sort) <(grep -oE '\([a-z-]+\.md\)' .ok-planner/design/concepts.md | tr -d '()' | sort)` shows no unexpected differences (allow for sectioning if applicable).

---

# Section R — Blessed-invariant updates

### R1. Update invariant 4b text in code — LANDED (dispatch 17)

**Files:**
- `foundation/locks/interface.go` (locate `@blessed-invariant 4b` annotation)

**Steps:**

1. Re-read the existing annotation. Update text per spec §Blessed-invariant updates: "single-writer-per-scope; overlap is producer-defined, byte-equal as the trivial default."

**Verify:** `grep -A2 '@blessed-invariant 4b' foundation/locks/interface.go` shows the updated text; `make lint` clean.

---

### R2. Update invariant 10 text in code — LANDED (dispatch 17)

**Files:**
- `runtime/runner_acquire.go` (locate `@blessed-invariant 10`)

**Steps:**

1. Update text per spec: "Lock acquisition is atomic with parent-run claim acquisition. The acquisition transaction either claims the parent run AND inserts the parent claim_handle row AND inserts all sub-claim handle rows for opted-into partitioning AND records the `Open`-returned addresses, or none of these."

**Verify:** `grep -A4 '@blessed-invariant 10' runtime/runner_acquire.go` shows the updated text; `make lint` clean.

---

### R3. Add new invariants in code — LANDED (dispatch 17)

**Files:**
- `runtime/auto_terminal.go` — `@blessed-invariant: held-durable claim handles persist across instance dispatches.` Annotate at the held-durable promote path.
- `runtime/subgraph_dispatch.go` — `@blessed-invariant: exit-node-writeback flows to parent run writeback.` Annotate at the exit-terminal carry-rule write.
- `runtime/message_delivery.go` — `@blessed-invariant: messages are inert in rimsky.` Annotate at the `walkPath` substitution-into-trigger-message path AND at the persistence-layer fetch in `GET /messages/{id}`.

**Verify:** `grep -rn '@blessed-invariant' runtime/auto_terminal.go runtime/subgraph_dispatch.go runtime/message_delivery.go` shows the three new annotations; `make lint` clean.

---

### R4. Update CLAUDE.md's invariant catalog — LANDED (dispatch 17)

**Files:**
- `CLAUDE.md`

**Steps:**

1. Update invariant 4b's text in the catalog section.
2. Update invariant 10's text.
3. Add the three new invariants under appropriate numbers (renumbering not needed; append).

**Verify:** `grep -c 'blessed-invariant' CLAUDE.md` shows three more lines than before the change (one per new invariant added); manual read confirms 4b and 10 are updated.

---

# Section S — Dashboard reframe

### S1. Asset-primary panel — LANDED (dispatch 17)

**Files:**
- `dashboards/rimsky-dashboard/src/` (locate the existing dashboard layout)
- New `dashboards/rimsky-dashboard/src/assets/` directory.

**Steps:**

1. Add an "Assets" top-nav alongside the existing "Instances" / "Templates" / "Services" views.
2. Asset list view: query `GET /instances/{id}/assets` (paginated per-instance; cross-instance asset view aggregates across instances).
3. Asset detail view: shows current version, version history (`GET .../versions`), materialization history, lineage upstream/downstream walks.
4. Materialize button → calls `POST .../materialize`.
5. Delete button (with confirmation) → calls `DELETE .../`.
6. Run `npm install && npm run build` from `dashboards/rimsky-dashboard/`.

**Verify:** dashboard builds; static smoke (jsdom-based or basic Vitest tests) for the asset view.

---

# Section T — Documentation and cleanup

### T1. `CHANGELOG.md`

**Files:**
- `CHANGELOG.md`

**Steps:**

1. Append a bullet group under `## Unreleased` summarizing the spec implementation. One sub-bullet per major thread:
   - New `DataProcessing` / `Validation` / `Sensor` protocols; ClaimProducer extended with `SplitScope` / `ScopesConflict` / version_id; new bundled executors, sensors, subscriber, example. Bundled reference stores for parquet / geo-parquet / geo-postgis are **not** delivered — see the next sub-bullet.
   - **Bundled reference stores cut.** `stores/parquet-store/`, `stores/geo-parquet-store/`, `stores/geo-postgis-store/` are not in this delivery and are not planned for a follow-up. Rimsky ships the DataProcessing / Validation protocols and their conformance suites; specialized format stores belong with the users who need them. The stub store gains a DataProcessing surface for self-test only.
   - Run-tree extension to `rimsky_node_runs`; state aggregation engine; recursive claim-tree resolution.
   - Unified message layer; sensors-as-service; backfill operation pattern; per-node `schedule:` retired (cron is a sensor).
   - Sub-graphs as first-class; entry absorption; exit writeback carry-rule; encapsulation rejections.
   - Claim lifetime (`subgraph` / `durable`); asset pattern; held-durable persistence; instance-termination release.
   - Parked-state 4-reason taxonomy.
   - Content lineage (`rimsky_lineage`); OpenLineage subscriber.
   - `graph/qualityrule/` retired; verifier-executor pattern replaces it.
   - Schema migrations: new tables (`rimsky_messages`, `rimsky_lineage`, `rimsky_sensor_watches`); extended (`rimsky_node_runs`, `rimsky_claim_handles`); renamed (`rimsky_claim_holders.holder_node` → `holder_run_id`); dropped (`rimsky_schedules`). **Pre-v1 baseline: migrations are NOT compatibility shims — they are table redefinitions. Dev-DB nuke (`dropdb && createdb && rimsky-migrate`) is the recommended operator action for upgrading from pre-spec versions.**
   - New conformance binaries.
   - Dashboard asset-primary panel.
2. Append a brief migration notes section to each B-section migration SQL file's leading comment (pre-v1 break-freely; document the column/table change in human prose alongside the SQL).

---

### T2. `CLAUDE.md` + depguard updates — LANDED (dispatch 17)

**Files:**
- `CLAUDE.md`
- `.golangci.yml`

**Steps:**

1. Extend the `.golangci.yml` `pgx-isolation` rule's allowlist to include `sensors/` and `subscribers/`. The current allowlist (per CLAUDE.md) is `foundation/persistence/postgres/`, `foundation/internal/pgtest/`, `cmd/`, `internal/pgtest/`, `graph/scenario/`, `stores/`, `test/smoke/`. Add `sensors/` (sensor-cron's state DB uses pgx) and `subscribers/` (openlineage cursor state DB uses pgx). Run `make lint` to confirm.
2. Confirm none of the four depguard purity rules (`foundation-purity`, `graph-purity`, `runtime-purity`, `foundation-internal-isolation`) need to mention the new top-level directories. The new directories sit at the same level as `stores/` and `executors/`: they consume `foundation/` + `protocols/` + the root module via go.work but are not imported back into the layered packages. No new purity rule entries needed; do confirm by running `make lint`.
3. Update the `CLAUDE.md` "Package import rules" section to mention `sensors/` and `subscribers/` in the bundled-deliverables paragraph alongside `cmd/`, `stores/`, `executors/`, `dashboards/`.
4. Add "Non-obvious gotchas" entries:
   - Sub-graph entry-node absorption is structural, not conceptual.
   - Held-durable claim handles persist past holding-subgraph completion; orphan-claim reaper skips them.
   - Frame-delivery mode per instance defaults to `coalesce`; serial_queue is opt-in.
   - Sensor watches are state on the rimsky side (`rimsky_sensor_watches`); sensors-side state can be reconstructed via `ListWatches` resync.
   - Per-node `schedule:` field retired; cron is now a sensor kind via the bundled `sensor-cron`.
   - Backfill cancellation in V1 only blocks future-enqueued work; in-flight frames complete normally.
   - DataProcessing-capable producers receive `BeginCandidate` at parent-acquisition time; the `producer_candidate_handle` lives on sub-claim rows.
5. Update the "Schema" section to reference the new tables and column changes.
6. Update the "Where to look first" section to add references to the new design docs (Q1 entries).

---

### T3. Module-layout doc — LANDED (dispatch 17)

**Files:**
- `.ok-planner/design/concepts/module-layout.md`

**Steps:**

1. Update the layout section to mention the new top-level directories: `sensors/`, `subscribers/`, `examples/`. Confirm `cmd/`, `stores/`, `executors/`, `dashboards/` listings reflect the new bundled deliverables.

---

### T4. Dead-code sweep — LANDED (dispatch 17)

**Steps:**

1. Grep for references to retired identifiers:
   - `QualityRule`, `qualityRule`, `QualityRules`
   - `Schedule` on `TemplateNodeDef` (now retired field)
   - `holder_node` (SQL column name)
   - `rimsky_schedules` (table name)
   - `node-state` (concept slug; replaced by `node-run`)
2. For each match, confirm it's removed or annotated as retired (in `_retired/` for concept docs; in archived sketches under `.ok-planner/history/` no action needed).
3. Run `make lint` — `unused` checker should flag any orphan helpers; remove.

**Verify:** `make build-all && make test-all && make lint` is clean.

---

### T5. `feature-index.md` does not apply — CONFIRMED (dispatch 17)

**Steps:**

1. Per CLAUDE.md in the parent zonebase repo, `feature-index.md` is a zonebase convention and does NOT apply inside `submodules/rimsky/`. Confirm — no `feature-index.md` to maintain in rimsky.

---

### T6. Final whole-repo verification — LANDED (dispatch 17)

**Steps:**

1. From repo root:
   ```sh
   make proto-gen      # regen all proto bindings; sanity check no drift
   make tidy           # go.mod tidy across modules
   make build-all
   make test-all
   make lint
   ```
2. Run scenario battery: `go test ./test/scenarios/... -count=1 -race`.
3. Run the smoke fixture: `go test ./test/smoke/... -count=1`.
4. Run conformance binaries against bundled services (`stores/parquet-store/`, `sensors/sensor-cron/`, `executors/verifier-shape-checks/`):
   ```sh
   go run ./cmd/rimsky-claim-producer-conformance --endpoint <parquet-store-addr>
   go run ./cmd/rimsky-data-processing-conformance --endpoint <parquet-store-addr>
   go run ./cmd/rimsky-validation-conformance --endpoint <parquet-store-addr> --role claim_producer
   go run ./cmd/rimsky-sensor-conformance --endpoint <sensor-cron-addr> --kind cron
   go run ./cmd/rimsky-executor-conformance --endpoint <verifier-shape-checks-addr> --transport grpc
   ```
5. Confirm `golangci-lint` is clean (no new warnings under any of the depguard rules: `foundation-purity`, `graph-purity`, `runtime-purity`, `foundation-internal-isolation`, `pgx-isolation`).

**Verify:** all green.

---

## Manual checks after completion

These items are NOT automated; the user runs them after the implementation and review are complete.

- **Dashboard visual check.** `cd dashboards/rimsky-dashboard && npm run dev`; navigate to `http://localhost:5173` (or wherever Vite serves); confirm the new "Assets" nav renders; create a test instance using the smoke fixture; click into asset detail; click "Materialize"; observe the materialization-history populating.
- **OpenLineage emission against a real Marquez.** `docker run -p 5000:5000 marquezproject/marquez:latest`; run the smoke fixture against it (`OPENLINEAGE_BACKEND_URL=http://localhost:5000`); navigate to Marquez UI and confirm jobs / datasets appear.
- **`parquet-store` against a real S3 bucket.** Optional; if the user has a personal AWS account, run the parquet-store integration test against a real bucket (not LocalStack) to confirm S3-substrate edge cases (e.g., copy+delete atomicity windowing).
- **Schema migration on a fresh dev DB.** `dropdb rimsky_dev && createdb rimsky_dev && rimsky-migrate --config deploy/rimsky.yml`; confirm all new tables exist; confirm the migration runner is happy with a from-zero start.
- **Multi-replica `sensor-cron` confirmation.** Start two `sensor-cron` replicas pointed at the same shared state DB with the advisory-lock option on; create an instance with a cron watch; assert only one replica fires.
- **Cross-substrate atomic-staging spot checks.** The reference example covers POSIX filesystem. The user should mentally walk through the documented caveats for Iceberg / S3 copy+delete / manifest pointer flip and confirm the doc accurately describes the atomicity envelope.
- **CHANGELOG narrative read.** Read the `## Unreleased` section top to bottom; confirm each major thread is captured at the right level of abstraction.

---

## End

The plan covers the full spec. Execute end-to-end; do not pause for confirmation between sections. Surface deviations and discoveries to `.ok-planner/plans/2026-05-15-data-platform-extensions-plan-notes.md` as you go.
