---
concept: error-policy
status: as-is
aliases:
  - error-types policy chain
---

# Error policy

## What it is

The template-level `error_types:` block maps per-`error_class` strings to one of four runtime actions: `pass`, `give_up`, `retry`, `discard_claims_then_retry`. The runtime resolves the error class through the policy chain at terminal-error dispatch. Cap: every dispatch tracks a consecutive-retries-without-progress counter on the dispatch row; when it exceeds the effective cap (a per-node `max_retries_without_progress` setting or a supervisor-level default), the runtime forces `Error { error_class: "retry_loop_no_progress" }`.

`error_types:` is the **single** decision surface for runtime error routing: every error variant arrives via `Error{error_class}` and is dispatched through the policy chain. The `acquire/*` class-name prefix is reserved for runtime acquisition failures (e.g. `acquire/unavailable` when a required claim handle cannot be opened, `acquire/producer_error` when a producer verb faults without naming a class); operators wanting retry-on-acquire declare it via `error_types: { "acquire/unavailable": { policy: [...] } }`. A producer may name a more specific class on an acquisition failure (declared in its capabilities vocabulary); the policy lookup for acquisition failures then falls back exact producer class → `acquire/*` family → unknown-class default, so a template declaring only the generic family still catches classified failures. Without any matching declaration the chain falls through to `give_up("unknown_error_class")` — fail-fast is the default; retry is opt-in.

## Three-name relationship

Three vocabulary surfaces describe the same mechanism — distinguish them by context:

- **Design-log noun** — `concept:error-policy` (this file).
- **Operator-facing YAML field** — `error_types:` (the map of `error_class` → action declared inside a template).
- **Implementation** — the runtime policy-chain resolver, entered from the terminal-error dispatch.

The four runtime actions are `pass`, `give_up`, `retry`, and `discard_claims_then_retry`. Names outside this 4-value vocabulary (for example `invalidate`, `discard_then_retry`, `resume_then_retry`) reject at template-validation time through the generic 4-value range-check applied to the `error_types:` block at registration.

`error_types:` keys (the error-class strings themselves) are range-checked at registration against the union of the declared vocabularies a key may legitimately come from: the node's executor's declared error classes, the `acquire/*` synthetic family (plus the other runtime-synthesized classes), and the declared error classes of every claim producer reachable from the node's claims block (producers advertise their vocabulary in the capabilities handshake; declaring nothing remains legal). A key attributable to no declared vocabulary registers as an advisory warning, never a hard rejection — the validator must accept whatever the runtime is able to route, and undeclared peer vocabularies must not lock operators out of their own routing.

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
- Per-node `max_retries_without_progress = 0` disables the cap; `nil` falls back to the supervisor default (100); `N > 0` uses N.
- `discard_claims_then_retry` releases held claim handles (fires the producer's abandon verb on each store) before retry; the regular `retry` preserves them by default.
- `pass` settles the run with a fresh color and advances the policy chain's action index so a subsequent same-class error in the same dispatch does not `pass` again.
- `give_up` settles the run with a failed color.
- `acquire/<reason>` is a reserved class-name prefix for runtime acquisition failures; operators may declare `error_types:` keys under this prefix. The acquisition-failure policy lookup resolves in fallback order: the exact producer-declared class first (when the producer named one on the failure), then the `acquire/*` synthetic family class for that failure kind (`acquire/unavailable` for unavailable claims, `acquire/producer_error` for unclassified producer faults), then the unknown-class default — `give_up("unknown_error_class")` (fail-fast; retry is opt-in). An exact-class entry always wins over the family entry. The fallback affects policy lookup only; the emitted signal carries the most specific class.
- A terminal-verdict metric tagged with `error_class="retry_loop_no_progress"` increments when the cap forces a failure.
