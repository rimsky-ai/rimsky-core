---
tension: transition-reason-missing-concept
category: unspecified
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - node-state
  - last-outcome
  - cascade
  - event-log
resolution:
  shape: promote-new-concept
  new-concept: concepts/transition-reason.md
  summary: |
    Promoted transition-reason to a concept with Definition, Purpose,
    Boundaries (owns the audit enum + write site at state transitions),
    Invariants (ReasonHandlerError dead-end sentinel; exhaustive
    enumeration), Adjacent (node-state, last-outcome, cascade, event-log).
    Node-state cross-references updated. The relationship-tension
    transition-reason-vs-last-outcome.md remains open by design.
---

# `transition-reason` is load-bearing audit vocabulary but has no concept file

## What is muddy

`TransitionReason` is a ~10-value enum in `foundation/cascade/state.go:28-44` (`ReasonHandlerComplete`, `ReasonHandlerError`, `ReasonPureCascade`, `ReasonInfraReenqueue`, `ReasonScheduleFire`, ...). It is the audit-vocabulary parallel to `last_outcome`: same "what just happened" question, different consumer (audit/event-log vs cascade-firing gate). It is mentioned inline in `concepts/node-state.md` ("audit metadata live in `transition-reason`") and has an open tension `transition-reason-vs-last-outcome.md` that is genuinely about reconciling the two vocabularies. But it has no concept entry, so a reader grep'ing the design log for `transition_reason` finds nothing canonical.

## Why it matters

The catalog currently documents `node-state` (dispatch-eligibility predicate) and `last-outcome` (cascade-fire predicate). A third sibling vocabulary — `transition-reason` (audit metadata) — is structurally parallel: same row, written at the same transition, but consumed by audit/event-log rather than gating a runtime predicate. The tension between `transition-reason` and `last-outcome` is hard to scope when only one of the two has a concept page.

Future code adding a new outcome path has to decide which vocabularies it appears in (last_outcome, transition_reason, both). Today the relationship is documented in a tension; promoting the audit enum to a concept lets the relationship be cross-linked.

## Resolution candidates (do NOT pick)

- **Promote** `concepts/transition-reason.md` with: Definition (the 10-value enum, where it lives in code), Purpose (audit vocabulary distinct from cascade-fire vocabulary), Boundaries (written by the state-transition apply path, read by event-log consumers), Invariants (`ReasonHandlerError` is a deliberate dead-end sentinel, etc.), Adjacent (`node-state`, `last-outcome`, `cascade`, `event-log`). Update `node-state.md` Boundaries / Adjacent to cite the new concept. The existing `transition-reason-vs-last-outcome` tension stays scoped to the relationship between the two.
- **Don't promote**; instead add a "TransitionReason audit vocabulary" subsection inside `cascade.md` or `node-state.md` and cross-link from `last-outcome.md`. Keeps concept count flat.

(Pre-decided shape: promote.)

## Evidence

- `foundation/cascade/state.go:14, 28-44`.
- `concepts/node-state.md` Boundaries (inline mention).
- `concepts/last-outcome.md` (parallel concept).
- `tensions/transition-reason-vs-last-outcome.md`.
- `review-notes.md` "Suspected-but-unconfirmed concepts" / `transition-reason` bullet.

