# Post-message-schema deferred work: parallel frames, supersede semantics, operator queue manipulation

**Date:** 2026-06-14
**Type:** Sketch (deferred-work capture)
**Origin:** Items explicitly scoped out of `spec:2026-06-14-message-schema-layer-design` during the brainstorm that produced it. Captured here so they don't vanish.

## 1. Parallel frame execution per instance

**Today:** `concept:frame` enforces ≤1 running frame per instance. Frames queue serially in arrival order; the partial uniqueness index on running frames enforces it.

**What's deferred:** Lifting that invariant to allow N running frames concurrently on the same instance.

**Why it's its own design problem:**
- The per-frame substitution context and attribute reads/writes were designed against a single in-flight frame.
- Concurrent frames race on `col:rimsky_attributes.value`; last-writer-wins, merge semantics, ordering all need explicit design.
- The per-frame settled-this-frame guard in the cascade walker loses its "this instance settled" meaning.
- The partial uniqueness index on running frames goes away; replacement invariants need design.
- Cascade walker isolation across concurrent frames.

**Plausible trigger to revisit:** throughput pressure on serial-frame instances; multi-tenant workloads where one instance's frame queue depth becomes a bottleneck.

## 2. Per-message-type supersede / latest-wins semantics

**Today (post-spec):** all messages deliver one-per-frame, always. N rapid-fire messages of the same type produce N sequential frames.

**What's deferred:** a per-`messages:` entry policy (working name `supersede_pending: true`) — "if a message of this type lands while another of the same type is queued, replace the queued one." Preserves the latest-wins-for-repeated-identical-type-inputs pattern that today's retired `coalesce` mode handled, but as a per-type policy declared by the template author rather than an instance-level mode.

**Why it's its own design problem:**
- Per-type policy belongs on the schema entry, where the author actually knows the answer (some types must never supersede; some always should).
- Requires changes to the message-delivery path's queue-selection logic.
- Interacts with parallel-frame-execution if that lands too.

**Plausible trigger to revisit:** real workloads where the per-type recompute-from-latest pattern is load-bearing — dashboard refresh, attribute aggregation, throttled-update sensors.

## 3. Operator manipulation of the instance message queue

**Today (post-spec):** operators send messages via the universal `POST /instances/{id}/messages` endpoint. Messages persist to the ledger and deliver at the next frame boundary. There is no operator surface for cancelling, reordering, prioritizing, or otherwise mutating in-flight queued messages beyond the standard message-history read endpoints.

**What's deferred:**
- "Cancel an emitted-but-undelivered message" — generalize from the retired backfill-cancel capability.
- More general cancel — "cancel this queued frame," "cancel this in-flight run," "cancel this parked node-run." Each lifecycle state has different cancellation semantics and cleanup paths.
- Possibly: reorder, prioritize, requeue.

**Why it's its own design problem:**
- Cancellation semantics differ across lifecycle states (queued vs running vs parked vs held-by-claim).
- Cleanup paths (releasing claims, draining wait-set rows, audit handling) need careful design per state.
- Interacts with `concept:claim-handle`'s claimant-guarded release machinery.

**Plausible trigger to revisit:** operator workflows that need recovery from a stuck queue or mid-flight cancellation; backfill workflows that today rely on the retired `backfill:cancel` endpoint.

## Relation to each other

The three items are functionally independent and can land separately. One mild coupling: if parallel frame execution lands first, the supersede-pending design has to account for what "queued" means under concurrent execution; if it lands second, supersede-pending only has to deal with serial queueing. Operator-queue-manipulation is independent of both axes.
