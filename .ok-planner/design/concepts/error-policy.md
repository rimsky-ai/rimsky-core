---
concept: error-policy
status: as-is
aliases:
  - error-types policy chain
---

# Error policy

## What it is

The template-level `error_types:` block maps per-`error_class` strings to one of four runtime actions: `pass`, `give_up`, `retry`, `discard_claims_then_retry`. The runtime resolves the error class through the policy chain at terminal-error dispatch. Cap: every dispatch tracks a consecutive-retries-without-progress counter on the dispatch row; when it exceeds the effective cap (a per-node `max_retries_without_progress` setting or a deployment-level scheduler default), the runtime forces `Error { error_class: "retry_loop_no_progress" }`.

`error_types:` is the **single** decision surface for runtime error routing: every error variant arrives via `Error{error_class}` and is dispatched through the policy chain. The `acquire/*` class-name prefix is reserved for runtime acquisition failures (e.g. `acquire/unavailable` when a required claim handle cannot be opened); operators wanting retry-on-acquire declare it via `error_types: { "acquire/unavailable": { policy: [...] } }`. Without an explicit declaration the chain falls through to `give_up("unknown_error_class")` — fail-fast is the default; retry is opt-in.

## Three-name relationship

Three vocabulary surfaces describe the same mechanism — distinguish them by context:

- **Design-log noun** — `concept:error-policy` (this file).
- **Operator-facing YAML field** — `error_types:` (the map of `error_class` → action declared inside a template).
- **Implementation** — the runtime policy-chain resolver, entered from the terminal-error dispatch.

The four runtime actions are `pass`, `give_up`, `retry`, and `discard_claims_then_retry`. Names outside this 4-value vocabulary (for example `invalidate`, `discard_then_retry`, `resume_then_retry`) reject at template-validation time through the generic 4-value range-check applied to the `error_types:` block at registration.

## Purpose

Different errors warrant different responses. A declarative policy spares every executor from reinventing retry/cascade semantics, lets the platform uniformly bound runaway retry loops, and treats executor `Error{class}` and runtime acquisition failure under one chain.

## Boundaries

Owns: the 4-value action vocabulary (`pass | give_up | retry | discard_claims_then_retry`), the per-class policy chain entry point (executor-Error + acquisition-failure), the retry-counter cap.

Does NOT own:
- The signal type-path taxonomy (lives in `concept:signal`).
- Cascade firing (lives in `concept:cascade`).
- Terminal-resolution stitching from terminal event to producer verb (lives in `concept:terminal-resolution`).

Adjacent: `signal` (settling_signal_type changes reset the retry counter), `frame` (sibling observe-only mechanism — `frame.stuck.observed` slog warning fires for no-progress windows), `terminal-resolution`.

## Invariants

- The `consecutive_retries_no_progress` counter resets on any `settling_signal_type` change between consecutive retries (the retry-cap gate compares the most recent two terminals' canonical signal type-paths; identical signals across N retries trigger the cap).
- Per-node `max_retries_without_progress = 0` disables the cap; `nil` falls back to deployment default (100); `N > 0` uses N.
- `discard_claims_then_retry` releases held claim handles (fires the producer's abandon verb on each store) before retry; the regular `retry` preserves them by default.
- `pass` settles the run with a fresh color and advances the policy chain's action index so a subsequent same-class error in the same dispatch does not `pass` again.
- `give_up` settles the run with a failed color.
- `acquire/<reason>` is a reserved class-name prefix for runtime acquisition failures; operators may declare `error_types:` keys under this prefix. Absent a declared entry, acquisition failure routes through node evaluation with no matching policy → `give_up("unknown_error_class")` (fail-fast; retry is opt-in).
- A terminal-verdict metric tagged with `error_class="retry_loop_no_progress"` increments when the cap forces a failure.
