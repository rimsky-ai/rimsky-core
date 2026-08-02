---
issue: intent-frame-timeout-claims-stale
kind: sprint
category: intent-ledger
artifacts:
  - concept:frame
status: open
opened: 2026-08-01T22:41:00Z
---

# Two dossiers still describe the retired frame_timeout_ms mechanism

## Problem

The validation dossier claims a live `frame_timeout_ms >= 60000` schema CHECK, and the observability dossier lists `frame_timeout_ms` as a live soft-warning mechanism. Both were retired by migration `024-retire-frame-timeout.sql` (column, CHECK, and the stuck-frame warning it fed); `last_progress_at` is the surviving progress clock and the live corpus is already correctly silent.

Evidence tier: artifact.

## Candidates

- Retire both ledger claims; the migration is the record.
- Restore a frame-timeout warning (contradicts the recorded retirement).
