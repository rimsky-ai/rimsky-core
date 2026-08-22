---
concept: claim-handle
---

# Claim handle

## What it is

A claim handle is the row in rimsky's own ledger that represents one acquired claim, or one acquisition of a named lock. It is the persistence-layer name for what the claim-producer protocol calls a claim, and the two names differ only in layer (see `concept:claim`, `concept:named-lock`). The row records which lock the holder acquired and under what name, the acquired claim scope, when the acquisition expires, the write semantics the producer realized, whether the claim is held, and which node-run holds it. The row also records the parent claim when this claim is a sub-claim of another (see `concept:claim-tree`), the aggregation policy and the child counts a fan-out parent resolves by (see `concept:fan-out`), the claim's lifetime (see `concept:claim-lifetime`), the version the producer reports when the claim settles (see `concept:lineage-record`, `concept:asset`), and an opaque candidate handle the producer issues for a typed-data flow (see `concept:data-processing`).

A handle carries one of three states. It is active while a supervisor holds it, committed once rimsky settles it as successful, and abandoned once rimsky settles it any other way. The row outlives its own settlement and records the moment it left the active state, so a settled claim stays readable.

A **held** claim is a claim whose life extends past its acquirer's terminal to cover the holding subgraph — the acquirer plus every co-holder the template declares (see `concept:claim-co-holdership`). The ledger tracks each member of that subgraph separately, keyed by the run that holds the claim: holders are runs, not nodes, and each member's participation is active, completed, or failed. An author marks no claim as held. Held-ness follows from the template's edges, because a claim becomes held exactly when some downstream node declares that it co-holds the claim.

## Purpose

The claim handle is rimsky's single source of truth for who holds what right now. Conflict checks, orphan reaping, and terminal resolution all read this one ledger, so rimsky's bookkeeping stays independent of producer-side state. Lock state lives here, and a producer neither keeps it nor shadows it.

## Boundaries

The claim handle owns the ledger of current holdership, the guard on mutating a row a supervisor still holds, and the row shape that lets a settled handle outlive the run that acquired it. It does not own producer-internal state, which belongs to `claim-producer`; run liveness, which belongs to `node-run`; or the dispatch of a claim's terminal verb, which belongs to `auto-terminal`.

A held claim extends one claim's life over several runs; it is not a transaction over those runs. Rimsky reports the aggregate outcome to the producer, and the producer decides what to do with its own state. Atomicity across resources belongs to a producer or to a compensating node, not to the handle.

See also: `claim`, `claim-producer`, `claim-co-holdership`, `claim-lifetime`, `claim-tree`, `node-run`, `supervisor`, `auto-terminal`, `orphan-reaper`, `inertness`, `asset`.
