---
audit: attribute-carry-forward
artifact: story:attribute-carry-forward
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:46Z
---

# Stateful nodes see their own prior writeback on the next same-RunScope dispatch, and start fresh in a new RunScope

The story is supported. The gate evaluator (`lib/runtime/gate_evaluator.go`, `loadReceiverCarryForward`) hydrates a receiver's bag from the immediately-prior node-run of the same node scoped by `run_scope_id`, and the non-cascade insert paths do the same via a shared `SnapshotBagForNewRun` routine (postgres and sqlite backends). A same-RunScope self-edge loop test (`TestSelfEdgeIntraFrameLoop_ReadOnlyAttributeCarriesForwardAcrossDispatches`) drives three dispatches of one node inside one frame and asserts each dispatch observes the readOnly `session_marker`/`turn` values the prior dispatch wrote via `attributes_delta`, with the loop's three runs sharing one run_scope and one frame — the intra-frame, executor-written-carry-forward claim. A companion end-to-end test (`TestAttributeCarryForwardWithinRunScopeThenSubgraphSeesSchemaDefault`) drives a loop-counter across three same-scope dispatches (count 1→2→3 via writeback plus carry-forward) and then crosses into a sub-graph RunScope, asserting the sub-graph's counter's first dispatch reads the schema default (0) rather than the parent scope's carried count=3 — the cross-RunScope-resets-to-default claim for the sub-graph-invocation case. Because both the cascade-gate hydration path and the non-cascade snapshot path key the prior-run lookup on `run_scope_id`, the same reset-to-default behavior applies uniformly to any new RunScope (fan-out partition included), not only the sub-graph case exercised directly.
