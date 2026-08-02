---
issue: intent-cascade-what-it-is-stale-walk-vocabulary
kind: sprint
category: intent-ledger
artifacts:
  - concept:cascade
status: open
opened: 2026-08-01T22:41:40Z
---

# concept:cascade's What-it-is section still describes the retired walk model

## Problem

`concept:cascade`'s What-it-is section leads with the retired scheduler-tick-driven, topology-ordered walk/propagation/fallthrough framing — vocabulary the ledger's own record says dissolved into the node_run-queue redesign (rounds, seal, gate evaluator, cascade modes). Current code fires the cascade walk from inside the settling transaction on terminal/attribute-changed signals; only the pure-cascade sweep is tick-driven. The concept's own Invariants section already describes the correct model, so the file is internally inconsistent.

Evidence tier: mixed.

## Candidates

- Rewrite the What-it-is section to match the Invariants: event-driven wait-set/pending/gate-evaluator propagation, tick-driven pure-cascade fallthrough only.
- Leave the prose; rule the framing harmless legacy color.
