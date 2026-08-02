---
issue: story-audit-artifact-no-special-reader-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:audit-artifact
  - decision:artifact-layout
  - decision:persistence-driver
status: open
opened: 2026-08-01T23:30:00Z
---

# The no-rimsky-specific-reader promise is stated only in audit-artifact's story prose

## Problem

`story:audit-artifact`'s prose commits that an operator opens the per-run artifact "with widely-available tooling for the format — no rimsky-specific reader is required". That is a real constraint on the artifact's storage format, not a restatement of the story sentence: it forbids a bespoke on-disk encoding even where one would be convenient. No live artifact carries it. `decision:artifact-layout` fixes the directory shape (per-run directory, state database beside blob store, `latest` pointer) but says nothing about the format being openable by third-party tooling; `decision:persistence-driver` picks the drivers without stating that the choice is load-bearing for operator inspection. The story was therefore left unreduced by the expression-normalization pass, which leaves it carrying prescriptive content whose honest home is a decision.

## Candidates

- Amend `decision:artifact-layout` (or `decision:persistence-driver`) to state the openable-with-standard-tooling constraint on the artifact's state database and blob spill root, then reduce the story to its canonical sentence.
- Rule the constraint an incidental consequence of the driver choice rather than a commitment, and reduce the story with the clause dropped.
