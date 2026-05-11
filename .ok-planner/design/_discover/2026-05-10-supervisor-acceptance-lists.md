---
topic: supervisor-acceptance-lists
kind: schema
---

# Supervisor pool specialization via `accepted_executors` / `accepted_stores` array columns

## Description

In a heterogeneous deployment some supervisors can only reach certain executor binaries (network policy, local resource constraints, regional placement) and some can only reach certain producers. A naive scheduler that hands any dispatch row to any supervisor breaks in those cases. Rimsky's solution: per-supervisor accept-lists denormalized at the row-claim filter.

Each supervisor registers `accepted_executors` and `accepted_stores` (string arrays) at startup. `rimsky_supervisors` (`foundation/persistence/postgres/migrations/001-initial.sql:89-95`) carries:

```sql
id                  TEXT PRIMARY KEY,            -- supervisor_id from config
accepted_executors  TEXT[] NOT NULL,
accepted_stores     TEXT[] NOT NULL DEFAULT '{}',
concurrency         INT NOT NULL,
callback_host       TEXT,
callback_port       INT,
last_heartbeat_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
active_node_count   INT NOT NULL DEFAULT 0,
registered_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
```

Dispatch rows in `rimsky_worker_request` carry `required_stores` (denormalized at enqueue from the template's per-node-type `nodeRequiredStores`). The `SelectCandidates` query (`foundation/persistence/postgres/queue.go`) filters:

```sql
WHERE (executor_name = ANY(:accepted_executors) OR executor_name IS NULL)
  AND required_stores <@ :accepted_stores
```

`<@` is Postgres array-contained-in: the dispatch row's required-stores set must be a subset of the supervisor's accepted-stores set.

`executor_name IS NULL` is the native (claim-only) path — those rows are claimable by any supervisor. Some node types declare `stores:` only with no executor; they run inside rimsky's own runner without a dispatched executor.

The denormalization is deliberate: `accepted_stores TEXT[]` with default empty array and the dispatch row's `required_stores` column are paid for at enqueue, redeemed at SelectCandidates, and re-evaluated at scale-time when a new supervisor registers. This makes the filter a pure SQL predicate over indexed columns — fast and observable. Alternative considered: dynamic capability check at claim time. Not chosen — would require every claim attempt to send the supervisor's accept-lists to the producer just to decide whether to claim, and to redo this for every candidate.

Alternative considered: external message broker for dispatch. Not chosen — the project deliberately avoids an external broker (Postgres-only state); the dispatch table is the queue.

New supervisors registered with different accept-lists immediately start picking different rows. A dispatch row whose `required_stores` no supervisor accepts gets stuck — operator visibility comes from the dispatch-queue-depth metric and the supervisor-registry view.

The denormalization has a known limitation: a template that changes its required_stores list does NOT retroactively re-route existing dispatch rows; that has to wait for the next re-enqueue. The denormalized `required_stores` column is point-in-time; mid-flight rows keep their original set.

## Code surface

- `foundation/persistence/postgres/migrations/001-initial.sql:89-95` — `rimsky_supervisors` schema.
- `foundation/persistence/worker_requests.go:23-65` — `required_stores` column + `SelectCandidates` parameterization.
- `foundation/persistence/postgres/queue.go` — `SelectCandidates` SQL.
- `foundation/persistence/supervisors.go` — Go-side CRUD.
- `cmd/rimsky-supervisor/main.go` — supervisor registration at startup.

## Prose surface

- `docs/concepts/operational-health.md` — supervisor pool specialization mentioned alongside dashboards.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — the candidate-selection contract.

## Adjacent topics

- `2026-05-10-worker-request-phase-lifecycle` — `rimsky_worker_request` is the dispatch table.
- `2026-05-10-postgres-only-runtime-state` — Postgres as the dispatch queue.
- `2026-05-10-three-go-module-split` — supervisors are scaled independently.

## Observations

- The denormalization-vs-template-source-of-truth gap is documented inline; a re-routing that requires the up-to-date set on already-enqueued rows would need a manual re-enqueue or admin invalidate. No automation for this exists today.
- The `accepted_stores TEXT[]` default `'{}'` empty array means a freshly-registered supervisor with no overrides accepts nothing — but the `required_stores <@ :accepted_stores` predicate is true only when both sides match (Postgres `<@` returns true when left subset of right). For an empty `required_stores`, the predicate is true for any `:accepted_stores`. For a non-empty `required_stores`, an empty-`accepted_stores` supervisor matches nothing.
- The list-based filter is per-supervisor, not per-region. Geographic placement is operator-managed; rimsky doesn't model regions explicitly.
- The supervisor-registry view via the control-api (`GET /admin/diagnostics/supervisors` or similar) is the operator's window into which supervisors accept which executors/stores.
