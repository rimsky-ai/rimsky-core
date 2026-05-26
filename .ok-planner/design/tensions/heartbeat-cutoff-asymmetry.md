---
tension: heartbeat-cutoff-asymmetry
category: inconsistent
status: open
affects:
  - orphan-reaper
  - node-run
  - claim-handle
---

# `5 × heartbeat_interval` cutoff is spelled two different ways across worker-request and claim-handle reapers

## What is muddy

CLAUDE.md "Blessed invariants" §6 states a single cutoff: `5 × heartbeat_interval`. But the implementation expresses this differently across the two row types:

- **`rimsky_worker_request`** uses `last_heartbeat_at < now() - (5 × heartbeat_interval)` — a runtime comparison.
- **`rimsky_claim_handle`** uses `expires_at < now()` where `expires_at = last_heartbeat_at + (5 × heartbeat_interval)` is set at heartbeat refresh time — a precomputed column.

Same logical cutoff, two representations. If the heartbeat-refresh path forgets to update `expires_at` on one side, the cutoff drifts.

## Why it matters

A future tuning of the multiplier (`5 ×` → `3 ×`) needs to be applied in two places that look syntactically different. A heartbeat-refresh code path that forgets one side produces silent drift.

## Resolution candidates (do NOT pick)

- Unify on one representation (computed-at-refresh for both, or runtime-comparison for both).
- Centralize the multiplier in one constant referenced by both reapers.
- Add a regression test that pins the cutoff equality.

## Evidence

- `_discover/orphan-claim-cutoff-five-heartbeats.md` Observations bullet 2.
- `foundation/integration/conductor.go:30-50`.
- `foundation/persistence/postgres/queue.go:229-265`.
- `foundation/persistence/postgres/claim_handles.go`.

