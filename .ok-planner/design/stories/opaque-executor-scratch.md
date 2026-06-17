---
story: opaque-executor-scratch
status: as-is
aliases: []
---

# Opaque bytes carried across recovery re-dispatch of the same node-run

## Role and capability

As an executor author, I can attach opaque bytes to a settling Outcome and observe them on the next dispatch's recovery of the same node-run, so I can carry in-flight state across the runtime's stale-recovery cycle without rimsky inspecting or modifying the bytes.

## Acceptance

I write an executor that writes scratch — either mid-dispatch via the executor-protocol scratch callback (mirroring the attributes incremental-writeback pattern) or by attaching scratch bytes to a settling Outcome (the unary `Execute` RPC's Outcome — Success, Error, or Park — or the async-callback body's outcome of the same shape); rimsky persists the scratch on the dispatch row that received it; when a successor dispatch row is created under one of the three recovery dispositions that link to a predecessor dispatch — `PRIOR_STALE_RECOVERY` (storage form `stale_recovery`), retry-after-error, or recalculate — the enqueue path copies the predecessor dispatch's scratch onto the new row, and the successor's incoming request carries the original scratch bytes verbatim. Normal cascade re-fires go through the cascade walker's stale-mark path, which does not stamp a `prior_dispatch_id` reference and so does not carry scratch; same-node state across normal cascade re-fires rides the attribute carry-forward channel, not scratch.

## Falsifier

Scratch persisted on the predecessor dispatch is not present on the next dispatch's incoming request. OR: the bytes returned differ from the bytes the executor wrote (rimsky inspected or transformed them).

## Proof

Executable proof exercising the round-trip: executor writes scratch (mid-dispatch via the scratch callback, or by attaching scratch bytes to a settling Outcome); enqueue a follow-on dispatch row with the `prior_dispatch_id` link set (using the same mechanism the cascade re-dispatch, `PRIOR_STALE_RECOVERY`, and retry-after-error paths use); assert the new dispatch's request carries the original scratch bytes verbatim.
