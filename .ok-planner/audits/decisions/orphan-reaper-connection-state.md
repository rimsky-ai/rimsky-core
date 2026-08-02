---
audit: orphan-reaper-connection-state
artifact: decision:orphan-reaper-connection-state
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:34Z
---

# Sync orphan detection is in-band connection failure; async is a periodic quiet/runtime sweep

Supported. For sync dispatches, `runner_dispatch.go`'s `dispatch` function calls the executor client's `Execute` inline and turns any RPC failure (dial failure, deadline exceeded, cancellation) directly into a terminal event handled synchronously in the same call — no separate watcher — and a conformance test explicitly asserts a sync-mode dispatch row never leaks into the async orphan sweep's candidate list. For async dispatches, `conductor.go`'s `SweepExecutorDeadlines`/`decideExecutorDeadlineRelease` is a periodic check keyed on `EffectiveMaxRuntimeSeconds` (time since claim) and `EffectiveMaxQuietPeriodSeconds` (time since last progress), each only evaluated when its configured deadline is non-zero, over the `ListOrphanedClaims` population which is filtered to rows with a non-null `async_ack_id` — checked the query predicate directly. The executor gRPC service's own comment confirms "no stream, no heartbeats, and no named events," and a repo-wide search for heartbeat-loss handling in the executor dispatch path (excluding the unrelated host-agent-proxy heartbeat protocol and a vestigial, nowhere-referenced `HEARTBEAT_LOST` event-kind enum) found none. Direct unit tests cover the deadline-decision function's four branch cases.
