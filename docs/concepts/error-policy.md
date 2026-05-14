---
concept: error-policy
definition: |
  The mechanism that maps an executor's error_class plus the run's
  retry counters onto an action: retry, invalidate(targets),
  give_up, or pass. Templates declare error_types per node;
  rimsky's runtime resolves the policy at terminal time.
proto_symbol: (none)
config_field: rimsky.yml:scheduler.max_retries_without_progress
api_surface: (none)
related: [handlers, node, executor, parked]
deprecated_terms: []
---

# Error policy

## Definition

The mechanism that maps an executor's `error_class` plus the run's
retry counters onto an action: `retry`, `invalidate(targets)`,
`give_up`, or `pass`. Templates declare `error_types` per node;
rimsky's runtime resolves the policy at terminal time.

## Why it exists

Different errors warrant different responses. A transient network
failure is a candidate for retry. An invalid input is a give-up. A
contention conflict might warrant invalidating a related node. Without
a declarative policy, every executor would have to invent its own
retry semantics; with one, the platform handles retry uniformly and
the template author expresses intent.

## Policy chain

A node's `error_types` block declares per-error_class actions. At
terminal time the runtime looks up the action for the executor-supplied
`error_class` and dispatches:

- `retry` — re-dispatch after a backoff.
- `invalidate(targets)` — invalidate the named target nodes; the
  emitted invalidates respect the optional `frame: in | next` setting.
- `give_up` — fail the node with the executor-supplied error class.
- `pass` — treat the failure as a no-op for cascade; transitions to
  `fresh+passed` without error routing.

> The pre-2026-05-12 action flavors `discard_then_retry` and
> `resume_then_retry` are retired; both now resolve to `retry`. The
> supervisor releases acquired claims at terminal regardless.

The three lifecycle handlers (`on_acquire_unavailable`,
`on_executor_complete`, `on_executor_errored`) override or extend this
resolution. See `docs/concepts/handlers.md`.

## `max_retries_without_progress` cap

To prevent runaway retry loops, every dispatch tracks a
`consecutive_retries_no_progress` counter. The counter increments on
retry and resets on any `last_outcome` change. When the counter
exceeds the effective cap, the runtime forces
`Errored { error_class: "retry_loop_no_progress" }` instead of another
retry.

The effective cap is resolved as:

1. The per-node value `max_retries_without_progress` from the template
   (a pointer integer; nil = use deployment default; 0 = disable cap;
   N > 0 = use N).
2. The deployment-level default
   `scheduler.max_retries_without_progress` from `rimsky.yml` (default
   100).

A per-node value of `0` disables the cap entirely — useful for nodes
that are expected to retry indefinitely (watchdog graphs, polling
nodes against external systems). For most nodes the deployment default
is appropriate.

The metric `rimsky_terminal_verdicts_total{error_class="retry_loop_no_progress"}`
fires whenever the cap forces a failure; alerting on this metric
surfaces retry loops before they exhaust budget.

## How you encounter it

- **Templates**: the `error_types:` block under each node.
- **Per-node tuning**: `max_retries_without_progress` as a sibling
  field on the node spec.
- **Deployment-level default**: `scheduler.max_retries_without_progress`
  in `rimsky.yml`.

## Common mistakes

- Conflating `Error{error_class: "executor_blocked"}` and `Error{error_class}`. `Error{error_class: "executor_blocked"}` means "I produced
  output but explicitly chose not to claim success" — typically routed
  via `on_executor_errored` to a downstream review or routing node.
  `Error{error_class}` means a true failure.
- Setting `max_retries_without_progress: 0` on every node. The cap
  exists to surface retry loops; disabling it broadly hides bugs.
- Expecting `give_up` to terminate the instance. It fails the node;
  whether the instance is recoverable depends on downstream handlers
  and the `frame_resolution:` policy.

## See also

- [`handlers.md`](handlers.md)
- [`executor.md`](executor.md)
- [`parked.md`](parked.md)
