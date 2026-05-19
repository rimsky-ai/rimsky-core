---
concept: error-policy
status: as-is
aliases:
  - error-types policy chain
references:
  - _discover/error-policy-retry-loop-cap.md
  - _discover/reactive-loops-and-lifecycle-handlers.md
---

# Error policy

## What it is

The template-level `error_types:` block maps per-`error_class` strings to actions: `retry`, `invalidate(targets)`, `give_up`, `pass`. The runtime's error-class resolver lives in `runtime/runner_error_policy.go::applyErrorPolicy` + `on_error.go`. Cap: every dispatch tracks `consecutive_retries_no_progress`; when it exceeds the effective cap (`max_retries_without_progress` per-node or `scheduler.max_retries_without_progress` deployment-level), the runtime forces `Error { error_class: "retry_loop_no_progress" }`.

## Three-name relationship

Three vocabulary surfaces describe the same mechanism — distinguish them by context:

- **Design-log noun** — `concept:error-policy` (this file).
- **Operator-facing YAML field** — `error_types:` (the map of `error_class` → action declared inside a template).
- **Implementation** — `code:runtime/runner_error_policy.go::applyErrorPolicy` (the policy-chain entry called from the terminal-error dispatch).

The four runtime actions are `retry`, `invalidate(targets)`, `give_up`, and `pass`. The pre-2026-05-12 vocabulary included `discard_then_retry` and `resume_then_retry` as separate retry flavors; under the post-E.2 proto restructure, retry semantics are uniform (claims are abandoned and retried) and the two-flavor split is retired.

Per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #9, Group E.2): the wire-level `Blocked` event collapsed into `Error{error_class}`. The lifecycle-handler slot `on_executor_blocked` is retired. As a consequence, `error_types:` is now the SINGLE decision surface for runtime error routing — every error variant arrives via `Error{error_class}` and is dispatched through the policy chain. Templates that previously declared `on_executor_blocked` migrate to `on_executor_errored` with an explicit `error_types: { executor_blocked: ... }` entry.

## Purpose

Different errors warrant different responses. A declarative policy spares every executor from reinventing retry/cascade semantics and lets the platform uniformly bound runaway retry loops.

## Boundaries

Owns: the action vocabulary, the policy chain entry point, the retry-counter cap. Does NOT own: the three lifecycle handlers (those run first; see `lifecycle-handler`), cascade firing (see `cascade`), the end-to-end stitching from terminal event to producer verb (see `terminal-resolution`). Adjacent: `lifecycle-handler`, `last-outcome` (changes reset the retry counter), `frame` (sibling observe-only mechanism — `frame.stuck.observed` slog warning fires for no-progress windows), `terminal-resolution`.

## Invariants

- The `consecutive_retries_no_progress` counter resets on any `last_outcome` change.
- Per-node `max_retries_without_progress = 0` disables the cap; `nil` falls back to deployment default (100); `N > 0` uses N.
- `discard_then_retry` releases held claim handles before retry; `resume_then_retry` preserves them.
- The metric `rimsky_terminal_verdicts_total{error_class="retry_loop_no_progress"}` fires when the cap forces a failure.

## Aliases and historical names

Pre-2026-05-12 the policy vocabulary included `discard_then_retry` and `resume_then_retry` as separate retry flavors; under the post-E.2 proto restructure, retry semantics are uniform (claims are abandoned and retried) and the two-flavor split is retired. The four actions are now `retry`, `invalidate(targets)`, `give_up`, `pass`. Implementation file renamed from `runner_terminal_errors.go::applyTerminalAppError` to `runner_error_policy.go::applyErrorPolicy` per `spec:2026-05-12-nomenclature-resolution` (audit ride-along I.2).

## Open within this concept

(none live; the previously open tensions on action-count drift and `Blocked`-vs-`Errored` routing were resolved by `spec:2026-05-12-nomenclature-resolution` Groups E.2 / E.9 / E.10 / I.2.)

## Notes

- Action vocabulary consolidated to four (`retry`, `invalidate(targets)`, `give_up`, `pass`) per `spec:2026-05-12-nomenclature-resolution` audit cross-layer #9. Implementation renamed to `code:runtime/runner_error_policy.go::applyErrorPolicy` (ride-along I.2). Wire-level `Blocked` event collapsed into `Error{error_class: "executor_blocked"}` (Group E.2); `on_executor_blocked` lifecycle-handler slot retired (E.10). Resolves `tension:_resolved/error-action-count-drift` and `tension:_resolved/blocked-vs-errored-routing`.
- 2026-05-14: `action: invalidate` retires; the four-action set reduces to `retry | give_up | pass` (plus the historical `discard_then_retry` / `resume_then_retry` retry flavors). Receivers declare cascade coupling via `subscribes: [{node: <sender>, on: state, when: failed, error_class: <class>}]`; the per-node retry-loop cap stays. Per spec `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
- [2026-05-18] Folded content from former `docs/concepts/error-policy.md` (now retired). The cap-resolution chain is: per-node `max_retries_without_progress` (a pointer integer — `nil` = use deployment default; `0` = disable the cap entirely; `N > 0` = use N), falling back to `cfg:scheduler.max_retries_without_progress` (default 100). Operator framing: a per-node `0` is for nodes expected to retry indefinitely (watchdog graphs, polling against external systems); blanket-disabling the cap across the deployment hides bugs. Alert on `rimsky_terminal_verdicts_total{error_class="retry_loop_no_progress"}` to surface retry loops before they exhaust budget.

