---
issue: intent-frame-timeout-claims-stale
kind: sprint
category: intent-ledger
artifacts:
  - concept:frame
status: answered
opened: 2026-08-01T22:41:00Z
---

# Two dossiers still describe the retired frame_timeout_ms mechanism

## Question

Does the live corpus still describe a `frame_timeout_ms` schema CHECK or a soft-warning mechanism fed by it?

## Answer

No — `concept:frame` contains no mention of `frame_timeout_ms` anywhere; its only progress-tracking invariant names "the last-progress timestamp refreshed on every node-run state transition inside the frame's own lifetime (a liveness heartbeat consumed only by stall detection)," i.e. `last_progress_at`. Code confirms the retirement: `lib/foundation/persistence/postgres/migrations/024-retire-frame-timeout.sql` (and the sqlite equivalent) drops the `frame_timeout_ms` column and its `>= 60000` CHECK; the only remaining code hits are a test asserting the key is rejected as retired at registration. The live corpus is already correctly silent — only the two historical intent-ledger dossiers still describe the retired mechanism.
