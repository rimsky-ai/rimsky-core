---
tension: heartbeat-cutoff-asymmetry
category: inconsistent
status: resolved
spec: 2026-06-16-executor-protocol-coherence
affects:
  - orphan-reaper
  - node-run
  - claim-handle
resolution:
  shape: mechanism-replaced
  summary: |
    The supervisor↔executor heartbeat-based orphan-detection mechanism
    was retired by the executor-protocol-coherence reshape. Orphan
    detection now keys on `last_progress_at` and RPC connection state,
    not on a heartbeat-interval cutoff. The two-representation
    asymmetry this tension described (`now() - 5×heartbeat_interval`
    on worker requests vs. precomputed `expires_at` on claim handles)
    no longer has a `last_heartbeat_at` to disagree about — the column
    was dropped from both tables and the worker-request reaper is
    gone. The surviving `expires_at` on claim handles is the single
    cutoff representation. The dead remnants of the retired mechanism
    in the proto layer (`HeartbeatLostPayload` message,
    `OPERATIONAL_KIND_HEARTBEAT_LOST` enum value, `heartbeat_lost = 19`
    oneof slot) and the matching kinds.go entry are removed; their
    field numbers are reserved against accidental reuse.
---
