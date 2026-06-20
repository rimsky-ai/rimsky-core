---
topic: event-log-append-only-jsonb
kind: schema
---

# Two append-only event logs: `rimsky_events` (audit) and `rimsky_node_events` (executor-emitted NamedEvents)

## Description

A reactive orchestrator needs an audit trail: who claimed what, why a node transitioned, what error class a terminal carried, what schedule fired. Rimsky splits this into two append-only tables with different write paths and different consumers.

**`rimsky_events`** (`foundation/persistence/postgres/migrations/001-initial.sql:131-142`) is the rimsky-internal audit log:

```sql
id          BIGSERIAL PRIMARY KEY,
instance_id UUID,
node_id     UUID,
kind        TEXT NOT NULL,
payload     JSONB NOT NULL,
occurred_at TIMESTAMPTZ DEFAULT NOW()
```

with indexes on `(node_id, occurred_at DESC)`, `(instance_id, occurred_at DESC)`, and `(kind, occurred_at DESC)`. The Go-side CRUD lives in `foundation/persistence/events.go`. Every supervisor write site that decides "this is observable" issues an `events.Insert`. The dashboard's `/events` route pages over this table.

**`rimsky_node_events`** (migration 006) is the executor-emitted `NamedEvent` ledger:

```sql
instance_id        UUID,
node_id            UUID,
emitter_node_type  TEXT,
event_name         TEXT,
payload_inline     BYTEA NULL,
payload_handle     TEXT NULL,
payload_handle_backend TEXT NULL,
occurred_at        TIMESTAMPTZ,
seq                BIGINT
```

with indexes that support the substitution-side `LatestByName DESC` lookup (`foundation/persistence/node_events.go:16-50`). Per the blob-inertness invariant (annotated at the file head), payload bytes flow through this file inert — they're never logged, normalized, or transformed beyond schema gates, and large payloads spill via `BlobBackend`.

The split is deliberate. Rimsky-internal audit (`rimsky_events`) and externally-emitted event payloads (`rimsky_node_events`) differ in:

- **Origin**: rimsky writes the first; an executor writes the second.
- **Opacity**: `rimsky_events.payload` is rimsky's own JSONB so rimsky can read it back for the dashboard. `rimsky_node_events.payload_*` is opaque per the blob-inertness and claim-inertness invariants.
- **Spill**: only `rimsky_node_events` has the inline/handle pair for large payloads.
- **Read pattern**: `rimsky_events` is paged by `(instance_id, occurred_at)` for timelines; `rimsky_node_events` is queried by `(node_id, event_name) ORDER BY seq DESC LIMIT 1` for substitution.

The `kind` column on `rimsky_events` is a free-form string (no enum CHECK). New event kinds are zero-migration — write a new `kind` and a new JSONB payload shape, and observability tooling that knows the kind can decode the payload. The trade-off is no schema enforcement: a typo in a `kind` string produces an event that no consumer can find by canonical name.

`docs/concepts/operational-health.md` "Surfaces" describes this from the operator's view: `/events?instance_id=<id>` is the timeline; per-tenant SLA observability composes by template tag.

## Code surface

- `foundation/persistence/postgres/migrations/001-initial.sql:131-156` — `rimsky_events`.
- `foundation/persistence/postgres/migrations/006-platform-extensions-park-blob-events.sql` — `rimsky_node_events` + spill columns.
- `foundation/persistence/events.go` — CRUD for `rimsky_events`.
- `foundation/persistence/node_events.go` — CRUD for `rimsky_node_events` (with invariant-21 annotation).
- `foundation/integration/runner_named_events.go` — emission site (executor `NamedEvent` → `rimsky_node_events` INSERT).
- `modeling/attribute/substitution.go:72-90` — `EventLookup` callback (consumer of `rimsky_node_events`).

## Prose surface

- `CLAUDE.md` "Blessed invariants" §21.
- `CLAUDE.md` "Non-obvious gotchas" — "Event payloads are inert in rimsky."
- `docs/concepts/operational-health.md` — `/events` endpoint.
- `.ok-planner/specs/2026-05-08-platform-extensions-for-agent-consumers-design.md` — the design that added `rimsky_node_events`.

## Adjacent topics

- `2026-05-10-opacity-of-userdata-claim-blob` — the blob-inertness, claim-inertness, and userdata-opacity invariants govern payload bytes here.
- `2026-05-10-blob-spill-pluggable-backends` — `rimsky_node_events` spills via BlobBackend.
- `2026-05-10-attribute-substitution-grammar` — `{{nodes.<emitter>.event.<name>.<path>}}` reads this table.
- `named-events-and-on-event-handlers` — emission side from executors.

## Observations

- The two tables share the conceptual word "events" but serve different purposes; `rimsky_events.kind` and `rimsky_node_events.event_name` are independent vocabularies. A casual reader could mistake one for the other.
- `rimsky_events` has no row-level retention policy in code — it's "operator-managed retention is required." `proto/v1/events.proto` defines event payload shapes for some kinds but is not enforced at write time; the JSONB column accepts anything that fits.
- The substitution-side lookup pattern (`LatestByName DESC`) means an event-emitter that emits the same `event_name` multiple times in a frame has only the latest visible to consumers — but every row is preserved for the event ledger view. The substitution semantics are "last write wins."
- The split between `rimsky_events` and `rimsky_node_events` mirrors the split between rimsky-as-substrate (audit) and rimsky-as-data-bus (NamedEvent fan-out). Both serve observability but the data flows are independent.
