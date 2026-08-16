---
audit: orphan-reaper-connection-state
artifact: decision:orphan-reaper-connection-state
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:26:44Z
---

# Orphan recovery keys on connection state for sync dispatches and on quiet period or max runtime for async

Supported. On the sync path a failed executor call returns a terminal event in band — a dial failure, a sync-deadline breach, or a cancellation each map to their own error class and flow through the ordinary terminal handling that releases the claim in the same pass; nothing defers sync cleanup to a sweep. On the async path a periodic sweep lists only claimed runs that registered an async acknowledgement and releases one when the time since claim exceeds the configured max runtime or the time since last progress (falling back to the claim time) exceeds the configured max quiet period, each guarded by the deadline being set and positive, and each release emits an orphaned-claim-released record naming the error class and reason. Unit tests cover the max-runtime arm and its precedence over quiet period, and a scenario test drives the quiet-period arm end to end by parking an acknowledged dispatch and sweeping it back to acquisition-eligible. No heartbeat-loss detector exists: the protocol has no streaming channel, and the only liveness inputs are the keepalive endpoint and attribute writeback, both of which advance the same last-progress timestamp the quiet-period check reads.
