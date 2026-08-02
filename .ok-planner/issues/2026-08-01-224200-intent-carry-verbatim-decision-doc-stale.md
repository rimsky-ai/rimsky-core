---
issue: intent-carry-verbatim-decision-doc-stale
kind: sprint
category: intent-ledger
artifacts:
  - decision:carry-verbatim-requires-one
  - concept:child-execution
status: open
opened: 2026-08-01T22:42:00Z
---

# decision:carry-verbatim-requires-one describes a retired model its validator no longer enforces

## Problem

The validator still carries a `carry_verbatim` branch, but as an unconditional legacy-token rejection on fan_out (`code:lib/graph/node/template_validator_holds.go`). The companion `decision:carry-verbatim-requires-one` still describes the old model — carry-verbatim as a conditionally-valid aggregation policy requiring exactly one child — contradicting `concept:child-execution`'s current text (a runtime routing tag, never author-facing; the aggregation kind removed).

Evidence tier: mixed.

## Candidates

- Rewrite the decision to record the current behavior (unconditional rejection with a pointer to the four valid policies).
- Retire the decision; concept:child-execution's invariant carries the substance.
