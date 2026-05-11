---
tension: transition-reason-vs-last-outcome
category: muddy-boundary
status: open
affects:
  - node-state
  - last-outcome
  - cascade
---

# `TransitionReason` and `last_outcome` carry overlapping audit vocabularies

## What is muddy

Two distinct vocabularies sit close together in the cascade code:

- `last_outcome` (5 values: `fresh_changed`, `fresh_unchanged`, `passed`, `pure_cascade`, `failed`) lives as a column on `rimsky_nodes`. Used as the cascade-firing gate.
- `TransitionReason` (a richer set including `ReasonHandlerComplete`, `ReasonHandlerError`, `ReasonPureCascade`, `ReasonInfraReenqueue`, `ReasonScheduleFire`, etc.) lives in `foundation/cascade/state.go:28-44`. Used for audit-trail variants in the event log.

They overlap conceptually — both describe "what just happened" — but live in different columns and serve different consumers. A reader looking at one without the other can miss the distinction between "cascade-relevant outcome" and "audit reason."

## Why it matters

Future code that adds a new outcome path has to decide which column(s) it appears in. A new "deadline-elapsed wake" event needs entries in both vocabularies if it should be both observable and audit-loggable, and the relationship isn't centrally tabulated.

## Resolution candidates (do NOT pick)

- Tabulate both vocabularies side-by-side in `docs/concepts/cascade.md` with explicit "use last_outcome here, use TransitionReason here" guidance.
- Collapse to one vocabulary (would lose the cascade-vs-audit split benefit).
- Keep both but cross-link annotations in `state.go`.

## Evidence

- `_discover/2026-05-10-cascade-fires-on-last-outcome.md` Observations bullet 1.
- `foundation/cascade/state.go:14, 28-44` — `TransitionReason` constants.
- `foundation/persistence/postgres/migrations/004-last-outcome-and-progress.sql` — `last_outcome` column.

