---
issue: story-runtime-diagnostics-split
kind: sprint
category: stories-splits
artifacts:
  - story:runtime-diagnostics
status: open
opened: 2026-08-01T22:30:50Z
---

# runtime-diagnostics bundles four observability surfaces

## Problem

The sentence covers parked nodes, pending wake dependencies, held sub-graph frames, and claim holders — four surfaces each usable alone.

## Candidates

- Split per surface.
- Keep bundled as one operator-diagnostics story.
