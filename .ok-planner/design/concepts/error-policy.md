---
concept: error-policy
status: as-is
aliases:
  - error-types policy chain
references:
  - _discover/error-policy-retry-loop-cap.md
  - _discover/reactive-loops-and-lifecycle-handlers.md
  - ../../specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md
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

The four runtime actions are `pass`, `give_up`, `retry`, and `discard_claims_then_retry`. Pre-2026-05-23 vocabulary surfaces (`invalidate`, `discard_then_retry`, `resume_then_retry`) all reject at template-validation time through the generic 4-value range-check applied to the `error_types:` block at registration.

Per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #9, Group E.2): the wire-level blocked terminal collapsed into an error outcome bearing an error-class field. The lifecycle-handler slot `on_executor_blocked` is retired. Templates that previously declared `on_executor_blocked` migrate to an explicit `error_types: { executor_blocked: ... }` entry.

## Purpose

Different errors warrant different responses. A declarative policy spares every executor from reinventing retry/cascade semantics, lets the platform uniformly bound runaway retry loops, and treats executor `Error{class}` and runtime acquisition failure under one chain.

## Boundaries

Owns: the 4-value action vocabulary (`pass | give_up | retry | discard_claims_then_retry`), the per-class policy chain entry point (executor-Error + acquisition-failure), the retry-counter cap.

Does NOT own:
- The signal type-path taxonomy (lives in `concept:signal`).
- Cascade firing (lives in `concept:cascade`).
- Terminal-resolution stitching from terminal event to producer verb (lives in `concept:terminal-resolution`).
- The handler-slot vocabulary (`pass | retry | error`) on `on_acquire_unavailable` — that retires entirely under spec 2026-05-23 (Pass 4); the slot's role folds into the unified `error_types:` surface via synthetic class `acquire/*`.

Adjacent: `signal` (settling_signal_type changes reset the retry counter), `frame` (sibling observe-only mechanism — `frame.stuck.observed` slog warning fires for no-progress windows), `terminal-resolution`.

## Invariants

- The `consecutive_retries_no_progress` counter resets on any `settling_signal_type` change between consecutive retries (the retry-cap gate compares the most recent two terminals' canonical signal type-paths; identical signals across N retries trigger the cap).
- Per-node `max_retries_without_progress = 0` disables the cap; `nil` falls back to deployment default (100); `N > 0` uses N.
- `discard_claims_then_retry` releases held claim handles (fires the producer's abandon verb on each store) before retry; the regular `retry` preserves them by default.
- `pass` settles the run with a fresh color and advances the policy chain's action index so a subsequent same-class error in the same dispatch does not `pass` again.
- `give_up` settles the run with a failed color.
- `acquire/<reason>` is a reserved class-name prefix for runtime acquisition failures; operators may declare `error_types:` keys under this prefix. Absent a declared entry, acquisition failure routes through node evaluation with no matching policy → `give_up("unknown_error_class")` (fail-fast; retry is opt-in).
- A terminal-verdict metric tagged with `error_class="retry_loop_no_progress"` increments when the cap forces a failure.

## Aliases and historical names

Pre-2026-05-23 the policy vocabulary included `invalidate(targets)`, `discard_then_retry`, and `resume_then_retry`. The 2026-05-23 reshape tightens the vocabulary to exactly four values (`pass | give_up | retry | discard_claims_then_retry`):

- `invalidate(targets)` — retired 2026-05-14. Receivers now declare cascade coupling via `subscribes: [{ node: <sender>, type: terminal/error/<class> }]`.
- `discard_then_retry` — renamed to `discard_claims_then_retry` (the new name makes clear the verb fires on the claim handles, not the node row).
- `resume_then_retry` — deleted; behaviorally identical to `discard_claims_then_retry` under the post-E.2 wire shape, so the duplicate slot retires without a shim.

The policy-chain resolver was renamed per `spec:2026-05-12-nomenclature-resolution` (audit ride-along I.2).

## Open within this concept

(none live; the previously open tensions on action-count drift and blocked-vs-errored routing were resolved by `spec:2026-05-12-nomenclature-resolution` Groups E.2 / E.9 / E.10 / I.2 — see `tension:_resolved/error-action-count-drift` and `tension:_resolved/blocked-vs-errored-routing`.)

## Notes

- Action vocabulary consolidated to four (`retry`, `invalidate(targets)`, `give_up`, `pass`) per `spec:2026-05-12-nomenclature-resolution` audit cross-layer #9. The policy-chain resolver was renamed (ride-along I.2). Wire-level blocked terminal collapsed into an error outcome bearing the `executor_blocked` error class (Group E.2); the `on_executor_blocked` lifecycle-handler slot retired (E.10). Resolves `tension:_resolved/error-action-count-drift` and `tension:_resolved/blocked-vs-errored-routing`.
- 2026-05-14: `action: invalidate` retires; the four-action set reduces to `retry | give_up | pass` (plus the historical `discard_then_retry` / `resume_then_retry` retry flavors). Receivers declare cascade coupling via `subscribes: [{node: <sender>, on: state, when: failed, error_class: <class>}]`; the per-node retry-loop cap stays. Per `spec:2026-05-14-subscription-cascade-and-quality-of-life`.
- [2026-05-18] Folded content from a former public-docs error-policy page (now retired). The cap-resolution chain is: per-node `max_retries_without_progress` (a pointer integer — `nil` = use deployment default; `0` = disable the cap entirely; `N > 0` = use N), falling back to a deployment-level scheduler default (100). Operator framing: a per-node `0` is for nodes expected to retry indefinitely (watchdog graphs, polling against external systems); blanket-disabling the cap across the deployment hides bugs. Alert on the terminal-verdict metric tagged `error_class="retry_loop_no_progress"` to surface retry loops before they exhaust budget.
- 2026-05-23 — Reshape per `spec:2026-05-23-signal-taxonomy-and-policy-decoupling` (Pass 3). Vocabulary tightened to 4 values (`resume_then_retry` deleted; `discard_then_retry` renamed to `discard_claims_then_retry`; `pass` added as first-class action with an explicit step-switch case in the policy-chain step evaluator). Policy resolution decoupled into a resolution tuple: a canonical signal type-path + payload (per `concept:signal`), a dispatch disposition (`end | retry | park_async | park_scheduled`), and a color (`fresh | failed | parked`). Acquisition failure folds into the `error_types:` surface via synthetic-class prefix `acquire/*` (Pass 4 work). The `on_executor_errored.error{error_class}` remap retires (the now-retired lifecycle-handler concept retires entirely in Pass 4). The template-validator action range-check rejects pre-reshape names (`invalidate`, `discard_then_retry`, `resume_then_retry`) through the generic 4-value gate.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- [2026-05-24] New error_class values added to the executor-Error and claim-producer-error vocabularies for proxy-mediated dispatch failures: host_agent_not_connected, binding_not_found, spawn_failed, host_agent_disconnected, contract_mismatch, executor_crashed. These ride the existing error_types: chain with no policy-mechanism changes. Per spec 2026-05-24-host-agent-and-proxy-design.
