# Rimsky v1 — Implementation Spec

Project-agnostic reactive cell-graph orchestration platform. Lives in `/rimsky/` alongside `/backend/` and `/frontend/`; intended for open-source release after v1 stabilizes. Primary (initial) consumer is Zonebase's ingestion layer, but rimsky itself contains no zonebase concepts.

Conceptual reference: `docs/cell-graph-design.md`. This spec implements the subset of that design selected as v1.

---

## 1. Scope

### 1.1 In scope

- Cell contract, state machine, default handlers.
- Two message types: `invalidate`, `recalculate`.
- Per-cell error taxonomy with ordered policy chains. Actions: `retry` (with backoff/jitter), `invalidate(targets)`, `give_up`.
- Resources with identity (`ResourceId`), versioning, double-buffering, `changed: bool` commit verdict, `no-op commit` semantics, quality rules with `severity` (error | warning).
- `ResourceStore` interface; inline-JSONB default implementation.
- Template YAML authoring + control-plane deploy/remove API. No update in v1.
- Graph instance creation indexed by `(template_id, consumer_key)`; rimsky-assigned UUID internally.
- Postgres-backed state (`CellStore`, `EventStore`), abstracted behind interfaces.
- Dispatch queue interface; Postgres-table default implementation.
- Scheduler: pure polling, configurable tick interval (default 1–2s), injected `Clock`.
- Supervisor process: 1:N concurrency (configurable), configurable cell-kind acceptance (`deterministic`, `agentic`, or both).
- Deterministic cell execution (inline handler).
- Agentic cell execution: Claude CLI subprocess, multi-tenant callback MCP (`report_complete` / `report_blocked` / `report_error`), silence detection, supervisor heartbeats, **no hard timeouts**.
- Single `cell_events` JSONB append-only table.
- Control API: template CRUD (minus update), instance create/remove, state read endpoints, operator overrides (`invalidate`, `reset`, `kill`).
- **Timer cells**: scheduled `invalidate` emission.
- **Concurrency tags**: per-cell, enforced by the scheduler at dispatch-claim time.
- Library entry points: `startScheduler`, `startSupervisor`, `startControlApi`.
- Reference env-var binaries: `rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`.
- Structured logging via `Logger` interface; pino default.
- Clock abstraction via `Clock` interface; wall-clock default, controllable test clock.
- Scenario-driven integration tests (testcontainers Postgres) + unit tests for state machine, policy evaluator, template validator.
- Feature-first source layout under `/rimsky/`.

### 1.2 Explicitly out of scope (deferred)

- Template **update** (only deploy and remove in v1).
- **Freshness policies** (`warn_after` / `fail_after`). The cell-graph design doc includes these; v1 does not implement them.
- **Observer cells** (external-state version reporters).
- **Audit cells** as a named pattern (consumers can write them as regular resource-owning cells; no dedicated affordances).
- **External trigger cells** (webhook bridges).
- **Priority** on cells or messages. (Concurrency tags are in; priority is not.)
- **S3 / object-store backend** for `ResourceStore` (interface allows it; only inline-JSONB ships).
- **`dispatch_skipped` events**, **`dedup_key` on messages**, **cursor on external-trigger cells**. Tied to deferred features.
- OpenTelemetry traces. Structured logs only in v1.
- Web UI, CLI tooling. Control API is the only operator surface.
- Content-hash-based staleness of any kind. Change detection is exclusively via the cell's `changed: bool` commit verdict.
- Retention garbage collection of old resource versions beyond a simple "keep N" policy applied on commit. No background sweeper in v1.

### 1.3 Non-goals

- Rimsky is not a workflow DSL for LLM applications (see LangGraph for that).
- Rimsky is not a multi-tenant SaaS platform; one rimsky deployment = one organization.
- Rimsky is not an ETL framework in the Dagster/Airflow sense. It is a reactive state machine for long-running pipelines.
- Rimsky does not own zonebase's data tables, its production schemas, or its API. Rimsky writes into consumer-owned schemas only via declared SQL access methods on resources.

---

## 2. Module layout

```
rimsky/
├── package.json
├── tsconfig.json
├── migrations/                     # rimsky-owned Postgres migrations
│   └── 001-initial.sql
├── src/
│   ├── cell/                       # state machine, template types, default handlers, policy evaluator
│   ├── resource/                   # ResourceId, version model, quality rules, ResourceStore interface
│   ├── message/                    # message types, routing helpers
│   ├── queue/                      # DispatchQueue interface + postgres impl
│   ├── storage/                    # CellStore, EventStore, TemplateStore, InstanceStore interfaces + postgres impls
│   ├── scheduler/                  # scheduler loop, dispatch selection, timer tick, concurrency-tag enforcement
│   ├── supervisor/                 # supervisor process, subprocess management, silence detection, heartbeats
│   ├── callback-mcp/               # multi-tenant MCP server for agent callbacks
│   ├── control-api/                # HTTP handlers for template CRUD, instance mgmt, state reads, operator overrides
│   ├── config/                     # typed config schemas, library entry points (startScheduler, startSupervisor, startControlApi)
│   ├── entrypoints/                # thin env-var-reading binaries: rimsky-scheduler, rimsky-supervisor, rimsky-control-api
│   └── shared/                     # cross-feature types, error classes, Clock, Logger interfaces
└── test/
    └── scenarios/                  # end-to-end scenario tests with testcontainers
```

Rules:
- `/rimsky/` has zero imports from `/backend/` or `/frontend/`. Enforced by tsconfig `paths` and lint rule.
- Cross-feature imports are forbidden (e.g., `cell/` cannot import from `scheduler/`). Shared types go in `shared/`.
- Tests co-located as `*_test.ts` next to source for unit tests. Scenario tests live under `test/scenarios/`.
- ~500 lines per file guideline; max 3 levels of nesting in function bodies.

---

## 3. Data model

### 3.1 Postgres schemas

Rimsky owns the `rimsky` schema (distinct from any consumer schema). All tables are prefixed `rimsky_` for clarity when operators inspect a shared DB.

```sql
-- Templates (deploy targets)
CREATE TABLE rimsky_templates (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    version     TEXT NOT NULL,
    spec        JSONB NOT NULL,           -- parsed template YAML
    deployed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, version)
);

-- Graph instances (one per consumer registration)
CREATE TABLE rimsky_instances (
    id           UUID PRIMARY KEY,
    template_id  UUID NOT NULL REFERENCES rimsky_templates(id),
    consumer_key TEXT NOT NULL,
    params       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (template_id, consumer_key)
);

-- Cell instances (one per cell declared in a template, per graph instance)
CREATE TABLE rimsky_cells (
    id                    UUID PRIMARY KEY,
    instance_id           UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    cell_type             TEXT NOT NULL,             -- template-declared type name
    kind                  TEXT NOT NULL,             -- deterministic | agentic | timer (denormalized from template for dispatch routing)
    state                 TEXT NOT NULL,             -- fresh | stale | running | failed
    dependencies          UUID[] NOT NULL,           -- resolved to cell ids at instantiation
    concurrency_tags      TEXT[] NOT NULL DEFAULT '{}',
    current_error_class   TEXT,
    retry_counter         INT NOT NULL DEFAULT 0,
    action_index          INT NOT NULL DEFAULT 0,
    last_heartbeat_at     TIMESTAMPTZ,               -- set while running; null otherwise
    assigned_supervisor_id TEXT,                     -- null if not running
    kill_requested        BOOLEAN NOT NULL DEFAULT FALSE,  -- operator-set; supervisor polls while running
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON rimsky_cells (state, updated_at);
CREATE INDEX ON rimsky_cells (instance_id, cell_type);

-- Resource registry (one row per resource; owned by exactly one cell)
CREATE TABLE rimsky_resources (
    id                 UUID PRIMARY KEY,
    resource_path      TEXT[] NOT NULL,              -- structured ResourceId
    owner_cell_id      UUID NOT NULL REFERENCES rimsky_cells(id) ON DELETE CASCADE,
    current_version_id UUID,                         -- null until first commit
    previous_version_id UUID,
    keep_versions      INT NOT NULL DEFAULT 2,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_cell_id, resource_path)
);
CREATE INDEX ON rimsky_resources USING GIN (resource_path);

-- Resource versions (append-only; GC'd by keep_versions on commit)
CREATE TABLE rimsky_resource_versions (
    id              UUID PRIMARY KEY,
    resource_id     UUID NOT NULL REFERENCES rimsky_resources(id) ON DELETE CASCADE,
    produced_by     UUID NOT NULL REFERENCES rimsky_cells(id),
    data            JSONB,                           -- inline storage; null if using external ResourceStore impl
    data_ref        TEXT,                            -- opaque reference for external impls
    change_summary  TEXT,
    committed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON rimsky_resource_versions (resource_id, committed_at DESC);

-- Supervisor registry (heartbeat + callback endpoints)
CREATE TABLE rimsky_supervisors (
    id                    TEXT PRIMARY KEY,          -- supervisor_id from config
    accepts               TEXT[] NOT NULL,           -- cell kinds this supervisor handles
    concurrency           INT NOT NULL,
    callback_host         TEXT,                      -- null for deterministic-only supervisors
    callback_port         INT,
    last_heartbeat_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active_cell_count     INT NOT NULL DEFAULT 0,
    registered_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON rimsky_supervisors (last_heartbeat_at);

-- Dispatch queue (cells ready to run)
CREATE TABLE rimsky_dispatch (
    id              UUID PRIMARY KEY,
    cell_id         UUID NOT NULL REFERENCES rimsky_cells(id) ON DELETE CASCADE,
    cell_kind       TEXT NOT NULL,                   -- deterministic | agentic (timer cells do not use the dispatch queue)
    concurrency_tags TEXT[] NOT NULL DEFAULT '{}',
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- may be future-dated for backoff
    claimed_by      TEXT,                            -- supervisor id; null until claimed
    claimed_at      TIMESTAMPTZ,
    UNIQUE (cell_id)                                 -- at most one pending dispatch per cell
);
CREATE INDEX ON rimsky_dispatch (claimed_by, enqueued_at) WHERE claimed_by IS NULL;

-- Event log (single append-only; JSONB payload)
CREATE TABLE rimsky_events (
    id          BIGSERIAL PRIMARY KEY,
    instance_id UUID REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    cell_id     UUID REFERENCES rimsky_cells(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,                       -- see §3.2
    payload     JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON rimsky_events (cell_id, occurred_at DESC);
CREATE INDEX ON rimsky_events (instance_id, occurred_at DESC);
CREATE INDEX ON rimsky_events (kind, occurred_at DESC);

-- Timer registry (one row per active timer cell)
CREATE TABLE rimsky_timers (
    cell_id        UUID PRIMARY KEY REFERENCES rimsky_cells(id) ON DELETE CASCADE,
    schedule_cron  TEXT NOT NULL,                    -- standard cron expression, UTC
    next_fire_at   TIMESTAMPTZ NOT NULL,
    target_cell_id UUID NOT NULL REFERENCES rimsky_cells(id),
    reason         TEXT,
    last_fired_at  TIMESTAMPTZ
);
CREATE INDEX ON rimsky_timers (next_fire_at);
```

### 3.2 Event kinds (v1)

Core kinds:

- `message_received` — payload: `{type, source_cell_id, target_cell_id, params}`
- `message_emitted` — payload: `{type, source_cell_id, target_cell_id, params}`
- `state_transition` — payload: `{from, to, reason}`
- `error` — payload: `{error_class, details, action_taken, action_index, delay_ms?}`
- `work_started` — payload: `{supervisor_id, cell_kind}`
- `work_completed` — payload: `{outcome}` where outcome is `committed | no_op | rejected | errored | infra_errored | quality_failed`
- `commit` — payload: `{resource_id, version_id, change_summary}` — always carries a non-null `resource_id`/`version_id`
- `non_resource_commit` — payload: `{change_summary}` — emitted by cells that own no resources but still cascade recalculate to dependents (probe / audit pattern from §16)
- `no_op_commit` — payload: `{reason}` (agent or handler reported `changed: false`)
- `quality_rule_failed` — payload: `{rule_type, rule_config, severity, details}`
- `timer_fired` — payload: `{timer_cell_id, target_cell_id}`
- `timer_dispatch_failed` — payload: `{cell_id, error}` — timer tick couldn't emit invalidate
- `heartbeat_lost` — payload: `{supervisor_id, last_heartbeat_at}`
- `silence_detected` — payload: `{supervisor_id, silence_duration_ms}`
- `operator_override` — payload: `{action, actor?, reason?, restore_version?}` (for API-triggered invalidate/reset/kill)
- `operator_kill_requested` — payload: `{supervisor_id, note}` — heartbeat observed `kill_requested=true`

Orphan-claim / double-execute diagnostics (§8.1):

- `orphaned_claim_released` — payload: `{dispatch_id, prior_claimed_by, claimed_at}` — scheduler sweep re-opened a dead supervisor's claim
- `orphaned_claim_lost_race` — payload: `{supervisor_id, dispatch_id?, ownership_kind?, current_claimed_by?, winning_supervisor_id?}` — runner's verify-before-run discovered its claim was released / re-claimed
- `unresolved_invalidate_target` — payload: `{error_class, instance_id, unresolved_targets, resolved_targets}` — invalidate policy referenced a target cell type that does not exist in the instance

Agentic callback diagnostics (§9.4/§9.5):

- `work_rejected` — payload: `{reason, errors}` — callback `report_complete` failed result-schema validation or produced an unserializable result

Deferred:

- `dispatch_skipped` — listed in §1.2 as deferred; consumers should not rely on it in v1.

All other kinds are reserved for future v1.x iterations.

---

## 4. Cell model

### 4.1 State machine

States: `fresh`, `stale`, `running`, `failed`.

Transitions:

| From → To | Trigger |
|---|---|
| `fresh` → `stale` | `invalidate` message received |
| `stale` → `running` | Supervisor claims dispatch row; scheduler verified all dependencies were `fresh` at enqueue time (see §4.2 / §8.2) |
| `running` → `fresh` | Work completes successfully (possibly with `changed: false`) |
| `running` → `stale` | Error with policy action `retry` or `invalidate(targets)` |
| `running` → `failed` | Error with policy action `give_up`; policy chain exhausted |
| `failed` → `stale` | Operator API `POST /cells/:id/reset` or `POST /cells/:id/invalidate` |

Timer cells are resource-less and do not follow the full state machine — they live in `fresh` perpetually; their firing is driven by `rimsky_timers.next_fire_at` (§8.1 step 2), not by state transitions on the cell itself.

All state changes write a `state_transition` event to the log.

### 4.2 Default message handlers

Cells do not declare handlers in v1; they inherit system defaults.

**on_invalidate(msg):**
1. If `msg.restore_version` is set and that version exists on owned resources: swap each resource's `current_version` to the named version, emit `recalculate` to dependents, return to `fresh`. No re-execution.
2. Else if state is already `stale` or `running`: no-op.
3. Else: transition to `stale`. Emit `invalidate` to all dependents. Enqueue a dispatch row if dependencies are satisfied; otherwise leave stale (will be re-evaluated when a dep completes).

**on_recalculate(msg):**
1. If `fresh`: no-op.
2. If `stale`: check all `dependencies`. If any is not `fresh`, no-op. If all are `fresh`, enqueue a dispatch row.

**on_work_complete(result, changed, change_summary):**
(Applies to resource-owning cells only. Timer cells do not execute and do not flow through this handler; see §4.1 state-machine note and §8.1 step 2 for timer firing.)

1. For each owned resource with output in `result`: run quality rules. `severity: error` failures → raise `error(quality_rule_failed)`; `severity: warning` failures → log warning and continue.
2. If all error-severity rules pass and `changed: true`: write a new `rimsky_resource_versions` row; update `current_version_id` / `previous_version_id`; GC versions past `keep_versions`. Log `commit`. Emit `recalculate` to dependents. Transition to `fresh`.
3. If `changed: false`: do not write a new version. Log `no_op_commit`. Do not emit `recalculate`. Transition to `fresh`.

**on_error(error_class):**
1. Look up `error_class` in template's `error_types`. Missing → treat as `give_up` with reason `unknown_error_class`.
2. If the cell's `current_error_class` is not `error_class`, reset `action_index = 0` and `retry_counter = 0`, then set `current_error_class = error_class`.
3. Examine the action at `action_index` in this class's policy chain.
4. Apply the action:
   - `retry`: if `retry_counter < count`, increment `retry_counter`; transition `running → stale`; re-enqueue dispatch with `enqueued_at = now + backoff_delay`. If exhausted, advance `action_index`, reset `retry_counter = 0`, re-enter step 3 for the next action.
   - `invalidate(targets)`: emit `invalidate` to each target; transition `running → stale`; reset `retry_counter = 0`. Advance `action_index` only on same-class recurrence at next run.
   - `give_up`: transition `running → failed`; log `error` with `action_taken: give_up`; stop.
5. Successful completion on a subsequent run resets `retry_counter`, `action_index`, and clears `current_error_class`.

### 4.3 Template schema

Templates are deployed as YAML and stored as JSONB. Schema (validated at deploy time):

```yaml
name: string                           # template identifier, e.g. "zoning-source-onboarding"
version: string                        # semver or arbitrary version tag
description: string
cells:
  - type: string                       # cell type name, unique within template
    description: string
    kind: deterministic | agentic | timer
    dependencies: [string]             # list of sibling cell type names
    concurrency_tags: [string]         # e.g. "agentic", "per-instance:{instance_id}"

    # Execution config (varies by kind)
    execution:
      # For deterministic:
      handler: string                  # registered handler name

      # For agentic:
      model: string
      system_prompt: string            # path or inline
      user_prompt_template: string
      tools: [string]                  # MCP server / tool refs
      result_schema: object            # JSON schema

      # For timer:
      schedule: string                 # cron expression, UTC
      emit:
        type: invalidate
        target: string                 # sibling cell type name
        reason: string

    # Resources owned (resource-owning cells only)
    owns_resources:
      - path: [string]                 # ResourceId segments; leading segments may include instance placeholders
        schema_ref: string | object
        retention:
          keep_versions: int           # default 2
        quality_rules:
          - type: string               # builtin name or "custom"
            config: object
            severity: error | warning  # default error

    # Resources read outside the dependency chain (optional)
    reads_resources:
      - path: [string]
        via: inline | sql | mcp | rest

    # Error policy
    error_types:
      <error_class>:
        policy:
          - action: retry
            count: int
            backoff: linear | exponential
            jitter: none | plus_minus
            max_delay_ms: int
          - action: invalidate
            targets: [string]          # sibling cell type names
            restore_version: previous | null
          - action: give_up
            reason_template: string

# Parameters the instance accepts at creation time
params_schema: object                  # JSON schema; defaults validated here
```

Validation at `POST /templates`:
- All `dependencies` reference declared cells in the template.
- All `error_types.<class>.policy[*].invalidate.targets` reference declared cells.
- All timer `execution.emit.target` references a declared cell.
- `owns_resources[*].path` placeholders are resolvable from `params_schema` or instance context.
- No dependency cycles.
- `kind`-specific execution config is well-formed per its discriminator.
- Handler names (for deterministic) and tool names (for agentic) are registered with the scheduler/supervisor (declared registry).

A template that fails validation is rejected at the API boundary; nothing is stored.

### 4.4 Cell types (v1)

- **Deterministic cell**: `kind: deterministic`. Handler is a registered function. Called with `{instance_params, cell_params, deps_data, clock, logger}` returning `{result, changed, change_summary}` (sync or async). `instance_params` is the per-instance params object; `cell_params` is the cell-specific override block from the template; `deps_data` is a map keyed by dependency cell type name to that dep's current resource version data.
- **Agentic cell**: `kind: agentic`. Supervisor spawns Claude CLI subprocess. Completion via callback MCP.
- **Timer cell**: `kind: timer`. Owns no resources. Scheduler ticks check `rimsky_timers.next_fire_at`; when due, emit configured `invalidate` message to target cell, update `last_fired_at`, compute and store new `next_fire_at` from cron expression.

### 4.5 Cell instantiation

When `POST /instances` is called:
1. Validate `consumer_key` uniqueness within `template_id`.
2. Validate `params` against the template's `params_schema`.
3. Allocate a new instance UUID.
4. For each cell in the template: allocate cell UUID; resolve `dependencies` to sibling cell UUIDs; resolve `concurrency_tags` (substituting `{instance_id}` placeholders); provision declared resources (allocate resource UUIDs, resolve `path` placeholders); enqueue dispatch rows for root cells (those with no dependencies).
5. For timer cells: insert `rimsky_timers` row with `next_fire_at` computed from the cron expression and current clock.
6. Log `state_transition` events for all cells (initial state `stale`).
7. Return `{instance_id, consumer_key, cell_count}`.

---

## 5. Resource model

### 5.1 ResourceId

Tuple of strings, stored as `TEXT[]` in `rimsky_resources.resource_path`. Rendered colon-separated for display: `("production", "phoenix-az-city", "zoning_districts")` → `production:phoenix-az-city:zoning_districts`.

Segments may include placeholders resolved at instantiation time: `("production", "{consumer_key}", "zoning_districts")`.

### 5.2 Versioning and commit flow

Each successful run of a resource-owning cell produces a new version (unless `changed: false`). The version row contains either inline `data: JSONB` or an opaque `data_ref: TEXT` referring to an external store.

On commit (from `on_work_complete`):
1. Insert new `rimsky_resource_versions` row with `produced_by = cell_id`, `change_summary`.
2. Update `rimsky_resources.previous_version_id = current_version_id`; `current_version_id = new_version_id`.
3. GC versions older than the Nth most recent (where N = `keep_versions`, default 2).

On `changed: false`: no insert, no pointer update, `no_op_commit` event logged.

### 5.3 Quality rules

Builtin rule types for v1:
- `row_count_ratio`: `{min_ratio: float}` — new version's row count must be at least ratio × previous version's row count. Skipped if no previous version.
- `no_nulls`: `{fields: [string]}` — specified fields must be non-null in all records.
- `nullable_fields_present`: `{fields: [string]}` — listed fields must exist in the output schema (possibly null).
- `custom`: `{handler: string}` — registered handler function returning `{passed: bool, details: string}`.

Each rule has `severity`: `error` (default) or `warning`. Error-severity failures reject the commit; warning-severity failures log `quality_rule_failed` with warning severity but do not block.

### 5.4 ResourceDataStore interface

This interface governs **where the bytes of a resource version live** — inline JSONB, S3, etc. It is distinct from the `ResourceRegistry` store (§12.1) which tracks resource identity and version pointers.

```typescript
interface ResourceDataStore {
  write(versionId: UUID, data: unknown): Promise<{inline?: unknown, data_ref?: string}>;
  read(version: ResourceVersionRow): Promise<unknown>;
  delete(version: ResourceVersionRow): Promise<void>;
}
```

v1 default implementation: `InlineJsonbResourceDataStore`. Writes the `data` argument directly into the resource-version row's `data` JSONB column; `data_ref` stays null.

No S3 implementation in v1.

### 5.5 Access methods

Declared per-resource in template. v1 supported values:
- `inline` (internal blob, read via rimsky control API or library)
- `sql` (external schema/table owned by consumer; rimsky writes via configured connection)

MCP and REST access methods are declared but not implemented in v1 (configuration accepted, implementation deferred). Consumers needing those publish them out of band.

---

## 6. Message model

### 6.1 Message shape

```typescript
interface Message {
  id: UUID;                              // aspirational in v1, see note
  type: "invalidate" | "recalculate";
  source_cell_id: UUID | null;         // null for externally-originated messages
  target_cell_id: UUID;
  occurred_at: Date;
  params: {
    // invalidate
    reason?: string;
    restore_version?: UUID | "previous" | null;
    // recalculate
    new_version_id?: UUID;
  };
}
```

**Note (v1):** the implementation never instantiates a `Message` object. `invalidate` / `recalculate` are delivered by direct function call (`invalidateCell` / `recalculateCell` in `src/scheduler/`) with their fields passed as arguments, and `message_emitted` / `message_received` events are appended to the event log at those call sites. The `Message.id: UUID` field is therefore aspirational — there is no persistent message lineage across the event log in v1. Consumers who need to correlate related events should join on `cell_id` + `occurred_at` windows.

### 6.2 Routing

Messages are delivered synchronously within the scheduler loop when a cell's state change produces them, or asynchronously when an operator API call or timer fires one.

Routing is direct: `target_cell_id` identifies the target; the scheduler picks up the target's new state on the next tick.

Messages are logged as `message_emitted` by the sender's handler and `message_received` by the receiver's handler.

### 6.3 No message delivery guarantees beyond at-most-once within a single scheduler tick

Because cells' default handlers are idempotent (invalidate on stale = no-op; recalculate on fresh = no-op; recalculate on stale with unmet deps = no-op), duplicate delivery is benign. There is no dedup layer in v1.

---

## 7. Error model

### 7.1 Error classes

Per-cell taxonomy. Declared in template `error_types`. Runtime emits error classes via:
- Deterministic handler throws a typed error whose class matches a template-declared class.
- Agentic cell calls `report_error(error_class, payload)` via MCP.
- System-emitted `quality_rule_failed` when a commit-time quality rule fails.

Classes not declared in the template are treated as `give_up` with reason `unknown_error_class`.

### 7.2 Infrastructure errors

Separate from cell-declared error classes. Raised by the supervisor, not the cell. Examples:
- `silence_timeout` (subprocess stdout silent beyond threshold)
- `supervisor_crash` (detected by scheduler via missed heartbeat)
- `subprocess_exit_nonzero` (subprocess exited before calling `report_complete`)

Infrastructure errors re-enqueue the cell via the scheduler. The cell's in-process `retry` counter is not incremented; these are restart events, not application retries. Cells do not see infra errors in their `error_types` map.

### 7.3 Policy chain evaluation

Per cell × error class:
- `action_index` tracks position in the chain (initially 0).
- `retry_counter` tracks attempts within a single `retry` action (initially 0).

On error:
1. Look up action at `action_index`.
2. `retry`: if `retry_counter < count`, increment counter; re-enqueue dispatch with backoff delay. If exhausted, reset counter; advance `action_index`; re-enter step 1 for the next action.
3. `invalidate(targets)`: emit `invalidate` messages to each target. Stay stale. Advance `action_index` **only** on same-class recurrence — the next failed run with the same error class increments the index.
4. `give_up`: transition to `failed`; log `error` with `action_taken: give_up`.

Reset on success: when the cell completes successfully, both `retry_counter` and `action_index` for all error classes reset to 0.

Reset on different class: if the cell errors with a *different* class than the one currently tracked in `current_error_class`, the new class takes over with its own fresh `action_index = 0`.

---

## 8. Scheduler

### 8.1 Loop

```
loop forever:
  1. Wait for next tick (configurable interval; default 1.5s)
  2. Process timers: for each rimsky_timers row with next_fire_at <= now:
     - Emit invalidate message to target_cell_id
     - Compute new next_fire_at from cron expression
     - Log timer_fired event
  3. Claim no-op: enqueue dispatch rows for cells that became stale with all deps fresh since last tick
     (query: SELECT cells WHERE state='stale' AND NOT EXISTS (dep with state != 'fresh') AND NOT EXISTS (dispatch row))
  4. Supervisor health: find cells with state='running' AND last_heartbeat_at < now - heartbeat_timeout
     - Log heartbeat_lost event
     - Clear assigned_supervisor_id, last_heartbeat_at
     - Re-enqueue dispatch row (no retry_counter increment; this is an infra restart)
  5. Orphaned-claim sweep: for every rimsky_dispatch row with claimed_by IS NOT NULL,
     claimed_at < now - orphaned_claim_timeout, and the associated cell still state='stale':
     - Release the claim (guarded by expected claimed_by — see "Orphaned-claim invariant" below)
     - Log orphaned_claim_released event
  6. Garbage-collect stale dispatch rows for failed cells
```

The scheduler never executes cells itself. It only enqueues dispatch work for supervisors to claim.

**Orphaned-claim invariant.** A claim is considered "orphaned" when its supervisor died between `queue.claim()` and the cell's state → `running` transition (i.e. the dispatch row carries `claimed_by` but the cell is still `stale` after a full cutoff window). The sweep re-opens such claims to a fresh supervisor. Two correctness pieces constrain the implementation:

1. **Generous cutoff.** `orphaned_claim_timeout` defaults to `5 * heartbeat_timeout` so that a live-but-slow supervisor (spending seconds on a large dep-data fetch between `claim()` and state flip) cannot be preempted. Setting this too low is a double-execute vector.
2. **Claimant-guarded release.** `releaseClaim` on the sweep path takes the snapshot's `claimed_by` and uses it in the UPDATE predicate: `UPDATE ... WHERE id=$1 AND claimed_by=$2`. This prevents a stale sweep from nulling the claim of a fresh supervisor that re-claimed the row between snapshot and update.
3. **Verify-before-run.** Every runner (deterministic and agentic) re-reads `claimed_by` on its dispatch row immediately before any expensive work (handler invocation or subprocess spawn). If the row has been released, re-claimed by another supervisor, or deleted, the runner logs `orphaned_claim_lost_race` and returns without running. This is the hard backstop against double-execute; #1 and #2 are optimizations that reduce how often the backstop fires, but #3 is the invariant that guarantees correctness.
4. **No idempotent state re-entry.** The cell-store `updateState` does NOT short-circuit when the requested state equals the current state. A stale supervisor that got as far as `updateState(cell_id, "running", { kind: "dispatch_claimed" })` while another supervisor was already running would silently succeed under an idempotent path — violating the claim-ownership check by never re-asserting the state machine. The state machine rejects `running → running` under `dispatch_claimed`, and `updateState` must always delegate to it.

Events emitted on the sweep path: `orphaned_claim_released` (sweep successfully re-opened a row) and `orphaned_claim_lost_race` (a runner's verify-before-run discovered its claim had been released / re-claimed).

### 8.2 Dispatch selection (by supervisors)

Supervisors claim work by querying the dispatch queue. The queue's `claim()` implementation performs these checks atomically per row:

1. `claimed_by IS NULL`
2. `cell_kind = ANY($supervisor_accepts)`
3. `enqueued_at <= NOW()` (honors backoff delays — see §8.3)
4. For every tag in the row's `concurrency_tags`: the global count of currently-running cells with that tag is strictly less than the tag's configured limit.

The first matching row (lowest `enqueued_at`) is claimed via `SELECT ... FOR UPDATE SKIP LOCKED` and updated with `claimed_by = $supervisor_id, claimed_at = NOW()`.

Illustrative SQL (not a final query; implementation may use multiple statements or a CTE for the tag-count subquery):

```sql
-- compute current tag counts from running cells (once per claim attempt)
WITH tag_counts AS (
  SELECT t AS tag, count(*) AS active
  FROM rimsky_cells c, unnest(c.concurrency_tags) AS t
  WHERE c.state = 'running'
  GROUP BY t
)
UPDATE rimsky_dispatch d
SET claimed_by = $supervisor_id, claimed_at = NOW()
WHERE id = (
  SELECT d2.id
  FROM rimsky_dispatch d2
  WHERE d2.claimed_by IS NULL
    AND d2.cell_kind = ANY($supervisor_accepts)
    AND d2.enqueued_at <= NOW()
    AND NOT EXISTS (
      SELECT 1
      FROM unnest(d2.concurrency_tags) AS row_tag
      LEFT JOIN tag_counts tc ON tc.tag = row_tag
      WHERE COALESCE(tc.active, 0) >= ($tag_limits ->> row_tag)::int
    )
  ORDER BY d2.enqueued_at
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
RETURNING d.*;
```

Note: `$tag_limits` is passed from the supervisor as a JSONB map; Postgres can parameterize it as a JSON document. Implementers may choose to materialize limits into a temp table or use any equivalent approach.

**Dependency freshness invariant.** Dispatch rows are enqueued only when all dependencies are `fresh` (see §4.2 `on_recalculate` step 2 and §8.1 step 3). The claim query does not re-check dependency state, because the reverse transition (fresh → stale) happens only via `invalidate`, which is propagated to the cell *itself* before being propagated to its dependents — i.e., if a dep regresses to stale, the downstream cell is also invalidated and any pending dispatch row for it becomes moot (the cell is back to stale and will re-evaluate). The scheduler removes a cell's pending dispatch row when the cell is invalidated while queued.

Concurrency-tag limits are configured at the rimsky-deployment level (a map of tag → max concurrent) and applied during `queue.claim()`. v1 semantics:
- Limits are a **supervisor-side** config: each supervisor passes its own `concurrencyLimits: Record<string, number>` to `startSupervisor`. `SchedulerConfig` has no `concurrencyLimits` field. The reference `rimsky-supervisor` binary reads the map from env var `RIMSKY_CONCURRENCY_LIMITS` (JSON).
- `agentic`: intended global limit = sum of `concurrency` across agentic supervisors. Enforce by setting `{ "agentic": N }` on every agentic supervisor config.
- `per-instance:{instance_id}` tags: automatic limit 1 (prevents concurrent runs within one instance). **NOTE (v1):** there is no runtime auto-populate of `{"per-instance:*": 1}` — the tag is attached to cells by template declaration, but the claim-time counter only applies it when the supervisor's `concurrencyLimits` map includes a key matching the tag. Consumers who rely on per-instance serialization must include `{"per-instance:*": 1}` explicitly (or the specific instance_id-substituted tags). A `per-instance:*` wildcard is not supported in v1; concrete tag keys only. Reference binaries log a warning on startup if `RIMSKY_CONCURRENCY_LIMITS` is empty and any supervisor accepts agentic or declares `per-instance:*` tags.

Supervisors poll the dispatch queue at a configurable interval (default 1s) when below capacity.

### 8.3 Backoff scheduling

When a `retry` action sets the dispatch to re-enqueue, the dispatch row is enqueued with `enqueued_at = now + backoff_delay` where `backoff_delay` is computed per the action config:
- `linear`: `delay = base_delay * retry_counter`
- `exponential`: `delay = base_delay * 2^retry_counter`
- `jitter: plus_minus`: multiply `delay` by a uniform random in `[0.5, 1.5]`
- Clamped to `max_delay_ms`

The claim query filters on `enqueued_at <= NOW()` (§8.2), so future-dated rows are skipped until their backoff elapses.

**Dispatch row lifecycle.** A dispatch row is created when a cell becomes eligible (§8.1 step 3 or §8.3 re-enqueue) and deleted on terminal outcome of that claim (success, `give_up`, or supervisor reassignment on heartbeat loss). On `retry` action, the current dispatch row is deleted and a new one is inserted with the future `enqueued_at`. On `invalidate` action, the current row is deleted (the cell will get a new dispatch row once its dependencies re-stabilize). This keeps `UNIQUE (cell_id)` satisfied at all times.

---

## 9. Supervisor

### 9.1 Process model

Long-running node process. Config:
- `supervisor_id`: unique string (default: hostname + pid)
- `accepts`: list of cell kinds (`deterministic`, `agentic`, or both)
- `concurrency`: integer, max concurrent cells in this supervisor
- `heartbeat_interval_ms`: default 5000
- `claim_poll_interval_ms`: default 1000
- `silence_timeout_ms`: default 180000 (3 min for agentic; not applied to deterministic)
- `callback_host`: host the callback MCP binds to (default `127.0.0.1`)
- `callback_port`: port (default 0 = OS-assigned)

### 9.2 Lifecycle

1. Start callback MCP server (if `agentic` is in accepts).
2. Register self with scheduler: upsert into `rimsky_supervisors` with `id`, `accepts`, `concurrency`, `callback_host`, `callback_port`, `last_heartbeat_at = NOW()`.
3. Loop: while active cells < concurrency, try to claim one from dispatch queue.
4. On claim: start cell execution (deterministic or agentic path below). Track in-memory active-cell state keyed by `cell_id`. Delete the claimed dispatch row on terminal outcome (success, failure, or re-enqueue — see §8.3 for the re-enqueue case which inserts a fresh row).
5. **Heartbeat tick** — every `heartbeat_interval_ms`:
   - Update `rimsky_supervisors.last_heartbeat_at` and `active_cell_count` for this supervisor.
   - For each active cell: update `rimsky_cells.last_heartbeat_at`.
   - For each active cell: read `rimsky_cells.kill_requested`. If `true`, tear down the cell's execution (see §9.4 step 9) and emit infra error `operator_kill`.
6. On shutdown signal: stop claiming new cells; wait for active cells to complete up to a grace period; kill remaining subprocesses and emit infra errors (`supervisor_shutdown`); write a final supervisor-row update (or delete row) and exit.

### 9.3 Deterministic cell execution

Inline:
1. Look up handler by name in the registered handler map.
2. Call handler with `{instance_params, cell_params, deps_data, clock, logger}` (see §4.4).
3. Handler returns `{result, changed, change_summary}` (sync or async).
4. Feed into `on_work_complete`.
5. On throw: if the error carries a typed `error_class` matching the cell's `error_types`, invoke `on_error(error_class)`. Otherwise, treat as `give_up` with `unknown_error_class` reason.

Handler registration happens at supervisor startup; consumer registers functions via library API.

### 9.4 Agentic cell execution

1. Generate per-cell `callback_token` (random UUID).
2. Record `(cell_id, callback_token, expected_result_schema)` in an in-memory map keyed by token.
3. Spawn Claude CLI subprocess with:
   - `RIMSKY_CALLBACK_URL=http://{callback_host}:{callback_port}`
   - `RIMSKY_CALLBACK_TOKEN={callback_token}`
   - System prompt + user prompt rendered from template with resolved params/deps.
   - Tool configuration including the callback MCP and any cell-declared tools.
4. Stream stdout. Each chunk resets silence timer. Silence longer than `silence_timeout_ms` → kill subprocess with infra error `silence_timeout`.
5. Watch for subprocess exit:
   - Exit after `report_complete` / `report_blocked` / `report_error` acknowledged and subprocess reaped: expected.
   - Unexpected exit (before any terminal callback): infra error `subprocess_exit_before_complete`.
6. When callback receives `report_complete(result, changed, change_summary)` with valid token:
   - Validate `result` against cell's `result_schema`.
   - On failure: return `{status: "rejected", errors: <field-level>}` in the MCP response. Do not kill subprocess. Agent continues in-session and may retry.
   - On success: return `{status: "accepted"}` in the MCP response **first** so the tool call resolves successfully in-subprocess. After the response is sent, initiate subprocess teardown: send SIGTERM, then SIGKILL after a short grace period (e.g. 5s). Once the subprocess is reaped, invoke `on_work_complete(result, changed, change_summary)`. (The agent is free to exit on its own after receiving the accepted response; the SIGTERM is a backstop.)
7. When callback receives `report_blocked(reason, context)` with valid token: return `{status: "accepted"}`; initiate subprocess teardown as in step 6; invoke `on_error('agent_blocked', {reason, context})` after reap.
8. When callback receives `report_error(error_class, payload)` with valid token: return `{status: "accepted"}`; initiate subprocess teardown; invoke `on_error(error_class, payload)` after reap.
9. **Kill request polling.** During the heartbeat tick (§9.2 step 5), the supervisor also reads `rimsky_cells.kill_requested` for each active cell. If `true`, send SIGTERM/SIGKILL to the corresponding subprocess and invoke `on_error('operator_kill')` as an infra error (re-enqueue without retry_counter increment).

### 9.5 Callback MCP server (multi-tenant)

Single HTTP server per supervisor. Tools:

```typescript
report_complete({
  token: string,
  result: unknown,
  changed: boolean,
  change_summary: string | null
}): {status: "accepted" | "rejected", errors?: object}

report_blocked({
  token: string,
  reason: string,
  context: unknown
}): {status: "accepted"}

report_error({
  token: string,
  error_class: string,
  payload: unknown
}): {status: "accepted"}
```

Token validation: reject with an MCP tool-call error (`{content: [{type: "text", text: "unknown_token"}], isError: true}`) if no matching active cell or if the token maps to a cell the supervisor is not currently executing. Prior spec text suggested the same `{status: "rejected", ...}` envelope as schema-validation failures; the implementation uses the MCP tool-error shape instead because an unknown token is a protocol error (the tool call itself is invalid) rather than an application-level rejection. This prevents cross-cell confusion within a multi-tenant supervisor.

`report_complete` runs result-schema validation synchronously; if invalid, returns `{status: "rejected", errors: <field-level>}` and the subprocess stays alive.

**Token security in v1.** The callback server binds to localhost by default (§9.1). Tokens are passed to the subprocess via env var and appear in tool-call arguments. Within a single supervisor, tokens provide cell-level scoping — a confused or buggy subprocess cannot call back with another cell's token unless that token is explicitly shared into its context (which the supervisor controls). Networked (non-localhost) deployment of the callback server is not recommended in v1 without adding transport-level authentication; see deferred items.

### 9.6 Silence detection

Per active agentic cell, track `last_stdout_chunk_at`. On every `heartbeat_interval_ms` (or a faster timer), check each active agentic cell: if `now - last_stdout_chunk_at > silence_timeout_ms`, kill the subprocess and invoke `on_error('silence_timeout')` handled as an infra error (re-enqueue without retry_counter increment; though consumers can choose to declare `silence_timeout` in their error_types if they want application-level policy on it).

### 9.7 No hard deadlines

Agentic cells run as long as they stream output and haven't called `report_blocked` / `report_error` / `report_complete`. Deterministic cells run as long as the handler does. Infrastructure enforces only silence and heartbeat.

---

## 10. Control API

HTTP, JSON-over-REST. Authentication scheme is consumer's choice (library integration point); reference binary exposes an unauthenticated local port by default.

### 10.1 Template endpoints

- `POST /templates` — body: YAML text or parsed JSON. Validates, stores. Returns `{template_id, name, version}`. 400 on validation failure with structured error list.
- `GET /templates` — list: `[{template_id, name, version, deployed_at}]`.
- `GET /templates/:id` — full stored spec.
- `DELETE /templates/:id` — remove. 409 if any instances reference it.
- **No PUT / PATCH in v1.** Updates are deploy-new + remove-old; consumer handles instance migration.

### 10.2 Instance endpoints

- `POST /instances` — body: `{template_id, consumer_key, params}`. Validates, creates, enqueues root cells. Returns `{instance_id, consumer_key, cell_count}`. 409 on consumer_key collision within template.
- `GET /instances` — list; supports `?template=` and `?consumer_key=` filters.
- `GET /instances/:id_or_key` — full details including cell list.
- `DELETE /instances/:id_or_key` — cascade-deletes cells (and via FK cascade: resources, resource versions, events, timer rows). 409 if any cell is currently `running`.

### 10.3 Cell state reads

- `GET /cells/:cell_id` — cell state, dependencies, error tracking, assigned supervisor. `cell_id` is a UUID (cells are not addressable by consumer key; they are always accessed by rimsky-assigned UUID, which callers obtain via the instance-cell listing).
- `GET /instances/:id_or_key/cells` — list all cells for an instance; accepts either the rimsky `instance_id` (UUID) or the `consumer_key` (resolved within the template scope provided as a query param `?template_id=...` when using the key form).

### 10.4 Operator overrides

- `POST /cells/:id/invalidate` — body: `{reason, restore_version?}`. Delivers an `invalidate` message as if from an external source. Logs `operator_override` event.
- `POST /cells/:id/reset` — only valid for `state=failed`. Clears `action_index`, `retry_counter`, `current_error_class`; transitions to `stale`. Logs `operator_override`.
- `POST /cells/:id/kill` — signals the assigned supervisor to abort (v1: sets a `kill_requested: true` flag the supervisor reads on next heartbeat; supervisor kills the subprocess and reports as infra error). No-op if cell is not running.

### 10.5 Event reads

- `GET /events` — paginated. Filters: `instance`, `cell`, `kind`, `since`, `until`. Page size default 100, max 1000. Returns `{events: [...], next_cursor: string|null}`.

### 10.6 Resource reads

- `GET /resources/:id/current` — current version data (inline) or data_ref.
- `GET /resources/:id/versions` — version list with metadata.
- `GET /resources/:id/versions/:version_id` — specific version's data.

No write endpoints on resources; only cell execution writes.

### 10.7 Health

- `GET /health` — `{status: "ok" | "degraded", scheduler: {...}, supervisors: [{id, last_heartbeat_at, active_cells}]}`.

---

## 11. Library entry points and reference binaries

### 11.1 Entry points

```typescript
// Scheduler
export function startScheduler(config: SchedulerConfig): SchedulerHandle;

interface SchedulerConfig {
  storage: StorageBackend;               // factory returning CellStore, EventStore, TemplateStore, etc.
  queue: DispatchQueue;
  clock: Clock;
  logger: Logger;
  tickIntervalMs: number;                // default 1500
  heartbeatTimeoutMs: number;            // default 15000
  concurrencyLimits: Record<string, number>; // tag → max concurrent
}

interface SchedulerHandle {
  shutdown(): Promise<void>;
  health(): SchedulerHealthReport;
}

// Supervisor
export function startSupervisor(config: SupervisorConfig): SupervisorHandle;

interface SupervisorConfig {
  supervisorId: string;
  accepts: CellKind[];                   // ["deterministic"] | ["agentic"] | ["deterministic", "agentic"]
  concurrency: number;
  storage: StorageBackend;
  queue: DispatchQueue;
  clock: Clock;
  logger: Logger;
  heartbeatIntervalMs: number;
  claimPollIntervalMs: number;
  silenceTimeoutMs: number;
  handlerRegistry: HandlerRegistry;      // for deterministic cells
  cliRunner?: CliRunner;                 // for agentic cells; default: spawn claude CLI
  callback?: CallbackConfig;             // host, port for agent callback MCP
}

// Control API
export function startControlApi(config: ControlApiConfig): ControlApiHandle;

interface ControlApiConfig {
  storage: StorageBackend;
  queue: DispatchQueue;
  clock: Clock;
  logger: Logger;
  port: number;
  host: string;
  // Authentication hook (default: no auth)
  authenticate?: (req) => Promise<AuthContext | null>;
}
```

Handles expose `shutdown()` for graceful termination.

### 11.2 Reference binaries

Located in `src/entrypoints/`:
- `rimsky-scheduler.ts`: reads env vars, builds `SchedulerConfig`, calls `startScheduler`, handles SIGTERM/SIGINT.
- `rimsky-supervisor.ts`: reads env vars (`RIMSKY_SUPERVISOR_ID`, `RIMSKY_SUPERVISOR_ACCEPTS`, `RIMSKY_SUPERVISOR_CONCURRENCY`, etc.), builds `SupervisorConfig`, calls `startSupervisor`.
- `rimsky-control-api.ts`: reads env vars, builds `ControlApiConfig`, calls `startControlApi`.

Published as bin entries in `package.json`.

Env-var naming convention: `RIMSKY_<COMPONENT>_<OPTION>`, e.g., `RIMSKY_SCHEDULER_TICK_MS`, `RIMSKY_SUPERVISOR_CONCURRENCY`, `RIMSKY_DB_URL`, `RIMSKY_LOG_LEVEL`.

---

## 12. Interfaces

### 12.1 Storage

```typescript
interface StorageBackend {
  templates: TemplateStore;
  instances: InstanceStore;
  cells: CellStore;
  resources: ResourceRegistry;     // tracks resource identity + version pointers
  resourceData: ResourceDataStore; // stores the actual bytes (see §5.4)
  events: EventStore;
  timers: TimerStore;
  supervisors: SupervisorStore;
}

interface TemplateStore {
  deploy(spec: TemplateSpec): Promise<{id: UUID, name: string, version: string}>;
  get(id: UUID): Promise<TemplateSpec | null>;
  list(filter?: {name?: string}): Promise<TemplateSummary[]>;
  delete(id: UUID): Promise<void>;       // throws if instances reference it
}

interface SupervisorStore {
  register(row: {id: string, accepts: CellKind[], concurrency: number, callbackHost?: string, callbackPort?: number}): Promise<void>;
  heartbeat(id: string, activeCellCount: number): Promise<void>;
  list(): Promise<SupervisorRow[]>;
  listStale(cutoff: Date): Promise<SupervisorRow[]>;  // last_heartbeat_at < cutoff
  unregister(id: string): Promise<void>;
}

// CellStore, InstanceStore, ResourceRegistry, EventStore, TimerStore follow the same pattern;
// full TypeScript interfaces defined in src/storage/ (specification left to implementation —
// schemas from §3.1 define the full field set these interfaces operate on).
```

Default implementation: `PostgresStorageBackend` constructed with a connection pool. All stores share the pool and participate in transactions when called within a transaction context.

### 12.2 Queue

```typescript
interface DispatchQueue {
  enqueue(req: DispatchRequest): Promise<void>;
  // claim internally computes running-cell tag counts (see §8.2 illustrative SQL).
  // Caller provides accepts list and the deployment's tag limits; the queue does the rest.
  claim(supervisorId: string, accepts: CellKind[], limits: Record<string, number>): Promise<DispatchRow | null>;
  complete(dispatchId: UUID): Promise<void>;
  fail(dispatchId: UUID, reason: string): Promise<void>;
}
```

Default: `PostgresDispatchQueue`.

### 12.3 Clock

```typescript
interface Clock {
  now(): Date;
  sleep(ms: number): Promise<void>;
}
```

Default: `SystemClock`. Test impl: `ControllableClock` exposing `advance(ms)`, `setNow(date)`, `tick()`.

### 12.4 Logger

```typescript
interface Logger {
  debug(msg: string, fields?: Record<string, unknown>): void;
  info(msg: string, fields?: Record<string, unknown>): void;
  warn(msg: string, fields?: Record<string, unknown>): void;
  error(msg: string, fields?: Record<string, unknown>): void;
  child(bindings: Record<string, unknown>): Logger;
}
```

Default: pino wrapper. Test impl: `CapturingLogger` or `SilentLogger`.

---

## 13. Testing strategy

### 13.1 Scenario tests (primary correctness signal)

Live in `test/scenarios/`. Use testcontainers-node to spin up Postgres. Each scenario:
1. Applies rimsky migrations.
2. Starts scheduler + supervisor(s) with controllable clock and capturing logger.
3. Deploys a small template (fixture YAML).
4. Creates an instance, drives events, makes assertions on state and event log.

Scenarios for v1 (from conversation):
- `happy-path` — register template, instantiate, all cells reach `fresh`.
- `cascade-invalidate` — downstream fails with class that invalidates upstream; upstream re-runs; cascade forward.
- `give-up` — repeated failure exhausts policy chain; cell transitions to `failed`.
- `double-buffering` — quality-rule rejection keeps previous version live.
- `agentic-valid-complete` — agentic cell calls `report_complete` with valid result; commit succeeds.
- `agentic-invalid-complete-retry` — agentic cell calls `report_complete` with invalid result; supervisor rejects; agent retries in-conversation; second attempt valid.
- `heartbeat-loss-reenqueue` — supervisor "dies" (simulated); scheduler re-enqueues; another supervisor picks up.
- `no-op-commit-no-cascade` — cell reports `changed: false`; downstream not invalidated.
- `rollback-via-invalidate-restore` — `invalidate(restore_version: previous)` swaps current to previous; downstream recalculates.
- `silence-timeout` — simulated stalled subprocess; supervisor kills it; infra error re-enqueues.
- `timer-fires-invalidate` — timer cell fires on schedule; target cell invalidated and re-runs.
- `concurrency-tag-limit` — two cells with `per-instance:X` tag; second one waits for first to complete.

### 13.2 Unit tests

Focused on pure logic:
- Cell state machine (transition table).
- Policy evaluator (action advancement, recurrence semantics, reset on success).
- Template validator (well-formed templates accepted; each failure mode rejected).
- Quality-rule evaluators (builtin rules individually).
- Cron-next-fire calculation for timer cells.
- Backoff/jitter computation.

No unit tests on:
- Glue code (Postgres queries, HTTP routing).
- Configuration parsing (env-var → config).
- Subprocess management (covered by scenarios).

### 13.3 Test infrastructure

- `test/harness.ts` — scenario harness: spawn DB, start components, return controllable handles.
- `test/fixtures/templates/` — YAML templates used by scenarios.
- `test/fakes/` — `FakeCliRunner` (simulated Claude CLI for agentic tests), `ControllableClock`, `CapturingLogger`.

Scenario tests use `vitest` (already in the repo) with extended timeouts (testcontainers startup is slow).

---

## 14. Migrations

Rimsky ships its own migration set under `rimsky/migrations/`. Applied via a dedicated `npm run migrate` script in the rimsky package. Uses a simple migration runner (ordering by filename, applied migrations tracked in a `rimsky_migrations` table).

First migration (`001-initial.sql`) creates the schemas from §3.1.

Consumers running rimsky alongside their own DB migrations run `rimsky migrate` as part of their deploy pipeline.

---

## 15. Open items and assumptions

The following are explicitly left for implementation discretion (not gaps in the spec):

- **Connection pool sizing.** Default 10 connections per component; configurable.
- **Authentication on control API.** v1 ships with no default auth; consumers wire their own via the `authenticate` hook. Reference binary binds to localhost only by default.
- **Concrete cron parser library.** Pick any maintained node cron library; standard 5-field cron (no seconds, no years). Timezone: UTC only.
- **Structured logger choice.** pino. Log level configurable via `RIMSKY_LOG_LEVEL`.
- **Postgres version.** Target 14+. JSONB and indexed array operations assumed.
- **Node version.** Target 20+ (matches existing repo).
- **Package manager.** npm (matches existing repo conventions; no workspaces in v1).

---

## 16. Implementation notes carried forward from design

- Cell IDs are UUIDs (rimsky-assigned) and never exposed in consumer-visible URLs as the primary key; consumer-facing paths use `consumer_key` where applicable.
- The `changed: bool` commit verdict is the only change-detection mechanism. No content hashing.
- Hard timeouts are not present; only silence detection and heartbeat loss.
- Messages are logged, not persisted as queued state. The dispatch queue is the only persisted queueable thing; messages are transient.
- Guard cells do not exist as a concept; quality rules on commits cover that role in v1.
- Probes are regular resource-owning cells with no special status; convention, not mechanism.
- Agentic cells do not write JSON to files for output. Output is exclusively via `report_complete`.

---

## 17. What this spec enables next

After implementation and test suite passing:
- Zonebase's ingestion layer becomes a rimsky consumer: writes cell templates for source-onboarding, registers handlers and tools, calls `POST /instances` per source. Full migration is a separate plan.
- Open-sourcing rimsky is a matter of extracting `/rimsky/` as a standalone repo with a new README; no code restructuring needed.
- v1.x additions (freshness policies, observer cells, external triggers, priority, S3 resource store, OpenTelemetry) layer in without breaking the cell contract.
