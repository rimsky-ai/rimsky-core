---
issue: story-verifier-severity-allowlist-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:verifier-severity-partition
status: open
opened: 2026-08-01T22:32:50Z
---

# Verifier severity allowlist validation has no corpus home

## Problem

`story:verifier-severity-partition`'s prose commits severity is validated against a closed allowlist (empty defaults to error; only warning/error accepted; anything else rejected with a structured error before any check runs). No verifier concept exists and no decision states this.

## Candidates

- Home it in a decision (verifier severity grammar), then reduce the story.
- Rule it below corpus altitude and reduce.
