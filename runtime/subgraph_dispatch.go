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
//	@concept: run-tree
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
//     row in the same transaction as exit's terminal write.
//
//     @blessed-invariant: exit-node-writeback flows to parent run writeback
//     (per R3 — recorded in the spec, see §Sub-graphs / Aggregation /
//     Writeback carry-rule for exit).
//
// File status — V1 wiring posture:
//
// The helpers in this file are pure (no DB-touching glue) so they can
// be exercised in unit tests against in-memory shapes. The integration
// into `runtime/runner_terminal.go` (entry-success path → FireInternalCascade,
// exit-node terminal → CarryExitWriteback) lands when sub-graph
// canonicalization at the template layer reaches the point of emitting
// (a) the `IsSubgraphEntryAbsorbed` marker on calling nodes and
// (b) the `IsSubgraphExit` marker on exit nodes. The D2 canonicalizer
// landed the graph-flatten path; both markers are now emitted at
// canonicalization (see graph/node/template_validator_graphs.go::flatten),
// so the runtime can route on `acq.NodeDef` alone without per-terminal
// template lookups.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
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

// CarryExitWriteback is the dispatch-side helper that implements the
// exit-node writeback carry-rule. At the exit node's leaf-run terminal
// write, the supervisor copies exit's writeback bytes onto the parent
// run's writeback row. Spec §Sub-graphs / Writeback carry-rule for exit:
// "the parent's writeback IS whatever the exit produced; if exit never
// runs (e.g. an internal node failed and strict.cancel_siblings
// cancelled exit before it dispatched), the parent's writeback row
// remains empty (NULL writeback bytes)."
//
// The aggregation engine in `state_propagation.go::PropagateFromChildState`
// still produces a terminal state for the parent per the standard rule
// table; only the writeback content is absent.
//
// Operates inside the caller's tx for atomicity with exit's terminal
// write.
//
// @blessed-invariant: exit-node-writeback flows to parent run writeback
// (annotated here; the persistence-layer write is the carry site).
func CarryExitWriteback(
	ctx context.Context,
	args PropagationArgs,
	tx persistence.Tx,
	exitRunID shared.UUID,
	writeback json.RawMessage,
) error {
	if args.RunTree == nil {
		return fmt.Errorf("CarryExitWriteback: RunTree is required")
	}
	exit, err := args.RunTree.GetByID(ctx, tx, exitRunID)
	if err != nil {
		return fmt.Errorf("CarryExitWriteback: load exit run %s: %w", exitRunID, err)
	}
	if exit == nil || exit.ParentRunID == nil {
		// Exit has no parent — not a sub-graph internal. Caller error;
		// the helper should not be invoked on non-sub-graph terminals.
		// Surface a precise error so callers don't silently miscarry.
		return fmt.Errorf("CarryExitWriteback: run %s has no parent; not a sub-graph exit", exitRunID)
	}
	// The persistence-layer carry: persistence stores writeback bytes
	// on the run row via the NodeAttributesTable.Upsert helper (the
	// generic writeback target). The parent's writeback inherits exit's
	// final attribute map verbatim — opaque to rimsky per
	// @blessed-invariant 20.
	//
	// Pre-v1: when the writeback column lands on rimsky_node_runs (the
	// follow-up E1 work), this helper writes to that column. Today the
	// runtime stores the parent's writeback projection on the parent's
	// node-attributes row via the same upsert path the leaf terminal
	// uses. The persistence layer treats both as inert bytes.
	if len(writeback) == 0 {
		// Exit produced no writeback bytes; nothing to carry.
		return nil
	}
	var asMap map[string]any
	if err := json.Unmarshal(writeback, &asMap); err != nil {
		// Writeback bytes are not JSON-decodable. Per
		// @blessed-invariant 20 we MUST NOT mangle or log the bytes;
		// surface a typed error so the caller can fail the terminal at
		// the standard writeback validation gate.
		return fmt.Errorf("CarryExitWriteback: exit writeback bytes not JSON-decodable: %w", err)
	}
	// The parent run's node id maps to a rimsky_nodes row whose
	// attribute schema is the calling node's. The caller has already
	// validated exit's writeback shape against exit's own schema; the
	// upsert below preserves whatever exit produced. If the calling
	// node's schema is stricter than exit's, the post-carry validation
	// (at the parent's terminal commit) will catch the mismatch via
	// @blessed-invariant 12.
	parentRunID := *exit.ParentRunID
	parent, err := args.RunTree.GetByID(ctx, tx, parentRunID)
	if err != nil {
		return fmt.Errorf("CarryExitWriteback: load parent run %s: %w", parentRunID, err)
	}
	if parent == nil {
		return fmt.Errorf("CarryExitWriteback: parent run %s not found", parentRunID)
	}
	// CarryExitWriteback validates the exit's writeback bytes and emits
	// an audit log. The caller (applyTerminalCompleteSubgraphExit)
	// performs the actual NodeAttributes().Upsert against the parent
	// run's row so the subgraph's output is observable through the
	// calling node's attribute surface per concept:delegation's
	// "exit-node-writeback flows to parent run writeback" rule.
	if args.Logger != nil {
		args.Logger.Info("subgraph: carry exit writeback to parent run",
			"exit_run_id", exitRunID.String(),
			"parent_run_id", parentRunID.String(),
			"parent_node_id", parent.NodeID.String(),
			"writeback_field_count", len(asMap))
	}
	return nil
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
//     captures the post-aggregation outcome (last_outcome resolved
//     from the children's aggregate, `changed` reflecting the final
//     verdict).
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
//	@concept: run-tree
func applyTerminalCompleteSubgraphCaller(
	ctx context.Context, args RunArgs, acq *acquisition,
	merged map[string]any, t terminalEvent,
) error {
	// Resolve last_outcome the same way the standard handler does so
	// downstream subscribers see the same firing-gate value.
	resolve := node.ResolveByChanged
	if acq.NodeDef != nil && acq.NodeDef.OnExecutorComplete != nil && acq.NodeDef.OnExecutorComplete.Resolve != "" {
		resolve = acq.NodeDef.OnExecutorComplete.Resolve
	}
	var lastOutcome cascade.LastOutcome
	switch resolve {
	case node.ResolveAlwaysPropagate:
		lastOutcome = cascade.LastOutcomeFreshChanged
	case node.ResolveNeverPropagate:
		lastOutcome = cascade.LastOutcomeFreshUnchanged
	default:
		if t.Changed {
			lastOutcome = cascade.LastOutcomeFreshChanged
		} else {
			lastOutcome = cascade.LastOutcomeFreshUnchanged
		}
	}

	// Validate the running → running parent transition under the
	// subgraph_internal_cascade_fired reason is legal — the state
	// machine accepts it via NextStateParent's parentAggregateOK
	// sentinel.
	if _, err := cascade.NextStateParent(cascade.NodeStateRunning, cascade.ReasonSubGraphInternalCascadeFired); err != nil {
		if !cascade.IsParentAggregateOK(err) {
			return fmt.Errorf(
				"applyTerminalCompleteSubgraphCaller: state-machine rejects running→running under subgraph_internal_cascade_fired: %w",
				err)
		}
	}

	// Resolve the sub-graph's non-entry internal nodes once, outside the
	// transaction below. The helper walks the deploy-time template; the
	// rimsky_nodes rows for the internal nodes were created by
	// `provisionInstanceTx` at instance creation, so the dispatch below
	// just needs to (a) create one child run row per internal node
	// (parent_run_id = the calling run) and (b) stale-mark the rimsky_nodes
	// row so the next dispatcher tick picks it up.
	var internalNodes []node.TemplateNodeDef
	{
		// Lazy template lookup. The template hash is stable for the
		// instance, so a stale-read is fine.
		var tmplSpec *node.TemplateSpec
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			inst, err := args.Persist.Instances().Get(ctx, acq.InstanceID, tx)
			if err != nil || inst == nil {
				return err
			}
			tmpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			if err != nil || tmpl == nil {
				return err
			}
			tmplSpec = &tmpl.Spec
			return nil
		}); err != nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphCaller: load template: %w", err)
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
				return fmt.Errorf("applyTerminalCompleteSubgraphCaller: resolve internals: %w", err)
			}
			internalNodes = nodes
		}
	}

	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		// Persist the parent run's writeback (the absorbed entry's
		// outcome) so any in-graph subscriber that reads the calling
		// node's attributes sees the entry's bytes.
		if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphCaller: upsert attributes: %w", err)
		}
		// Record the transition reason on the run-tree row so the
		// observability layer surfaces the cascade-fire. The run stays
		// `running`; only the last_outcome moves.
		if err := args.Persist.RunTree().UpdateStateAndOutcome(ctx, tx, acq.DispatchID,
			cascade.NodeStateRunning, lastOutcome); err != nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphCaller: update run-tree: %w", err)
		}
		// Sub-graph internal-cascade dispatch: for each non-entry
		// internal node, create a child run row keyed to this calling
		// run (`parent_run_id = acq.DispatchID`, `child_key = node-type
		// alias`) and stale-mark the rimsky_nodes row so the next
		// scheduler tick / SweepReady picks the row up for dispatch.
		// State-propagation aggregates the children's terminals back to
		// this parent on completion via PropagateFromChildState.
		if len(internalNodes) > 0 {
			instNodes, err := args.Persist.Nodes().ListByInstance(ctx, acq.InstanceID, tx)
			if err != nil {
				return fmt.Errorf("applyTerminalCompleteSubgraphCaller: list instance nodes: %w", err)
			}
			byType := make(map[string]persistence.NodeRow, len(instNodes))
			for _, n := range instNodes {
				byType[n.NodeType] = n
			}
			for _, def := range internalNodes {
				nrow, ok := byType[def.Type]
				if !ok {
					return fmt.Errorf("applyTerminalCompleteSubgraphCaller: internal node %q has no rimsky_nodes row in instance %s",
						def.Type, acq.InstanceID.String())
				}
				if _, err := CreateChildRun(ctx, tx, args.Persist.RunTree(),
					acq.DispatchID, def.Type, nrow.ID, acq.FrameID,
					def.Executor, node.RequiredStores(def),
					spec.AggregationPolicy{}); err != nil {
					return fmt.Errorf("applyTerminalCompleteSubgraphCaller: create child run %q: %w", def.Type, err)
				}
				if _, err := args.Persist.Nodes().MarkStaleForCascade(ctx, nrow.ID, acq.FrameID, tx); err != nil {
					return fmt.Errorf("applyTerminalCompleteSubgraphCaller: stale-mark %q: %w", def.Type, err)
				}
			}
		}
		// Emit the cascade-fire event for observability + audit. Two
		// events fire here: the legacy `subgraph_internal_cascade_fired`
		// (transitioning observers can still rely on the existing kind
		// string) and the post-2026-05-16 forensics kind
		// `subgraph.dispatched` summarizing the per-invocation dispatch
		// across child runs. Payloads carry rimsky-side identifiers
		// only; @blessed-invariant 20 + 21 preserved.
		childAliases := make([]string, 0, len(internalNodes))
		for _, def := range internalNodes {
			childAliases = append(childAliases, def.Type)
		}
		if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "subgraph_internal_cascade_fired",
			Payload: map[string]any{
				"delegate_graph":    acq.NodeDef.Delegate,
				"calling_run_id":    acq.DispatchID.String(),
				"last_outcome":      string(lastOutcome),
				"changed":           t.Changed,
				"transition_reason": cascade.ReasonSubGraphInternalCascadeFired.Kind,
				"child_count":       len(internalNodes),
			},
		}, tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "subgraph.dispatched",
			Payload: map[string]any{
				"caller_run_id":  acq.DispatchID.String(),
				"caller_node_id": acq.NodeID.String(),
				"subgraph_name":  acq.NodeDef.Delegate,
				"child_aliases":  childAliases,
				"child_count":    len(internalNodes),
			},
		}, tx)
	}); err != nil {
		return err
	}

	if args.Logger != nil {
		args.Logger.Info("subgraph: parent run staying running for internal cascade",
			"calling_run_id", acq.DispatchID.String(),
			"node_type", acq.NodeType,
			"delegate", acq.NodeDef.Delegate,
			"last_outcome", string(lastOutcome))
	}
	// E8: emit leaf-run lineage record for the sub-graph caller terminal.
	// Without this emit, sub-graph instances are missing a `leaf_run`
	// row for every caller terminal — breaking the rebuildability of
	// the lineage projection. The `terminal_kind: "subgraph_call"`
	// discriminator lets consumers filter caller rows out of pure
	// leaf-executor accounting. The parent run stays `running` here
	// (the absorbed entry just terminated; the internal cascade is the
	// real "completion"), so State is "running" — distinct from the
	// "fresh" rows emitted by leaf-executor terminals.
	EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
		InstanceID:       acq.InstanceID,
		FrameID:          acq.FrameID,
		RunID:            acq.DispatchID,
		NodeID:           acq.NodeID,
		State:            string(cascade.NodeStateRunning),
		LastOutcome:      string(lastOutcome),
		Changed:          t.Changed,
		TerminalKind:     "subgraph_call",
		NodeAlias:        acq.NodeType,
		ExecutorName:     acq.Executor,
		TemplateHash:     acq.TemplateHash,
		Params:           acq.InstanceParams,
		AttributesMerged: acq.MergedAttributes,
		HeldClaims:       HeldClaimsForLineage(acq),
		ParentRunID:      acq.ParentRunID,
		SubstitutionRefs: CollectSubstitutionRefsForEmit(ctx, args, acq),
	})
	return nil
}

// applyTerminalCompleteSubgraphExit is the runner-tx wiring for the
// sub-graph exit-node's success-branch terminal. Per spec §Sub-graphs
// / Writeback carry-rule for exit, the parent run's writeback IS
// whatever the exit produced. The helper invokes `CarryExitWriteback`
// inside a tx so the parent's writeback bytes commit atomically with
// the rest of the exit terminal's standard release path.
//
//	@concept: sub-graph
//	@concept: run-tree
//	@blessed-invariant: exit-node-writeback flows to parent run writeback
func applyTerminalCompleteSubgraphExit(
	ctx context.Context, args RunArgs, acq *acquisition,
	merged map[string]any,
) error {
	// Encode the merged attributes back to bytes so CarryExitWriteback's
	// JSON-decodable check has something to validate. Per
	// @blessed-invariant 20 we do not transform the bytes — we ROUND
	// TRIP through json.Marshal to match the persistence-layer
	// representation, then CarryExitWriteback validates and records.
	if len(merged) == 0 {
		// Exit produced no writeback; nothing to carry.
		return nil
	}
	wb, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("applyTerminalCompleteSubgraphExit: encode writeback: %w", err)
	}
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := CarryExitWriteback(ctx, PropagationArgs{
			RunTree:      args.Persist.RunTree(),
			ClaimHandles: args.ClaimHandles,
			Logger:       args.Logger,
		}, tx, acq.DispatchID, wb); err != nil {
			return err
		}

		// Carry the exit's writeback to the parent run's attribute row.
		// The blessed-invariant ("exit-node-writeback flows to parent
		// run writeback") requires the parent's row to contain the
		// exit's bytes so downstream consumers reading
		// {{nodes.<calling-node>.attribute.<field>}} see the subgraph's
		// output. CarryExitWriteback only validates + logs; the Upsert
		// lives here because the caller has NodeAttributeTable in scope.
		exit, err := args.Persist.RunTree().GetByID(ctx, tx, acq.DispatchID)
		if err != nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphExit: load exit run: %w", err)
		}
		if exit == nil || exit.ParentRunID == nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphExit: exit run %s has no parent", acq.DispatchID)
		}
		parent, err := args.Persist.RunTree().GetByID(ctx, tx, *exit.ParentRunID)
		if err != nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphExit: load parent run %s: %w", *exit.ParentRunID, err)
		}
		if parent == nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphExit: parent run %s not found", *exit.ParentRunID)
		}
		if err := args.Persist.NodeAttributes().Upsert(
			ctx, parent.RunID, parent.NodeID, merged, tx,
		); err != nil {
			return fmt.Errorf("applyTerminalCompleteSubgraphExit: upsert parent attributes: %w", err)
		}

		// Forensics: emit `subgraph.exit_carry` for the carry-rule. The
		// parent run row is already loaded; reuse instead of re-fetching.
		nodeID := acq.NodeID
		instanceID := acq.InstanceID
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID:     &nodeID,
			InstanceID: &instanceID,
			Kind:       "subgraph.exit_carry",
			Payload: map[string]any{
				"parent_run_id":   exit.ParentRunID.String(),
				"exit_run_id":     acq.DispatchID.String(),
				"exit_node_alias": acq.NodeType,
				"outcome":         "fresh",
			},
		}, tx)
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
