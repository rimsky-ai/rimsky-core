# NodeRow derived-state removal — Design Sketch (follow-up to cascade-no-reinvalidation)

**Date:** 2026-06-20
**Status:** Sketch (not authorization to build; pulled out of the 2026-06-19 cascade-no-reinvalidation sweep as a separate workstream)

## Why this is its own sketch

The cascade-no-reinvalidation work landed sections A, B (modulo B1), and C of its punchlist. Section D — "Node-row surface refactor: no derived state, only summary" — landed only its additive piece (D4: `NodeRunSummary` on the operator response). The structural piece — D1/D2/D3/D5: strip derived columns from `NodeRow`, collapse the `nodeSelect` LATERAL JOIN, and rewrite the 6 runtime callers + 227 test sites that read `NodeRow.State` — was deferred. The cost is large (~140 runtime touch points + 227 test sites) and the upside is structural rather than user-visible. This sketch captures the work so it can ship as its own focused change.

## What stayed live and why it's a hazard

`code:lib/foundation/persistence/nodes.go::NodeRow` still carries `State`, `SettlingSignalType`, `AssignedSupervisorID`, `InFlightRunID`, `RunScopeID`. The postgres `nodeSelect` and the sqlite mirror compute these via a `LEFT JOIN LATERAL` over `rimsky_node_runs` with a three-key sort order: scope-priority first (main scope > sub-scopes), in-flight states first, most-recent `active_terminal_at` last. The synthesized "node state" is whatever the heuristic picks.

For nodes with one in-flight run the answer is correct. For fan-out nodes (parent and N children share `node_id`) or for nodes with cascade-pending + non-cascade-stale coexisting, the heuristic picks one run and calls it "the" state. Operators looking at `/nodes/{id}` see one fan-out child's state pretending to be the parent's. Runtime callers (`on_error.go`, `runner_error_policy.go`, `cascade_recalculate.go`) make policy decisions on the same synthesized field. The hazard is the dual source of truth the cascade-no-reinvalidation work was supposed to retire — the derived field can disagree with what the actual runs say, and there's no way to know without querying the runs directly.

`code:lib/control/controlapi/nodes.go::handleResetNode` gates reset eligibility on `row.State == NodeStateFailed`. If the heuristic picks a non-failed run while the operator-visible run is failed, reset silently refuses. The HTTP response now includes `run_summary` alongside `state`, so consumers can distinguish — but the dual surface is the failure mode, not the fix.

## What needs to happen

**D1 — Strip derived fields from `NodeRow`.** Drop `State`, `SettlingSignalType`, `AssignedSupervisorID`, `InFlightRunID`, `RunScopeID`. Keep `ID`, `InstanceID`, `NodeType`, `Executor`, `CurrentErrorClass`, `RetryCounter`, `ActionIndex`, `FrameID`, `Tags`, `CreatedAt`, `UpdatedAt`. Add `CascadeMode` (currently fetched via the separate `GetCascadeMode` call).

**D2 — Collapse `nodeSelect`.** Drop the LATERAL JOIN in postgres and sqlite; `nodeCols` selects only stored `rimsky_nodes` columns. The C7 scope-priority heuristic vanishes with the join.

**D3 — Rewrite runtime callers to read run state directly.** Six known call sites: `on_error.go#96` (retry-branch `cur.State` check), `on_error.go#211-226` (pass-branch state switch), `runner_error_policy.go::applyResolvedAction` retry/end guards, `runner_error_policy.go::applyTerminalInfraError` guard, `cascade_recalculate.go#64` (`target.State != NodeStateStale` early-exit). Each already has a specific run id (`acq.DispatchID`) or can fetch one via `Queue.GetInFlightRunForNode(node, scope)`. Read run state from `RunTree().GetByID(runID).State` (or `GetRunForGate` / a new `GetRunStateByID` helper). No code path consults `NodeRow.State` after this.

**D5 — Rewrite test helpers.** `test/support/scenario/harness.go::WaitForNodeState` polls `NodeRow.State` today. Replace with `WaitForRunState(runID, state, timeout)` for tests that have a specific run id and `WaitForLatestRunState(nodeID, scopeID, state, timeout)` for tests that want the most recent run in a scope. The 227 existing call sites either have a run id locally or can fetch one — most are simple substitutions; a handful (the "wait until this node's first cascade-driven run lands and settles to fresh" pattern) need genuine rethinking and may want a third helper.

**Operator surfaces.** `handleGetNode` drops the `state` field on `nodeResponse` once D1 lands (no NodeRow.State to forward). `RunSummary` already carries the categorical counts. `handleResetNode` gates reset on the most recent failed run for the node — concretely, on whether `GetFailedTerminalRunScopeID(nodeID)` returns a scope. The cascade-graph view in `lib/control/observability/cascade_graph.go` returns `NodeRunSummary` per node rather than synthesizing a single state.

**Migration risk.** None — pre-v1, no on-disk schema change; the field removals are Go-side only. Test-side risk is the 227 call sites; a mechanical sweep handles most. Real risk: the handful of tests that conflate "the node's state" with "what we expect the latest run to be in" — they were correct by accident because the LATERAL JOIN heuristic happened to pick the run they meant. Those need real expression of intent.

## Scope

- **In scope:** the four bullets above (D1, D2, D3, D5) plus the operator-surface cleanups that follow.
- **Out of scope:** changing the `NodeAttributes` table (lives separately from `rimsky_nodes`); changing the cascade walker / gate evaluator (already landed in cascade-no-reinvalidation); the deferred B1 (dispatch-time substitution removal) which has its own constraints.

## Remaining gap: non-cascade dispatch-time substitution

The cascade-no-reinvalidation B1 work landed for the cascade hot path: the gate evaluator now runs schema substitution (lenient on claim refs), persists the resolved bag via `SetDispatchInputBag(sealed=true)` and seeds the live bag, and the dispatcher's short-circuit loads that sealed bag and runs `fillClaimRefs` to fill in claim-referencing properties using acquired claims. `upsertAttributesPreDispatch` is retired.

The non-cascade tail (`operator_invalidate`, `policy_retry`, `infra_reenqueue` creation paths) still falls through to dispatch-time substitution via `resolveAttributes`. Those rows skip the gate evaluator entirely — they are created directly in state `stale` by the runtime callers in `on_error.go` / `runner_error_policy.go` / `debug_override.go`, and there is no pre-populated sealed bag for them. Dispatch substitutes from scratch.

Closing this remaining gap is a focused follow-up: each non-cascade creation site should pre-populate the new run's `NodeAttributes` (carry-forward from the prior run's bag) and `SetDispatchInputBag(sealed=true)`. After that the dispatch short-circuit covers every dispatch and `resolveAttributes`'s substitution fallback can be deleted entirely.
