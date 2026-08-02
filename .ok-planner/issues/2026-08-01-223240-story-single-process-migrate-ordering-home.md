---
issue: story-single-process-migrate-ordering-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:single-process-all-in-one
  - decision:single-process-mode
status: open
opened: 2026-08-01T22:32:40Z
---

# All-in-one synchronous-migration ordering is stated only in story prose

## Problem

`story:single-process-all-in-one`'s prose commits that the all-in-one stack runs migrations synchronously before the roles start; decision:single-process-mode details role startup but not migration ordering.

## Candidates

- Amend decision:single-process-mode to state the migrate-before-roles ordering, then reduce the story.
- Rule it below corpus altitude and reduce.
