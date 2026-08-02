---
issue: story-rimsky-health-check-dependency-semantics-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:rimsky-health-check
  - concept:control-api
status: open
opened: 2026-08-01T22:32:20Z
---

# Health-probe degraded-dependency semantics have no home

## Problem

`story:rimsky-health-check`'s prose commits the probe returns non-success when a critical dependency is down; concept:control-api confirms the probe is unauthenticated but never states the success/non-success mapping.

## Candidates

- Home the dependency semantics as a concept:control-api invariant (or a decision), then reduce the story.
- Rule it below corpus altitude and reduce.
