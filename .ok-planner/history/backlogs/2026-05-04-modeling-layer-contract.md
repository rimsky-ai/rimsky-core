# Modeling-Layer Contract

**Status:** Authoritative until v1, 2026-05-04.
**Scope:** Comprehensive contract for Rimsky's modeling layer. Defines templates, instances, frames, schedules, attributes, control-plane API, public vocabularies, YAML config shape, modeling persistence contract, and CLI shape.
**Authority:** Single source of truth for the modeling layer. Supersedes the archived per-subsystem design docs in `docs/history/` (preserved as design records).
**Layer position:** Modeling sits above the foundation (`foundation/` module per `2026-05-04-foundation-contract.md`) and consumes the service protocols (`protocols/` module per `2026-05-04-service-protocol-contract.md`) for control-plane lifecycle events.

---

## 1. Purpose

The modeling layer gives operators a coherent abstraction over the foundation's primitives. Where the foundation traffics in node-state bits, claim handles, scope bytes, and an undifferentiated parameterized failure-terminal, the modeling layer presents named states (`fresh`/`stale`/`running`/`failed`), named messages (`invalidate`/`recalculate`), named error actions (`retry`/`invalidate(targets)`/`give_up`), and named higher-order objects (templates, instances, frames, schedules, attributes).

The modeling layer is the layer humans learn. It owns the YAML config shape, the control-plane HTTP API, and the CLI surface. It owns most of the persistence schema (everything except the foundation's three tables). It owns the substitution engine that resolves attribute templates against acquired claim content. It does NOT own the cascade engine, the lock manager, the dispatch loop, or the integration tx — those are foundation.

The modeling layer programs the foundation through four narrowly-scoped predicates (see §2 below) and consumes the service protocols (see `2026-05-04-service-protocol-contract.md`) at the control-plane edge for lifecycle events.

In the four-layer model: foundation < modeling < service protocols (cross-cutting) < bundled services + examples.

## 2. Foundation/modeling boundary

The modeling layer programs the foundation through four predicates:

1. **Cascade target predicate.** Given a node and the executor-supplied `changed: bool`, computes the set of dependent nodes to receive an invalidate signal. Default policy: `changed=true` → all direct dependents marked `has_value=false`; `changed=false` → propagation halts at this node.
2. **Holding-subgraph completion predicate.** Given a claim handle, computes whether the subgraph holding it has reached terminal across all members. Foundation invariant 13 depends on this returning true at exactly one moment per held subgraph.
3. **Aggregate-outcome predicate.** Given a holding subgraph at completion, computes commit-vs-abandon. Default: any-failed → abandon; all-completed → commit.
4. **Coexistence predicate.** Given a pair of byte-equal-scope claim handles with announced `WriteSemantics`, computes whether they may coexist. Default: identical semantics on both sides → coexist iff the semantics permits (e.g., `staged_async` is read-shared); differing semantics → fail at acquisition (the byte-equal-scope uniformity invariant on `realized_write_semantics` makes this impossible in practice — the predicate exists for defense in depth).

These four predicates are the totality of the foundation's "read me at decision points" surface. The foundation has no other knowledge of modeling semantics.

## 3. Templates

### 3.1 Purpose & scope

A **template** is a content-addressed, reusable graph shape declaring nodes, edges, attributes, frame-resolution policy, and any embedded executor/producer references. Templates are the unit of declaration; instances (§4) are the unit of execution.

Per-node template specs MAY declare four lifecycle-handler blocks (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`) per the reactive-loops + lifecycle-handlers spec at `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`. Each handler declares a `resolve` ∈ `{pass, error, retry}` and an optional `invalidate { targets, frame }` emit. Validation at template-deploy enforces the resolve enum and any `error_class` reference resolves against the node's declared `error_types` policy. Templates without any lifecycle-handler block preserve today's hardcoded supervisor behavior (default `by_changed` cascade gate; executor-supplied error class on Errored).

PolicyAction emits and lifecycle-handler emits both gain a per-emit `frame: in | next` field controlling whether the emitted invalidate joins the current running frame (`in`) or enqueues a new one (`next`). Default for cascade recalculation is the scheduler's choice (not configurable); default for handler/policy emits is `next`.

### 3.2 Content-addressing

Template ID is `sha256-<64-hex>` over the RFC 8785 JCS-canonicalized template spec bytes. Implementation: the `modeling/template/canonical/` package (formerly `core/canonical/`) computes `CanonicalSpecHash`. Hash bytes are not pinned across pre-v1 changes; consumers MUST re-resolve by tag rather than caching hash strings.

### 3.3 Registration

`POST /templates` accepts a spec body, computes the hash, and inserts a row in `rimsky_templates`. Re-registering the same spec is idempotent (returns the same hash; no-op on repeat). Tags are managed via a separate table (`rimsky_template_tags`) and do NOT migrate live instances — instances bind to the resolved hash at creation time and stay bound.

### 3.4 Tags

Tags in `rimsky_template_tags` are movable aliases pointing at template hashes. The `compose:<project>:<...>` namespace is reserved for the `rimsky-cli compose` subcommand. Manual `rimsky-cli template register --tag compose:foo:bar` is rejected by client-side CLI validation. Manual `curl POST /tags` against the same prefix is NOT rejected by control-api (server-side enforcement of the reserved prefix is a known v1 open question). Operator guidance: pick distinct compose project names to avoid collision when sharing a control-api across teams.

### 3.5 Persistence schema

```sql
CREATE TABLE rimsky_templates (
  id          TEXT        PRIMARY KEY,        -- sha256-<64-hex>
  spec        JSONB       NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE rimsky_template_tags (
  tag             TEXT        PRIMARY KEY,
  template_id     TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_rimsky_template_tags_template_id ON rimsky_template_tags(template_id);
```

### 3.6 Invariants

1. **Template ID is a canonical hash.** Re-registering the same spec produces the same hash.
2. **Tag movement does not migrate live instances.** Instances bind to a hash at creation.

### 3.7 Out of scope

- Hash bytes are not pinned (pre-v1).
- No template versioning beyond movable tags.
- No template parameterization beyond the attributes mechanism (§7).

## 4. Instances

### 4.1 Purpose & scope

An **instance** is a concrete invocation of a template, bound at creation to a specific template hash and parameterized via the `params` field.

### 4.2 Instance lifecycle

`POST /instances` body: `{template, instance_key?, params}`. The control-api resolves `template` (hash or tag) to a hash, inserts a row in `rimsky_instances` with `template_hash` set and `terminated_at = NULL`. A control-api background terminator goroutine polls `rimsky_instances.terminated_at`; on transition from NULL to a timestamp, fires `OnInstanceTerminated` to all subscribed `LifecycleSubscriber` peers.

### 4.3 Instance-key namespace

`instance_key` is nullable. The `compose:<project>:<...>` namespace is reserved for the compose CLI. Uniqueness is scoped per-template (no cross-template key collisions).

### 4.4 Persistence schema

```sql
CREATE TABLE rimsky_instances (
  id              UUID        PRIMARY KEY,
  template_hash   TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
  instance_key    TEXT,
  params          JSONB       NOT NULL DEFAULT '{}',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  terminated_at   TIMESTAMPTZ,
  UNIQUE (template_hash, instance_key)
);

CREATE INDEX idx_rimsky_instances_terminated
    ON rimsky_instances (terminated_at)
    WHERE terminated_at IS NOT NULL;
```

### 4.5 Invariants

1. **Bind-at-creation.** `template_hash` is fixed at creation; cannot be live-rebound.
2. **Instance-key uniqueness within a template.** Enforced via the `UNIQUE (template_hash, instance_key)` constraint. Postgres treats `NULL` as distinct under UNIQUE, so multiple key-less instances per template coexist; rows with non-NULL `instance_key` collide on duplicate `(template_hash, instance_key)` pairs.
3. **Terminator fires exactly once.** Background goroutine guarantees single-fire on the NULL → timestamp transition.

### 4.6 Out of scope

- No live re-bind to a different template.
- No instance migration.

## 5. Frames

### 5.1 Purpose & scope

A **frame** is the unit of cascade resolution within an instance — a window during which an invalidate signal propagates to its terminal targets and the resulting recalculation work runs to completion.

### 5.2 Resolution modes

Templates declare `frame_resolution: coalesce | serial_queue` as a required field. Control-api rejects template registration without it.

- **`coalesce`**: operator-originated invalidates fold into a pending coalesce row; the next frame transition consumes the coalesced state.
- **`serial_queue`**: operator-originated invalidates enqueue a new frame; frames execute in order.

### 5.3 At-most-one-running enforcement

A unique partial index `uq_rimsky_frames_running` on `(instance_id) WHERE state = 'running'` enforces that at most one frame is running per instance at any moment.

### 5.4 Frame-end SQL predicate

A frame ends when "no `rimsky_nodes` rows are in state `stale` or `running` for this instance." The scheduler's `frame.RunTick` evaluates this predicate every tick.

### 5.5 Cascade-tick relationship

Each running frame iterates the cascade engine (foundation) until the frame-end predicate is true. The frame transitions advance after frame-end.

### 5.6 Foundation worker-request lifecycle relationship

Every `rimsky_worker_request` row carries `frame_id NOT NULL`. Every non-fresh `rimsky_nodes` row carries `frame_id`. Worker-requests born within a frame stay correlated to it.

### 5.7 Persistence schema

```sql
CREATE TABLE rimsky_frames (
  id           UUID        PRIMARY KEY,
  instance_id  UUID        NOT NULL,
  state        TEXT        NOT NULL,           -- 'pending' | 'running' | 'ended'
  resolution   TEXT        NOT NULL,           -- 'coalesce' | 'serial_queue'
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_rimsky_frames_running ON rimsky_frames(instance_id) WHERE state = 'running';
```

### 5.8 Invariants

1. **At most one running frame per instance.** Enforced by `uq_rimsky_frames_running`.
2. **Frame-end is SQL-predicate-driven.** No external "frame is done" signal.
3. **In-flight work runs to terminal.** Operator-originated invalidates do NOT preempt running work. The historical `kill_requested` column has been retired; there is no kill-poll path.

### 5.9 Out of scope

- No kill-poll path.
- No in-flight cancellation.

## 6. Schedules

### 6.1 Purpose & scope

A **schedule** drives cron-based invalidation: at the configured cron time, a target node is invalidated, kicking off a cascade.

### 6.2 Cron parsing

Uses `robfig/cron/v3` semantics: 5-field standard cron with optional descriptors. No 6-field (seconds-precision) variant is enabled.

### 6.3 Advancement

Schedules advance from `row.NextFireAt`, not `clock.Now()`. Missed fires are NOT backfilled — if the scheduler is offline across a fire window, that fire is lost rather than replayed.

### 6.4 Admin force-fire

`POST /admin/scheduled-nodes/{node_id}/force-fire` bypasses the cron next-fire calculation and updates `rimsky_schedules.next_fire_at = now()` immediately. Returns 204 without waiting for the cascade. Admin-only route.

### 6.5 Persistence schema

```sql
CREATE TABLE rimsky_schedules (
  node_id        UUID        PRIMARY KEY,
  cron_expr      TEXT        NOT NULL,
  next_fire_at   TIMESTAMPTZ NOT NULL,
  last_fire_at   TIMESTAMPTZ
);
```

### 6.6 Invariants

1. **Advancement from row, not now.** `NextFireAt` advances from `row.NextFireAt`.
2. **No backfill.** Missed fires are dropped.

### 6.7 Out of scope

- No replay.
- No backfill.

## 7. Attributes

### 7.1 Purpose & scope

**Attributes** are typed inputs to executors with a substitution engine that resolves `{{...}}` directives against acquired claim content.

### 7.2 Schema language

JSON Schema augmented with the `properties[*].source` extension. The `source` field declares where each attribute's value comes from — typically a claim ID + JSON path.

### 7.3 Substitution engine

`{{<source-name>.<json-path>}}` directives are resolved at dispatch time (post-claim-acquisition), expanding leaf paths from acquired claim payload/address. Implementation: `modeling/attribute/substitution.go::walkPath`.

### 7.4 Validation

**Modeling-layer invariant 12:** attributes validate twice. Once at dispatch (post-substitution; before the executor call) and once at commit (executor writeback; against the post-substitution schema). Both gates are mandatory.

### 7.5 Userdata-is-opaque

**Modeling-layer invariant 11:** `userdata` is NEVER inspected, parsed, substituted, or validated by Rimsky. Identical-looking text in `userdata` reaches the executor verbatim. The substitution engine does not even see `userdata` — it operates only over the `properties[*].source` extension within `attributes`.

### 7.6 Substitution-leaf-extraction

The only sanctioned introspection site for claim content (foundation invariant 20) is `modeling/attribute/substitution.go::walkPath`. It lazy-unmarshals into a transient `map[string]any` only at leaf-extraction call time. No other code path reads claim payload/address/scope.

### 7.7 Persistence

Attribute schemas are part of template specs (no separate table).

### 7.8 Invariants

- **11.** Userdata-is-opaque (above).
- **12.** Attributes validate twice (above).

### 7.9 Out of scope

- No template-level substitution beyond `properties[*].source`.
- No userdata substitution.

## 8. Control-plane API

### 8.1 Purpose & scope

The HTTP/JSON surface that operators and the CLI talk to. Implementation: `modeling/controlapi/`. Listens by default on `:8080`.

### 8.2 Routes

Non-admin routes:

- `GET /health`
- `GET /templates`, `POST /templates`, `GET /templates/{hash}`
- `GET /tags`, `POST /tags`, `GET /tags/{tag}`, `DELETE /tags/{tag}`
- `GET /instances`, `POST /instances`, `GET /instances/{idOrKey}`
- `POST /instances/{id}/terminate`
- `GET /v1/observability/...` (existing observability surface; see dashboard/observability spec)

Admin routes (require admin auth):

- `POST /admin/scheduled-nodes/{node_id}/force-fire`

### 8.3 Admin route distinction

`/admin/*` paths require admin auth. v1 will add explicit auth; today auth is implicit (deployment-level network controls).

### 8.4 Lifecycle event firing model

Control-api fires lifecycle events synchronously to subscribed `LifecycleSubscriber` peers at template/instance state transitions. The six events are `OnTemplateRegistered/Deployed/Undeployed/Deregistered/OnInstanceCreated/Terminated`. Idempotency tracked in `rimsky_lifecycle_idempotency` (renamed from `rimsky_store_lifecycle` per the layer-crystallization plan); each event keyed by (peer-name, event-type, object-id). Failures are retried per the existing retry policy. Reference: `2026-05-04-service-protocol-contract.md` §3 for the LifecycleSubscriber surface.

### 8.5 Versioning

Bare paths (no `/v1/` prefix on most routes; the observability surface is the exception). Rolling upgrades are operator-managed. Endpoints used by both versions work; endpoints only on one version return 404 / 405.

### 8.6 Invariants

1. **Admin route boundary.** `/admin/*` is admin-auth-gated.
2. **Lifecycle idempotency.** Events keyed by (peer-name, event-type, object-id); replay is a no-op.

### 8.7 Out of scope

- Auth (v1 concern).
- Rate limiting (v1 concern).

## 9. Public vocabularies

### 9.1 State vocabulary

The four user-facing state names map to the foundation's two-bit-plus-flag space:

| has_value | has_outstanding_request | auto_recovers | name      |
|-----------|-------------------------|---------------|-----------|
| true      | false                   | n/a           | `fresh`   |
| false     | false                   | true          | `stale`   |
| false     | true                    | n/a           | `running` |
| false     | false                   | false         | `failed`  |

Each `rimsky_nodes` row also carries a `last_outcome` sibling field expressing the resolution flavor of the most recent terminal: `fresh_changed | fresh_unchanged | passed | pure_cascade | failed`. The cascade-firing gate is `last_outcome == fresh_changed` (functionally identical to the prior `t.Changed` gate under default `by_changed`; divergent under the lifecycle-handler `always_propagate` / `never_propagate` resolves).

### 9.2 Message vocabulary

Two messages:

- **`invalidate`** — the only graph-level message. Cascades from a node losing/replacing its value to a chosen target set.
- **`recalculate`** — internal to the dispatch loop. Per-node action; not a message in the foundation sense.

### 9.3 Error-action vocabulary

Three error actions, each realized as a specific (auto_recovers, cascade_targets) pair on the foundation's parameterized failure-terminal primitive:

| Action               | auto_recovers | cascade_targets |
|----------------------|---------------|-----------------|
| `retry`              | true          | {}              |
| `invalidate(targets)`| true          | targets         |
| `give_up`            | false         | {}              |

### 9.4 Vocabulary stability

These names are stable until v1; renames require a successor design doc.

## 10. YAML config shape

### 10.1 `rimsky.yml` schema

The unified config file (default `/etc/rimsky/rimsky.yml`, overridable via `RIMSKY_CONFIG`):

```yaml
persistence:
  driver: postgres                            # or 'sqlite' (dev-only)
  postgres:
    url: postgresql://...
  sqlite:
    path: /var/lib/rimsky/state.db

claim_producers:
  - name: items-pg
    endpoint: localhost:7001
    protocols: [claim_producer, lifecycle_subscriber]   # default: [claim_producer]
    write_semantics_envelope: [staged_async]            # operator-declared; ⊆ producer-declared at handshake

executors:
  - name: claude-agent
    endpoint: localhost:7100
    protocols: [executor]

named_locks:
  - name: api-rate-limit
    mode: counting
    capacity: 5
```

Rules:

- Each peer entry's `protocols` field defaults to a single-element list matching the block name (`claim_producer` for entries under `claim_producers:`; `executor` for entries under `executors:`).
- There is no separate `lifecycle_subscribers:` block. A peer that implements LifecycleSubscriber declares it via the `protocols` list on its primary block.
- Operator-declared `write_semantics_envelope` (a set) MUST be a subset of the producer-declared envelope returned by `Capabilities()`. Startup fails fast if not.
- Validation rules: every template-referenced producer name MUST be declared; every named lock referenced in a template MUST be declared.

### 10.2 RIMSKY_CONFIG loading

`RIMSKY_CONFIG` is loaded by all four rimsky binaries: `rimsky-control-api`, `rimsky-supervisor`, `rimsky-scheduler`, `rimsky-migrate`. The unified `rimsky.yml` declares persistence, claim_producers, executors, and named_locks in one file. The three runtime processes dial each entry, run the `Capabilities()` handshake per declared protocol, and validate strict equality against the operator-declared block; any failure (unreachable, mismatch) fails the rimsky process at startup. `rimsky-migrate` consumes the `persistence:` block only.

## 11. Modeling persistence contract

### 11.1 Tables owned by modeling

- `rimsky_templates`
- `rimsky_template_tags`
- `rimsky_instances`
- `rimsky_schedules`
- `rimsky_frames`
- `rimsky_events`
- `rimsky_lifecycle_idempotency` (renamed from `rimsky_store_lifecycle`)
- `rimsky_nodes` (boundary case — node-state lives on top of foundation node-state; see §11.3)

### 11.2 Driver interface set

Scoped to modeling tables only:

- `Templates`
- `Instances`
- `Schedules`
- `Frames`
- `Events`
- `LifecycleIdempotency`
- `NodeMeta` (modeling-side node metadata; foundation-side node-state lives in foundation persistence)

### 11.3 Boundary with foundation persistence

`rimsky_nodes` is split-owned:

- Foundation owns: `has_value`, `has_outstanding_request`, `auto_recovers` columns.
- Modeling owns: `frame_id`, `template_node_id` (the spec-side identifier within the template), and any other modeling correlation columns.

Implementation: a single `rimsky_nodes` table with columns owned per-layer; migrations distinguish.

### 11.4 Migrations

Modeling migrations live alongside foundation migrations in the migration runner; ordering ensures foundation migrations precede modeling migrations that depend on them.

## 12. CLI shape

### 12.1 Purpose

`rimsky-cli` is a thin client to the control-plane API. v1 does not version the control-api; the CLI talks to bare paths.

### 12.2 Commands

- `template register`, `template get`, `template list`
- `tag set`, `tag get`, `tag list`, `tag delete`
- `instance create`, `instance get`, `instance list`, `instance terminate`
- `admin force-fire`
- `compose up`, `compose down` (compose subcommand owns the `compose:` namespace)

### 12.3 Versioning

Bare paths; no client-side server-version check; rolling upgrades operator-managed. Endpoints used by both versions work; endpoints only on one return 404 / 405.

### 12.4 Compose subcommand

`rimsky-cli compose` owns the `compose:<project>:<...>` namespace for tags and instance keys. Client-side validation rejects manual usage of that prefix elsewhere via the CLI.

### 12.5 Out of scope

- No auth flow yet (v1 concern).
- No plugin system.

## 13. Vocabulary mapping (modeling ↔ foundation)

| Modeling name           | Foundation correlate                                                            |
|-------------------------|---------------------------------------------------------------------------------|
| `fresh`                 | `(has_value=true, has_outstanding_request=false)`                               |
| `stale`                 | `(has_value=false, has_outstanding_request=false, auto_recovers=true)`          |
| `running`               | `(has_value=false, has_outstanding_request=true)`                               |
| `failed`                | `(has_value=false, has_outstanding_request=false, auto_recovers=false)`         |
| `invalidate`            | Cascade signal                                                                  |
| `recalculate`           | Per-node action (dispatch loop); not a foundation message                       |
| `retry`                 | Failure-terminal `(auto_recovers=true, cascade_targets={})`                     |
| `invalidate(targets)`   | Failure-terminal `(auto_recovers=true, cascade_targets=targets)`                |
| `give_up`               | Failure-terminal `(auto_recovers=false, cascade_targets={})`                    |
| `frame`                 | Modeling-only — no foundation correlate                                         |
| `template`              | Modeling-only — no foundation correlate                                         |
| `instance`              | Modeling-only — no foundation correlate                                         |
| `schedule`              | Modeling-only — no foundation correlate                                         |
| `attributes`            | Modeling-only; substitution leaf-extraction is the sole reader of claim content |
| `userdata`              | Modeling-only; opaque to rimsky                                                 |

## 14. Open questions

- **Server-side enforcement of `compose:` reserved-prefix on tags.** Today only the CLI rejects; a `curl POST /tags compose:foo:bar` succeeds at the control-api. Operators sharing a control-api should pick distinct compose project names.

## 15. Out of scope

- Foundation concerns (covered in `2026-05-04-foundation-contract.md`).
- Service protocol surface (covered in `2026-05-04-service-protocol-contract.md`).
- Bundled service implementations (covered in `docs/claim-producer-author-guide.md` and `docs/executor-author-guide.md`).
- Dashboard / observability (separate spec).

---

*End of contract.*
