# Cascade no-re-invalidation — Implementation Punchlist

Working punchlist for the sketch at `2026-06-19-cascade-no-reinvalidation-sketch.md`. Each item: location, what to do, why it matches the sketch, status. Items move to ✅ DONE as the implementation lands them.

---

## A. Held-subgraph cascade blocker (NEW DESIGN — section §`held` as state with subgraph-scoped cascade defer)

The simplest, most-robust model: `held` state expands to cover ALL participants of an active claim (acquirer + co-holders). Cascade from any held node is filtered to members of any subgraph the sender participates in. State transition `held → fresh/failed` per holder fires that holder's cascade to non-members. Triggered by `CheckAndFireResolution` walking all holders of a resolving claim.

### A1. Expand `held` semantics to cover co-holders
- **Where:** `lib/runtime/runner_terminal.go::applyTerminalComplete` (and the error/abandon counterparts).
- **What:** When a run's executor returns success/error AND the run has an active `claim_holders` row for any claim_handle in `active` state, transition `running → held` instead of `running → fresh / failed`. The "executor returned held=true" path (today's behavior) is the acquirer-specific case of this general rule.
- **Why:** Co-holders' outputs are provisional until the claim resolves; calling them held is honest. This is what makes the per-member transition + cascade work uniformly.
- **Status:** ⏳ OUTSTANDING

### A2. Generalize cascade walker filter to subgraph-membership union
- **Where:** `lib/runtime/runner_terminal.go::cascadeSubscribersStaleInTxWithVisited` and the `inheritorFilter` / `nonInheritorFilter` helpers in `lib/runtime/held_cascade_defer.go`.
- **What:** Replace the per-acquirer "is receiver an inheritor of THIS acquirer" filter with a per-sender "is receiver a member of ANY subgraph the sender participates in." Computed from `claim_holders.ListByHolderRunID(sender_run_id)` + `HoldingSubgraphsForTemplate(spec)`.
- **Why:** Handles overlapping claim sets correctly. A co-holder of multiple claims cascades to the union of those subgraphs' members.
- **Status:** ⏳ OUTSTANDING — current code filters only at the acquirer's held-terminal.

### A3. Per-holder state transition on claim resolution
- **Where:** `lib/runtime/auto_terminal.go::CheckAndFireResolution` and `lib/runtime/terminal_decision.go::ResolveClaimHandleTerminal`.
- **What:** When a claim resolves (commit or abandon), walk ALL rows in `claim_holders` for that claim_handle. For each holder run: check its OTHER `claim_holders` rows. If all of its claim_handles are now resolved, transition `held → fresh` (all committed) or `held → failed` (any abandoned — poison rule). Each transition fires the holder's own cascade.
- **Why:** This is the trigger that "picks up the cascade again" when a claim resolves. Overlapping claim sets handled because each holder evaluates its own complete claim portfolio.
- **Status:** ⏳ OUTSTANDING — today this function only acts on the acquirer's run.

### A4. Per-member deferred cascade at transition
- **Where:** `lib/runtime/held_cascade_defer.go::emitDeferredHeldCascade` (or a generalized replacement).
- **What:** When a held member transitions out of held, fire that member's own terminal cascade to non-members. Signal type: `terminal/success` (fresh) or `terminal/error/abandoned` (failed-via-poison). Filter: non-members of any subgraph the (just-transitioned) member participates in.
- **Why:** The sketch requires each member that executed to broadcast its own deferred cascade. Today only the acquirer's cascade fires at commit/abandon.
- **Status:** ⏳ OUTSTANDING — current code fires only A's cascade at commit (with a non-inheritor filter that doesn't generalize).

### A5. Gate-evaluator carveout generalized to subgraph co-membership
- **Where:** `lib/runtime/gate_evaluator.go::anySubscribedUpstreamInFlight`.
- **What:** When checking "any upstream in-flight" for receiver R, skip an upstream U if U is in `held` state AND U is a co-member with R of any held subgraph. Today's code checks the simpler "R has Holds.From=U.NodeType" — generalize to membership.
- **Why:** A held member must not gate another co-member's dispatch; they share the same in-flight transaction.
- **Status:** ⏳ PARTIAL — Holds.From check is in place; needs generalization to transitive co-membership.

---

## B. Sketch violations (architectural)

### B1. Dispatcher: drop `resolveAttributes` + `upsertAttributesPreDispatch` from claim path
- **Where:** `lib/runtime/runner.go#176-185` and `lib/runtime/runner_dispatch.go::resolveAttributes`.
- **What:** The dispatcher's hot path still calls `resolveAttributes` + `upsertAttributesPreDispatch`. The gate evaluator writes the bag with `sealed=false`; the dispatcher's `loadSealedDispatchBag` reads only `sealed=true`. Result: the gate-evaluator-built bag is used ONLY by idempotent-mode comparison; the actual dispatch bag is re-resolved fresh and overwrites the gate-eval bag with `sealed=true`.
- **Fix:** Drop `resolveAttributes` + `upsertAttributesPreDispatch` from the dispatch path. Replace with `loadBagByRunID` that reads the gate-evaluator-built bag (need to either drop the sealed/unsealed distinction or have the gate evaluator write sealed=true). The bag built at pending→stale IS the bag dispatched.
- **Why sketch requires:** "Every dispatch loads the persisted bag; no path resolves the bag at dispatch time." (Bag composition section + Gate evaluator section.)
- **Status:** ⏳ OUTSTANDING

### B2. JCS canonicalization for idempotent-mode comparison
- **Where:** `lib/runtime/gate_evaluator.go::canonicalEqual#252`.
- **What:** Uses raw `json.Marshal`. Sketch requires RFC 8785 JCS via the existing `lib/graph/template/canonical::CanonicalSpecHash` helper.
- **Fix:** Replace `json.Marshal` with JCS canonicalization. Reuse the existing helper.
- **Why sketch requires:** Idempotent-queue and idempotent-settled mode rules explicitly name JCS as the comparison algorithm.
- **Status:** ⏳ OUTSTANDING

### B3. Parked-sealed invariant
- **Where:** `lib/runtime/cascade_walker.go::ensureCascadePending#27-31` and `::wakeParkedReceiverIfPresentInTx#57-98`.
- **What:** When sender is message-virtual (senderNodeID is zero), the walker wakes parked receivers in place via `parked → stale (deadline_resume)`. This mutates an in-flight (parked) run.
- **Fix:** Delete the call to `wakeParkedReceiverIfPresentInTx` from the walker. Cascade should create a new pending and the parked run stays parked. Delete `wakeParkedReceiverIfPresentInTx` itself.
- **Why sketch requires:** Core invariant — parked is in-flight; in-flight runs are sealed; cascade creates a new pending.
- **Status:** ⏳ OUTSTANDING

### B4. Walker rule (a) reads drained-only senders
- **Where:** `lib/foundation/persistence/postgres/wait_set.go::ListSenderNodesForReceiver#117-122` (and sqlite mirror).
- **What:** Filter `AND w.drained_at IS NOT NULL` was added to work around `pullForceRefreshUpstreams` pre-seeding wait-set rows that would otherwise spuriously trigger rule (a)'s "create new pending" branch.
- **Fix:** Remove the drained-only filter. The honest alternative is to dedupe the natural-cascade `WaitSet().Insert` at `cascadeSubscribersStaleInTxWithVisited` against existing rows by `sender_run_id` so the pre-seed isn't double-counted. Rule (a) then reads per-sender-node as the sketch states ("does not already cover the sender's node" — no drained qualifier).
- **Why sketch requires:** "The accumulation gate is per-sender-node: a new cascade row accumulates into the latest pending iff that pending's wait-set does not already cover the sender's node."
- **Status:** ⏳ OUTSTANDING

---

## C. Dead code / workaround residue

### C1. `SweepReady` stub
- **Where:** `lib/runtime/conductor.go#118-120`.
- **What:** Function body is `return nil`. Stubbed during earlier debugging.
- **Fix:** Delete the function and all callers. Pre-v1 break-freely.
- **Status:** ⏳ OUTSTANDING

### C2. `ReasonHandlerError` unused
- **Where:** `lib/foundation/cascade/state.go#46`.
- **What:** Constant defined; only referenced in `state_test.go`. Not in `NextState` switch.
- **Fix:** Delete the constant and the test references.
- **Status:** ⏳ OUTSTANDING

### C3. Reason-input self-loops
- **Where:** `lib/foundation/cascade/state.go#79` (`stale + deadline_resume → stale`), `#115` (`held + handler_held → held`), `#126` (`parked + handler_park → parked`).
- **What:** Added during implementation to silence "illegal transition" errors when callers called `UpdateState` twice with the same reason. The sketch's transition table has no self-loops.
- **Fix:** Remove all three. Find the call sites that fire the redundant `UpdateState` and dedupe them.
- **Status:** ⏳ OUTSTANDING

### C4. `subgraph_internal_cascade_fired` self-loop
- **Where:** `lib/foundation/cascade/state.go#144-145`.
- **What:** `running → running` self-loop on `NextStateParent`. Workaround for the subgraph cascade firing UpdateState on an already-running parent.
- **Fix:** Remove. Find the call site and stop firing the redundant transition.
- **Status:** ⏳ OUTSTANDING

### C5. `RevertRunningToStaleIfOrphaned`
- **Where:** `lib/foundation/persistence/postgres/nodes.go::RevertRunningToStaleIfOrphaned` (and sqlite mirror), `lib/runtime/runner_acquire_postcommit.go::handleOrphanedClaim#39-50`.
- **What:** `ClaimDispatchRow` does the `stale → running` transition inline as part of the claim CAS. If `verifyBeforeRun` fails after commit, state is stuck at `running` with no claim. This primitive reverts it back.
- **Fix:** Split the claim CAS: claim ownership (set `claimed_by`) without state change; transition state to `running` only AFTER `verifyBeforeRun` succeeds. Delete `RevertRunningToStaleIfOrphaned`. Delete the noop-stub from `lib/runtime/runner_acquire_helpers_test.go#692` and `lib/control/controlapi/admin_diagnostics_test.go#129`.
- **Status:** ⏳ OUTSTANDING

### C6. Serialization-gate index carveout
- **Where:** `lib/foundation/persistence/postgres/migrations/001-initial.sql#312-315` (and sqlite mirror).
- **What:** Index condition is `WHERE claimed_by IS NOT NULL OR state IN ('held','parked')`. The two-leg dispatcher claim (C5) leaves a row briefly in `state='stale' AND claimed_by IS NOT NULL` between the claim leg and the promote-to-running leg; the gate must cover that window.
- **Resolution:** Achieved differently from the original narrowing target. The carveout is intentional and matches the two-leg claim's invariant (any row with a non-null claim blocks concurrent claims for the same node/scope, regardless of state). The sketch's narrower `WHERE state IN ('running','held','parked')` would race the dispatcher's claim sequence.
- **Status:** ⚠ ACHIEVED DIFFERENTLY — index covers `claimed_by IS NOT NULL OR state IN ('held','parked')` to encompass the two-leg claim window.

### C7. (superseded — see section D)
The `nodeSelect` LATERAL JOIN scope-priority heuristic at `lib/foundation/persistence/postgres/nodes.go#34-44` vanishes entirely with the NodeRow surface refactor (D below). The whole lateral join is deleted, not just the priority ordering.

### C8. `fanoutRecalculate` `IsFanOutNode` gate
- **Where:** `lib/runtime/runner_terminal.go::fanoutRecalculate#553-556`.
- **What:** Function was firing for all terminal handlers and creating duplicate runs for non-fanout nodes. Gated to fan-out-only as a workaround.
- **Audit result:** The function performs unique work beyond the cascade walker — it post-commit re-enqueues a NEW dispatch for fan-out receivers whose latest stale run has drained but not been claimed (the walker handles wait-set + pending creation only; it does not poke the dispatcher). The fan-out gate is correct: non-fanout subscribers are handled by the cascade walker's normal signal path. Keep the function; its `IsFanOutNode` gate is the correct domain check. Issue #9 (creation_reason=cascade default) is fixed separately by passing `CreationReasonInfraReenqueue`.
- **Status:** ✅ KEEP — fan-out-only post-commit re-enqueue, distinct from the cascade walker's in-tx wait-set work.

### C9. `ListInFlightRunPhases` legacy name
- **Where:** `lib/foundation/persistence/node_runs.go#96` (interface) and `lib/runtime/gate_evaluator.go#126` (call site).
- **What:** Returns `map[UUID][]string` of state values; named after the retired `phase` column.
- **Fix:** Rename to `ListInFlightRunStates`. Mechanical rename across interface + implementations + call sites.
- **Status:** ⏳ OUTSTANDING

---

## D. NodeRow surface refactor (sketch §"Node-row surface: no derived state, only summary")

`rimsky_nodes` stops carrying derived run state. The `nodeSelect` LATERAL JOIN goes away. Runtime callers read run state directly by run id. Operator-facing surfaces return categorical run counts via `NodeRunSummary`.

### D1. Strip derived fields from `NodeRow`
- **Where:** `lib/foundation/persistence/nodes.go::NodeRow`.
- **What:** Remove `State`, `SettlingSignalType`, `AssignedSupervisorID`, `InFlightRunID`, `RunScopeID`. Keep `ID`, `InstanceID`, `NodeType`, `Executor`, `CurrentErrorClass`, `RetryCounter`, `ActionIndex`, `FrameID`, `Tags`, `CreatedAt`, `UpdatedAt`. Add `CascadeMode` (currently only exposed via separate `GetCascadeMode` lookup).
- **Status:** ⏳ OUTSTANDING

### D2. Collapse `nodeSelect` to plain `rimsky_nodes` SELECT
- **Where:** `lib/foundation/persistence/postgres/nodes.go::nodeSelect` + `nodeCols` (and sqlite mirror at `lib/foundation/persistence/sqlite/nodes.go`).
- **What:** Drop the LATERAL JOIN to `rimsky_node_runs` + `rimsky_run_scopes`. `nodeCols` selects only stored columns from `rimsky_nodes`. The scope-priority heuristic from C7 vanishes with the join.
- **Status:** ⏳ OUTSTANDING

### D3. Rewrite runtime callers to read run state directly
- **Where:** Six call sites:
  - `lib/runtime/on_error.go#96` (`cur.State == NodeStateRunning` in retry branch)
  - `lib/runtime/on_error.go#211-226` (`switch cur.State` in pass branch)
  - `lib/runtime/runner_error_policy.go#143` (DispositionRetry guard)
  - `lib/runtime/runner_error_policy.go#192` (DispositionEnd guard)
  - `lib/runtime/runner_error_policy.go#250` (`applyTerminalInfraError` guard)
  - `lib/runtime/cascade_recalculate.go#64` (`target.State != NodeStateStale` early-exit)
- **What:** Each caller already has a specific run id (`acq.DispatchID`) or can fetch one via `Queue.GetInFlightRunForNode(node, scope)`. Read run state from `RunTree().GetByID(runID).State` (or equivalent). No code path consults `NodeRow.State`.
- **Status:** ⏳ OUTSTANDING

### D4. `NodeRunSummary` view for operator-facing surfaces
- **Where:** New persistence method (e.g. `Nodes().GetRunSummary(nodeID)` returning a `NodeRunSummary` struct), wired into:
  - `lib/control/controlapi/nodes.go::toNodeResponse` (HTTP `/nodes/{id}`)
  - `lib/control/observability/cascade_graph.go` (cascade graph view)
- **What:** `NodeRunSummary` = categorical counts: `ActiveCount` (running ∪ held ∪ parked), `PendingCount` (pending ∪ stale), `FreshCount`, `FailedCount`. HTTP response drops `state` string, gains the summary. Cascade graph view returns the summary per node.
- **Status:** ⏳ OUTSTANDING

### D5. Test helper rewrite: `WaitForNodeState` and similar
- **Where:** `test/support/scenario/harness.go::WaitForNodeState` (and any sibling helpers polling derived node state).
- **What:** Delete `WaitForNodeState`. Add `WaitForRunState(runID, state, timeout)` for tests that have a specific run id, and `WaitForLatestRunState(nodeID, scopeID, state, timeout)` for tests that want the most recent run in a given scope. Each existing call site picks one of the new helpers based on what it actually wants.
- **Scope:** Mechanical but voluminous — every scenario test that polls node state. Pre-identifiable by grep on `WaitForNodeState`.
- **Status:** ⏳ OUTSTANDING
