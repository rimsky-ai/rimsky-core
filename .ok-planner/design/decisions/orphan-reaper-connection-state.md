---
decision: orphan-reaper-connection-state
status: as-is
aliases: []
---

# Orphan reaper keys on connection state / quiet-period

## Choice

For sync dispatches, the supervisor's gRPC client failure drives in-band claim cleanup. For async dispatches, the parked-sweep-style periodic check keys on `now - last_progress_at > max_quiet_period` (when set) and `now - dispatched_at > max_runtime` (when set). Heartbeat-loss detection is removed entirely.

## Rationale

Heartbeat-loss is gone with streaming; the replacements are honest signals — connection-state observation (sync) and persistent quiet-period detection (async). Both observe real signals rather than easily-faked heartbeats.

## Alternatives

Synthesize a "soft heartbeat" from attribute writeback alone — rejected because the keepalive endpoint plus writeback dual mechanism already covers this surface.
