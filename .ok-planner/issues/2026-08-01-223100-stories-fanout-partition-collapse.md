---
issue: stories-fanout-partition-collapse
kind: sprint
category: stories-collapses
artifacts:
  - story:fs-fanout-expand-folder
  - story:fs-fanout-list-array
  - story:pg-fanout-list-array
status: open
opened: 2026-08-01T22:31:00Z
---

# Three fan-out partition stories express one outcome per surface

## Problem

`story:fs-fanout-list-array` and `story:pg-fanout-list-array` are the identical outcome (fan out over an upstream list) told once per backend; `story:fs-fanout-expand-folder` is the same fan-out capability with a folder-derived partition source. One user outcome, three per-surface tellings.

## Candidates

- Collapse to one fan-out-partition story; move the per-backend surface choice to a decision.
- Keep per-surface stories; rule that each backend's partition grammar is its own promise.
