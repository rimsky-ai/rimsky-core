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

The template-level `error_types:` block maps per-`error_class` strings to actions: `retry`, `discard_then_retry`, `resume_then_retry`, `invalidate(targets)`, `give_up`, `pass`. The runtime's error-class resolver lives in `foundation/integration/runner_terminal_errors.go` + `on_error.go`. Cap: every dispatch tracks `consecutive_retries_no_progress`; when it exceeds the effective cap (`max_retries_without_progress` per-node or `scheduler.max_retries_without_progress` deployment-level), the runtime forces `Errored { error_class: "retry_loop_no_progress" }`.

## Purpose

Different errors warrant different responses. A declarative policy spares every executor from reinventing retry/cascade semantics and lets the platform uniformly bound runaway retry loops.

## Boundaries

Owns: the action vocabulary, the policy chain entry point, the retry-counter cap. Does NOT own: the four lifecycle handlers (those run first; see `lifecycle-handler`), the `Blocked` route (handled by `on_executor_blocked`, not error-types), cascade firing (see `cascade`), the end-to-end stitching from terminal event to producer verb (see `terminal-resolution`). Adjacent: `lifecycle-handler`, `last-outcome` (changes reset the retry counter), `frame` (sibling observe-only mechanism — `frame.stuck.observed` slog warning fires for no-progress windows), `terminal-resolution`.

## Invariants

- The `consecutive_retries_no_progress` counter resets on any `last_outcome` change.
- Per-node `max_retries_without_progress = 0` disables the cap; `nil` falls back to deployment default (100); `N > 0` uses N.
- `discard_then_retry` releases held claim handles before retry; `resume_then_retry` preserves them.
- The metric `rimsky_terminal_verdicts_total{error_class="retry_loop_no_progress"}` fires when the cap forces a failure.

## Aliases and historical names

CLAUDE.md "Vocabulary" cites 3 error actions (`retry`, `invalidate(targets)`, `give_up`); `docs/concepts/error-policy.md` enumerates 5+1 (`retry`, `discard_then_retry`, `resume_then_retry`, `invalidate(targets)`, `give_up`, `pass`). The doc is the current authority.

## Open within this concept

- Error-action count drift between CLAUDE.md "Vocabulary" (3) and `docs/concepts/error-policy.md` (5+1) — see `tensions/error-action-count-drift.md`.
- `Blocked` vs `Errored` confusion potential — see `tensions/blocked-vs-errored-routing.md`.

