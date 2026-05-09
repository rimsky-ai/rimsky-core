---
concept: handlers
definition: |
  Per-node declarative slots that decide what the supervisor does with each terminal event from the executor protocol, plus the new on_event slot for non-terminal NamedEvents. Five slots in total: on_acquire_unavailable, on_executor_complete, on_executor_blocked, on_executor_errored, on_event.
proto_symbol: ExecuteEvent (Complete | Blocked | Errored | ParkRequested | NamedEvent)
config_field: nodes[*].on_*
api_surface: (none)
related: [node-state, parked, invalidate, executor]
deprecated_terms: []
---

# Handlers

## Definition

A node's reactive policy is expressed as a set of declarative
**handlers** in the template DSL. Each handler maps one event from
the executor protocol to a small action vocabulary: `pass`, `retry`,
`error`, plus an optional `invalidate` emit.

The supervisor terminal pipeline routes every event through the
matching handler. The handler's `resolve` decides the cascade
behavior; the optional `invalidate` slot fires unconditionally
alongside `resolve`.

## The five slots

- `on_acquire_unavailable` — when any required claim's `Open` returns
  `Unavailable`. Resolves: `pass | retry | error`.
- `on_executor_complete` — when the executor emits `Complete`.
  Resolves: `by_changed | always_propagate | never_propagate`.
- `on_executor_blocked` — when the executor emits `Blocked`. Resolves:
  `pass | error`.
- `on_executor_errored` — when the executor emits `Errored`. Resolves:
  `pass | retry | error`.
- `on_event` — a per-event-name map keyed by names declared in the
  executor's `Capabilities.declared_events`. Each handler entry has
  the same shape as the others: `resolve` + optional `invalidate`.
  Non-terminal: a node may emit any number of named events between
  start and its terminal event.

## DSL example

```yaml
nodes:
  - type: classifier
    executor: ml-classifier
    on_executor_complete:
      resolve: by_changed
    on_executor_blocked:
      resolve: pass
      invalidate:
        targets: [low_confidence_review]
        frame: next
    on_executor_errored:
      resolve: retry
    on_event:
      score_emitted:
        invalidate:
          targets: [aggregator]
          frame: in
```

## Resolve verdicts

| Verdict             | Cascade behavior                                      | Valid in                                  |
|---------------------|-------------------------------------------------------|-------------------------------------------|
| `pass`              | Treats the event as no-op for cascade.                | acquire_unavailable, blocked, errored, on_event |
| `retry`             | Re-enqueues the dispatch after a backoff.             | acquire_unavailable, errored, on_event    |
| `error`             | Forces the node to `failed` with `error_class`.       | acquire_unavailable, blocked, errored, on_event |
| `by_changed`        | Default for Complete: cascade fires iff `changed=true`. | complete                                |
| `always_propagate`  | Force `fresh_changed`; cascade fires regardless of `changed`. | complete                          |
| `never_propagate`   | Force `fresh_unchanged`; cascade does not fire even if `changed=true`. | complete                  |

## Handler-emitted invalidate

The `invalidate` slot fires unconditionally when its handler runs.
Targets are template node types or `self`; `frame` is `in` (same frame
the source dispatched in) or `next` (default — a fresh frame). The
invalidate-emitted target can be a parked node — the unified
invalidate handler wakes it the same way `POST
/admin/instances/.../invalidate` does, with `resume_reason:
"external_invalidate"`.

## Validation at template registration

- `on_event` keys are cross-checked against the executor's
  `Capabilities.declared_events`. Templates referring to an
  undeclared event are rejected at registration.
- `error_class` is required on `resolve: error`.
- `invalidate.targets` must reference a declared node type or `self`.
- `frame` must be `in` or `next`.

When the executor's capabilities are not yet visible (e.g. peer is
unreachable at template-deploy time), the cross-check is skipped
silently — the runtime then defends against unknown event names by
treating them as no-ops.

## Userdata vs. handlers

Userdata is opaque to rimsky (`@blessed-invariant 11`); rimsky never
inspects it. Handlers are rimsky-side and project-agnostic. Anything
project-specific belongs in userdata; anything that drives
state-machine behavior belongs in handlers.
