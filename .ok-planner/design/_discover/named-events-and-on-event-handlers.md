---
topic: named-events-and-on-event-handlers
kind: concept
---

# Named events: non-terminal executor emissions persisted to `rimsky_node_events`, consumable via `{{nodes.<emitter>.event.<name>.<path>}}` and `on_event` handlers

## Description

A node's executor can emit zero or more `NamedEvent` events during its run (`protocols/proto/v1/executor.proto::ExecuteEvent`). Each event carries a name (the event-name string, declared in the executor's `Capabilities.declared_events`) and an opaque payload. Rimsky persists each emission to the `rimsky_node_events` ledger and surfaces it through two consumption paths: attribute substitution and the `on_event` handler.

**Emission side**: `foundation/integration/runner_named_events.go` is the receiver for `NamedEvent` events streamed from the executor during `Execute`. Per the file head comment: "Per `@blessed-invariant 21` the payload bytes are never logged, formatted with `%v`, transformed, or attached to traces — they're inert in rimsky." The receiver INSERTs into `rimsky_node_events` (migration 006), spilling via `BlobBackend` if over threshold.

**Persistence**: `rimsky_node_events` (`foundation/persistence/node_events.go`) has columns for `instance_id`, `emitter_node_id`, `event_name`, `payload_inline` / `payload_handle` / `payload_handle_backend`, `occurred_at`, `seq`. Indexes support a `LatestByName DESC` lookup for substitution.

**Consumption path 1 — attribute substitution**: `{{nodes.<emitter>.event.<name>.<json_path>}}` in an attribute schema's `source:` field. The `EventLookup` callback in `ResolveContext` (`modeling/attribute/substitution.go:72-90`) resolves the lookup at dispatch. The most-recent emission of `(emitter, name)` wins. Payload bytes are walked via the same `walkPath` machinery used for `deps.<node>.<field>` and `claim.<alias>.payload.<field>`, preserving `@blessed-invariant 20` and `21`.

**Consumption path 2 — `on_event` handlers**: each node can declare `on_event:` as a map keyed by event names. When the executor emits a `NamedEvent` for which the receiving node (any node, not necessarily the emitter) has a handler entry, the supervisor runs the handler's `resolve` + optional `invalidate`. This is the "named events drive other nodes" loop. `on_event` handlers are non-terminal: a node may emit any number of named events between start and its terminal event, and any handler firing does not advance the emitter's state machine.

Validation at template registration cross-checks `on_event` keys against the executor's `Capabilities.declared_events` (`modeling/observability/discovery.go`). Templates referring to an undeclared event are rejected at registration when the executor is reachable. CLAUDE.md "Non-obvious gotchas": "When the executor's capabilities are not visible at registration time (peer unreachable), the cross-check is skipped silently — runtime treats unknown event names as no-ops."

Event payloads are subject to opacity discipline (per `2026-05-10-opacity-of-userdata-claim-blob`): rimsky reads bytes only at the `walkPath` substitution leaf and the persistence-layer fetch on event read. No logging, no `%v` formatting, no validation beyond schema gates, no attachment to traces.

The decision to ship named events as a non-terminal channel (rather than rolling them into the gRPC stream's terminal events) is documented in the platform-extensions design (`.ok-planner/specs/2026-05-08-platform-extensions-for-agent-consumers-design.md`). It lets an agentic executor emit per-step progress events that other nodes can observe without coupling to the terminal vocabulary.

## Code surface

- `protocols/proto/v1/executor.proto` — `NamedEvent` variant of `ExecuteEvent`.
- `foundation/integration/runner_named_events.go` — emission-receiver + `rimsky_node_events` INSERT.
- `foundation/persistence/node_events.go` — Go-side CRUD with invariant-21 annotation.
- `foundation/persistence/postgres/migrations/006-platform-extensions-park-blob-events.sql` — `rimsky_node_events` schema.
- `modeling/attribute/substitution.go:72-90, 119-180` — `EventLookup` callback + integration.
- `modeling/observability/discovery.go` — `declared_events` cross-check at template registration.
- `modeling/observability/handshake.go` — `declared_events` carried in observability Capabilities.

## Prose surface

- `docs/concepts/handlers.md` — `on_event` slot.
- `docs/concepts/attributes.md` — `nodes.<emitter>.event.<name>.<path>` source kind.
- `CLAUDE.md` "Non-obvious gotchas" — Event payloads are inert in rimsky; `on_event` handlers validated against `declared_events`.
- `.ok-planner/specs/2026-05-08-platform-extensions-for-agent-consumers-design.md` — design.

## Adjacent topics

- `2026-05-10-attribute-substitution-grammar` — the substitution-side consumer.
- `2026-05-10-event-log-append-only-jsonb` — `rimsky_node_events` table.
- `2026-05-10-observability-optional-protocols` — `declared_events` source of truth.
- `reactive-loops-and-lifecycle-handlers` — `on_event` slot in the handler family.
- `2026-05-10-opacity-of-userdata-claim-blob` — payload-bytes opacity discipline.

## Observations

- `NamedEvent` is non-terminal: the gRPC stream stays open after emission and an executor may emit many before the terminal event. The terminal vocabulary (`Complete | Blocked | Errored | AsyncAccepted | ParkRequested`) is exactly the set of events that close the stream.
- The most-recent emission of `(emitter, name)` wins in substitution. An executor that emits the same event-name multiple times in a frame has only the latest observable to consumers; the full history is in the ledger but not substitution-visible. This is "last-write-wins" semantics for the substitution view.
- The `on_event` handler can fire on *any* node, not just the emitter. The receiving node specifies the emitter's node-type in its handler map: `on_event: { score_emitted: { invalidate: { targets: [aggregator] } } }` says "when *any* node of any type emits `score_emitted`, this node reacts." Cross-checked against the emitter's `declared_events`.
- Event ledger growth is monotonic; there is no per-frame cleanup. Operator-managed retention applies (same as `rimsky_events`).
