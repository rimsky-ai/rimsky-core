---
decision: orphan-reaper-connection-state
status: as-is
aliases: []
---

# Orphan reaper keys on connection state / quiet-period

## Choice

For sync dispatches, the supervisor's gRPC client failure drives in-band claim cleanup. For async dispatches, a parked-sweep-style periodic check keys on time since last progress exceeding the configured max quiet period (when set) and time since dispatch exceeding the configured max runtime (when set). There is no heartbeat-loss detection.

## Rationale

With no streaming channel there is no heartbeat to lose; connection-state observation (sync) and persisted quiet-period detection (async) are honest signals — both observe real progress rather than an easily-faked heartbeat.

## Alternatives

Synthesize a "soft heartbeat" from attribute writeback alone — rejected because the keepalive endpoint plus writeback dual mechanism already covers this surface.
