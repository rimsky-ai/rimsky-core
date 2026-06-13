// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// subgraph_dispatch.go — E6. Sub-graph dispatch: entry-absorption,
// internal-cascade fire on entry success, and the exit-node writeback
// carry-rule.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sub-graphs / Invocation semantics + §Sub-graphs / Aggregation for sub-graphs.
//
//	@concept: sub-graph
//	@concept: delegation
//	@concept: run-scope
//
// Architectural shape:
//
//   - Canonicalizer-phase: a template node with `delegate: <graph>` is
//     absorbed (D2 step 4). The calling node's `rimsky_nodes` row carries
//     the entry's executor + the entry's sub-graph-internal declarations,
//     merged with what the caller declared externally. Subscription edges
//     in non-entry internal nodes that point to the entry alias get a
//     canonicalizer-set marker (`resolves_via_calling_node: true`) so the
//     runtime cascade walker resolves the edge to THIS invocation's calling-
//     node-run (not a shared-singleton "entry alias node").
//
//   - Dispatch-phase: the calling node dispatches like any other run; the
//     parent run's executor IS the entry executor (absorbed at canonical-
//     ization, so the standard runner_dispatch.go path runs unchanged). On
//     the parent run's terminal verdict:
//
//   - Failure / parked → parent transitions directly to failed/parked;
//     no internal cascade. Sub-claims the entry acquired Abandon per the
//     standard auto-terminal path.
//
//   - Success (`fresh_changed` / `fresh_unchanged`) → parent stays
//     `running` with transition reason `ReasonSubGraphInternalCascadeFired`.
//     Fire the internal cascade: stale-mark non-entry internal nodes
//     whose subscription targets the entry alias (per-invocation
//     resolution via the calling node's run id).
//
//   - Exit-node terminal: copy exit's writeback to the parent's writeback
//     row in the same transaction as exit's terminal write. The carry is
//     the carry-verbatim settlement shape of the unified settle-children
//     primitive (`child_execution.go::SettleChildren`, which carries the
//     `@blessed-invariant: exit-node-writeback` annotation at the carry
//     site); `applyTerminalCompleteSubgraphExit` below is the thin
//     runner-tx wrapper.
//
// Both markers the runtime routes on (`IsSubgraphEntryAbsorbed` on
// calling nodes, `IsSubgraphExit` on exit nodes) are emitted at
// canonicalization (see graph/node/template_validator_graphs.go::flatten),
// so the runtime routes on `acq.NodeDef` alone without per-terminal
// template lookups.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// SubgraphInternalCascadeArgs is the dispatch input for the entry-success
// fire of a sub-graph's internal cascade. Caller: `runner_terminal.go`
// success branch when the calling node's template has
// `Delegate != ""` (i.e. it's a sub-graph caller).
type SubgraphInternalCascadeArgs struct {
	// CallingNodeRunID is the parent run that just terminated successfully
	// at the absorbed-entry phase. Children dispatched by this call carry
	// `parent_run_id = CallingNodeRunID`.
	CallingNodeRunID shared.UUID
	// CallingNodeID is the calling node's `rimsky_nodes.id`. Used for
	// instance-scoped node lookup (`Nodes().ListByInstance`).
	CallingNodeID shared.UUID
	// InstanceID — instance scope for the internal-node lookups.
	InstanceID shared.UUID
	// FrameID — frame this invocation belongs to.
	FrameID shared.UUID
	// Template is the deploy-time template definition (already
	// canonicalized). Caller is responsible for loading it (typically
	// via `args.Persist.Templates().Get(hash)`).
	Template *node.TemplateSpec
	// DelegateGraphName is the value of the calling node's `Delegate`
	// field (the sub-graph's graph name). Empty means the calling node
	// is not a sub-graph caller — caller should not invoke this helper
	// in that case.
	DelegateGraphName string
}

// SubgraphInternalCascade collects the non-entry internal nodes that
// must be stale-marked as children of the calling-node parent run. The
// caller is the supervisor's terminal handler; it enqueues one
// `rimsky_node_runs` child row per returned node (via
// `runtime.CreateChildRun`), then drives them through the normal
// dispatch + state-propagation flow.
//
// Pure function: takes the in-memory template + dispatch context,
// returns the set of internal nodes to stale-mark. The actual stale-
// mark INSERTs happen at the caller site (single tx with the entry's
// terminal write, so the parent transitions `running → running` with
// `ReasonSubGraphInternalCascadeFired` and the children dispatch in
// the same frame).
func SubgraphInternalCascade(in SubgraphInternalCascadeArgs) ([]node.TemplateNodeDef, error) {
	if in.Template == nil {
		return nil, fmt.Errorf("SubgraphInternalCascade: Template is required")
	}
	if in.DelegateGraphName == "" {
		return nil, fmt.Errorf("SubgraphInternalCascade: DelegateGraphName is required")
	}
	// Locate the delegate graph in the template's `graphs:` block. The
	// flatten step in the canonicalizer (template_validator_graphs.go)
	// concatenates every graph's nodes into TemplateSpec.Nodes; the
	// per-graph membership info still lives on the GraphSpec list.
	for _, g := range in.Template.Graphs {
		if g.Name != in.DelegateGraphName {
			continue
		}
		// Filter to non-entry internal nodes. Exit is included (it's a
		// child like any other internal; the carry-rule fires at exit's
		// terminal).
		out := make([]node.TemplateNodeDef, 0, len(g.Nodes))
		for _, n := range g.Nodes {
			if n.Type == g.Entry {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
	return nil, fmt.Errorf(
		"SubgraphInternalCascade: delegate graph %q not declared in template",
		in.DelegateGraphName)
}

// SubgraphParentSuccessCascade is the entry-point the supervisor's
// terminal handler invokes when a sub-graph-caller parent run's
// executor terminal is a success (`fresh_changed` / `fresh_unchanged`).
//
// The function:
//
//  1. Looks up the delegate graph's internal non-entry nodes.
//  2. Records the parent's state-transition reason as
//     `ReasonSubGraphInternalCascadeFired` (`running → running`).
//  3. Returns the internal nodes the caller must stale-mark as
//     children of `CallingNodeRunID`.
//
// The caller is responsible for the per-child INSERTs (via
// `persistence.RunTreeTable.CreateChildRun`) and the per-child
// stale-mark via the existing `MarkStaleForCascade` helper. Putting
// those in this file would require pulling in the cascade walker;
// keeping them at the call site preserves the file's cohesion as
// "sub-graph dispatch primitives" and matches the project's "feature-
// not-layer" cold-read convention.
//
// State-machine cross-check: the
// `cascade.NextStateParent(NodeStateRunning, ReasonSubGraphInternalCascadeFired)`
// → `NodeStateRunning` is the load-bearing transition. Verified by
// `foundation/cascade/state_test.go::TestNextStateParent_SubGraphInternalCascadeFired_RunningOnly`.
func SubgraphParentSuccessCascade(
	in SubgraphInternalCascadeArgs,
) (internalNodes []node.TemplateNodeDef, transitionReason cascade.TransitionReason, err error) {
	internals, err := SubgraphInternalCascade(in)
	if err != nil {
		return nil, cascade.TransitionReason{}, err
	}
	// Validate the running → running transition is legal under the
	// `subgraph_internal_cascade_fired` reason. Surfaces precise errors
	// to the caller when somehow invoked from a non-running parent.
	if _, err := cascade.NextStateParent(cascade.NodeStateRunning, cascade.ReasonSubGraphInternalCascadeFired); err != nil {
		if !cascade.IsParentAggregateOK(err) {
			return nil, cascade.TransitionReason{}, fmt.Errorf(
				"SubgraphParentSuccessCascade: state-machine rejects running→running under subgraph_internal_cascade_fired: %w", err)
		}
	}
	return internals, cascade.ReasonSubGraphInternalCascadeFired, nil
}

// IsSubgraphCaller reports whether a template node delegates to a
// sub-graph. Cheap predicate for the supervisor's terminal handler:
// when the parent run's node-def has Delegate != "", the success
// branch routes through `SubgraphParentSuccessCascade` instead of the
// standard run-tree aggregation (which has no children to aggregate
// at that point).
//
// Pre-v1: when an internal child has its own `delegate:` (nested
// sub-graph), the same predicate applies — the inner cascade fires at
// the inner caller's entry-success terminal. Recursion is bounded by
// the canonicalizer's `subgraph_recursion_unsupported` rejection.
func IsSubgraphCaller(def *node.TemplateNodeDef) bool {
	if def == nil {
		return false
	}
	return def.Delegate != ""
}

// IsSubgraphExit reports whether a template node is the declared exit
// of its containing sub-graph. Test-only convenience helper that walks
// the in-memory template's GraphSpecs to find a non-main graph whose
// `exit:` names `nodeType`. The runtime no longer calls this — the
// canonicalizer emits `TemplateNodeDef.IsSubgraphExit` and the
// supervisor's terminal handler reads that marker via
// `isSubgraphExitNode(acq)`; keeping the predicate here so test
// fixtures can assert the canonicalization invariant directly against
// a template they constructed.
func IsSubgraphExit(tmpl *node.TemplateSpec, nodeType string) bool {
	if tmpl == nil {
		return false
	}
	for _, g := range tmpl.Graphs {
		if g.Name == spec.MainGraphName {
			continue
		}
		if g.Exit == nodeType {
			return true
		}
	}
	return false
}

// applyTerminalCompleteSubgraphCaller is the runner-tx wiring for the
// sub-graph caller's success-branch terminal. Per spec §Sub-graphs /
// Invocation semantics step 4 (success), the parent run stays
// `running` (state-machine self-transition under
// `ReasonSubGraphInternalCascadeFired`) and the sub-graph's non-entry
// internal nodes dispatch as children of the calling-node parent run.
//
// Lineage emission shape: a sub-graph caller produces TWO `leaf_run`
// rows in `table:rimsky_lineage` per dispatch.
//
//  1. The first row fires here, at internal-cascade-fire time, carrying
//     `terminal_kind: "subgraph_call"` + `state: "running"`. The
//     calling node has just absorbed its entry's terminal; the parent
//     run stays running while the internal cascade runs, so the row
//     captures the inputs (params_snapshot_hash, attributes_hash,
//     held_claims, parent_run_id) at internal-cascade-fire — the
//     "what the caller saw" moment.
//  2. The second row fires later from the standard `applyTerminalComplete`
//     path on the parent's aggregation terminal (driven by the
//     last internal child's terminal via PropagateFromChildState),
//     carrying `terminal_kind: "complete"` + `state: "fresh"`. The row
//     captures the post-aggregation outcome (settling_signal_type
//     resolved from the children's aggregate, `changed` reflecting the
//     final verdict).
//
// Downstream consumers can pair the two rows by `run_id` (both rows
// reference the same calling-run UUID) and discriminate on
// `terminal_kind`.
//
// OpenLineage subscriber consequence: `MakeLeafRunEvent` (in
// `subscribers/openlineage/emitter.go`) maps every leaf_run row to a
// single OpenLineage `COMPLETE` event keyed on `runId =
// instance_id[+child_key]`. The two-row shape therefore produces TWO
// `COMPLETE` events for the same `runId` — once at the
// `subgraph_call` row (carrying `rimsky.terminal_kind = "subgraph_call"`
// in the rimsky facet) and once at the `complete` row (carrying
// `rimsky.terminal_kind = "complete"`). Downstream OpenLineage
// consumers that treat `COMPLETE` as terminal should discriminate by
// `rimsky.terminal_kind`: the first event reports inputs at
// internal-cascade-fire time; the second reports the post-aggregation
// outcome. The pair is intentional (the calling node's inputs are
// distinct from the post-aggregation outcome); it is not a duplicate
// emission.
//
// Locks acquired by the caller (including the absorbed entry's
// claims/holds) STAY HELD across the internal cascade; they release
// when the parent's aggregated terminal fires (driven by
// `state_propagation.go::PropagateFromChildState` on the last internal
// child's terminal, which then routes back through the standard
// `applyTerminalComplete` path on the parent — at which point
// `IsSubgraphEntryAbsorbed` is false on the parent's aggregation-
// terminal write because the marker only applies to the entry-
// executor terminal).
//
// The function does NOT release locks, does NOT transition the
// rimsky_nodes row to fresh, and does NOT fire the outer cascade.
// All three are deferred to the parent's aggregation terminal.
//
// Pre-v1 simplification: the per-child run-row creation +
// stale-mark + dispatch wiring lands as a paired E6-runtime-cascade
// follow-up alongside the scenario tests. The helper records the
// transition reason and emits a structured event so operator-facing
// observability (control-api `/events`, dashboard event stream)
// surfaces the sub-graph cascade fire; the child-run wave is then
// driven by the runtime cascade walker's normal subscription-graph
// traversal (subscribers of the entry alias resolve to this calling
// node per `ResolvesViaCallingNode`).
//
//	@concept: sub-graph
//	@concept: run-scope
func applyTerminalCompleteSubgraphCaller(
	ctx context.Context, args RunArgs, acq *acquisition,
	merged map[string]any, t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {
	// Resolve the settling signal type-path. Post-2026-05-23 the
	// on_executor_complete handler's always_propagate /
	// never_propagate / by_changed resolves retired with
	// concept:lifecycle-handler — cascade-fire is subscriber-driven
	// (per concept:signal). Selectivity on changed-vs-unchanged is now
	// declared receiver-side via CEL `when: payload.changed`. Post-
	// Pass 5 the run row carries settling_signal_type instead of the
	// retired last_outcome enum.
	settlingSig := "terminal/success"

	// Validate the running → running parent transition under the
	// subgraph_internal_cascade_fired reason is legal — the state
	// machine accepts it via NextStateParent's parentAggregateOK
	// sentinel.
	if _, err := cascade.NextStateParent(cascade.NodeStateRunning, cascade.ReasonSubGraphInternalCascadeFired); err != nil {
		if !cascade.IsParentAggregateOK(err) {
			return nil, fmt.Errorf(
				"applyTerminalCompleteSubgraphCaller: state-machine rejects running→running under subgraph_internal_cascade_fired: %w",
				err)
		}
	}

	// Resolve the sub-graph's non-entry internal nodes. The helper walks
	// the deploy-time template; the rimsky_nodes rows for the internal
	// nodes were created by `provisionInstanceTx` at instance creation,
	// so the dispatch below just needs to (a) create one child run row
	// per internal node (parent_run_id = the calling run) and (b)
	// stale-mark the rimsky_nodes row so the next dispatcher tick picks
	// it up. The template load happens inside the outer tx — a stale
	// read of an immutable template is harmless and avoids a separate
	// round-trip.
	var internalNodes []node.TemplateNodeDef
	var tmplSpec *node.TemplateSpec
	{
		inst, err := args.Persist.Instances().Get(ctx, acq.InstanceID, tx)
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: load instance: %w", err)
		}
		if inst != nil {
			tmpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			if err != nil {
				return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: load template: %w", err)
			}
			if tmpl != nil {
				tmplSpec = &tmpl.Spec
			}
		}
	}
	if tmplSpec != nil {
		nodes, err := SubgraphInternalCascade(SubgraphInternalCascadeArgs{
			CallingNodeRunID:  acq.DispatchID,
			CallingNodeID:     acq.NodeID,
			InstanceID:        acq.InstanceID,
			FrameID:           acq.FrameID,
			Template:          tmplSpec,
			DelegateGraphName: acq.NodeDef.Delegate,
		})
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: resolve internals: %w", err)
		}
		internalNodes = nodes
	}

	// Primary state-mutation work runs inline in the caller's outer tx.
	// Persist the parent run's writeback (the absorbed entry's outcome)
	// so any in-graph subscriber that reads the calling node's
	// attributes sees the entry's bytes.
	if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: upsert attributes: %w", err)
	}
	// Record the transition reason on the run-tree row so the
	// observability layer surfaces the cascade-fire. The run stays
	// `running`; only the settling_signal_type moves.
	if err := args.Persist.RunTree().UpdateStateAndOutcome(ctx, tx, acq.DispatchID,
		cascade.NodeStateRunning, &settlingSig); err != nil {
		return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: update run-tree: %w", err)
	}
	// Sub-graph internal-cascade dispatch: delegation is the degenerate
	// child-execution shape — ONE partition (the sub-graph RunScope,
	// empty partition key) holding one child run per non-entry internal
	// node, entry absorbed (the parent's executor terminal WAS the
	// absorbed entry's, so each child run is stale-marked for the
	// cascade walker / SweepReady to pick up). The per-partition
	// RunScope + child-row allocation runs in the unified
	// `child_execution.go::DispatchChildren` primitive, inside this
	// caller's tx. State-propagation aggregates the children's
	// terminals back to this parent on completion via
	// PropagateFromChildState.
	if len(internalNodes) > 0 {
		instNodes, err := args.Persist.Nodes().ListByInstance(ctx, acq.InstanceID, tx)
		if err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: list instance nodes: %w", err)
		}
		byType := make(map[string]persistence.NodeRow, len(instNodes))
		for _, n := range instNodes {
			byType[n.NodeType] = n
		}
		children := make([]ChildRunSpec, 0, len(internalNodes))
		for _, def := range internalNodes {
			nrow, ok := byType[def.Type]
			if !ok {
				return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: internal node %q has no rimsky_nodes row in instance %s",
					def.Type, acq.InstanceID.String())
			}
			children = append(children, ChildRunSpec{
				NodeID:         nrow.ID,
				Executor:       def.Executor,
				RequiredStores: node.RequiredStores(def),
			})
		}
		if _, err := DispatchChildren(ctx, args, tx, ChildExecutionInput{
			ParentRunID:       acq.DispatchID,
			ParentRunScopeID:  acq.RunScopeID,
			InstanceID:        acq.InstanceID,
			FrameID:           acq.FrameID,
			ChildGraphName:    acq.NodeDef.Delegate,
			AggregationPolicy: spec.AggregationPolicy{},
			EntryAbsorbed:     true,
			Partitions:        []PartitionDescriptor{{PartitionKey: ""}},
			Children:          children,
		}); err != nil {
			return nil, fmt.Errorf("applyTerminalCompleteSubgraphCaller: %w", err)
		}
	}
	// Emit the cascade-fire event for observability + audit. Two events
	// fire here: the legacy `subgraph_internal_cascade_fired`
	// (transitioning observers can still rely on the existing kind
	// string) and the post-2026-05-16 forensics kind
	// `subgraph.dispatched` summarizing the per-invocation dispatch
	// across child runs. Payloads carry rimsky-side identifiers only;
	// @blessed-invariant 20 + 21 preserved.
	childAliases := make([]string, 0, len(internalNodes))
	for _, def := range internalNodes {
		childAliases = append(childAliases, def.Type)
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindSubgraphInternalCascadeFired(),
		Payload: map[string]any{
			"delegate_graph":       acq.NodeDef.Delegate,
			"calling_run_id":       acq.DispatchID.String(),
			"settling_signal_type": settlingSig,
			"changed":              t.Changed,
			"transition_reason":    cascade.ReasonSubGraphInternalCascadeFired.Kind,
			"child_count":          len(internalNodes),
		},
	}, tx); err != nil {
		return nil, err
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindSubgraphDispatched(),
		Payload: map[string]any{
			"caller_run_id":  acq.DispatchID.String(),
			"caller_node_id": acq.NodeID.String(),
			"subgraph_name":  acq.NodeDef.Delegate,
			"child_aliases":  childAliases,
			"child_count":    len(internalNodes),
		},
	}, tx); err != nil {
		return nil, err
	}

	if args.Logger != nil {
		args.Logger.Info("subgraph: parent run staying running for internal cascade",
			"calling_run_id", acq.DispatchID.String(),
			"node_type", acq.NodeType,
			"delegate", acq.NodeDef.Delegate,
			"settling_signal_type", settlingSig)
	}
	// Post-commit: emit leaf-run lineage record for the sub-graph caller
	// terminal. EmitLeafRunLineage opens its own tx so must run after
	// the outer state-mutation tx commits.
	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			RunID:              dispatchID,
			NodeID:             acq.NodeID,
			State:              string(cascade.NodeStateRunning),
			SettlingSignalType: settlingSig,
			Changed:            t.Changed,
			TerminalKind:       "subgraph_call",
			NodeAlias:          acq.NodeType,
			ExecutorName:       acq.Executor,
			TemplateHash:       acq.TemplateHash,
			Params:             acq.InstanceParams,
			AttributesMerged:   acq.MergedAttributes,
			HeldClaims:         HeldClaimsForLineage(acq),
			ParentRunID:        scope.ParentRunID,
			ChildKey:           scope.PartitionKey,
			SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
	}
	return post, nil
}

// applyTerminalCompleteSubgraphExit is the runner-tx wiring for the
// sub-graph exit-node's success-branch terminal — a thin wrapper over
// the unified settle-children primitive
// (`child_execution.go::SettleChildren`). Per spec §Sub-graphs /
// Writeback carry-rule for exit, the parent run's writeback IS
// whatever the exit produced: delegation settlement is the
// carry-verbatim degenerate case — the single settlement child's
// (the exit's) outcome carries verbatim to the parent, the sub-graph
// RunScope closes, and the parent-settlement cascade bridge fires,
// all inside the primitive and inside this caller's tx so the carry
// commits atomically with the rest of the exit terminal's standard
// release path. Template validation guarantees the N=1 shape
// (carry_verbatim_requires_single_child).
//
//	@concept: sub-graph
//	@concept: delegation
//	@concept: run-scope
func applyTerminalCompleteSubgraphExit(
	ctx context.Context, args RunArgs, acq *acquisition,
	merged map[string]any, tx persistence.Tx,
) error {
	// Encode the merged attributes back to bytes so SettleChildren's
	// JSON-decodable check has something to validate. Per
	// @blessed-invariant 20 we do not transform the bytes — we ROUND
	// TRIP through json.Marshal to match the persistence-layer
	// representation, then the primitive validates and records.
	//
	// An empty attribute map produces a nil Writeback, and the primitive
	// runs the FULL settlement minus the attribute upsert: the sub-graph
	// RunScope close, OnRunScopeTerminal fan-out, parent-settlement
	// cascade bridge, in-tx wait-set drain, and the `subgraph.exit_carry`
	// forensics event all still fire. Early-returning here would skip the
	// primitive entirely and leak the scope open (the defect class the
	// settle-children unification removes).
	var wb []byte
	if len(merged) > 0 {
		encoded, err := json.Marshal(merged)
		if err != nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphExit: encode writeback: %w", err)
		}
		wb = encoded
	}
	return SettleChildren(ctx, args, tx, ChildSettlementInput{
		Policy:        spec.AggregationPolicy{Kind: spec.AggregationKindCarryVerbatim},
		ExitRunID:     acq.DispatchID,
		ExitNodeID:    acq.NodeID,
		ExitNodeAlias: acq.NodeType,
		InstanceID:    acq.InstanceID,
		Writeback:     wb,
	})
}

// isSubgraphExitNode is the runtime-side predicate for the carry-rule
// branch of `applyTerminalComplete`. It consults the canonicalizer-set
// `IsSubgraphExit` marker on the run's node-def, mirroring the way
// `IsSubgraphEntryAbsorbed` is consulted on the caller side.
//
// Under the 2026-05-20 per-run attribute-keying spec the carry-rule is
// LOAD-BEARING: without it persisting, downstream
// `{{nodes.<calling-node>.attribute.<field>}}` reads return
// `ErrMissingSource`. Routing on a static marker (set at
// canonicalization, persisted alongside the template) eliminates the
// transient-failure mode that the previous template-lookup predicate
// carried — the marker is either there or it isn't; there is no
// "lookup failed" branch that could silently downgrade a load-bearing
// carry into a no-op.
//
// Returns false on a nil acquisition or nil NodeDef (defensive guards
// against test-double shapes; the canonicalizer-driven path always
// populates NodeDef for live runs).
func isSubgraphExitNode(acq *acquisition) bool {
	if acq == nil || acq.NodeDef == nil {
		return false
	}
	return acq.NodeDef.IsSubgraphExit
}
