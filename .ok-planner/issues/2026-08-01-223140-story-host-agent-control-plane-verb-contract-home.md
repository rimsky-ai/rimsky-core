---
issue: story-host-agent-control-plane-verb-contract-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:host-agent-control-plane
  - concept:rimsky
status: open
opened: 2026-08-01T22:31:40Z
---

# Host-agent CLI verb contracts are stated only in story prose

## Problem

`story:host-agent-control-plane`'s prose carries the per-verb contract (start launches connected to the configured proxy or refuses with a diagnostic; status reports connection state, configured proxy, spawned children). `concept:rimsky`'s CLI-surface bullet stops at "lifecycle control".

## Candidates

- Home the verb contracts in concept:rimsky's CLI-surface bullet (or a decision), then reduce the story.
- Rule the contracts below corpus altitude and reduce.
