---
issue: intent-cascade-two-boundary-opacity-uncarried
kind: sprint
category: intent-ledger
artifacts:
  - concept:cascade
  - concept:run-scope
status: open
opened: 2026-08-01T22:41:50Z
---

# A ratified two-boundary/opacity cascade invariant is stated nowhere in the corpus

## Problem

A 2026-07-14 ruling (transcript tier) ratified that cascade crosses a RunScope boundary at exactly two places — sub-graph entry-success and fan-out-parent settlement — and nowhere else, with sub-graphs externally opaque to cascade. The individual trigger points are documented piecemeal (`concept:delegation`, `concept:fan-out`), but the closure/opacity half — 'and nowhere else' — appears in no live artifact.

Evidence tier: transcript.

## Candidates

- Add the two-boundary + opacity invariant to concept:cascade or concept:run-scope (owner picks the owning concept).
- Rule the closure below corpus altitude (scenario tests are the record).
