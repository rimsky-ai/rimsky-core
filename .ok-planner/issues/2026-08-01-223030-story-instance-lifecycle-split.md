---
issue: story-instance-lifecycle-split
kind: sprint
category: stories-splits
artifacts:
  - story:instance-lifecycle
status: open
opened: 2026-08-01T22:30:30Z
---

# instance-lifecycle enumerates five operator actions in one sentence

## Problem

`story:instance-lifecycle` bundles create, watch, pause/resume, force-terminate, and remove. Create already stands alone as `story:instance-create-is-idle`, so the bundle overlaps a sibling as well as fusing distinct outcomes.

## Candidates

- Split the lifecycle verbs into their own stories and drop the create overlap.
- Keep as one umbrella story and rule the overlap harmless.
