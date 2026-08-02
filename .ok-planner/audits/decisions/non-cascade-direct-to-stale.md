---
audit: non-cascade-direct-to-stale
artifact: decision:non-cascade-direct-to-stale
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:46Z
---

# Operator-invalidate, recalculate, and message-delivery insert node-runs directly as `stale`, bypassing `pending`

Supported. All three named paths route through code that inserts `state = 'stale'` directly with a fresh `sequence` (`MAX(sequence)+1` scoped to `(node_id, run_scope_id)`), never `pending`: operator-invalidate and message-delivery call the shared `Nodes().CreateNonCascadeStale` (postgres and sqlite implementations checked), and fanout-parent recalculate (`lib/runtime/cascade_recalculate.go::RecalculateNode`) calls `Queue.Enqueue`, whose insert statement is identically `state='stale'`. Both writers snapshot the run's input bag at creation via the same `SnapshotBagForNewRun` routine, which carries forward the immediately-prior in-scope run for the two intra-frame paths (operator-invalidate, recalculate) and starts empty for a fresh frame; message-delivery (`lib/runtime/message_delivery.go::deliverNamedMessageInTx`) then overwrites that bag with the message envelope's payload verbatim via `NodeAttributes().Upsert`. By contrast, the cascade walker's own pending-row insert (`lib/foundation/persistence/postgres/nodes.go`) is the sole place `state = 'pending'` is written, always with `creation_reason = 'cascade'`, and mode-rule queries (`HasLaterCascadePending`, `DeletePriorCascadeStales`, `GetPriorCascadeQueuedNotClaimed`) filter on `creation_reason = 'cascade'`, so non-cascade stales are structurally excluded from accumulation, most-recent deletion, and idempotent dedup. The claim/dispatch selection query (`SelectCandidates`) enforces at most one claimed/held/parked run per `(node_id, run_scope_id)` regardless of `creation_reason`, so cascade and non-cascade stales share one serialization gate. A dedicated conformance test (`testCreateNonCascadeStaleCarriesForward`, run against both backends) asserts the carried-forward attribute data and the snapshotted dispatch-input bag.
