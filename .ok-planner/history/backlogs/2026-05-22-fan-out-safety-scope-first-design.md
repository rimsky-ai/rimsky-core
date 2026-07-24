# Fan-out + Sub-graph Safety: RunScope as First-Class Data Model

**Date:** 2026-05-22
**Topic:** Introduce `RunScope` as a first-class entity for execution contexts (main graph / sub-graph / fan-out partition); rename the existing `Scope` concept (claim-identity bytes) to `ClaimScope` to disambiguate; complete fan-out + sub-graph safety; close the bug class surfaced by recent audits.

---

## Goal

Complete rimsky's fan-out and sub-graph dispatch safety by replacing the current `(parent_run_id, child_key)` inline-disambiguation model with a first-class `rimsky_run_scopes` table that uniformly represents execution contexts across main / sub-graph / fan-out partition cases. The reshape eliminates a bug class (lazy-allocation drift, by-node SELECT ambiguity, missing-parent-context in re-enqueue paths) at the data-model level rather than via incremental patches. Adopting RunScope-first also enables future capabilities — depth gating, agentic-executor recovery handoff — by giving the platform a structured representation of its execution stack.

The spec also renames the existing `concept:scope` (claim-identity bytes returned by `ClaimProducer.Open`) to `concept:claim-scope`. The two names now have qualifying prefixes: `RunScope` for execution contexts, `ClaimScope` for claim-identity bytes. Bare `Scope` becomes a parking-lot word, never used standalone in code or prose.

`RunSheet` is adopted as a prose-only noun for "the conceptual list of all in-flight `rimsky_node_runs` across all RunScopes." Not a database entity; useful for operator-facing language about "what's running now."

## Background

Recent work on the attribute-overrides matcher overlay surfaced three pieces of unfinished platform work in rimsky:

1. **Fan-out infinite recursion** in `code:runtime/runner_acquire_helpers.go::acquireFanOutIfDeclared` (no parent-run-id guard; children re-fired SplitScope ad infinitum). Fixed with a one-line guard in a prior cleanup cycle.
2. **Sub-graph entry absorption** was half-built (canonicalizer set a marker but never merged entry's executor/stores/holds onto the calling node). Completed in a prior cleanup cycle.
3. **Persistence per-run ambiguity** in numerous methods using `WHERE node_id = $1 AND phase IN (...)` patterns that return arbitrary rows when multiple in-flight fan-out children share a `node_id`. Multiple cleanup cycles addressed individual methods, but each cycle surfaced more instances. A comprehensive six-audit sweep enumerated the complete pattern landscape, revealing that the data model itself (parent_run_id + child_key as inline-nullable fields, with two partial-unique indexes, and ad-hoc disambiguator parameters threaded through 30+ callsites) accumulated incidental complexity by treating fan-out as a special case bolted onto an originally-non-fan-out node_run shape.

The audit findings cluster into two categories:

- **Bugs that disappear under a different data model.** Roughly half of the audited findings are manifestations of the disambiguation shape — adopting RunScope-first retires them at the data-model level (no inline disambiguator → no drift, no hardcoded-nil escape, no missing parent-context plumbing).
- **One-off bugs independent of the data model.** Roughly the other half are SQL drift, state-machine atomicity, etc. — they need fixing regardless and are enumerated below.

Test coverage analysis revealed that the existing fan-out and sub-graph tests largely exercise pieces in isolation (semaphores, aggregation functions, plan generators, directly-seeded forensics) rather than the full dispatch lifecycle. The matcher-overlay scenario was the first end-to-end test to drive fan-out children through `transitionToRunning` — exposing the entire bug class for the first time.

## Architecture

### RunScope-as-first-class

An execution context — the runtime instantiation of a graph — becomes a first-class entity with its own table, identity, and lifecycle. A **RunScope** owns a set of `rimsky_node_runs` rows for the graph it instantiates. Three RunScope kinds exist, distinguished by their parent relationships:

- **Main RunScope:** the top-level graph instantiation. One per instance. No parent RunScope, no parent run.
- **Sub-graph RunScope:** a sub-graph (declared in the template's `graphs:` block) invoked via a calling node's `delegate:`. Parent RunScope = the calling node's RunScope; parent run = the calling node's run.
- **Fan-out partition RunScope:** one per partition emitted by a fan-out node's `SplitScope`. Parent RunScope = the fan-out node's RunScope; parent run = the fan-out node's run; carries a non-empty `partition_key`.

RunScopes form a tree via `parent_run_scope_id`. Walking the tree from a leaf to the root recovers the full execution stack — useful for depth gating (complementing the canonicalizer's static `subgraph_recursion_unsupported` rejection per `code:.ok-planner/design/concepts/sub-graph.md` with a runtime safety net), parent aggregation, and operator forensics.

Frames and RunScopes are orthogonal: a single cascade frame can span multiple RunScopes (when cascade propagation crosses RunScope boundaries at sub-graph entry-success or fan-out parent settlement); a single RunScope can host multiple frames (when the RunScope's graph fires repeatedly across multiple cascade resolutions).

### What the model retires

- Inline `(parent_run_id, child_key)` on `rimsky_node_runs` → replaced by `run_scope_id` non-null FK to `rimsky_run_scopes`.
- Two partial-unique indexes (`uq_node_runs_in_flight_per_root_node`, `uq_node_runs_in_flight_per_child`) → collapsed to one: `UNIQUE (node_id, run_scope_id) WHERE phase IN ('pending','active','held','parked')`.
- `runID *shared.UUID` disambiguator parameters threaded through nine persistence methods → unnecessary; methods key on `(node_id, run_scope_id)` instead.
- `ParentRunID *shared.UUID` + `ChildKey string` fields on `code:foundation/persistence.DispatchRequest` (the Go struct in `code:foundation/persistence/node_runs.go`) → replaced by `RunScopeID`.
- The "lazy allocation via `MarkStaleForCascade` insert path" with its associated snapshot-drift bug class → replaced by an explicit `AffirmNodeRunRow` primitive whose contract guarantees the lazy↔eager allocation rewrite is a no-op.

### What RunScope-first does NOT change

- Eager vs. lazy allocation of `rimsky_node_runs` rows is independent of the RunScope-first model. This spec adopts lazy allocation (per RunScope) with an explicit `AffirmNodeRunRow` primitive. Switching to eager allocation later is a single-day refactor (delete all `AffirmNodeRunRow` calls; allocate at RunScope creation).
- The cascade engine's semantics — subscription edges, hard-deps, wait-set drain — stay the same; the walker just carries `run_scope_id` through edges instead of resolving rows by `(node_id, frame_id)`.
- The claim engine — `rimsky_claim_handles`, `concept:claim-tree`, `concept:claim-producer`, `concept:auto-terminal` — stays the same in shape. ClaimScope semantics (the renamed `concept:scope`) stay unchanged in meaning; only the name changes. The `concept:claim-tree` and the RunScope-tree are parallel structures: the spec updates `concept:claim-tree`'s cross-reference to reflect the new RunScope-based run-tree shape.

### What this spec also changes (independent from RunScope-first)

- **`Scope` → `ClaimScope` rename.** The existing `concept:scope` (claim-identity bytes per `code:.ok-planner/design/concepts/scope.md`) is renamed to `concept:claim-scope`. The column `col:rimsky_claim_handles.scope_data` becomes `claim_scope_data`. The function `code:foundation/locks/conflict.go::ScopesByteEqual` becomes `ClaimScopesByteEqual`. The substitution directive `{{claim.<alias>.scope}}` becomes `{{claim.<alias>.claim_scope}}`. Pre-v1 break-freely covers the migration.
- **Recovery-aware executor protocol** (`prior_dispatch_id` + `prior_dispatch_disposition` on `ExecuteRequest`; `ack_status` + `current_dispatch_id` on callback ack). This is a parallel feature batched into this spec because the implementation work touches the same callback path. It does not depend on RunScope-first; the dispatch IDs it carries are run-identity, not RunScope-identity. Bundled for delivery efficiency, not architectural coupling.
- **Park terminal proto change** (closed `ParkReason` enum). Parallel feature; closes a separate proto-grammar gap.

## Schema

### `rimsky_run_scopes` (new table)

```sql
CREATE TABLE rimsky_run_scopes (
  id                  UUID PRIMARY KEY,
  parent_run_scope_id UUID NULL REFERENCES rimsky_run_scopes(id),
  parent_run_id       UUID NULL REFERENCES rimsky_node_runs(id),
  graph_name          TEXT NOT NULL,
  partition_key       TEXT NOT NULL DEFAULT '',
  instance_id         UUID NOT NULL REFERENCES rimsky_instances(id),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  closed_at           TIMESTAMPTZ NULL,

  CONSTRAINT run_scope_main_has_no_parents CHECK (
    (parent_run_scope_id IS NULL AND parent_run_id IS NULL)
    OR
    (parent_run_scope_id IS NOT NULL AND parent_run_id IS NOT NULL)
  )
);

-- Uniqueness: at most one open fan-out partition RunScope per (parent_run_id, partition_key).
CREATE UNIQUE INDEX uq_run_scopes_fanout_partition_open
  ON rimsky_run_scopes (parent_run_id, partition_key)
  WHERE partition_key != '' AND closed_at IS NULL;

-- Tree-walk index: parent chain navigation for depth-gating and aggregation.
CREATE INDEX idx_run_scopes_parent_chain ON rimsky_run_scopes (parent_run_scope_id);
```

A `kind` column is **not** stored — kind is derivable from the other columns:
- `parent_run_scope_id IS NULL` → main
- `partition_key != ''` → fanout_partition
- otherwise → subgraph

### `rimsky_node_runs` (modified)

The columns `parent_run_id` and `child_key` are removed. A new column `run_scope_id UUID NOT NULL REFERENCES rimsky_run_scopes(id)` replaces them. The two partial-unique indexes collapse to one:

```sql
ALTER TABLE rimsky_node_runs DROP COLUMN parent_run_id;
ALTER TABLE rimsky_node_runs DROP COLUMN child_key;
ALTER TABLE rimsky_node_runs ADD COLUMN run_scope_id UUID NOT NULL REFERENCES rimsky_run_scopes(id);

DROP INDEX uq_node_runs_in_flight_per_root_node;
DROP INDEX uq_node_runs_in_flight_per_child;

CREATE UNIQUE INDEX uq_node_runs_in_flight_per_run_scope
  ON rimsky_node_runs (node_id, run_scope_id)
  WHERE phase IN ('pending', 'active', 'held', 'parked');
```

Pre-v1 break-freely (per `code:submodules/rimsky/.claude/rules/rules.md`) covers the migration approach: drop and recreate rather than write compat-shim migrations.

### `code:foundation/persistence.NodeRow` projection

The `NodeRow` Go struct (in `code:foundation/persistence/nodes.go`) gains a `RunScopeID shared.UUID` field (non-nullable) and loses the now-removed `parent_run_id` / `child_key` projection. The `InFlightRunID *shared.UUID` projection (added in a prior cleanup cycle) stays.

### `code:foundation/persistence.DispatchRequest`

A Go struct in `code:foundation/persistence/node_runs.go`. Loses `ParentRunID *shared.UUID` + `ChildKey string`. Gains `RunScopeID shared.UUID` (non-nullable). The `code:foundation/persistence.Queue.EnqueueInTx` NOT EXISTS guard simplifies from the current two-branch (root vs. child) to one branch:

```sql
INSERT INTO rimsky_node_runs (...)
WHERE NOT EXISTS (
  SELECT 1 FROM rimsky_node_runs
   WHERE node_id = $1
     AND run_scope_id = $2
     AND phase IN ('pending','active','held','parked')
)
```

### `code:foundation/persistence.RunTreeTable` (reshape)

The existing run-tree accessor at `code:foundation/persistence/run_tree.go::RunTreeTable` is built around `(parent_run_id, child_key)` inline on `rimsky_node_runs`. Under RunScope-first, the tree shape moves to `rimsky_run_scopes` (`parent_run_scope_id` chain); the table's surface reshapes:

**`code:foundation/persistence.RunTreeRow`**:
```go
type RunTreeRow struct {
    RunID             shared.UUID
    NodeID            shared.UUID
    FrameID           shared.UUID
    RunScopeID        shared.UUID  // replaces ParentRunID *shared.UUID + ChildKey string
    State             cascade.NodeState
    LastOutcome       cascade.LastOutcome
    AggregationPolicy spec.AggregationPolicy
}
```

**`code:foundation/persistence.CreateRootRunInput`**: stays largely the same, but adds `RunScopeID shared.UUID` (the main RunScope's id; non-nullable). The "root" framing remains accurate: a root run is the first run in its RunScope, with no preceding ancestor run in that scope.

**`code:foundation/persistence.CreateChildRunInput`**: replaces `ParentRunID shared.UUID` + `ChildKey string` with `RunScopeID shared.UUID` (the new sub-graph or fanout_partition RunScope's id). Idempotency now keys on `(node_id, run_scope_id)` rather than `(parent_run_id, child_key)`. Re-creates return nil; the existing run is reachable via `GetInFlightRunForNode(node_id, run_scope_id)`.

**Method reshapes**:

- `CreateRootRun(ctx, tx, in)` — unchanged signature; semantics depend on the new `RunScopeID` field on the input.
- `CreateChildRun(ctx, tx, in)` — unchanged signature; semantics depend on the new `RunScopeID` field on the input.
- `GetByID(ctx, tx, runID)` — unchanged. Still returns a single run by id.
- `GetByParentChildKey(ctx, tx, parentRunID, childKey)` — **removed**. The (parent_run_id, child_key) lookup is replaced by `Queue.GetInFlightRunForNode(node_id, run_scope_id)` — callers that previously wanted "the in-flight child for this partition" now look up the partition RunScope first (via `RunScopeTable.GetFanoutPartition(parentRunID, partitionKey)`), then read the in-flight run within it. Removal is structural — callers must rewrite to the two-step pattern.
- `LockTreeForUpdate(ctx, tx, runID)` — unchanged. Still SELECTs the row by id for the state-propagation tx.
- `ListChildren(ctx, tx, parentRunID)` — **reshaped**. Semantics become "list all in-flight runs in any RunScope whose `parent_run_id = parentRunID`" — i.e., walk the immediate child RunScopes (sub-graph + fanout_partition) of the parent run and gather their runs. Implementation: JOIN `rimsky_run_scopes` ON `parent_run_id = $1` with `rimsky_node_runs` ON `run_scope_id`. Callers (aggregation engine, lineage walks) get the same logical result as before.
- `UpdateStateAndOutcome(ctx, tx, runID, state, lastOutcome)` — unchanged.
- `UpdateAggregationPolicy(ctx, tx, runID, policy)` — unchanged. AggregationPolicy still lives on the parent run row (the calling node's run for sub-graph; the fan-out parent's run for partitions).

**A new sibling table `code:foundation/persistence.RunScopeTable`** exposes RunScope CRUD:

```go
type RunScopeRow struct {
    ID              shared.UUID
    ParentRunScopeID *shared.UUID
    ParentRunID     *shared.UUID
    GraphName       string
    PartitionKey    string
    InstanceID      shared.UUID
    CreatedAt       time.Time
    ClosedAt        *time.Time
}

type RunScopeTable interface {
    // Insert a new RunScope. Caller chooses the id (so the same tx can
    // INSERT the RunScope and INSERT its first node_runs row referring to it).
    Create(ctx context.Context, tx Tx, row RunScopeRow) error
    // GetByID returns the RunScope row by id, or nil if missing.
    GetByID(ctx context.Context, tx Tx, id shared.UUID) (*RunScopeRow, error)
    // GetFanoutPartition returns the fanout_partition RunScope for
    // (parentRunID, partitionKey), or nil if none.
    GetFanoutPartition(ctx context.Context, tx Tx, parentRunID shared.UUID, partitionKey string) (*RunScopeRow, error)
    // Close stamps closed_at on the RunScope. Called by carry-rule / aggregation walks.
    Close(ctx context.Context, tx Tx, id shared.UUID) error
    // ListChildScopes returns immediate child RunScopes for a parent run.
    ListChildScopes(ctx context.Context, tx Tx, parentRunID shared.UUID) ([]RunScopeRow, error)
    // ListParentChain walks up via parent_run_scope_id; returns from leaf
    // to root inclusive. Used by depth-gating and forensics.
    ListParentChain(ctx context.Context, tx Tx, id shared.UUID) ([]RunScopeRow, error)
}
```

Postgres + SQLite implementations live at `code:foundation/persistence/postgres/run_scopes.go` and `code:foundation/persistence/sqlite/run_scopes.go`.

**Callers of `RunTreeTable` to update** (verified during implementation):

- `code:runtime/run_tree.go::CreateChildRun` (the runtime wrapper) — accepts `RunScopeID` from the caller; passes through.
- `code:runtime/state_propagation.go::walkUpwards` — walks via `RunTreeRow.RunScopeID → RunScopeRow.ParentRunID` instead of `RunTreeRow.ParentRunID`. The two-hop walk replaces the one-hop walk; behavior is equivalent.
- `code:runtime/state_propagation.go::ListChildren` (and its callers) — uses the reshaped `RunTreeTable.ListChildren` which walks via child RunScopes.
- `code:runtime/fanout_dispatch.go::CreateFanOutChildren` — creates fanout_partition RunScope per child, then creates the child run within it (two-step: `RunScopeTable.Create` + `RunTreeTable.CreateChildRun`).
- `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller` and callers — creates sub-graph RunScope, then internal cascade allocates runs lazily via `AffirmNodeRunRow`.

## Lifecycle

### RunScope creation (eager)

RunScope rows are inserted in the same transaction as the operation that triggers them. Each kind has a specific trigger:

- **Main RunScope:** created at instance creation (`POST /instances` handler), in the same tx as the instance row insert. The instance's `template_hash` determines the main graph; the main RunScope's `graph_name` is the reserved name `main` (per `code:.ok-planner/design/concepts/graph.md`).
- **Sub-graph RunScope:** created at the calling node's success terminal, atomic with the internal cascade firing. The entry-absorbed run lives in the parent RunScope (the calling node IS the entry per `concept:delegation`); when the calling node's `applyTerminalComplete` resolves to "sub-graph caller success" and routes to `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller`, the sub-graph RunScope row is inserted in the same tx as `code:runtime/subgraph_dispatch.go::SubgraphParentSuccessCascade` fires. `parent_run_scope_id = calling_node_run.run_scope_id`, `parent_run_id = calling_node_run.id`, `graph_name = node.Delegate`.
- **Fan-out partition RunScope:** created during fan-out child planning in `code:runtime/fanout_dispatch.go::CreateFanOutChildren`, atomic with the child `rimsky_node_runs` insert. `partition_key = SubClaimScopeDescriptor.partition_key`, `graph_name = parent_node's_graph_name`. (Earlier drafts of this spec placed creation in `code:runtime/runner_subclaim.go::AcquireSubClaims`, alongside the sub-claim handle inserts. The implemented shape keeps RunScope creation on the run-scope-tree side — `CreateFanOutChildren` is the sole emission path for fan-out child run rows — rather than collocating it with the claim-tree-side handle inserts. The two trees are parallel structures; this factoring keeps each owned by one function.)

Eager creation aligns with `@blessed-invariant 10` (atomic sub-claim acquisition) and gives the RunScope row a foreign-key existence guarantee for any subsequent operation. `AffirmNodeRunRow` can assume `run_scope_id` resolves; async-callback paths can assume RunScope lookup succeeds.

### RunScope closure

`closed_at IS NOT NULL` indicates the RunScope's parent-run rendezvous has fired. Per kind:

- **Sub-graph RunScope:** closed when the exit node terminates and `CarryExitWriteback` (per `concept:delegation`'s exit carry-rule) fires. The carry-rule's tx UPDATEs `rimsky_run_scopes SET closed_at = NOW() WHERE id = $1`.
- **Fan-out partition RunScope:** closed when the partition's outcome is aggregated into the parent's `code:runtime.Aggregate` decision (per `concept:fan-out`'s parent-terminal-rendezvous and the recursive auto-terminal walk in `code:runtime/auto_terminal.go::resolveParentClaimChain`). The aggregation walker's tx closes the partition RunScope.
- **Main RunScope:** closed when the instance terminates (no in-flight runs remain; instance is forensic-only). Operator-driven instance termination is the trigger.

### What closure means

- `AffirmNodeRunRow` on a closed RunScope returns `ErrRunScopeClosed`.
- Cascade walker reaching INTO a closed RunScope is a bug; the walker shouldn't cross into closed RunScopes. Defensive check in the walker.
- Aggregation reads a closed RunScope as final.
- Async-callback for a run in a closed RunScope still receives the callback (callbacks are per-run, not per-RunScope); the callback handler applies the determinism rule (below), which naturally rejects callbacks for runs whose phase is non-{active, held} — typical for runs in closed RunScopes.

## Persistence primitives

### `AffirmNodeRunRow`

```go
// AffirmNodeRunRow ensures an in-flight rimsky_node_runs row exists for
// (nodeID, runScopeID). If no in-flight row exists, INSERTs a pending row;
// if one exists, no-op. Returns only error.
//
// Callers MUST NOT depend on this method's return shape beyond
// error/no-error. The architectural property is that lazy↔eager
// allocation is a no-op rewrite: every AffirmNodeRunRow call could be
// deleted with no other code change if the system switched to eager
// allocation at RunScope creation time. Callers that need the row's id
// or state perform a separate read after the affirm.
//
// Errors:
//   - ErrRunScopeClosed: the RunScope's closed_at is set.
//   - underlying database errors: propagated.
//
// @concept: run-scope (the lazy-allocation primitive)
AffirmNodeRunRow(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx Tx) error
```

Implementation: `INSERT INTO rimsky_node_runs (...)` with `WHERE NOT EXISTS (...)` keyed on `(node_id, run_scope_id, in-flight phases)`. Postgres and SQLite both express this as a single statement. On `run_scope_id` pointing at a closed RunScope, returns `ErrRunScopeClosed` (a new error sentinel in `code:foundation/persistence`, alongside the existing `ErrRunRowMissing` per `code:foundation/persistence/node_runs.go`).

Caller pattern (affirm-then-read):

```go
if err := tx.Nodes().AffirmNodeRunRow(ctx, nodeID, runScopeID, tx); err != nil { ... }
row, err := tx.Nodes().GetInFlightRunForNode(ctx, nodeID, runScopeID, tx)
// use row.ID
```

Under eager allocation (future): delete the `AffirmNodeRunRow` call; the read still works because the row is already there from RunScope creation. No other change.

### `code:foundation/persistence.Queue.GetInFlightRunForNode` (reshape)

Already exists. Signature changes from `(nodeID, frameID, disambiguator *shared.UUID)` to `(nodeID, runScopeID)`. The disambiguator parameter is removed; the SELECT keys on `(node_id, run_scope_id, in-flight phases)` — unambiguous by the new unique index.

### Other persistence methods (reshape)

All methods that currently accept a `runID *shared.UUID` disambiguator OR a `parent_run_id` / `child_key` parameter reshape to accept `runScopeID shared.UUID` instead:

- `code:foundation/persistence.NodeTable.UpdateState(... runScopeID, ...)` — replaces `runID` disambiguator
- `code:foundation/persistence.NodeTable.UpdateHeartbeat(... runScopeID, ...)` — replaces `runID` disambiguator
- `code:foundation/persistence.NodeTable.ClearLastOutcome(... runScopeID, ...)` — replaces `runID` disambiguator
- `code:foundation/persistence.NodeTable.ClearSupervisorAssignment(... runScopeID, ...)` — replaces `runID` disambiguator
- `code:foundation/persistence.NodeTable.ResetFailedTerminalLastOutcome(... runScopeID, ...)` — gains the disambiguator it was missing
- `code:foundation/persistence.Queue.GetParkedByNode(... runScopeID, ...)` — replaces `runID` disambiguator
- `code:foundation/persistence.Queue.RemoveForNodeInTx(... runScopeID, ...)` — replaces `runID` disambiguator
- `code:foundation/persistence.Queue.EnqueueInTx(... DispatchRequest{RunScopeID, ...})` — replaces `ParentRunID` + `ChildKey` on the struct
- `code:foundation/persistence.Queue.GetInFlightRunForNode(... runScopeID)` — replaces disambiguator
- `code:foundation/persistence.Queue.SetRetryNoProgressForNodeInTx(... runScopeID, ...)` — replaces predicate-based scoping

The legacy `runID` parameter is removed everywhere. Callers that previously passed `nil` (admin paths, operator resets) now pass a real `runScopeID` — which they have, since every operation occurs in a RunScope.

`code:foundation/persistence.NodeAttributes.GetLatestByNode` similarly accepts `runScopeID` to disambiguate which run's attributes (forensic by-node lookup under fan-out picks the right partition's attributes).

## Cascade walker reshape

The cascade walker (`code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx` and its hard-dep sibling `code:runtime/runner_terminal.go::pullHardDepUpstreams`) currently reads an "initial work-set" via `code:foundation/persistence.NodeTable.ListByInstance`, walks subscription edges, and mutates as it goes — leading to the snapshot-drift bug class.

Reshape:

1. The walker still starts with an initial work-set read (`ListByInstance` returning current `NodeRow`s, each with `run_scope_id` and `InFlightRunID` projected).
2. For each subscription edge from the sender, compute the **target RunScope** for the receiver:
   - **Same-RunScope cascade** (the common case): receiver inherits sender's RunScope. Most cascade propagation is within a single RunScope (main-graph to main-graph, sub-graph internal to sub-graph internal).
   - **Cross-RunScope cascade**: occurs at sub-graph entry-success (entry-absorbed run cascades into the sub-graph internal nodes — target RunScope is the sub-graph RunScope, just created in `applyTerminalCompleteSubgraphCaller`'s tx) and at fan-out parent settlement (parent's downstream subscribers are in the parent's RunScope; partition-internal cascades don't propagate outward).
3. Call `AffirmNodeRunRow(receiver_node_id, target_run_scope_id)` — ensures the receiver row exists in the target RunScope.
4. Call `GetInFlightRunForNode(receiver_node_id, target_run_scope_id)` — reads the row's id and current state.
5. Call the state mutator (`MarkStaleForCascade`, transitioning the row's state to `stale`) keyed by the row's id (now known).
6. Install the wait-set row keyed by `(sender_run_id, receiver_run_id, frame_id)`.

The mutation happens after the read; the read returns the actual row id (no snapshot drift). `code:foundation/persistence.NodeTable.MarkStaleForCascade` simplifies dramatically — its insert path goes away (AffirmNodeRunRow owns allocation); it becomes a pure UPDATE keyed by `run_id`.

Parked receivers: if the read returns `phase == "parked"`, the walker chains into `code:runtime/wake_parked.go::wakeParkedReceiverInTx` explicitly. The wake operation is the cascade walker's policy, not AffirmNodeRunRow's responsibility — `AffirmNodeRunRow` stays narrow per its no-return-value contract.

## Callback determinism

A callback for a run is honored if and only if the run's `phase ∈ {active, held}` at the moment the callback is processed, checked atomically inside the same tx as the state mutation (`SELECT ... FOR UPDATE`). Any other phase → ack-but-noop with a structured log event.

### Why the rule

Late callbacks are an unavoidable distributed-systems shape. They arrive after the supervisor has already transitioned the run (heartbeat-stale recovery, operator cancellation, parked-timeout, duplicate-callback after network retry). Accepting them silently overwrites the canonical post-transition state, producing non-deterministic outcomes (which dispatch's terminal wins depends on timing). Rejecting them (ack-but-noop) makes outcomes deterministic: whichever dispatch is `active`/`held` when its callback arrives wins; all others are dropped.

### Why ack-but-noop rather than hard-error

Hard-error triggers executor retry storms for callbacks that will never succeed. Ack-but-noop with a structured log event gives operators full visibility without retry pressure.

### Implementation shape

The callback handler in `code:runtime/callback.go::driveTerminal` performs the dispatch-id → run lookup (currently via `code:runtime/callback.go::populateAcquisitionLineageFields` using `RunTree.GetByID` + `Instances.Get`; reshape per below) and checks the phase atomically:

```go
err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
    // run-tree lookup by dispatch_id; under RunScope-first the run carries
    // run_scope_id directly, and the lookup returns the NodeRow with FOR UPDATE.
    row, err := tx.Nodes().GetRunByDispatchIDForUpdate(ctx, dispatchID, tx)
    if err != nil { return err }
    if row == nil {
        args.Logger.Warn("callback.late_or_stale_run",
            "dispatch_id", dispatchID,
            "reason", "run_not_found")
        return nil  // ack-but-noop
    }
    if row.Phase != "active" && row.Phase != "held" {
        args.Logger.Warn("callback.late_or_stale_run",
            "dispatch_id", dispatchID,
            "current_phase", row.Phase,
            "expected_phase", "active|held")
        return nil  // ack-but-noop
    }
    return applyTerminal(ctx, row, terminal, tx)
})
```

`GetRunByDispatchIDForUpdate` is a new persistence method (replaces the cycle-3-era best-effort `populateAcquisitionLineageFields` lookup pattern). It returns the in-flight `NodeRow` with `FOR UPDATE` row lock, or `nil` if no such row exists. Under RunScope-first, the run row carries `run_scope_id` directly, so the callback path no longer needs the separate RunTree + Instance fetches that `populateAcquisitionLineageFields` did.

### Consumer audit

Downstream consumers of "terminal events for a dispatch" must not assume "every dispatch produces a terminal event." This is already largely the case (forensics tolerate gaps; parent aggregation walks the RunScope tree, not the event log), but the spec calls out the audit as a verification line during implementation.

## Recovery-aware executor protocol

Two optional field pairs on the executor wire format, symmetric. Independent of RunScope-first; bundled into this spec because the implementation touches the same callback handler.

### On `code:protocols/proto/v1/executor.proto::ExecuteRequest` (dispatch)

```proto
message ExecuteRequest {
  // ... existing fields (use real, non-colliding field numbers when adding;
  // verify against current ExecuteRequest field set at implementation time) ...

  // prior_dispatch_id is set when this dispatch supersedes a prior failed
  // or abandoned dispatch for the same RunScope+node. Empty/unset for
  // initial dispatches. Executors that maintain per-dispatch session state
  // (e.g., agentic executors with mid-conversation state, partial tool
  // calls) MAY use this to resume from the prior dispatch's state.
  // Executors that don't maintain such state ignore it and execute from
  // scratch.
  optional string prior_dispatch_id = N;

  // prior_dispatch_disposition explains why prior_dispatch_id is set, so
  // the executor can route resume logic appropriately.
  enum PriorDispatchDisposition {
    PRIOR_NONE = 0;
    PRIOR_HEARTBEAT_STALE = 1;
    PRIOR_RETRY_AFTER_ERROR = 2;
    PRIOR_RECALCULATE = 3;
  }
  optional PriorDispatchDisposition prior_dispatch_disposition = N+1;
}
```

The supervisor populates these whenever it knows the new dispatch supersedes a prior one — heartbeat-stale recovery (`code:runtime/conductor.go::SweepStaleHeartbeats` re-enqueue), retry-after-error (`code:runtime/runner_error_policy.go::applyResolvedAction` retry branch and `code:runtime/on_error.go::OnError` retry branch), and recalculate-driven re-dispatch (`code:runtime/cascade_recalculate.go::RecalculateNode`). For initial dispatches, both fields are unset.

### On callback ack (HTTP response body)

The callback path is HTTP+JSON per `code:runtime/callback.go`'s `POST {callback_url}/v1/callback/{async_ack_id}` route. The supervisor's response body (currently effectively empty) becomes structured:

```jsonc
{
  "ack_status": "accepted" | "rejected_run_terminal" | "rejected_run_stale" | "rejected_run_parked" | "rejected_unknown",
  "current_dispatch_id": "<uuid>"  // optional; set when supervisor knows the canonical successor
}
```

HTTP status stays `200 OK` for both accepted and rejected (per the ack-but-noop discipline — the supervisor successfully processed the callback even if it didn't apply state). The body's `ack_status` field is the authoritative signal. The supervisor sets `current_dispatch_id` when the canonical successor's id is known (e.g., heartbeat-stale recovery has already created a successor; the callback handler resolves it by RunScope-id lookup from the original run's RunScope).

Executors update their HTTP client to parse the response body and dispatch on `ack_status`. Existing executors that ignore the response body see no behavioral change for accepted callbacks; rejected callbacks are silently dropped (executor's view: success). The observability + handoff value is opt-in.

### What rimsky commits to

- Populating both field pairs in the relevant scenarios
- Documenting the wire-format semantics in the executor protocol docs
- Conformance tests that verify (a) supervisor populates fields correctly, (b) executors can read them (stub-executor support in `code:executors/claude-agent` test harness)

### What rimsky leaves to the executor

- The handoff mechanism itself. How does an executor donate work-in-progress to the successor? Out-of-band (shared store keyed by `prior_dispatch_id`), through a side channel, via rimsky-mediated state transfer — each executor type implements its own pattern. The agentic-executor recovery case can be built as "executor persists session state to a store keyed by `dispatch_id` on each step; on dispatch with `prior_dispatch_id` set, executor reads from that store and resumes."

## Park terminal proto change

The park terminal proto's `ParkReason` enum becomes a closed set of two values. The current enum at `code:protocols/proto/v1/executor.proto::ParkReason` has seven values: `PARK_REASON_UNSPECIFIED`, `PARK_REASON_TIME_WAIT`, `PARK_REASON_SIGNAL_WAIT`, `PARK_REASON_AWAITING_HUMAN`, `PARK_REASON_RETRY_BACKOFF`, `PARK_REASON_CALLBACK_WAIT`, `PARK_REASON_OTHER`. These collapse to two:

```proto
enum ParkReason {
  // No UNSPECIFIED, no OTHER, no TIME_WAIT/SIGNAL_WAIT/AWAITING_HUMAN/
  // RETRY_BACKOFF/CALLBACK_WAIT — collapsed to two real categories.
  // proto3 zero-value defaults to AWAIT_CALLBACK (the more conservative
  // case — an executor that forgets to set the field gets a wait-on-
  // callback interpretation, which won't auto-resume).
  PARK_REASON_AWAIT_CALLBACK = 0;  // waiting on async signal; wake triggered by external callback
  PARK_REASON_SNOOZE         = 2;  // resume at a known time; supervisor schedules the wake
}

// `Park` retains its existing six-field shape; only the enum collapses.
// The `payload` / `session_token` fields are load-bearing for the
// resume-roundtrip contract (executors receive them back as
// ResumeContext.payload / ResumeContext.session_token on the resume
// dispatch). `reason_note` is read by CLI / diagnostics surfaces.
// Renaming `Park` to `ParkTerminal` and dropping these fields would
// silently delete the resume-payload contract, so the message keeps
// its name and full shape; the change is the enum collapse only.
message Park {
  ParkReason reason = 1;
  bytes payload = 2;                          // inert to rimsky; returned as ResumeContext.payload on resume
  google.protobuf.Timestamp resume_at = 3;    // required iff reason == SNOOZE; SweepParkedNodes wakes at this time
  string session_token = 4;                   // optional; inert to rimsky; returned as ResumeContext.session_token on resume
  string reason_note = 5;                     // free-form operator-visible annotation
  string reason_label = 6;                    // optional freeform tag; opaque to rimsky; persisted for observability
}
```

### Mapping from current values to new

- `TIME_WAIT`, `RETRY_BACKOFF`, `SNOOZE` → `SNOOZE` (the supervisor-scheduled-wake case)
- `SIGNAL_WAIT`, `AWAITING_HUMAN`, `CALLBACK_WAIT` → `AWAIT_CALLBACK` (the external-signal case)
- `UNSPECIFIED`, `OTHER` → removed; no longer a valid value

### Downstream impact

- Executor implementations emitting any of the removed values must be rebuilt to emit `AWAIT_CALLBACK` or `SNOOZE`.
- Schema `col:rimsky_node_runs.parked_reason` (TEXT column) gets a CHECK constraint or PostgreSQL enum type limiting values to `'await_callback' | 'snooze'`.
- Prometheus labels and CLI flags that filter on the old reason names get updated to the new closed set.
- The cycle-2 in-code rejection in `code:runtime/runner_terminal_park.go::applyTerminalPark` for `PARK_REASON_UNSPECIFIED` becomes dead code (proto wire layer catches it before the handler runs) and is deleted.
- Pre-v1 break-freely covers the migration. No compat shim. CHANGELOG documents the proto change.

### `reason_label` semantics

Optional free-form string. Opaque to rimsky (read-only persistence, never inspected or interpreted, never used for routing decisions). Persisted on the parked run row for operator observability and executor self-audit. Agentic executors may use it to annotate `"waiting for user confirmation on file edit"`; snoozing executors may annotate `"backoff after rate-limit; retry in 30s"`. Same inertness discipline as `Error.payload` (per `concept:inertness`).

## State-machine tx atomicity invariant

A new `@blessed-invariant` on the state-machine write surface:

> **State-machine writes for a single run must be tx-atomic.** Any operation that reads a run's current state to decide what state to write must perform the read and the write in the same transaction. Splitting the read and write across transactions (or auto-commit on either side) creates a race window where the row's state can change between read and write, producing inconsistent transitions.

Sites where this invariant is currently violated and must be fixed:

- `code:runtime/on_error.go::OnError` retry branch: reads `EvaluatorState` in one tx; writes the state mutation in another. Must bundle into a single tx.
- `code:runtime/on_error.go::OnError` give_up branch: `RemoveForNode` is auto-commit, outside the tx that updated state. Must move into the same tx.
- `code:runtime/cascade_invalidate.go::invalidateInFrame`: see fix #5 below for the specific reconciliation (the existing code intentionally resolves `frame_id` outside the mutating tx to avoid SQLite deadlock; the fix is structural rather than a one-line move).

The invariant is sited as a code-level annotation on the state-machine write helpers; future violations are caught at code-review or by dedicated atomicity tests.

## Remaining explicit fixes

Independent of the data-model reshape, these one-off bugs need fixing in the same spec:

1. **`code:foundation/persistence/postgres/node_attributes.go::GetLatestByNode`** — by-node forensic lookup. Accept `runScopeID shared.UUID` parameter; SELECT keys on `(node_id, run_scope_id)`. SQLite mirror.
2. **`code:foundation/persistence.NodeTable.ResetFailedTerminalLastOutcome`** — accept `runScopeID` disambiguator (currently selects "most-recent failed-terminal row" without disambiguation under fan-out). Fix driver drift: Postgres currently always bumps `rimsky_nodes.updated_at` even on no-op; SQLite returns early. Align to one behavior (skip the bump when nothing changed).
3. **SQLite nested-tx deadlock in `code:runtime/on_error.go::OnError`**: `requiredStoresForNode` opens a nested `sb.Transaction` inside the outer tx wrap. SQLite's `MaxOpenConns=1` pool blocks waiting on the only connection. Hoist `requiredStoresForNode` out of the outer tx (compute pre-tx; pass the resulting `[]string` into the closure via captured variable). Same shape as `code:runtime/runner_error_policy.go::applyResolvedAction` already uses.
4. **CROSS-TX-SPLIT in `code:runtime/on_error.go::OnError`** — covered by the state-machine tx atomicity invariant above (read EvaluatorState and write state mutation in same tx; move auto-commit `RemoveForNode` into the same tx as the give_up state write).
5. **`code:runtime/cascade_invalidate.go::invalidateInFrame` frame_id resolution** — the current code (around lines 217-241) explicitly resolves `frame_id` outside the mutating tx because calling `invalidateNextFrame` from inside an open tx self-deadlocks under SQLite (`MaxOpenConns=1`) and ties up two pool connections under postgres. The atomicity gap is real (frame_id resolution can stale between resolve and mutate), but the fix is NOT to move the resolve into the tx (that re-introduces the deadlock the comment block was avoiding). Fix shape: hoist `invalidateNextFrame` (the fallback path that creates the deadlock) out of the in-tx code path. Resolve `frame_id` outside, mutate inside; if `frame_id` is stale by the time the mutation runs (detectable by the in-tx read of the source node's current `frame_id`), abort the mutation cleanly and let the calling cascade walker retry from a fresh resolve.
6. **`code:foundation/persistence.InstanceTable.IncrementAttributeOverrideMatchCounts` WARN contract drift** — contract docstring promises WARN observability for out-of-range indices; no impl emits one. Either update the contract docstring to reflect actual silent-no-op behavior, or implement the WARN by extending the helper's return shape to count dropped indices.
7. **Async-callback `RunTree` invariant under RunScope-first** — `code:runtime/callback.go::driveTerminal` resolves `run_scope_id` via the dispatch-id lookup (`GetRunByDispatchIDForUpdate`). If the dispatch_id doesn't resolve to a run (run not found), the callback is rejected per the determinism rule (ack-but-noop + structured log). Under RunScope-first, "silently succeed with nil parent context" is impossible (`run_scope_id` is non-null per schema); the cycle-3 best-effort behavior in `populateAcquisitionLineageFields` is retired.

(The cycle-3 audit findings that flagged `ClearLastOutcome` docstring drift and `absorbEntryIntoCaller` overlapping errors are stale — both already landed in tree. Dropped from this spec's fix list.)

## Test coverage matrix

Production paths to add end-to-end test coverage for. Each test exercises the full dispatch lifecycle (not isolated helpers, not directly-seeded rows). Each test file lives under `code:test/scenarios/` following the existing scenario harness conventions.

### Must-pass for spec convergence (gates verification)

These tests are required to pass for the spec to be considered complete. They exercise the load-bearing properties of the RunScope-first model:

**Fan-out E2E (4 must-pass scenarios):**

1. **F1: Child success terminal + cascade-mark-stale through fan-out parents** — fan-out parent fires; partition children execute to success; aggregation settles parent; downstream main-graph subscriber receives cascade; wait-set drains correctly when parent settles. Pins: RunScope creation at SplitScope, partition RunScope closure at aggregation, cross-RunScope cascade at parent settlement.
2. **F2: Child error terminal + retry via `applyResolvedAction`** — one child errors; retry policy fires; child re-dispatches in the same partition RunScope; recovery-aware protocol fields populate; parent aggregation sees the eventual outcome. Pins: retry path threads RunScope correctly; `prior_dispatch_id` populated.
3. **F3: Child heartbeat-stale recovery** — one child's supervisor "dies" mid-execution (test simulates by stopping heartbeat); sweep transitions row; new supervisor dispatches successor in the same partition RunScope with `prior_dispatch_id` set. Pins: heartbeat-stale recovery threads RunScope correctly.
4. **F4: Child async-callback resume with late-callback rejection** — child dispatched, parks (AWAIT_CALLBACK), external callback completes; second callback for the same dispatch_id arrives after the first was processed; second is rejected per the determinism rule. Pins: callback determinism rule under fan-out.

**Sub-graph E2E (4 must-pass scenarios):**

5. **S1: Entry-absorbed dispatch + internal cascade firing** — calling node succeeds; sub-graph RunScope created; internal cascade propagates; internal nodes dispatch in the sub-graph RunScope. Pins: sub-graph RunScope creation at `applyTerminalCompleteSubgraphCaller`.
6. **S2: Exit-writeback carry-rule under real dispatch** — exit node terminates with a writeback; carry-rule fires; calling node's writeback receives exit's output; sub-graph RunScope closes. Pins: sub-graph RunScope closure semantics.
7. **S3: Sub-graph internal node error + retry within sub-graph** — internal node errors; retry stays within the sub-graph RunScope; exit eventually terminates. Pins: retry path within sub-graph context.
8. **S4: Cascade-mark-stale walking through sub-graph exit** — outer node fires; cascade traverses to a node downstream of the sub-graph's exit; correctly traces back through carry-rule to the calling node. Pins: cross-RunScope cascade at exit.

### Aspirational coverage (does not gate verification but recommended)

Additional terminal paths for both fan-out and sub-graph that fill out the matrix:

- Child give-up terminal; child OnError handling (other paths)
- Child park (SNOOZE variant); child park/wake lifecycle
- Recalculate hitting fan-out children; recalculate hitting sub-graph internals
- Sub-graph entry-absorbed terminal paths for each error/park/give-up class

These can be added incrementally after the must-pass set lands.

### Conformance tests

Persistence-layer conformance tests, each implemented for both Postgres and SQLite backends in `code:foundation/persistence/conformance/`:

- **`run_scope_lifecycle.go`** (must-pass): RunScope create / FK constraints / close-when-rendezvous-fires / `AffirmNodeRunRow` errors when closed.
- **`affirm_node_run_row.go`** (must-pass): helper is idempotent (call twice → one row); errors on closed RunScope; returns only error; affirm-then-read pattern produces consistent results.
- **`run_in_flight_lookup.go`** (must-pass): `GetInFlightRunForNode(node_id, run_scope_id)` returns at most one row; under fan-out (multiple in-flight runs sharing node_id across different RunScopes), each RunScope's lookup returns its own row.
- **`run_state_writes_isolated_by_scope.go`** (must-pass): seeds runs in two RunScopes sharing node_id; `UpdateState` in scope A doesn't touch the run in scope B. Replacement for the cycle-2/3 disambiguator-specific tests which become inexpressible under the unique index.
- **`recovery_aware_dispatch.go`** (must-pass): a supervisor populates `prior_dispatch_id` and `prior_dispatch_disposition` in heartbeat-stale recovery; verified via inspection of the dispatched `ExecuteRequest`. The inspection harness extends `code:graph/scenario/harness.go` with an executor stub that records the received `ExecuteRequest` payload for assertion.

Existing fan-out / disambiguator conformance tests (`code:foundation/persistence/conformance/nodes_update_state_fanout_run_id.go`, `code:foundation/persistence/conformance/nodes_clear_fanout_run_id.go`, `code:foundation/persistence/conformance/queue_remove_for_node_fanout_run_id.go`, `code:foundation/persistence/conformance/queue_enqueue_fanout_partition.go`, `code:foundation/persistence/conformance/queue_in_flight_run_for_node_fanout.go`) are retired — their cases become inexpressible under the unique-index-on-(node_id, run_scope_id) keying.

### Recovery-aware protocol unit tests

- **`code:executors/claude-agent`** (TypeScript): unit test that a stub executor reads `prior_dispatch_id` from `ExecuteRequest` and `ack_status` / `current_dispatch_id` from a callback ack.

### State-machine tx atomicity tests

- **`code:runtime/on_error_tx_atomicity_test.go`** (must-pass): asserts the retry path's read-and-write happens in a single tx (verified by hooking the tx mechanism with a test double that counts opens).
- **Similar coverage for `code:runtime/runner_error_policy.go::applyResolvedAction`** (already correct; regression pin).

## Codebase rename

Two rename disciplines apply across this spec, enumerated below. Both are sweeping; the audit step at convergence (Verification §8) catches anything missed.

### Rename 1: `parent_run_id` + `child_key` → `run_scope_id`

Inline `(parent_run_id, child_key)` references throughout source code, prose, conformance tests, scenarios, CHANGELOG entries, and concept docs become `run_scope_id` (or RunScope-related equivalent). The rename touches:

- `code:foundation/persistence/` — interface definitions, postgres impls, sqlite impls, conformance tests; including the `RunTreeTable` reshape and the new `RunScopeTable`
- `code:runtime/` — acquisition, dispatch, terminal, cascade, on_error, wake_parked, sweep_parked, callback, state_propagation, fanout_dispatch, subgraph_dispatch, run_tree paths
- `code:control/controlapi/` — instance creation, admin reset paths, backfill admin handler
- `code:graph/scheduler/` — pure_cascade path
- `code:test/scenarios/` — every scenario that names `parent_run_id` or `child_key`
- `code:protocols/proto/v1/` — `ExecuteRequest` field additions; no proto-level rename needed for parent_run_id/child_key (those weren't on the wire)
- CHANGELOG.md — under Unreleased
- `code:.ok-planner/design/concepts/` — see Design changes section below

### Rename 2: `scope` (claim-identity bytes) → `claim_scope`

The existing `concept:scope` becomes `concept:claim-scope`. Every reference that uses the bare term "scope" or "Scope" to mean claim-identity bytes becomes "claim scope" or "ClaimScope" — qualified to disambiguate from `RunScope`. Touched sites:

**Concept docs (mutations enumerated in Design changes):**
- `code:.ok-planner/design/concepts/scope.md` → rename to `claim-scope.md` (the primary mutation)
- `code:.ok-planner/design/concepts/claim-handle.md` — references `lock_kind ∈ {named, scope}` and `scope_data`
- `code:.ok-planner/design/concepts/inertness.md` — "claim scope" stream references, `concept:scope` adjacency, invariant references
- `code:.ok-planner/design/concepts/lineage-record.md` — `scope_data_hash` references
- `code:.ok-planner/design/concepts/claim.md` — `concept:scope` adjacency, "byte-equal scope" references
- `code:.ok-planner/design/concepts/claim-producer.md` — "scope bytes" references, `concept:scope` adjacency
- `code:.ok-planner/design/concepts/write-semantics.md` — "byte-equal scope" references, `concept:scope` adjacency
- `code:.ok-planner/design/concepts.md` (TOC) — auto-regenerated to reflect the rename

**Go code:**
- `code:foundation/locks/conflict.go::ScopesByteEqual` → `ClaimScopesByteEqual`
- `code:protocols/claimproducer/types.go::ClaimResult.Scope` → `ClaimScope`
- Other Go fields/methods/comments in `code:foundation/locks/`, `code:foundation/persistence/`, `code:runtime/` that use bare "Scope" for claim-identity bytes

**Proto:**
- `code:protocols/proto/v1/claim_producer.proto` — field renames for `scope` → `claim_scope` on the relevant messages (verify exhaustively at implementation time; pre-v1 break-freely covers wire compat)

**Schema:**
- `col:rimsky_claim_handles.scope_data` → `claim_scope_data` (both postgres + sqlite migrations)
- `lock_kind` CHECK constraint enum value `'scope'` → `'claim_scope'` (per `code:foundation/persistence/postgres/migrations/001-baseline.sql` and the SQLite mirror)
- Index `idx_rimsky_claim_handles_scope` → `idx_rimsky_claim_handles_claim_scope`

**Substitution grammar:**
- `{{claim.<alias>.scope}}` → `{{claim.<alias>.claim_scope}}` (parser, examples, docs)

**Tests:**
- `code:test/scenarios/` — every scenario referencing `scope` in the claim-bytes sense; especially `code:test/scenarios/asset/durable_lifetime_e2e_test.go` and other claim-related scenarios.

The audit step at Verification §8.A picks up any missed `WHERE node_id = ?` patterns from rename 1; an analogous audit step at Verification §8.G catches `lock_kind = 'scope'` / `scope_data` / `ScopesByteEqual` / bare `scope` references missed by rename 2.

## Verification

The spec is complete when all of the following hold:

1. **Build:** `cmd:go build ./...` clean across all three `code:go.work` modules (root, foundation, protocols).
2. **Lint:** `cmd:make lint` clean across all three modules.
3. **Unit tests:** `cmd:go test ./...` clean across all packages (modulo documented pre-existing testcontainer cold-start flakes).
4. **Race tests:** `cmd:go test -race -count=3 ./foundation/persistence/... ./runtime/...` clean.
5. **Must-pass scenarios:** all 8 must-pass scenarios (F1–F4, S1–S4) from the test coverage matrix pass.
6. **Must-pass conformance tests:** all 5 must-pass conformance tests pass on both Postgres and SQLite.
7. **TS executor:** `cd executors/claude-agent && npm install && npm test && npm run build` clean.
8. **Audit re-run** — running the seven pattern audits reports zero new findings:
   - **Audit A:** `WHERE node_id = ?` SELECT pattern audit across `code:foundation/persistence/`
   - **Audit B:** `code:foundation/persistence.DispatchRequest` construction site audit
   - **Audit C:** disambiguator USE-site audit (every callsite of the run-scope-accepting persistence methods passes a real RunScope id, not nil-where-one-is-available)
   - **Audit D:** state-machine write atomicity audit (every state-machine write site bundled with its read in a single tx)
   - **Audit E:** fan-out + sub-graph test coverage gap audit (every production path in the matrix above has end-to-end coverage)
   - **Audit F:** cascade / wait-set / park-wake under fan-out audit (no stale-snapshot disambiguators; no hardcoded-nil disambiguators in cascade walkers)
   - **Audit G:** ClaimScope rename audit (no remaining bare `scope` / `Scope` references in claim-bytes sense; no `scope_data` / `ScopesByteEqual` / `lock_kind = 'scope'` / `idx_rimsky_claim_handles_scope` / `{{claim.<alias>.scope}}` / `concept:scope` references)
   Convergence is "audits return nothing new."
9. **Citation grammar discipline** applied to CHANGELOG entries and concept-doc Notes blocks per `code:.claude/rules/citation-grammar.md`.

## Design changes

This spec materially changes the design docs under `code:.ok-planner/design/`. The following mutations are applied by `execute-plan` during plan execution:

### New concept

**Create `code:.ok-planner/design/concepts/run-scope.md`** with sections:

- **What it is:** RunScope as first-class entity; the execution context for a graph instantiation (main / sub-graph / fan-out partition); the tree shape via `parent_run_scope_id`. Persisted as `code:rimsky_run_scopes`. Each RunScope owns a set of `rimsky_node_runs` rows.
- **Purpose:** uniform representation of execution contexts; eliminates the bug class of inline-disambiguator drift; enables depth-gating via parent-chain walks (complementing canonicalizer-level recursion rejection per `concept:sub-graph`); enables agentic-executor recovery handoff via the `prior_dispatch_id` / `current_dispatch_id` protocol.
- **Boundaries:** owns the per-RunScope `rimsky_node_runs` set; owns RunScope lifecycle (creation / closure); owns the parent-RunScope / parent-run relationship. Does NOT own claim semantics (parallel structure via `concept:claim-tree`); does NOT own cascade-edge semantics (`concept:cascade` traverses subscription edges within and across RunScopes); does NOT own frame semantics (frames and RunScopes are orthogonal). Adjacent: `concept:fan-out`, `concept:delegation`, `concept:frame`, `concept:claim-tree`, `concept:cascade`, `concept:node-run`.
- **Invariants:**
  - RunScope rows are inserted eagerly in the tx that triggers them (main at instance creation; sub-graph at calling-node's success terminal via `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller`; fan-out partition at SplitScope sub-claim acquisition per `@blessed-invariant 10`).
  - `parent_run_scope_id IS NULL ⇔ parent_run_id IS NULL ⇔ main RunScope`.
  - `partition_key != ''` iff fan-out partition RunScope; uniqueness of open fan-out partition per (parent_run_id, partition_key) enforced by partial-unique index.
  - `closed_at IS NOT NULL` means parent-run rendezvous has fired; `AffirmNodeRunRow` returns `ErrRunScopeClosed`; cascade walker reaching INTO a closed RunScope is a bug.
  - `AffirmNodeRunRow` is the lazy-allocation primitive; callers must not depend on its return value beyond error/no-error (preserves lazy↔eager rewrite property).
  - Depth gating (runtime safety net): a sub-graph that would create a RunScope already present in the parent chain at any depth is rejected. The canonicalizer already rejects recursive sub-graphs statically per `concept:sub-graph`; this runtime check is defense-in-depth.
- **Annotation sites:** `code:foundation/persistence/postgres/run_scopes.go`, `code:foundation/persistence/sqlite/run_scopes.go`, `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller` (sub-graph RunScope creation), `code:runtime/fanout_dispatch.go::CreateFanOutChildren` (fan-out partition RunScope creation), `code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx` (cascade walker carries RunScope), `code:runtime/callback.go::driveTerminal` (callback resolves RunScope via dispatch_id).
- **Notes:** `2026-05-22 — Created per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`

### Existing concept mutations

**Rename `code:.ok-planner/design/concepts/scope.md` to `code:.ok-planner/design/concepts/claim-scope.md`** and apply the following in-file mutations:

- Front-matter: `concept: scope` → `concept: claim-scope`; add `aliases: [scope (pre-2026-05-22, retired)]`.
- Title: `# Scope` → `# Claim Scope`.
- `## What it is`: rewrite to use "claim scope" throughout. Replace "Scope is the opaque byte stream..." → "ClaimScope is the opaque byte stream a `ClaimProducer.Open` returns to identify 'what was acquired.' Persisted as `col:rimsky_claim_handles.claim_scope_data`. Compared byte-equally via `code:foundation/locks/conflict.go::ClaimScopesByteEqual`."
- `### Selector vs scope` subsection: rename to `### Selector vs claim scope`. Replace all "scope" → "claim scope" in the body. Update the substitution directive line: `{{claim.<alias>.scope}}` → `{{claim.<alias>.claim_scope}}`.
- `## Purpose`: rewrite first paragraph to use "claim scope." The byte-equality rationale stays the same.
- `## Boundaries`: rewrite to use "claim scope." Update column reference to `claim_scope_data`.
- `## Invariants`: rewrite each invariant to use "claim scope."
- `## Aliases and historical names`: append a note: "Renamed from `scope` to `claim-scope` per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md, to disambiguate from `concept:run-scope` (the execution-context concept). The legacy bare-`scope` term is fully retired."
- `## Common pitfalls`: keep the JS-scope / AWS-scope / OAuth-scope disambiguation (still useful). Remove or update the "Rimsky's scope is not [other scopes]" framing since now ClaimScope's name is self-disambiguating.
- `## Notes`: append `2026-05-22 — Renamed from concept:scope to concept:claim-scope per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md to make room for concept:run-scope.`

**Mutate `code:.ok-planner/design/concepts/fan-out.md`** in place:

- Update the Definition paragraph: "Each child leaf run gets `parent_run_id = parent's run id`, `child_key = <partition_key>`" → "Each child runs in its own fan-out partition RunScope (per `concept:run-scope`), with `parent_run_id = fan-out parent's run id`, `parent_run_scope_id = fan-out parent's RunScope id`, `partition_key = <partition_key>`. The child's leaf run lives in this RunScope."
- Update the Invariants line that documents the parent_run_id + child_key shape (currently "Each child leaf run gets `parent_run_id = parent's run id`, `child_key = <partition_key>`") to reflect the RunScope-based shape.
- Update Boundaries to add: "Owns the SplitScope-driven RunScope creation at parent acquisition; does NOT own RunScope semantics in general (see `concept:run-scope`)."
- Append `Notes` entry: `2026-05-22 — Reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: fan-out children now live in fan-out partition RunScopes (concept:run-scope) rather than carrying inline parent_run_id + child_key on the node_run row.`

**Mutate `code:.ok-planner/design/concepts/delegation.md`** in place:

- Update the Definition's "asymmetric identity" paragraphs:
  - Entry absorption stays structurally the same (entry IS the calling node, in the parent RunScope), but the sub-graph's internal nodes now live in a sub-graph RunScope with `parent_run_id = calling node's run id`, `parent_run_scope_id = calling node's RunScope id`. The calling node's run remains in the parent RunScope.
  - Exit-writeback carry-rule fires at exit's leaf-run terminal, atomically with sub-graph RunScope closure (`closed_at` set).
- Update Annotation sites to reference `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller` for sub-graph RunScope creation.
- Append `Notes` entry: `2026-05-22 — Reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: sub-graph internal nodes now live in a sub-graph RunScope (concept:run-scope); carry-rule closure semantics added.`

**Mutate `code:.ok-planner/design/concepts/cascade.md`** in place:

- Update the body to note that the cascade walker carries `run_scope_id` through subscription edges; the `AffirmNodeRunRow` primitive owns row allocation; `MarkStaleForCascade` simplifies to a pure UPDATE keyed by `run_id` (no insert path).
- Specifically update the 2026-05-14 Notes entry's description of "wait-set rows are inserted on every cascade-walk match" to reflect that allocation is now via `AffirmNodeRunRow` and wait-set insertion uses the affirm-then-read pattern.
- Append `Notes` entry: `2026-05-22 — Reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: cascade walker is RunScope-aware; AffirmNodeRunRow is the allocation primitive; MarkStaleForCascade is a pure UPDATE.`

**Mutate `code:.ok-planner/design/concepts/frame.md`** in place:

- Add a clarifying note: "Frames and RunScopes (per `concept:run-scope`) are orthogonal: a single cascade frame can span multiple RunScopes (cascade propagation across sub-graph entry-success or fan-out parent settlement); a single RunScope can host multiple frames (the same graph firing across multiple cascade resolutions)."
- Append `Notes` entry: `2026-05-22 — Clarified orthogonality with concept:run-scope per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`

**Mutate `code:.ok-planner/design/concepts/node-run.md`** in place:

- Update the `## What it is` section. The current text lists `parent_run_id`, `child_key`, and `aggregation_policy` as columns lifted in the 2026-05-15 run-tree extension. Reshape:
  - Remove `parent_run_id UUID NULL` and `child_key TEXT NULL` from the column list.
  - Add `run_scope_id UUID NOT NULL` to the column list with the note "FK to `rimsky_run_scopes` (per `concept:run-scope`). All scoping — parent/child relationship for fan-out, sub-graph membership for delegation — is now expressed through this FK chain rather than inline on the node_run row."
  - Keep `aggregation_policy JSONB NULL` (still snapshotted from the template-node spec at run creation time).
- Rewrite the `**Run-tree**` paragraph: "node-runs form a tree via `parent_run_id` + `child_key`. A root run has both columns NULL..." → "node-runs are organized into RunScopes (per `concept:run-scope`) via `run_scope_id`. The tree shape that previously lived inline on the node_run row now lives on `rimsky_run_scopes` via `parent_run_scope_id`. Walking the RunScope tree from a leaf RunScope to the main RunScope recovers the full execution stack. State aggregation walks bottom-up through the RunScope tree."
- Update `## Boundaries` to reference `concept:run-scope` as adjacent.
- Append `Notes` entry: `2026-05-22 — Reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: parent_run_id and child_key removed from rimsky_node_runs; replaced by run_scope_id (FK to rimsky_run_scopes). Run-tree shape moves to concept:run-scope.`

**Mutate `code:.ok-planner/design/concepts/claim-tree.md`** in place:

- Update the Definition's parenthetical: "(which uses `parent_run_id` + `child_key`)" → "(which uses `run_scope_id` per `concept:run-scope`, with the parent-child shape on `rimsky_run_scopes` rather than inline on the node_run row)."
- Append `Notes` entry: `2026-05-22 — Updated cross-reference to reflect the run-tree shape change per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md. Claim-tree (parent_claim_handle_id on rimsky_claim_handles) and RunScope-tree (parent_run_scope_id on rimsky_run_scopes) are now both first-class trees at the persistence layer; they remain parallel structures owned by different concepts.`

**Mutate `code:.ok-planner/design/concepts/parked-state.md`** in place (ParkReason 7→2 collapse):

- Update the Common pitfalls section: replace the 4-reason enum citation (`TIME_WAIT / CALLBACK_WAIT / RETRY_BACKOFF / OTHER`) with the new 2-reason set (`AWAIT_CALLBACK / SNOOZE`).
- Update the 2026-05-14 / 2026-05-15 Notes entries that describe the typed-enum taxonomy: keep them as historical record but append a 2026-05-22 entry that supersedes the prior taxonomy.
- Update the executor mapping guidance: `long-running-job → CALLBACK_WAIT` becomes `long-running-job → AWAIT_CALLBACK`; `time-based polling → TIME_WAIT` and `rate-limit-aware → RETRY_BACKOFF` both become `SNOOZE`; `awaiting-human → CALLBACK_WAIT` becomes `awaiting-human → AWAIT_CALLBACK`.
- Update any per-reason `max_park_duration` defaults: the watchdog config keyed on the old reason names migrates to the two-reason set; document the new defaults (suggested: AWAIT_CALLBACK = 24h or unbounded; SNOOZE = capped at the next-resume-at + grace window).
- Append `Notes` entry: `2026-05-22 — ParkReason enum collapsed from 7 values to 2 (AWAIT_CALLBACK, SNOOZE) per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md. PARK_REASON_UNSPECIFIED and PARK_REASON_OTHER removed entirely; TIME_WAIT/RETRY_BACKOFF map to SNOOZE; SIGNAL_WAIT/AWAITING_HUMAN/CALLBACK_WAIT map to AWAIT_CALLBACK. Watchdog config and executor mapping guidance updated above.`

**Mutate `code:.ok-planner/design/concepts/attribute.md`** in place (child_key matcher anchor reconciliation):

- Update the L5 matcher overlay invariant to reflect that the matcher's `child_key` key sources from the run's RunScope's `partition_key` (per `concept:run-scope`), not from a column on the node_run row. The matcher behavior is unchanged from the operator's perspective; only the implementation sourcing changes. Specifically: the dispatch context still carries an effective `child_key` value derived from the RunScope (the partition RunScope's `partition_key`; non-fan-out dispatches see empty string).
- Update the `## Matcher overlay (by_match)` section: add a sentence "Under RunScope-first (per spec 2026-05-22), the `child_key` matcher key sources its value from the dispatched run's RunScope's `partition_key`; the equality semantics and ordinal-rejection vocabulary remain unchanged."
- Append `Notes` entry: `2026-05-22 — child_key matcher anchor sourcing reconciled per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md: matcher reads from RunScope's partition_key now that parent_run_id + child_key are removed from rimsky_node_runs.`

**Mutate concept files that reference ClaimScope (the renamed concept). For each, the rename rule is: bare "scope" / "Scope" → "claim scope" / "ClaimScope" when referring to claim-identity bytes; `concept:scope` → `concept:claim-scope`; `scope_data` → `claim_scope_data`; `{{claim.<alias>.scope}}` → `{{claim.<alias>.claim_scope}}`:**

- `code:.ok-planner/design/concepts/claim-handle.md` — update the Columns section: `lock_kind ∈ {named, scope}` → `lock_kind ∈ {named, claim_scope}`; `scope_data` → `claim_scope_data`. Append `Notes` entry: `2026-05-22 — Updated for ClaimScope rename per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`
- `code:.ok-planner/design/concepts/inertness.md` — update all "scope" mentions in the claim-identity sense to "claim scope"; update `concept:scope` adjacency reference to `concept:claim-scope`; update invariant references to use the qualified name. Append `Notes` entry: `2026-05-22 — Updated for ClaimScope rename per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`
- `code:.ok-planner/design/concepts/lineage-record.md` — update `scope_data_hash` references to `claim_scope_data_hash`; update the `leaf_run` and `claim_terminal` projections to reflect (a) the column rename and (b) the new sourcing of `partition_key` / `parent_run_id` for run-tree-bearing projections — those values come from joining `rimsky_node_runs.run_scope_id → rimsky_run_scopes` rather than from inline columns. Append `Notes` entry: `2026-05-22 — Updated for ClaimScope rename and run-tree reshape per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`
- `code:.ok-planner/design/concepts/claim.md` — update `concept:scope` adjacency to `concept:claim-scope`; update "byte-equal scope" references to "byte-equal claim scope". Append `Notes` entry: `2026-05-22 — Updated for ClaimScope rename per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`
- `code:.ok-planner/design/concepts/claim-producer.md` — update "scope bytes" references to "claim scope bytes"; update `concept:scope` adjacency to `concept:claim-scope`. Append `Notes` entry: `2026-05-22 — Updated for ClaimScope rename per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`
- `code:.ok-planner/design/concepts/write-semantics.md` — update "byte-equal scope" references to "byte-equal claim scope"; update `concept:scope` adjacency to `concept:claim-scope`. Append `Notes` entry: `2026-05-22 — Updated for ClaimScope rename per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.`

**Fix `code:.ok-planner/design/tensions/_resolved/region-vs-scope-legacy.md` frontmatter:** the file lives under `_resolved/` but its frontmatter `status` is `open`. Fix to `status: resolved`. Append a Notes line noting the rename to ClaimScope per this spec is consistent with the original resolution's qualified-naming spirit.

**Regenerate `code:.ok-planner/design/concepts.md` (TOC):** after the renames and additions, the TOC needs regeneration to reflect (a) the renamed `claim-scope` entry replacing `scope`, (b) the new `run-scope` entry, (c) any one-sentence definition changes in the touched concept files. Auto-generated; no human edit.

### New invariants (added as `@blessed-invariant` annotations at the cited sites)

- **State-machine tx atomicity:** "State-machine writes for a single run must be tx-atomic. Any operation that reads a run's current state to decide what state to write must perform the read and the write in the same transaction." Sites: `code:runtime/on_error.go::OnError`, `code:runtime/runner_error_policy.go::applyResolvedAction`, `code:runtime/cascade_invalidate.go::invalidateInFrame`.
- **AffirmNodeRunRow no-return-value-dependency:** "Callers of AffirmNodeRunRow must not depend on its return shape beyond error/no-error. The architectural property is that lazy↔eager allocation is a no-op rewrite." Site: `code:foundation/persistence/nodes.go::NodeTable.AffirmNodeRunRow`.
- **Callback determinism:** "A callback for a run is honored if and only if the run's phase ∈ {active, held} at acceptance, checked atomically inside the same tx as the state mutation." Site: `code:runtime/callback.go::driveTerminal`.
- **Park terminal closed-enum:** "The ParkReason enum is a closed set of two values: AWAIT_CALLBACK and SNOOZE. The proto layer rejects any other value at wire decode." Site: `code:protocols/proto/v1/executor.proto::ParkReason`.

### No tensions catalog changes anticipated

The spec resolves the ambiguity by adopting RunScope-first; it does not surface new unresolved tensions. The existing `code:.ok-planner/design/tensions/_resolved/region-vs-scope-legacy.md` stays resolved — the rename to ClaimScope is a refinement consistent with that resolution's spirit (qualified-naming discipline), not a reopening.

## Out of scope

- **Switching to eager run-row allocation at RunScope creation.** The lazy-with-`AffirmNodeRunRow` model in this spec preserves the option (the helper's contract makes the rewrite a no-op), but the actual switch is a separate refactor.
- **Defining the executor-side handoff protocol.** Rimsky exposes the `prior_dispatch_id` / `current_dispatch_id` IDs; each executor type (claude-agent, stateless workers, others) implements its own handoff semantics on top.
- **Operator UI changes** to expose the RunScope tree (e.g., a dashboard showing the RunScope hierarchy for a running instance). The data model supports this; the UI work is separate.
- **Cross-instance RunScope linking** (e.g., one instance triggering another). RunScopes are per-instance; cross-instance coordination uses the existing message and trigger mechanisms.
- **Operator-side migration semantics** (data loss / dev-Postgres-nuke). Pre-v1 break-freely (per `code:submodules/rimsky/.claude/rules/rules.md`) covers it; the spec doesn't define a data-preservation migration.
