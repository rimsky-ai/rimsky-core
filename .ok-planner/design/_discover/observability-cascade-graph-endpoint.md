---
topic: observability-cascade-graph-endpoint
kind: schema
---

# Cascade-graph observability: per-instance node-by-edge projection joining template spec with live `rimsky_nodes`

## Description

A reactive orchestrator's observability surface needs to show "this instance's graph state" — which nodes exist, what state they're in, what their dependencies are, what terminal events fired. Rimsky's control-api exposes this as `GET /nodes/{instance_id}/{node_type}` and the broader instance/frame endpoints, with the underlying computation in `modeling/observability/cascade_graph.go`.

`computeCascadeGraph` (`cascade_graph.go:37-100+`) builds the per-instance cascade graph by joining the template spec's node declarations with the live `rimsky_nodes` rows for the instance. The output is `[]CascadeNode` (lines 15-25):

```go
type CascadeNode struct {
    NodeType          string             `json:"node_type"`
    NodeID            shared.UUID        `json:"node_id"`
    State             shared.NodeState   `json:"state"`
    CurrentErrorClass string             `json:"current_error_class,omitempty"`
    RetryCounter      int                `json:"retry_counter"`
    ActiveDispatchID  *shared.UUID       `json:"active_dispatch_id,omitempty"`
    LastTerminalEvent *terminalEventView `json:"last_terminal_event,omitempty"`
    EdgesIn           []string           `json:"edges_in"`
    EdgesOut          []string           `json:"edges_out"`
}
```

The algorithm:

1. Index live `rimsky_nodes` rows by `node_type` for O(1) projection.
2. Single batch query for terminal events across every live node via `Events.LastTerminalByNodes` — avoids the per-node N+1.
3. Build `edges_in` per type from the template's dependency graph: `dependencies[a] = list of types a depends on → edges_in`. `edges_out` is the inverse.
4. Project per-node: state, retry_counter, active_dispatch_id, last_terminal_event with edges populated.

When the template is not available (e.g. the template was deregistered but the instance still has live rows), the projection runs with empty edge lists.

This endpoint is **read-only**. Per `modeling/observability/handler.go::inTx` (lines 28-30), every observability handler runs inside a fresh short transaction: a single read or small fan-out followed by JSON serialization. The transaction discipline keeps handlers simple under "option C" (every Store method requires an explicit tx).

The companion observability HTTP routes mount under the chi router at `modeling/observability/handler.go::Routes` (lines 48-72):

- `/stores`, `/stores/{name}` — list/get producer peers (from `rimsky.yml` projection).
- `/executors`, `/executors/{name}` — list/get executor peers.
- `/templates`, `/templates/{hash}` — template registry views.
- `/instances`, `/instances/{id}` — instance views.
- `/schedules` — scheduled-nodes list.
- `/frames`, `/frames/{id}` — frame views.
- `/nodes/{instance_id}/{node_type}` — node view (computed via cascade-graph).
- `/dispatches`, `/dispatches/{id}` — dispatch (worker_request) views.
- `/lock-holders`, `/lock-holders/{id}` — claim-handle views.
- `/events` — event-log paging.
- `/system/health`, `/system/summary` — top-level health.

Pagination is uniform: `limit` (default 50, max 500) + opaque `cursor` (`parsePagination` at handler.go:79-94). All responses are JSON; content-type set explicitly.

The dashboards consume these routes and overlay them with peer-side data fetched via `http_bridge_url` (per `2026-05-10-observability-optional-protocols`).

## Code surface

- `modeling/observability/cascade_graph.go` — entire file (`computeCascadeGraph`).
- `modeling/observability/handler.go` — chi router + handlers + `inTx`.
- `modeling/observability/discovery.go` — peer-cache backing the `/executors`, `/stores` routes.
- `modeling/observability/metrics.go` — Prometheus metric definitions.
- `modeling/observability/metrics_hook.go` — metric instrumentation hooks.
- `foundation/persistence/events.go::LastTerminalByNodes` — batch lookup.

## Prose surface

- `docs/concepts/operational-health.md` "Surfaces" — operator-facing description.
- `docs/humans/dashboard.md` — operator-facing dashboard usage.

## Adjacent topics

- `2026-05-10-observability-optional-protocols` — peer-side observability protocols.
- `2026-05-10-event-log-append-only-jsonb` — `rimsky_events` is the audit log this reads.
- `2026-05-10-frame-resolution-model` — `/frames` routes.
- `2026-05-10-worker-request-phase-lifecycle` — `/dispatches` route shape.

## Observations

- `Routes` mounts 18+ HTTP endpoints; the inner cascade-graph computation is one specific node-projection. The naming `/nodes/{instance_id}/{node_type}` is unusual — `node_type` is a string from the template, not a UUID, so the route doubles as "get the row for this template-declared node within this instance."
- `inTx` wraps every handler in a short transaction. This is per-handler, not per-route — a route that fan-outs internally (e.g. listing-with-counts) does multiple inner Transaction calls. Snapshot consistency across the fan-out depends on the underlying isolation level.
- The cascade-graph projection is O(N) in live nodes plus one batch event query; it scales linearly with instance size. Very large graphs may benefit from edge-list caching but that's not implemented.
- The `LastTerminalEvent` field's `Kind` is one of the dispatch-terminal set; the `OccurredAt` is RFC3339. A dashboard that wants ordering across nodes uses the timestamp; ordering by node-id alone is meaningless.
