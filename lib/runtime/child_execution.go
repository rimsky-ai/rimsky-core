// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// child_execution.go — the unified child-execution primitives:
// `DispatchChildren` (dispatch) and `SettleChildren` (settlement).
//
// Delegation (sub-graph) and fan-out are the same run-side operation:
// allocate child RunScopes under a parent run, allocate child leaf-run
// rows inside them, and wire any already-acquired sub-claim handles to
// the runs they belong to. Delegation is the degenerate case — one
// partition, empty key, entry absorbed; fan-out is N partitions from
// the producer's SplitScope results. `DispatchChildren` is the single
// implementation both wrappers delegate to:
//
//   - sub-graph caller success terminal
//     (`subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller`)
//   - fan-out post-acquisition dispatch
//     (`fanout_dispatch.go::dispatchFanOutChildren`)
//
// The primitive accepts already-acquired sub-claims as input; it NEVER
// calls the producer's split itself (`AcquireSubClaims` and the rest
// of the claim-tree machinery are untouched upstream of this call).
//
// `SettleChildren` is the settlement mirror — the single primitive
// that fires on every child terminal and drives the parent's
// settlement per the aggregation policy:
//
//   - carry-verbatim (delegation): the single settlement child's
//     outcome is copied verbatim to the parent's writeback, the
//     child execution context (the sub-graph RunScope) closes in the
//     same transaction, and the parent-settlement cascade bridge
//     fires — all inside the primitive, so no caller can carry
//     without closing or settle without cascading. Caller:
//     `subgraph_dispatch.go::applyTerminalCompleteSubgraphExit`.
//   - strict | threshold | best_effort | first (fan-out / claim
//     chain): the child's outcome is recorded on the parent claim
//     handle, sibling cancellation fires per policy, and — once every
//     child has resolved and the parent's holding subgraph (if any)
//     has settled — the partition RunScopes close and the parent's
//     aggregate Commit/Abandon settlement fires through
//     `ResolveClaimHandleTerminal` (which recurses back into
//     `SettleChildren` for the grandparent). Caller:
//     `terminal_decision.go::ResolveClaimHandleTerminal` step 6. The
//     parent run's downstream cascade bridge for this shape fires in
//     `state_propagation.go::PropagateIfChildAfterTerminal` (the
//     run-state settlement walk), inline with the propagation walk in
//     its own post-terminal transaction — the same transaction
//     discipline the pre-unification path had.
//
//	@concept: child-execution
//	@concept: sub-graph
//	@concept: run-scope
//	@concept: claim-tree

package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// PartitionDescriptor describes one child RunScope the primitive must
// allocate (or reuse, when an open scope for the same
// `(parent_run_id, partition_key)` pair already exists).
type PartitionDescriptor struct {
	// PartitionKey is the RunScope's partition key. Fan-out: the
	// producer-assigned key from the SplitScope descriptor. Delegation:
	// empty (the sub-graph scope is unkeyed).
	PartitionKey string
	// SubClaimHandleID optionally names the already-acquired sub-claim
	// row backing this partition. When set, the primitive repoints the
	// row's `node_run_id` at the partition's child run so the leaf can
	// resolve its own sub-claim (and the partition-scope closure walk
	// finds the scope). Zero value = no sub-claim (delegation).
	SubClaimHandleID shared.UUID
}

// ChildRunSpec describes one leaf-run row to allocate INSIDE each
// partition. Fan-out passes exactly one (the parent's own node — same
// node-type, distinct RunScope per partition); delegation passes one
// per non-entry internal node of the delegate graph.
type ChildRunSpec struct {
	NodeID         shared.UUID
	Executor       string
	RequiredStores []string
}

// ChildExecutionInput is the input to DispatchChildren. The run rows
// created are the cross product Partitions × Children: delegation is
// 1 partition × N internal nodes; fan-out is N partitions × 1 node.
type ChildExecutionInput struct {
	// ParentRunID is the run the children hang under (the fan-out
	// node's run / the sub-graph calling node's run).
	ParentRunID shared.UUID
	// ParentRunScopeID is the parent run's own RunScope; every child
	// scope is rooted under it via `parent_run_scope_id`.
	ParentRunScopeID shared.UUID
	InstanceID       shared.UUID
	FrameID          shared.UUID
	// ChildGraphName is stamped on each child RunScope row. Fan-out:
	// the parent's graph. Delegation: the delegate graph's name.
	ChildGraphName string
	// AggregationPolicy is snapshotted on each child run row (the
	// `CreateChildRun` policy parameter). Both wrappers currently pass
	// the zero value — the parent-side author-policy snapshot stays at
	// the fan-out call site (`UpdateAggregationPolicy` on the parent
	// run), matching the pre-unification behavior exactly.
	AggregationPolicy spec.AggregationPolicy
	// EntryAbsorbed marks the delegation shape: the parent's executor
	// terminal WAS the absorbed entry's terminal, so the children must
	// be stale-marked for the cascade walker to dispatch them
	// (`MarkStaleForCascade` per created run). Fan-out children leave
	// it false — their rows dispatch through the queue's in-flight
	// sweep without a node stale-mark.
	EntryAbsorbed bool
	Partitions    []PartitionDescriptor
	Children      []ChildRunSpec
}

// DispatchedChild reports one child run the primitive allocated (or
// found, on the idempotent re-fire path).
type DispatchedChild struct {
	RunID        shared.UUID
	RunScopeID   shared.UUID
	NodeID       shared.UUID
	PartitionKey string
}

// DispatchChildren is the unified dispatch-children primitive. Per
// partition it allocates (or reuses) the child RunScope
// (`partition_key`, `parent_run_id`, `parent_run_scope_id`,
// `graph_name`), allocates the child leaf-run row(s) inside it, wires
// the partition's sub-claim handle to its run when present, and
// stale-marks each run when the entry was absorbed.
//
// Load-bearing property: every write runs inside the CALLER's
// transaction `tx` — RunScope inserts, child-run inserts, sub-claim
// repointing, and stale-marks commit atomically with the caller's
// acquisition / terminal write, exactly as the two pre-unification
// dispatch sites did. Nothing here opens a transaction or escapes tx.
//
// Idempotent per partition: an open RunScope for the same
// `(parent_run_id, partition_key)` is reused, and `CreateChildRun` is
// idempotent on `(node_id, run_scope_id)` — a duplicate fire returns
// the existing run ids instead of double-allocating.
//
// Empty-key delegation asymmetry: the uniqueness index backing the
// per-partition reuse (`uq_run_scopes_fanout_partition_open` in
// migrations 001, both drivers) is PARTIAL — it covers only NON-empty
// partition keys on OPEN scopes — so the delegation shape's single sub-graph
// scope (empty partition key) has no database-level uniqueness
// constraint. That is safe because (a) the open-scope reuse above goes
// through `GetFanoutPartition(parent_run_id, partition_key)` for the
// empty key too, inside the caller's tx, so the only duplicate-insert
// window is two CONCURRENT first-fires for the same parent run — and a
// delegation dispatch fires from the parent run's own terminal tx,
// which is serialized per-run by the dispatch row's claimed_by guard;
// and (b) fan-out partitions never carry an empty key —
// `AcquireSubClaims` rejects a producer-returned empty partition_key
// before any row is inserted, so the empty key remains an unambiguous
// delegation discriminator (the settlement walk relies on it to skip
// closing non-partition scopes). The index cannot simply drop the
// non-empty-key predicate: a parent legitimately holds its
// delegation sub-graph scope open across the same window in which
// retry re-fires reuse it, and widening the index would also have to
// distinguish reuse-by-lookup from insert races across both drivers'
// differing partial-index semantics — the documented invariant above
// is the constraint.
//
// The primitive accepts already-acquired sub-claims; it never calls
// the producer (the claim-tree split happened upstream in
// `AcquireSubClaims`).
//
//	@concept: child-execution
//	@concept: run-scope
func DispatchChildren(
	ctx context.Context, args RunArgs, tx persistence.Tx, in ChildExecutionInput,
) ([]DispatchedChild, error) {
	if len(in.Partitions) == 0 || len(in.Children) == 0 {
		return nil, nil
	}
	// Validate the parent scope exists before allocating under it —
	// both pre-unification sites surfaced a precise error here rather
	// than letting the FK violation name the failure.
	parentScope, err := args.Persist.RunScopes().GetByID(ctx, tx, in.ParentRunScopeID)
	if err != nil {
		return nil, fmt.Errorf("DispatchChildren: load parent run scope: %w", err)
	}
	if parentScope == nil {
		return nil, fmt.Errorf("DispatchChildren: parent run scope %s not found", in.ParentRunScopeID)
	}
	out := make([]DispatchedChild, 0, len(in.Partitions)*len(in.Children))
	for _, p := range in.Partitions {
		// A sub-claim handle binds to exactly one run. The cross-product
		// shape makes >1 child per sub-claim-bearing partition
		// unrepresentable on the wire today (fan-out always passes one
		// child spec); guard it so a future caller can't silently bind a
		// sub-claim to an arbitrary sibling.
		if p.SubClaimHandleID != (shared.UUID{}) && len(in.Children) != 1 {
			return nil, fmt.Errorf(
				"DispatchChildren: partition %q carries a sub-claim handle but %d child specs; a sub-claim binds to exactly one run",
				p.PartitionKey, len(in.Children))
		}
		// Allocate (or reuse) the partition's RunScope keyed on
		// (parent_run_id, partition_key). The lookup keeps the re-fire
		// path idempotent for both shapes — fan-out partitions and the
		// delegation sub-graph scope (empty key) alike.
		var childScopeID shared.UUID
		existing, err := args.Persist.RunScopes().GetFanoutPartition(ctx, tx, in.ParentRunID, p.PartitionKey)
		if err != nil {
			return nil, fmt.Errorf("DispatchChildren: lookup partition %q: %w", p.PartitionKey, err)
		}
		if existing != nil {
			childScopeID = existing.ID
		} else {
			childScopeID = shared.UUID(uuid.New())
			parentRunIDCopy := in.ParentRunID
			parentScopeIDCopy := parentScope.ID
			if err := args.Persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
				ID:               childScopeID,
				ParentRunScopeID: &parentScopeIDCopy,
				ParentRunID:      &parentRunIDCopy,
				GraphName:        in.ChildGraphName,
				PartitionKey:     p.PartitionKey,
				InstanceID:       in.InstanceID,
			}); err != nil {
				return nil, fmt.Errorf("DispatchChildren: create partition %q: %w", p.PartitionKey, err)
			}
		}
		for _, c := range in.Children {
			runID, err := CreateChildRun(
				ctx, tx, args.Persist.RunTree(), args.Queue,
				c.NodeID, in.FrameID, childScopeID,
				c.Executor, c.RequiredStores, in.AggregationPolicy)
			if err != nil {
				return nil, fmt.Errorf("DispatchChildren: child run (partition %q, node %s): %w",
					p.PartitionKey, c.NodeID, err)
			}
			// Repoint the sub-claim's node_run_id from the parent run (set
			// at acquire time in AcquireSubClaims) to its OWN child leaf
			// run. This makes the sub-claim resolvable from the leaf by
			// `node_run_id = its own dispatch id`, so the leaf-dispatch
			// path can read back the persisted `producer_candidate_handle`
			// and carry it onto the wire. It also keeps the
			// fanout_partition RunScope closure walk correct
			// (`child_execution.go::SettleChildren` loads each
			// sub-claim's run by node_run_id to find the partition scope to
			// close). Runs after CreateChildRun so the FK target exists.
			if p.SubClaimHandleID != (shared.UUID{}) {
				if args.Persist.ClaimHandles() == nil {
					return nil, fmt.Errorf("DispatchChildren: partition %q carries sub-claim %s but no claim-handle table is wired",
						p.PartitionKey, p.SubClaimHandleID)
				}
				if err := args.Persist.ClaimHandles().UpdateNodeRunID(ctx, p.SubClaimHandleID, runID, tx); err != nil {
					return nil, fmt.Errorf("DispatchChildren: link sub-claim %s to child run %q: %w",
						p.SubClaimHandleID, p.PartitionKey, err)
				}
			}
			// Delegation shape: the absorbed entry already succeeded, so
			// the children dispatch via the cascade — stale-mark each run
			// for the scheduler's sweep.
			if in.EntryAbsorbed {
				if err := args.Persist.Nodes().MarkStaleForCascade(ctx, runID, in.FrameID, tx); err != nil {
					return nil, fmt.Errorf("DispatchChildren: stale-mark run %s: %w", runID, err)
				}
			}
			out = append(out, DispatchedChild{
				RunID:        runID,
				RunScopeID:   childScopeID,
				NodeID:       c.NodeID,
				PartitionKey: p.PartitionKey,
			})
		}
	}
	return out, nil
}

// ChildSettlementInput is the input to SettleChildren. `Policy.Kind`
// discriminates the settlement shape:
//
//   - `carry_verbatim` — delegation. The Exit* / Writeback fields are
//     consumed; the single settlement child's writeback bytes carry
//     verbatim to the parent run. Canonicalization guarantees N=1
//     (`carry_verbatim_requires_single_child` at template
//     registration), so there is no aggregation to compute.
//   - empty / anything else — fan-out claim-chain aggregation. The
//     ParentClaimHandleID / ChildClaimHandleID / ChildOutcome fields
//     are consumed; the policy actually applied is the snapshot on the
//     parent claim-handle row (`strict` | `threshold` | `best_effort`
//     | `first`, defaulting to strict), not this field — the caller is
//     a per-child terminal that has no business overriding the
//     snapshot taken at acquisition time.
type ChildSettlementInput struct {
	// Policy selects the settlement shape (see above). Delegation
	// passes {Kind: carry_verbatim}; the claim-chain caller passes the
	// zero value.
	Policy spec.AggregationPolicy

	// --- carry-verbatim (delegation) shape ---

	// ExitRunID is the settlement child's run (the sub-graph exit's
	// leaf run that just reached its success terminal).
	ExitRunID shared.UUID
	// ExitNodeID / ExitNodeAlias / InstanceID carry the forensics
	// identity for the `subgraph.exit_carry` event.
	ExitNodeID    shared.UUID
	ExitNodeAlias string
	InstanceID    shared.UUID
	// Writeback is the exit's writeback bytes. Empty → legal no-op
	// (per the carry-rule: "if exit never runs … the parent's
	// writeback row remains empty"). Non-JSON bytes are rejected —
	// per @blessed-invariant 20 the bytes are never mangled or
	// logged; the round-trip through json.Unmarshal only enforces
	// the schema contract.
	Writeback json.RawMessage

	// --- claim-chain (fan-out) shape ---

	// ParentClaimHandleID is the parent claim handle whose children's
	// outcomes aggregate.
	ParentClaimHandleID shared.UUID
	// ChildClaimHandleID is the just-resolved child's claim handle —
	// recorded on the parent's per-outcome counters and excluded from
	// sibling cancellation.
	ChildClaimHandleID shared.UUID
	// ChildOutcome is the just-resolved child's Commit/Abandon
	// verdict. Doubles as the seed fallback when the parent row
	// carries no fan-out counters (legacy non-fan-out leaves that set
	// ParentClaimHandleID).
	//
	// Claimant note: the per-outcome counter bump on the PARENT row is
	// claimant-guarded on the parent row's ACTUAL holder (read under
	// the settlement's SELECT … FOR UPDATE), not on the child's
	// supervisor — under a ≥2-supervisor deployment the child's
	// terminal legitimately runs on a replica that does not hold the
	// parent handle. The parent settlement itself runs under
	// args.SupervisorID after a CAS takeover of the handle.
	ChildOutcome AggregateOutcome
	// ChildProducerMetadata is the producer-supplied metadata bytes
	// from the just-resolved child's base-protocol Commit response.
	// Surfaced verbatim (base64-encoded — JSON cannot carry raw bytes)
	// in the fan-out parent run's writeback under the
	// `producer_metadata` key, keyed by the child's partition key.
	// Recorded atomically with the child's terminal (the same per-child
	// record-at-terminal pattern as the per-outcome counters), so the
	// metadata survives a crash between the child's terminal and the
	// parent's settlement. Inert in rimsky per @blessed-invariant 20 —
	// never parsed, transformed, or logged. Empty on Abandon and for
	// producers that stamp nothing.
	ChildProducerMetadata []byte
}

// SettleChildren is the unified settle-children primitive — the single
// settlement path for both child-execution shapes. It fires on every
// child terminal: records the child's outcome, applies the aggregation
// policy, and — when the policy settles the parent — closes the child
// RunScope(s), writes the parent settlement, and fires the
// parent-settlement cascade bridge, all with the same transaction
// discipline as the pre-unification paths (every write runs inside the
// CALLER's tx; nothing here opens a transaction or escapes tx).
//
//	@concept: child-execution
//	@concept: run-scope
//	@concept: claim-tree
func SettleChildren(
	ctx context.Context, args RunArgs, tx persistence.Tx, in ChildSettlementInput,
) error {
	if in.Policy.Kind == spec.AggregationKindCarryVerbatim {
		return settleCarryVerbatim(ctx, args, tx, in)
	}
	return settleClaimChainAggregate(ctx, args, tx, in)
}

// settleCarryVerbatim is the delegation settlement shape: the single
// settlement child (the sub-graph exit, N=1 by canonicalization) has
// reached its success terminal, and its outcome IS the parent's
// settlement. Per the writeback carry-rule: "the parent's writeback IS
// whatever the exit produced; if exit never runs (e.g. an internal
// node failed and strict.cancel_siblings cancelled exit before it
// dispatched), the parent's writeback row remains empty (NULL
// writeback bytes)."
//
// Load-bearing property (carry-rule atomicity): the carry-writeback,
// the child execution context's closure (RunScopes().Close on the
// sub-graph scope), and the parent-settlement cascade bridge all
// commit in the caller's single transaction — the same boundary the
// pre-unification path had (exit's terminal write tx), neither widened
// nor narrowed. The aggregation engine in
// `state_propagation.go::PropagateFromChildState` still produces the
// parent's terminal state per the standard rule table; only the
// writeback content carries here.
//
// @blessed-invariant: exit-node-writeback flows to parent run writeback
// (the NodeAttributes().Upsert against the parent's row below is the
// carry site; downstream {{nodes.<calling-node>.attribute.<field>}}
// reads depend on it).
func settleCarryVerbatim(
	ctx context.Context, args RunArgs, tx persistence.Tx, in ChildSettlementInput,
) error {
	// NOTE: an empty writeback (exit terminated with no attribute map)
	// skips ONLY the attribute carry below. The rest of the settlement
	// — sub-graph RunScope close, OnRunScopeTerminal fan-out, the
	// parent-settlement cascade bridge, and the `subgraph.exit_carry`
	// forensics event — must still run, or the scope leaks open and the
	// lifecycle surface never observes the sub-graph's terminal.
	if args.Persist == nil {
		return fmt.Errorf("SettleChildren: Persist is required")
	}
	rt := args.Persist.RunTree()
	if rt == nil {
		return fmt.Errorf("SettleChildren: RunTree is required")
	}
	scopes := args.Persist.RunScopes()
	if scopes == nil {
		return fmt.Errorf("SettleChildren: RunScopes is required")
	}
	exit, err := rt.GetByID(ctx, tx, in.ExitRunID)
	if err != nil {
		return fmt.Errorf("SettleChildren: load exit run %s: %w", in.ExitRunID, err)
	}
	if exit == nil {
		return fmt.Errorf("SettleChildren: run %s not found", in.ExitRunID)
	}
	exitScope, err := scopes.GetByID(ctx, tx, exit.RunScopeID)
	if err != nil {
		return fmt.Errorf("SettleChildren: load exit run scope %s: %w", exit.RunScopeID, err)
	}
	if exitScope == nil || exitScope.ParentRunID == nil {
		// Exit has no parent — not a sub-graph internal. Caller error;
		// the primitive must not be invoked on non-sub-graph terminals.
		// Surface a precise error so callers don't silently miscarry.
		return fmt.Errorf("SettleChildren: run %s has no parent; not a sub-graph exit", in.ExitRunID)
	}
	var asMap map[string]any
	if len(in.Writeback) > 0 {
		if err := json.Unmarshal(in.Writeback, &asMap); err != nil {
			// Writeback bytes are not JSON-decodable. Per
			// @blessed-invariant 20 we MUST NOT mangle or log the bytes;
			// surface a typed error so the caller can fail the terminal at
			// the standard writeback validation gate.
			return fmt.Errorf("SettleChildren: exit writeback bytes not JSON-decodable: %w", err)
		}
	}
	parentRunID := *exitScope.ParentRunID
	parent, err := rt.GetByID(ctx, tx, parentRunID)
	if err != nil {
		return fmt.Errorf("SettleChildren: load parent run %s: %w", parentRunID, err)
	}
	if parent == nil {
		return fmt.Errorf("SettleChildren: parent run %s not found", parentRunID)
	}
	if args.Logger != nil {
		args.Logger.Info("subgraph: carry exit writeback to parent run",
			"exit_run_id", in.ExitRunID.String(),
			"parent_run_id", parentRunID.String(),
			"parent_node_id", parent.NodeID.String(),
			"writeback_field_count", len(asMap))
	}
	// The carry: the parent run's attribute row inherits exit's final
	// attribute map verbatim — opaque to rimsky per
	// @blessed-invariant 20. The parent run's node id maps to a
	// rimsky_nodes row whose attribute schema is the calling node's;
	// the caller has already validated exit's writeback shape against
	// exit's own schema, and a stricter calling-node schema is caught
	// by the post-carry validation at the parent's terminal commit
	// (@blessed-invariant 12). This makes the sub-graph's output
	// observable through the calling node's attribute surface per
	// concept:child-execution's "exit-node-writeback flows to parent
	// run writeback" rule.
	if args.Persist.NodeAttributes() == nil {
		return fmt.Errorf("SettleChildren: NodeAttributes is required")
	}
	if len(in.Writeback) > 0 {
		if err := args.Persist.NodeAttributes().Upsert(
			ctx, parent.RunID, parent.NodeID, asMap, tx,
		); err != nil {
			return fmt.Errorf("SettleChildren: upsert parent attributes: %w", err)
		}
	}
	// Close the child execution context atomically with the writeback
	// carry. Per concept:run-scope §"Lifecycle / RunScope closure": a
	// sub-graph RunScope is closed when the exit node terminates and
	// the carry-rule fires. closed_at marks the parent-run rendezvous
	// as having fired; subsequent AffirmNodeRunRow on this scope
	// returns ErrRunScopeClosed. Close() is idempotent — re-closing is
	// a no-op.
	//
	// @concept: run-scope
	if err := scopes.Close(ctx, tx, exit.RunScopeID); err != nil {
		return fmt.Errorf("SettleChildren: close sub-graph run scope %s: %w", exit.RunScopeID, err)
	}
	// Fire OnRunScopeTerminal to lifecycle subscribers for this
	// sub-graph RunScope, atomically with the close above. Resolve the
	// template spec via the two-step instance → template lookup. No-op
	// when the supervisor wasn't wired with lifecycle outbound (nil
	// LifecycleSubs / LifecyclePeersForSpec) or when the lookups fail —
	// the close is the load-bearing write and must not roll back on a
	// fan-out resolution miss. Nil tables (partial test wiring) skip the
	// best-effort fan-out the same way a lookup miss does.
	if instTbl, tplTbl := args.Persist.Instances(), args.Persist.Templates(); instTbl != nil && tplTbl != nil {
		if inst, err := instTbl.Get(ctx, exitScope.InstanceID, tx); err == nil && inst != nil {
			if tpl, err := tplTbl.GetByHash(ctx, inst.TemplateHash, tx); err == nil && tpl != nil {
				FanOutRunScopeEvent(ctx, args.Persist, args.LifecycleSubs,
					args.LifecyclePeersForSpec, tpl.Spec, exit.RunScopeID,
					exitScope.InstanceID, "subgraph_exit", tx)
			}
		}
	}
	// Parent-settlement cascade bridge: fire cascadeSubscribersStaleInTx
	// for the calling node so main-graph subscribers receive the
	// cascade when the sub-graph terminates. Without this, downstream
	// nodes that subscribe to the calling node never get marked stale
	// and never dispatch. Living INSIDE the settlement primitive means
	// no caller can settle without cascading (the historical absence
	// of this bridge from one settlement path is the defect class the
	// unification removes).
	//
	// @concept: cascade
	if args.Persist.Nodes() == nil {
		// The cascade bridge is load-bearing (TD-cascade-inside-settlement:
		// no caller may settle without cascading); a missing Nodes table
		// is a wiring bug, surfaced as an error rather than a panic.
		return fmt.Errorf("SettleChildren: Nodes table is required for the parent-settlement cascade bridge")
	}
	callingNodeRow, err := args.Persist.Nodes().Get(ctx, parent.NodeID, tx)
	if err != nil {
		return fmt.Errorf("SettleChildren: load calling node: %w", err)
	}
	if callingNodeRow != nil && callingNodeRow.FrameID != nil {
		// Synthesize the calling-node's settlement signal so the
		// subscriber-driven cascade walker can apply CEL predicates.
		// The exit's writeback has just propagated to the parent, so
		// the calling node is effectively terminal-success-changed
		// from the perspective of its main-graph subscribers.
		exitBridgeSig := signalpkg.Signal{
			Type: signalpkg.TypePath("terminal/success"),
			Payload: map[string]any{
				"changed":          true,
				"attributes_delta": orEmptyMap(asMap),
				"change_summary":   "subgraph_exit_carry",
			},
		}
		if err := cascadeSubscribersStaleInTx(ctx, args, tx,
			parent.NodeID, callingNodeRow.NodeType, parent.RunID,
			in.InstanceID, *callingNodeRow.FrameID, exitBridgeSig); err != nil {
			return fmt.Errorf("SettleChildren: cascade subscribers of calling node: %w", err)
		}
		// The wait-set rows the cascade walker just inserted are
		// gated on parent.RunID (the calling node's run). The
		// calling node is effectively settled at this point — the
		// carry-rule's writeback has landed and the run-tree's
		// state-propagation will transition it to a terminal state
		// — so drain the wait-set rows here so the freshly-stale
		// downstream subscribers can advance in this frame.
		if err := args.Persist.WaitSet().MarkDrainedBySender(ctx, *callingNodeRow.FrameID, parent.RunID, tx); err != nil {
			return fmt.Errorf("SettleChildren: drain wait-set for calling node: %w", err)
		}
	}
	// Forensics: emit `subgraph.exit_carry` for the carry-rule.
	if args.Persist.Events() == nil {
		return fmt.Errorf("SettleChildren: Events table is required for the exit-carry forensics record")
	}
	nodeID := in.ExitNodeID
	instanceID := in.InstanceID
	return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID:     &nodeID,
		InstanceID: &instanceID,
		Kind:       events.KindSubgraphExitCarry(),
		Payload: map[string]any{
			"parent_run_id":   parentRunID.String(),
			"exit_run_id":     in.ExitRunID.String(),
			"exit_node_alias": in.ExitNodeAlias,
			"outcome":         "fresh",
		},
	}, tx)
}

// settleClaimChainAggregate is the fan-out / claim-chain settlement
// shape. It records the just-resolved child's outcome on the parent
// claim handle, fires sibling cancellation per policy, then walks
// upward from the sub-claim's parent. At each level, the parent's
// resolution fires only when:
//
//  1. The parent's own holding subgraph (if any) has settled — every
//     `rimsky_claim_holders` row for the parent_claim_handle_id is
//     non-active. When the parent is itself held with co-holders still
//     working, the parent's normal `CheckAndFireResolution` path will
//     re-drive this walk later.
//  2. Every sub-claim row beneath it has resolved — either via the
//     standard Promote branch (state flips to `committed` / `abandoned`)
//     or via the held-durable preservation (rows linger with
//     `state = 'committed' AND lifetime = 'durable'` but do not block
//     the parent).
//
// Once those preconditions hold, the parent's aggregate Commit/Abandon
// decision is computed across ALL children's outcomes per the
// snapshotted `aggregation_policy` — not just the outcome of the
// just-resolved child. The aggregation rules mirror
// `runtime/run_tree.go::Aggregate` for run-state aggregation, mapped
// onto the Commit/Abandon binary the claim layer carries:
//
//	strict (default)        — any abandoned → Abandon; else Commit
//	threshold(max_failures) — abandoned > max_failures → Abandon; else Commit
//	best_effort             — committed > 0 → Commit; else Abandon
//	first                   — committed > 0 → Commit; else Abandon
//
// Counters (`expected_children_count`, `committed_children_count`,
// `abandoned_children_count`) are bumped atomically inside the same tx
// as each child's terminal Promote (`ResolveClaimHandleTerminal`), so
// the read here under SELECT … FOR UPDATE sees a consistent view.
func settleClaimChainAggregate(
	ctx context.Context, args RunArgs, tx persistence.Tx, in ChildSettlementInput,
) error {
	// Lock the parent row FIRST: every parent-row mutation below (the
	// producer_metadata merge, the per-outcome counter bump) and the
	// settlement decision all run under the same SELECT … FOR UPDATE,
	// serializing concurrently-settling siblings across supervisors.
	parent, err := args.ClaimHandles.LockForUpdate(ctx, in.ParentClaimHandleID, tx)
	if err != nil {
		return err
	}
	if parent == nil {
		// Already resolved (and reaped).
		return nil
	}
	// Surface the just-resolved child's base-Commit producer_metadata in
	// the parent run's writeback. Runs BEFORE every guard below —
	// including the claimant and state guards: this call is the only
	// one carrying the child's response body, and every re-drive path
	// (CheckAndFireResolution, a different supervisor's settlement)
	// re-enters without it — recording now, in the child's terminal tx,
	// is what makes the metadata durable even when the child resolves
	// under a supervisor that is not the parent handle's holder. The
	// write is an idempotent per-child-key merge, and the parent
	// claim-handle row's SELECT … FOR UPDATE (taken above) serializes
	// concurrent writers, protecting the read-merge-write from lost
	// updates.
	if err := recordChildCommitMetadata(ctx, args, tx, in, parent); err != nil {
		return fmt.Errorf("SettleChildren: record child producer_metadata: %w", err)
	}
	if parent.HolderSupervisorID == nil || parent.State != spec.ClaimHandleStateActive {
		// Already resolved — auto-terminal already fired on this row
		// (committed via durable promotion, or abandoned). Mirrors the
		// CheckAndFireResolution guard. (The migration-009 CHECK pair
		// makes the two conditions equivalent; both are checked for
		// defense in depth.)
		return nil
	}
	// Record the child's outcome on the parent's per-outcome counters.
	// Guarded on the parent row's ACTUAL holder, read under the FOR
	// UPDATE lock above (a CAS-equivalent claimant guard,
	// @blessed-invariant 4): under a ≥2-supervisor deployment the child's
	// terminal legitimately runs on a supervisor that does NOT hold the
	// parent handle — guarding the bump on the CHILD's supervisor would
	// silently drop the outcome from the aggregate and skew the parent's
	// Commit/Abandon verdict.
	outcomeKey := "commit"
	if in.ChildOutcome == AggregateAbandon {
		outcomeKey = "abandon"
	}
	if err := args.ClaimHandles.BumpChildOutcomeCount(ctx, in.ParentClaimHandleID, *parent.HolderSupervisorID, outcomeKey, 1, tx); err != nil {
		return fmt.Errorf("SettleChildren: BumpChildOutcomeCount: %w", err)
	}
	// Sibling cancellation per policy (`strict.cancel_siblings`); the
	// helper itself no-ops for every other policy shape.
	//
	// @concept: cancel-siblings
	if in.ChildOutcome == AggregateAbandon {
		if err := cancelInFlightSiblings(ctx, args, tx, in.ParentClaimHandleID, in.ChildClaimHandleID); err != nil {
			return fmt.Errorf("SettleChildren: cancelInFlightSiblings: %w", err)
		}
	}
	// Re-read the parent row (the FOR UPDATE lock above is still held in
	// this tx): the local copy predates this child's counter bump, and
	// the sibling-cancel walk may have bumped further outcomes through
	// its recursive settlements. The aggregate decision below must see
	// the post-bump counter view.
	{
		refreshed, err := args.ClaimHandles.Get(ctx, in.ParentClaimHandleID, tx)
		if err != nil {
			return fmt.Errorf("SettleChildren: re-read parent: %w", err)
		}
		if refreshed == nil || refreshed.HolderSupervisorID == nil || refreshed.State != spec.ClaimHandleStateActive {
			// The recursive cancel walk resolved the parent already.
			return nil
		}
		parent = refreshed
	}
	// Holding-subgraph guard: if the parent is itself a held claim with
	// active co-holders, defer parent resolution. The parent's normal
	// `CheckAndFireResolution` path will re-enter this walk after the
	// last holder transitions to non-active.
	holders, err := args.Persist.ClaimHolders().ListByClaimHandleID(ctx, in.ParentClaimHandleID, tx)
	if err != nil {
		return fmt.Errorf("SettleChildren: ListByClaimHandleID: %w", err)
	}
	for _, h := range holders {
		if h.State == persistence.ClaimHolderStateActive {
			// Parent's holding subgraph not yet complete; the
			// CheckAndFireResolution path will re-drive when the last
			// holder transitions. Skip parent resolution this round.
			return nil
		}
	}
	children, err := args.ClaimHandles.ListChildClaimHandles(ctx, in.ParentClaimHandleID, tx)
	if err != nil {
		return fmt.Errorf("SettleChildren: ListChildClaimHandles: %w", err)
	}
	// If any sub-claim is still active, the parent isn't ready to
	// resolve yet. Non-active children (committed / abandoned) are
	// treated as resolved and don't block the parent.
	//
	// @blessed-invariant 22: held-durable claim handles persist across
	// instance dispatches. A claim handle with `state = 'committed'
	// AND lifetime = 'durable'` is not deleted by holding-subgraph
	// auto-terminal; only by explicit operator action
	// (`DELETE /instances/{id}/assets/{alias}`) or instance termination
	// (`ReleaseHeldDurableClaims`). The orphan-claim reaper skips
	// non-active rows. The claim-chain settlement walk treats a
	// committed-durable child the same as a resolved-and-deleted
	// child: it does not block the parent from firing its own
	// auto-terminal. The child stays available for future
	// co-holdership via `holds:` until explicit release.
	for _, c := range children {
		if c.State == spec.ClaimHandleStateActive {
			return nil
		}
	}
	// The policy settles the parent — close the child execution
	// contexts. Fan-out partition RunScope closure (concept:run-scope
	// §"Lifecycle / RunScope closure"): when the aggregation walker
	// confirms every child sub-claim has resolved, the fanout_partition
	// RunScopes rooted at each child are eligible for closure — the
	// parent-run rendezvous has fired. Closing the partition RunScopes
	// atomically with the parent claim resolution ensures the
	// supervisor's lazy-allocation primitive (AffirmNodeRunRow) refuses
	// new in-flight rows in those scopes from this point onward — any
	// in-flight callbacks for those scopes route through the
	// determinism rule (callback.late_or_stale_run / ack-but-noop).
	//
	// Idempotent: Close() is a no-op on an already-closed RunScope, so
	// non-fan-out parents (committed-durable children without partition
	// RunScopes) and re-entry through the same chain are both safe.
	//
	// @concept: child-execution
	// @concept: run-scope
	if scopes := args.Persist.RunScopes(); scopes != nil {
		for _, c := range children {
			if c.NodeRunID == nil {
				continue
			}
			childRun, err := args.Persist.RunTree().GetByID(ctx, tx, *c.NodeRunID)
			if err != nil {
				return fmt.Errorf("SettleChildren: load child run %s: %w", c.NodeRunID, err)
			}
			if childRun == nil {
				continue
			}
			childScope, err := scopes.GetByID(ctx, tx, childRun.RunScopeID)
			if err != nil {
				return fmt.Errorf("SettleChildren: load child run scope %s: %w", childRun.RunScopeID, err)
			}
			// Only close fanout_partition scopes — those with a
			// non-empty partition_key. Non-fan-out children (e.g.
			// legacy callers that set ParentClaimHandleID on a
			// non-fan-out leaf) live in the same RunScope as the
			// parent and must not be closed here.
			if childScope == nil || childScope.PartitionKey == "" {
				continue
			}
			if err := scopes.Close(ctx, tx, childRun.RunScopeID); err != nil {
				return fmt.Errorf("SettleChildren: close partition scope %s: %w", childRun.RunScopeID, err)
			}
			// Fire OnRunScopeTerminal to lifecycle subscribers for this
			// fanout-partition RunScope, atomically with the close.
			// Resolve the template spec via the two-step instance →
			// template lookup. No-op when the supervisor wasn't wired
			// with lifecycle outbound or when the lookups miss — the
			// close is the load-bearing write.
			if instTbl, tplTbl := args.Persist.Instances(), args.Persist.Templates(); instTbl != nil && tplTbl != nil {
				if inst, err := instTbl.Get(ctx, childScope.InstanceID, tx); err == nil && inst != nil {
					if tpl, err := tplTbl.GetByHash(ctx, inst.TemplateHash, tx); err == nil && tpl != nil {
						FanOutRunScopeEvent(ctx, args.Persist, args.LifecycleSubs,
							args.LifecyclePeersForSpec, tpl.Spec, childRun.RunScopeID,
							childScope.InstanceID, "fanout_partition_terminal", tx)
					}
				}
			}
		}
	}
	// Aggregate across ALL children using the snapshotted policy +
	// per-outcome counters. `expected_children_count` reflects the
	// total fan-out width set at AcquireSubClaims time; committed +
	// abandoned reflect resolved children (each child's terminal
	// bumped its counter above). If for some reason the counters are
	// uninitialized (e.g. a non-fan-out leaf that happened to set
	// ParentClaimHandleID), fall back to the just-resolved child's
	// outcome — so we don't strand the parent.
	outcome := aggregateParentOutcome(parent, in.ChildOutcome)
	producerName := ""
	if parent.ProducerName != nil {
		producerName = *parent.ProducerName
	}
	// Terminal-resolution path (not dispatch-time acquisition): bare Get
	// — the parent claim was already bound at acquire time; no instance
	// context is threaded into the recursive resolution walk.
	producer, ok := args.StoreRegistry.Get(producerName)
	if !ok {
		return fmt.Errorf("SettleChildren: unknown producer %q", producerName)
	}
	// Lineage hint for the parent claim resolution. Same shape as the
	// held-claim path in CheckAndFireResolution.
	parentHint := ClaimLineageHint{
		ProducerName: producerName,
		VersionID:    parent.VersionID,
		NodeID:       parent.HolderNodeID,
	}
	if parent.FrameID != nil {
		parentHint.FrameID = *parent.FrameID
	}
	if parent.NodeRunID != nil {
		parentHint.RunID = *parent.NodeRunID
	}
	if acquirer, aErr := args.Persist.Nodes().Get(ctx, parent.HolderNodeID, tx); aErr == nil && acquirer != nil {
		parentHint.InstanceID = acquirer.InstanceID
	}
	// Settlement takeover (cross-supervisor fan-out): the supervisor
	// resolving the LAST child drives the parent's settlement, but the
	// parent handle may still carry the original acquirer's supervisor
	// id. Deferring to that holder would stall the chain — nothing
	// re-drives a fan-out parent after its last child resolves on
	// another replica. Take over the handle CAS-guarded from the holder
	// read under this tx's FOR UPDATE lock (@blessed-invariant 4), so
	// the engine's claimant-guarded Promote below runs under the
	// supervisor actually firing the settlement. No-op write avoided on
	// the single-supervisor common path.
	if *parent.HolderSupervisorID != args.SupervisorID {
		if err := args.ClaimHandles.ReassignHolderSupervisor(
			ctx, in.ParentClaimHandleID, *parent.HolderSupervisorID, args.SupervisorID, tx,
		); err != nil {
			return fmt.Errorf("SettleChildren: settlement takeover of parent %s (holder %s → %s): %w",
				in.ParentClaimHandleID, *parent.HolderSupervisorID, args.SupervisorID, err)
		}
	}
	// Write the parent settlement: the producer verb + the
	// claimant-guarded row promotion run through the unified
	// claim-handle resolution engine. Recursion upward happens by
	// forwarding ParentClaimHandleID through ResolveClaimHandleTerminal:
	// the engine's step 6 invokes `SettleChildren` on a non-nil parent,
	// so the chain walks the entire claim tree without an explicit
	// recursive call here. The parent run's downstream cascade bridge
	// fires from the run-state settlement walk
	// (`PropagateIfChildAfterTerminal`), preserving the pre-unification
	// transaction boundary exactly.
	if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
		ClaimHandleID:       in.ParentClaimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              HeldTerminal,
		Outcome:             outcome,
		Producer:            producer,
		Scope:               []byte(parent.ClaimScopeData),
		Address:             []byte(parent.Address),
		Lifetime:            parent.Lifetime,
		CandidateHandle:     parent.ProducerCandidateHandle,
		ProducerName:        producerName,
		LineageHint:         parentHint,
		ParentClaimHandleID: parent.ParentClaimHandleID,
	}); err != nil {
		return err
	}
	return nil
}

// recordChildCommitMetadata surfaces a child's base-Commit
// producer_metadata bytes in the fan-out parent run's writeback row.
// The writeback shape extends with a `producer_metadata` object keyed
// by the child's partition key (falling back to the child claim-handle
// id when the child has no partition scope), each value the child's
// metadata bytes base64-encoded — the canonical JSON mapping for proto
// bytes; the writeback row is JSON and cannot carry raw bytes. The
// bytes are inert in rimsky per @blessed-invariant 20 — encoded
// verbatim, never parsed or logged.
//
// Caller MUST hold the parent claim-handle row's SELECT … FOR UPDATE:
// the read-merge-write against the parent's attribute row is serialized
// by that lock (lost-update protection across concurrently-settling
// siblings). Recording happens in the CHILD's terminal tx — the same
// record-at-child-terminal durability discipline as the per-outcome
// counters — so a crash between the child's terminal and the parent's
// settlement loses nothing.
//
// No-op on Abandon (that verb carries no response body), on empty
// metadata, and on a parent claim handle without a run to write back
// to (no writeback surface exists).
func recordChildCommitMetadata(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	in ChildSettlementInput, parent *persistence.ClaimHandleRow,
) error {
	if in.ChildOutcome != AggregateCommit || len(in.ChildProducerMetadata) == 0 {
		return nil
	}
	if parent.NodeRunID == nil {
		// No parent run row — nothing to surface the metadata on. A
		// fan-out parent always carries its acquiring run's id; this
		// guard covers legacy non-fan-out rows only.
		return nil
	}
	attrs := args.Persist.NodeAttributes()
	if attrs == nil {
		return fmt.Errorf("NodeAttributes is required")
	}
	key, err := childPartitionWritebackKey(ctx, args, tx, in.ChildClaimHandleID)
	if err != nil {
		return err
	}
	existing, err := attrs.GetByRun(ctx, *parent.NodeRunID, tx)
	if err != nil {
		return fmt.Errorf("load parent writeback row: %w", err)
	}
	merged := map[string]any{}
	if existing != nil && existing.Data != nil {
		merged = existing.Data
	}
	meta, _ := merged["producer_metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta[key] = base64.StdEncoding.EncodeToString(in.ChildProducerMetadata)
	merged["producer_metadata"] = meta
	if err := attrs.Upsert(ctx, *parent.NodeRunID, parent.HolderNodeID, merged, tx); err != nil {
		return fmt.Errorf("upsert parent writeback row: %w", err)
	}
	return nil
}

// childPartitionWritebackKey resolves the writeback key for a child's
// producer_metadata entry: the child run's partition key (the fan-out
// case), falling back to the child claim-handle id for children
// without a partitioned scope so the entry is never silently dropped.
func childPartitionWritebackKey(
	ctx context.Context, args RunArgs, tx persistence.Tx, childID shared.UUID,
) (string, error) {
	fallback := childID.String()
	child, err := args.ClaimHandles.Get(ctx, childID, tx)
	if err != nil {
		return "", fmt.Errorf("load child claim handle %s: %w", childID, err)
	}
	if child == nil || child.NodeRunID == nil {
		return fallback, nil
	}
	childRun, err := args.Persist.RunTree().GetByID(ctx, tx, *child.NodeRunID)
	if err != nil {
		return "", fmt.Errorf("load child run %s: %w", child.NodeRunID, err)
	}
	if childRun == nil {
		return fallback, nil
	}
	scope, err := args.Persist.RunScopes().GetByID(ctx, tx, childRun.RunScopeID)
	if err != nil {
		return "", fmt.Errorf("load child run scope %s: %w", childRun.RunScopeID, err)
	}
	if scope == nil || scope.PartitionKey == "" {
		return fallback, nil
	}
	return scope.PartitionKey, nil
}

// aggregateParentOutcome computes the parent's Commit/Abandon verdict
// from the snapshotted aggregation policy + the per-outcome counters on
// the parent claim_handle row. Falls back to `seedOutcome` when the
// counters indicate "no fan-out children expected" (legacy callers that
// set ParentClaimHandleID on a non-fan-out leaf) so we never strand
// the parent.
//
// Rule table (mapped from spec §State aggregation rules onto the
// Commit/Abandon binary):
//
//	strict (default)        — any abandoned → Abandon; else Commit
//	threshold(max_failures) — abandoned > max_failures → Abandon; else Commit
//	best_effort             — committed > 0 → Commit; else Abandon
//	first                   — committed > 0 → Commit; else Abandon
//	(unknown kind)          — defaults to strict for safety
func aggregateParentOutcome(parent *persistence.ClaimHandleRow, seedOutcome AggregateOutcome) AggregateOutcome {
	if parent == nil {
		return seedOutcome
	}
	if parent.ExpectedChildrenCount == 0 {
		// Non-fan-out parent on this row — no aggregation needed; carry
		// the seed.
		return seedOutcome
	}
	policy, err := persistence.UnmarshalAggregationPolicy(parent.AggregationPolicy)
	if err != nil || policy.Kind == "" {
		// Missing / malformed policy → default to strict.
		policy = spec.AggregationPolicy{Kind: spec.AggregationKindStrict}
	}
	committed := parent.CommittedChildrenCount
	abandoned := parent.AbandonedChildrenCount
	switch policy.Kind {
	case spec.AggregationKindStrict:
		if abandoned > 0 {
			return AggregateAbandon
		}
		return AggregateCommit
	case spec.AggregationKindThreshold:
		if abandoned > policy.MaxFailures {
			return AggregateAbandon
		}
		return AggregateCommit
	case spec.AggregationKindBestEffort, spec.AggregationKindFirst:
		if committed > 0 {
			return AggregateCommit
		}
		return AggregateAbandon
	default:
		// Unknown kind: safest is strict semantics.
		if abandoned > 0 {
			return AggregateAbandon
		}
		return AggregateCommit
	}
}
