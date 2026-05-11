---
topic: error-policy-retry-loop-cap
kind: discipline
---

# Error-types policy chain maps `error_class × retry_counter` to an action; `max_retries_without_progress` caps runaway loops

## Description

Different errors warrant different responses. A transient network failure is a candidate for retry. An invalid input is a give-up. A contention conflict might warrant invalidating a related node. Without a declarative policy, every executor would have to invent its own retry semantics; with one, the platform handles retry uniformly and the template author expresses intent.

`docs/concepts/error-policy.md` is the canonical concept doc. A node's `error_types:` block declares per-`error_class` actions. At terminal time the runtime looks up the action for the executor-supplied `error_class` and dispatches:

- **`retry`** — re-dispatch after a backoff.
- **`discard_then_retry`** — release acquired claims, re-dispatch fresh.
- **`resume_then_retry`** — preserve claims, re-dispatch with the same acquisition.
- **`invalidate(targets)`** — invalidate the named target nodes; the emitted invalidates respect the optional `frame: in | next` setting.
- **`give_up`** — fail the node with the executor-supplied error class.
- **`pass`** — skip without error routing (used in lifecycle-handler context, not error-types).

The runtime's error-class resolver is `foundation/integration/runner_terminal_errors.go` + `foundation/integration/on_error.go`. The handler chain checks `on_executor_errored` first (the lifecycle handler from `2026-05-10-reactive-loops-and-lifecycle-handlers` family); if the handler resolves to `retry`, the error-types policy is consulted; the looked-up action drives the next transition.

To prevent runaway retry loops, every dispatch tracks a `consecutive_retries_no_progress` counter. The counter increments on retry and resets on any `last_outcome` change. When the counter exceeds the effective cap, the runtime forces `Errored { error_class: "retry_loop_no_progress" }` instead of another retry. This is a deliberate observe-and-fail signal (like the frame-stuck warning is observe-only, but error-types is observe-and-cap).

The effective cap is resolved as:

1. The per-node value `max_retries_without_progress` from the template (pointer integer; `nil` = use deployment default; `0` = disable cap; `N > 0` = use N).
2. The deployment-level default `scheduler.max_retries_without_progress` from `rimsky.yml` (default 100).

A per-node value of `0` disables the cap entirely — useful for nodes expected to retry indefinitely (watchdog graphs polling external systems). For most nodes the deployment default is appropriate.

The metric `rimsky_terminal_verdicts_total{error_class="retry_loop_no_progress"}` fires whenever the cap forces a failure; alerting on this metric surfaces retry loops before they exhaust budget.

`docs/concepts/error-policy.md` notes the relationship to the four lifecycle handlers: "The four lifecycle handlers (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`) override or extend this resolution." The handler runs first; if it resolves `retry` and there's no explicit policy, the error-types policy chain takes over.

Blocked vs Errored is a common confusion. `Blocked` means "I produced output but explicitly chose not to claim success" — typically routed via `on_executor_blocked` to a downstream review or routing node. `Errored` means a true failure with an `error_class`. The two terminal events trigger different handlers and different policies.

## Code surface

- `foundation/integration/runner_terminal_errors.go` — error-class policy resolution.
- `foundation/integration/on_error.go` — error-action dispatch.
- `foundation/persistence/postgres/migrations/` (look for `consecutive_retries_no_progress` column) — counter column on `rimsky_worker_request` or `rimsky_nodes`.
- `foundation/persistence/postgres/migrations/004-last-outcome-and-progress.sql` — `last_outcome` whose change resets the counter.
- `modeling/config/` — `scheduler.max_retries_without_progress` default.
- `modeling/observability/metrics.go` — `rimsky_terminal_verdicts_total` metric.

## Prose surface

- `docs/concepts/error-policy.md` — concept-doc (the primary surface).
- `docs/concepts/handlers.md` — handler/policy interaction.
- `docs/concepts/operational-health.md` "Detect retry loops with no progress" — operator playbook.
- `CLAUDE.md` "Vocabulary" — 3 error actions (`retry`, `invalidate(targets)`, `give_up`) plus `pass`.

## Adjacent topics

- `terminal-resolution` — Stage 4 in the end-to-end terminal flow; this entry is the Stage 4 internal mechanics.
- `reactive-loops-and-lifecycle-handlers` — the four handlers including `on_executor_errored`.
- `2026-05-10-cascade-fires-on-last-outcome` — `last_outcome` changes reset the retry counter.
- `2026-05-10-frame-stuck-is-advisory` — sibling observe-only mechanism at the frame level.
- `2026-05-10-parked-state-and-resume` — alternative to retry for "waiting" workloads.

## Observations

- The 3 error actions (`retry`, `invalidate(targets)`, `give_up`) cited in CLAUDE.md vocabulary differ from the 5 actions enumerated in `docs/concepts/error-policy.md` (the doc adds `pass`, `discard_then_retry`, `resume_then_retry`). The vocabulary list is older; the concept doc is the current authority.
- `discard_then_retry` vs `resume_then_retry` matter for held claims: `discard` releases the claim handle before retry; `resume` keeps it. Use cases differ — `discard` for transient errors where re-acquisition is safe; `resume` for retries that must preserve the same snapshot.
- The cap default (100) is a `scheduler.` config field, not `error.`. This is consistent with the cap being a scheduler-tick-time enforcement rather than a per-dispatch limit.
- `Blocked` is a routing signal (with `on_executor_blocked: { resolve: pass, invalidate: { targets: [reviewer] } }` as the canonical idiom per `docs/concepts/executor.md`); it does not increment the retry counter (only `retry` does).
- The metric `rimsky_terminal_verdicts_total{error_class=...}` is one of several `terminal_verdicts` cuts; alerting on `retry_loop_no_progress` specifically surfaces the cap firing.
