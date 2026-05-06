---
concept: node-state
definition: |
  The four named runtime states a node can occupy: `fresh`, `stale`, `running`, `failed`. The state-machine vocabulary covers every legal combination of "do we have a value?" and "is work pending?" plus the `failed` distinction (work attempted, no value, no auto-recovery scheduled).
proto_symbol: (none)
config_field: (none)
api_surface: GET /nodes/{id}
related: [node, cascade, invalidate]
deprecated_terms: []
---

# Node state

## Definition

The four named runtime states a node can occupy: `fresh`, `stale`, `running`, `failed`. The state-machine vocabulary covers every legal combination of "do we have a value?" and "is work pending?" plus the `failed` distinction (work attempted, no value, no auto-recovery scheduled).

## Why it exists

Rimsky's reactive model rests on a small, exhaustive state vocabulary. The four named states are the names operators read in dashboards, log lines, control-api responses, and error messages. Internally they're derived from a `(has_value, has_outstanding_request, auto_recovers)` triple — that detail is rarely useful to consumers, but the steady-state mapping is:

| has_value | has_outstanding_request | auto_recovers | name      |
|-----------|-------------------------|---------------|-----------|
| true      | false                   | n/a           | `fresh`   |
| false     | false                   | true          | `stale`   |
| false     | true                    | n/a           | `running` |
| false     | false                   | false         | `failed`  |

A note on the genesis case: a freshly-created node carries the named state `fresh` but has no value yet. Once the node runs at least once and emits a value, the steady-state mapping above applies.

The state machine rejects illegal transitions (e.g., `running → running` under the same dispatch reason is an error, not a no-op). This is a load-bearing safety property that prevents subtle double-dispatch bugs.

### `last_outcome` — resolution flavor

Alongside `state`, every node row carries a `last_outcome` column capturing the **resolution flavor** of the most recent terminal-for-this-frame transition. Five values:

- `fresh_changed` — node committed and propagated; downstream cascade fires.
- `fresh_unchanged` — node committed without change; downstream cascade does not fire.
- `passed` — handler resolved `pass` (Unavailable / Blocked / Errored skipped without error routing).
- `pure_cascade` — node transitioned `stale → fresh` via dependency cascade only (no executor invocation).
- `failed` — node landed in `failed` via give_up policy or dispatch_impossible.

`last_outcome` is **observability metadata, not a dispatch gate**. The cascade-firing predicate is now expressed as `last_outcome == fresh_changed`; under the default `by_changed` resolution this is functionally identical to the prior `t.Changed` gate.

The four named states (`fresh`, `stale`, `running`, `failed`) are unchanged. `last_outcome` is an additional column written by the same transition that lands the node in `fresh` or `failed`.

## How you encounter it

- **Control API**: `GET /nodes/{id}` returns the current state by name. `GET /instances/{idOrKey}/nodes` lists nodes with their states.
- **Cascade behavior**: only `stale` nodes are eligible for dispatch (subject to claim/lock acquisition); only `running` nodes can transition to `fresh` or `failed` on executor terminal.
- **Manual operations**: `POST /nodes/{id}/invalidate` sets a node to `stale`; `POST /nodes/{id}/reset` returns it to a clean `fresh` baseline.

## Consumer-visible guarantees

- The state-machine transitions are explicit and exhaustive — no implicit transitions. Any transition that would violate the matrix above is rejected; the system does not silently coerce same-state transitions to no-ops.
- Within an instance, at most one frame is in flight at a time; within a frame, multiple nodes may be `running` concurrently.

## Common mistakes

- Treating `failed` as a "permanent" state. `failed` nodes can be returned to `stale` by manual invalidate, by a downstream cascade, or by a graph-author retry policy.
- Confusing `stale` with "not yet computed." `stale` means "value invalidated; awaits recalculation by the scheduler." Newly-created nodes begin life in `fresh` even though they have no value yet — the genesis exception above.
- Equating Rimsky node states with task statuses in workflow systems like Airflow. The `running` → `fresh`/`failed` terminal arrow only fires once per dispatch attempt; reactive recomputation is driven by `invalidate` cascades, not by task-status polling.

## See also

- [`node.md`](node.md)
- [`cascade.md`](cascade.md)
- [`invalidate.md`](invalidate.md)
