---
concept: handlers
definition: |
  Per-node declarative slots that decide what the supervisor does with each terminal event from the executor protocol. Three slots: on_acquire_unavailable, on_executor_complete, on_executor_errored. Each maps the event to a resolve verdict (`pass`, `retry`, `error`, or one of the cascade-gating verdicts for `on_executor_complete`). Cascade coupling is declared receiver-side via `subscribes:`.
proto_symbol: ExecuteEvent in protocols/proto/v1/executor.proto
config_field: rimsky.yml:nodes
api_surface: (none)
related: [node-state, parked, invalidate, executor, subscription]
deprecated_terms: []
---

# Handlers

## Definition

A node's reactive policy is expressed as a set of declarative
**handlers** in the template DSL. Each handler maps one event from
the executor protocol to a small action vocabulary: `pass`, `retry`,
or `error` (plus the cascade-gating verdicts for `on_executor_complete`).

The supervisor terminal pipeline routes every event through the
matching handler. The handler's `resolve` decides the cascade gate.
Cascade coupling between nodes is declared receiver-side via
`subscribes:` (see [`subscription.md`](subscription.md)) — handlers
no longer carry a send-side `invalidate:` slot.

## The slots

Three lifecycle handler slots:

- `on_acquire_unavailable` — when any required claim's `Open` returns
  `Unavailable`. Resolves: `pass | retry | error`.
- `on_executor_complete` — when the executor emits `Complete`.
  Resolves: `by_changed | always_propagate | never_propagate`.
- `on_executor_errored` — when the executor emits `Error{error_class}`
  (including the `executor_blocked` error class, which collapsed into
  this slot post-2026-05-12). Resolves: `pass | retry | error`. Use
  `error_types: { <error_class>: { action: ... } }` to discriminate by
  the producer-declared `error_class`.

Named events emitted by the executor are not handled by lifecycle
handlers; receivers subscribe to them directly via
`subscribes: [{node: <emitter>, on: event, name: <event-name>}]`.

## DSL example

```yaml
nodes:
  - type: classifier
    executor: ml-classifier
    on_executor_complete:
      resolve: by_changed
    error_types:
      executor_blocked:
        action: pass
    on_executor_errored:
      resolve: retry

  - type: low_confidence_review
    executor: human-review
    subscribes:
      - { node: classifier, on: state, when: failed, error_class: executor_blocked }

  - type: aggregator
    executor: aggregate
    subscribes:
      - { node: classifier, on: event, name: score_emitted, frame: in }
```

## Resolve verdicts

| Verdict             | Cascade behavior                                      | Valid in                                  |
|---------------------|-------------------------------------------------------|-------------------------------------------|
| `pass`              | Treats the event as no-op for cascade.                | acquire_unavailable, errored              |
| `retry`             | Re-enqueues the dispatch after a backoff.             | acquire_unavailable, errored              |
| `error`             | Forces the node to `failed` with `error_class`.       | acquire_unavailable, errored              |
| `by_changed`        | Default for Complete: cascade fires iff `changed=true`. | complete                                |
| `always_propagate`  | Force `fresh_changed`; cascade fires regardless of `changed`. | complete                          |
| `never_propagate`   | Force `fresh_unchanged`; cascade does not fire even if `changed=true`. | complete                  |

## Validation at template registration

- `error_class` is required on `resolve: error`.
- Subscriptions in `subscribes:` validate against the upstream node's
  declared output topology (attribute names, declared events) when
  reachable; see [`subscription.md`](subscription.md) for the validator
  rules.

When the executor's capabilities are not yet visible (e.g. service is
unreachable at template-deploy time), the cross-check is skipped
silently — the runtime then defends against unknown event names by
treating them as no-ops.

## Userdata vs. handlers

Userdata is inert in Rimsky (`@blessed-invariant 11`); rimsky never
inspects it. Handlers are rimsky-side and project-agnostic. Anything
project-specific belongs in userdata; anything that drives
state-machine behavior belongs in handlers.

## See also

- [`node-state.md`](node-state.md)
- [`subscription.md`](subscription.md)
- [`error-policy.md`](error-policy.md)
- [`invalidate.md`](invalidate.md)
- [`executor.md`](executor.md)
