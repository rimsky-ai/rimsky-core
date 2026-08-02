---
issue: story-idempotent-mode-dedupes-split
kind: sprint
category: stories-splits
artifacts:
  - story:idempotent-mode-dedupes
status: open
opened: 2026-08-01T22:30:20Z
---

# idempotent-mode-dedupes covers two named modes as one story

## Problem

The sentence covers both `idempotent-queue` (queued-predecessor dedupe) and `idempotent-settled` (also check most-recent-settled) — two named modes per `concept:cascade-mode`, each a standalone user outcome.

## Candidates

- Split per mode.
- Keep as one story over the idempotent-mode family.
