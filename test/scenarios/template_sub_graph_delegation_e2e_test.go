// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-template-sub-graph-delegation acceptance proof.
//
// Per the spec story
// .ok-planner/specs/2026-06-08-design-corpus-bootstrap-design.md
// (STORY-template-sub-graph-delegation): a template author declares a
// node with `delegate: <graph-name>` plus a separately-named graph
// providing entry/exit nodes; rimsky dispatches the sub-graph as the
// delegating node's execution unit; once the sub-graph settles, the
// delegating node settles with the sub-graph's terminal outcome
// propagated back.
//
// This test drives the assembled rimsky stack through the harness (the
// full supervisor + scheduler + control-api stack against real Postgres
// via testcontainers — same stack the platform ships). The delivery
// surface is the template DSL's `delegate:` field on a `TemplateNodeDef`
// + a sibling `GraphSpec` providing the named sub-graph; sub-graph
// composition is a single-template construct (the runtime resolves
// `delegate:` against the template's `Graphs:` block — see
// runtime/subgraph_dispatch.go::SubgraphInternalCascade). Two scenarios
// pin BOTH halves of the Falsifier brief end-to-end:
//
//	TestTemplateSubGraphDelegation_SuccessPropagates — happy-path
//	propagation. The delegate calling node only settles to fresh AFTER
//	the sub-graph's exit node settles. Asserted by:
//
//	  - (a) Sub-graph RunScope is created (graph_name = <delegate>) once
//	    the calling node's entry-success terminal fires
//	    applyTerminalCompleteSubgraphCaller — proves dispatch routes
//	    through the sub-graph machinery, not the standard cascade.
//	  - (b) The internal nodes (inner-mid, inner-exit) dispatch INSIDE
//	    the sub-graph RunScope, not the main RunScope — proves the
//	    sub-graph is the calling node's execution unit.
//	  - (c) ORDERING: while the sub-graph is in flight (inner-mid is
//	    not yet terminal) the calling node's run row state is
//	    'running', NOT 'fresh'. Falsifies "delegate node settles
//	    before the sub-graph does" by directly witnessing the calling
//	    node held-non-settled during sub-graph execution.
//	  - (d) Outcome propagation: after the sub-graph exit reaches
//	    terminal, the calling node's run row aggregates to state =
//	    'fresh' (success outcome propagated via walkUpwards under
//	    strict aggregation).
//
//	TestTemplateSubGraphDelegation_ErrorPropagates — failure
//	propagation: an internal sub-graph node errors and the parent's
//	delegate node settles to NodeStateFailed reflecting the sub-graph's
//	terminal-error outcome. Asserted by:
//
//	  - (a) Inner-mid errors with an unknown class — default policy is
//	    give_up, so the inner-mid run reaches NodeStateFailed and the
//	    sub-graph's strict aggregator surfaces strict_failed.
//	  - (b) The parent calling node's run row aggregates to state =
//	    'failed' — proves the sub-graph's terminal-error outcome
//	    propagates to the parent (Falsifier brief "sub-graph's
//	    terminal outcome doesn't propagate to the parent").
//	  - (c) Parent's settling_signal_type carries the strict_failed
//	    aggregate signal — pins that the failure came from the
//	    sub-graph's strict aggregator (not from some unrelated internal
//	    error during the cascade-fire path).
//
// Why the test asserts on rimsky_node_runs.state directly rather than
// scenario.WaitForNodeState for the caller: a sub-graph caller's
// settlement happens via state_propagation.go::walkUpwards (driven by
// PropagateIfChildAfterTerminal at the inner exit's post-terminal
// site), which calls RunTree.UpdateStateAndOutcome on the caller's run
// row. UpdateStateAndOutcome only updates `state` + `settling_signal_type`
// — it does NOT emit a `terminal/success` event for the caller, since
// the caller's own runner_terminal path already fired its entry-success
// emit at internal-cascade-fire time. WaitForNodeState requires a
// `terminal/success` event to confirm Fresh (see harness::hasRunEvent),
// so it would never observe the caller's eventual settlement. Querying
// the run row's state column directly is the correct observation
// surface for a sub-graph caller's final settlement.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestTemplateSubGraphDelegation_SuccessPropagates pins the happy-path
// propagation: a calling node delegating to a sub-graph stays
// non-settled while the sub-graph runs and settles to NodeStateFresh
// only AFTER the sub-graph's exit terminates. The sub-graph's success
// outcome (fresh) propagates back to the calling node.
//
// Falsifier brief addressed: "The delegate node settles before the
// sub-graph does" — directly falsified by the held-non-settled window
// assertion below, which witnesses the calling node's run state
// staying 'running' while at least one sub-graph internal is still
// in flight.
func TestTemplateSubGraphDelegation_SuccessPropagates(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: The calling node's executor IS the absorbed entry's executor
	// (per the canonicalizer's D2 step 4 entry-absorption); the stub
	// dispatches against `caller` are the entry's executions. Per-leaf
	// Delay on the internals creates a natural window between the
	// calling-node entry-success terminal and the sub-graph exit's
	// terminal — wide enough that the held-non-settled snapshot below
	// is robust to clock jitter / commit-latency skew. A delegating-
	// node-pre-settles bug would land the calling node at state='fresh'
	// during the in-flight window, which the ordering gate detects.
	const innerDelay = 600 * time.Millisecond
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-mid").
		Success(map[string]any{"ok": true}, true, "mid").
		Delay(innerDelay)
	h.Stub.WhenType("inner-exit").
		Success(map[string]any{"done": true}, true, "exit").
		Delay(innerDelay)

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-subgraph-delegation-success", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						// @deliberate: The delegating node: `delegate: worker` names the
						// sub-graph. Canonicalization absorbs the worker's
						// entry executor onto this node so the standard
						// dispatch path runs the entry; the runtime then
						// routes the success terminal through
						// applyTerminalCompleteSubgraphCaller per
						// concept:sub-graph.
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						openAttrs,
					),
				},
			},
			{
				// @deliberate: The named sub-graph: entry/exit declared per the
				// concept:sub-graph contract. inner-entry is the
				// absorbed entry (its executor lives on the calling
				// node); inner-mid + inner-exit dispatch as children
				// of the calling-node run in the sub-graph RunScope.
				Name:  "worker",
				Entry: "inner-entry",
				Exit:  "inner-exit",
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-entry", Executor: "stub"},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-mid", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*"},
							},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-mid", Type: "terminal/*"},
							},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-story-subgraph-delegation-success", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")
	exitNode := h.FindNode(iid, "inner-exit")
	require.NotNil(t, exitNode, "inner-exit node missing")
	midNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, midNode, "inner-mid node missing")

	// @deliberate: (a) Sub-graph RunScope is created.
	// applyTerminalCompleteSubgraphCaller inserts a rimsky_run_scopes row
	// for the delegate graph at the calling node's entry-success
	// terminal — proves the dispatch routed through the sub-graph
	// machinery (not the standard non-sub-graph terminal path, which
	// would settle the calling node directly to fresh).
	mainScopeID := h.GetMainRunScopeID(iid)
	require.Eventually(t, func() bool {
		var subScopes int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_run_scopes
			 WHERE instance_id = $1
			   AND graph_name = 'worker'
			   AND id <> $2
		`, []any{iid, mainScopeID}, &subScopes)
		return subScopes >= 1
	}, 60*time.Second, 50*time.Millisecond,
		"sub-graph RunScope (graph_name='worker') must be created at the calling-node entry-success terminal "+
			"(applyTerminalCompleteSubgraphCaller's RunScope INSERT)")

	// @deliberate: (b) Internal nodes dispatch INSIDE the sub-graph
	// RunScope. Falsifier complement: if the delegating node weren't
	// actually running the sub-graph as its execution unit, inner-mid /
	// inner-exit would either never run or would run in the main scope.
	for _, internal := range []struct {
		typ    string
		nodeID shared.UUID
	}{
		{"inner-mid", midNode.ID},
		{"inner-exit", exitNode.ID},
	} {
		require.Eventually(t, func() bool {
			var inSubgraph int
			h.QueryRowSQL(`
				SELECT COUNT(*) FROM rimsky_node_runs r
				  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
				 WHERE r.node_id = $1 AND rs.graph_name = 'worker'
			`, []any{internal.nodeID}, &inSubgraph)
			return inSubgraph >= 1
		}, 60*time.Second, 50*time.Millisecond,
			"%s must run inside the sub-graph RunScope (graph_name='worker') — "+
				"the sub-graph is the delegating node's execution unit", internal.typ)
	}

	// @deliberate: (c) ORDERING: caller held NON-SETTLED while sub-graph
	// in flight. THE LOAD-BEARING ASSERTION for this story's Falsifier brief
	// ("delegate node settles before the sub-graph does").
	//
	// At the calling node's entry-success terminal,
	// applyTerminalCompleteSubgraphCaller sets the caller's run row
	// state to 'running' and fires the internal cascade. The caller's
	// run state MUST remain 'running' until the sub-graph's children
	// aggregate to terminal via state_propagation.go::walkUpwards.
	//
	// Poll-window mechanics: we wait until at least one sub-graph
	// internal run exists in the main scope graph rs.graph_name='worker'
	// AND inner-exit has not yet reached terminal state (state != 'fresh').
	// During that window the caller's main-scope run row state MUST be
	// 'running'. A delegate-pre-settles bug would land state='fresh' on
	// the caller in this window — caught here.
	heldWitnessed := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		// @deliberate: Is the sub-graph in flight? Defined as: at least one
		// rimsky_node_runs row in the worker scope whose phase is not
		// yet terminal (= still pending/active/held/parked or whose
		// state column hasn't yet flipped to a terminal value).
		var inflightInternals int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE rs.graph_name = 'worker'
			   AND rs.instance_id = $1
			   AND r.phase IN ('pending','active','held','parked')
		`, []any{iid}, &inflightInternals)

		// @deliberate: Has the sub-graph exit settled? Once exit's run reaches
		// phase='completed' the carry-rule has fired and the caller's
		// aggregation will follow imminently — close the window.
		var exitTerminal int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_node_runs
			 WHERE node_id = $1
			   AND phase = 'completed'
		`, []any{exitNode.ID}, &exitTerminal)

		if inflightInternals >= 1 && exitTerminal == 0 {
			// @deliberate: In the held-window. Read the caller's run row state.
			var callerState string
			h.QueryRowSQL(`
				SELECT COALESCE(state, 'fresh')
				  FROM rimsky_node_runs
				 WHERE node_id = $1
				 ORDER BY enqueued_at DESC
				 LIMIT 1
			`, []any{callerNode.ID}, &callerState)
			require.NotEqual(t, string(cascade.NodeStateFresh), callerState,
				"calling-node run row settled to 'fresh' while a sub-graph internal was still in flight "+
					"(%d in-flight internals) — delegate must NOT settle before the sub-graph does",
				inflightInternals)
			require.NotEqual(t, string(cascade.NodeStateFailed), callerState,
				"calling-node run row settled to 'failed' while a sub-graph internal was still in flight "+
					"(%d in-flight internals) — delegate must NOT settle before the sub-graph does",
				inflightInternals)
			heldWitnessed = true
		}
		if exitTerminal >= 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.True(t, heldWitnessed,
		"never observed the calling node held non-settled while sub-graph internals were in flight; "+
			"the %s per-leaf inner-delay should have created a wide enough window for the 25ms poll. "+
			"If this fails without a delegate-pre-settles bug, widen innerDelay.", innerDelay)

	// @deliberate: (d) Outcome propagation: caller settles to fresh after
	// exit. Once the sub-graph exit terminates (carry-rule fires; the
	// internal cascade completes), state_propagation.go::walkUpwards
	// aggregates the sub-graph children's terminal states to the
	// calling node's run under strict aggregation. All-success children
	// → parent fresh. The calling node's run row state column must
	// reach 'fresh' (no terminal/success event is emitted for the
	// caller — see file-level docstring — so we poll the run row state
	// directly rather than scenario.WaitForNodeState).
	require.True(t,
		h.WaitForNodeState(exitNode.ID, cascade.NodeStateFresh, 60*time.Second),
		"sub-graph exit must reach fresh — its terminal fires the carry-rule "+
			"that propagates the sub-graph's outcome to the calling node")

	require.Eventually(t, func() bool {
		var callerState string
		h.QueryRowSQL(`
			SELECT COALESCE(state, 'fresh')
			  FROM rimsky_node_runs
			 WHERE node_id = $1
			 ORDER BY enqueued_at DESC
			 LIMIT 1
		`, []any{callerNode.ID}, &callerState)
		return callerState == string(cascade.NodeStateFresh)
	}, 60*time.Second, 100*time.Millisecond,
		"calling node's run row must aggregate to 'fresh' after the sub-graph settles — "+
			"sub-graph's success outcome must propagate to the parent via "+
			"runtime/state_propagation.go::walkUpwards under strict aggregation")
}

// TestTemplateSubGraphDelegation_ErrorPropagates pins the
// failure-propagation half of acceptance: a sub-graph internal node
// errors and the parent calling node settles to NodeStateFailed
// reflecting the sub-graph's terminal-error outcome.
//
// Falsifier brief addressed: "the sub-graph's terminal outcome doesn't
// propagate to the parent". A pre-settle-to-fresh bug or a swallowed-
// error bug would land the calling node at state='fresh'; this test's
// assertion that the calling node's run row reaches state='failed'
// pins the error-propagation path through strict aggregation.
//
// Mechanics: inner-mid errors with an unknown class. The default policy
// on an unknown class is immediate give_up (lib/graph/node/policy.go),
// so inner-mid's run transitions to NodeStateFailed. The default
// aggregation on a sub-graph parent is strict; the strict aggregator
// projects parent state to Failed with signal
// terminal/error/aggregate/strict_failed (runtime/run_tree.go::aggregateStrict).
// PropagateFromChildState walks the run-tree upward — from inner-mid's
// run, up via the sub-graph RunScope's ParentRunID to the calling
// node's run — and the calling node's run row state column transitions
// to 'failed'.
func TestTemplateSubGraphDelegation_ErrorPropagates(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @constraint: caller (= absorbed entry) succeeds; inner-mid errors with an
	// unknown class so the default-policy give_up fires immediately.
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-mid").Error("subgraph_doom", map[string]any{"why": "internal failure"})
	// @constraint: inner-exit is scripted to succeed but must NEVER run — strict
	// aggregation with cancel-siblings retires it. Scripting it
	// defensively ensures any accidental fire still produces a
	// deterministic terminal (rather than a stub-mismatch hang) so the
	// test fails on the propagation assertion rather than on stub state.
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-subgraph-delegation-error", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						openAttrs,
					),
				},
			},
			{
				Name:  "worker",
				Entry: "inner-entry",
				Exit:  "inner-exit",
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-entry", Executor: "stub"},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-mid", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-entry", Type: "terminal/*"},
							},
							// @deliberate: No error_types: declared — default policy on
							// an unknown class is give_up (immediate
							// terminal failure). Strict aggregation
							// projects to parent Failed.
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-mid", Type: "terminal/*"},
							},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-story-subgraph-delegation-error", map[string]any{})

	callerNode := h.FindNode(iid, "caller")
	require.NotNil(t, callerNode, "caller node missing")
	midNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, midNode, "inner-mid node missing")
	// @deliberate: inner-exit's row is created by provisionInstanceTx (canonicalized
	// sub-graph nodes get rimsky_nodes rows on instance create) — confirm
	// the row exists so a missing-node bug is caught at setup time
	// rather than as a mysterious behavioural difference downstream. The
	// node is not otherwise referenced because default strict aggregation
	// settles the parent immediately on a child failure WITHOUT
	// cancelling in-flight siblings, so inner-exit may run anyway; the
	// Falsifier-relevant property is the parent's settlement, asserted
	// in (b) below.
	require.NotNil(t, h.FindNode(iid, "inner-exit"), "inner-exit node missing")

	// @deliberate: (a) Sub-graph internal errors. The calling node's
	// entry-success fires the internal cascade; the
	// internal cascade dispatches inner-mid; inner-mid errors with an
	// unknown class; default policy give_up fires; inner-mid's run
	// transitions to NodeStateFailed.
	require.True(t,
		h.WaitForNodeState(midNode.ID, cascade.NodeStateFailed, 60*time.Second),
		"inner-mid must reach NodeStateFailed (default give_up policy on unknown error class)")

	// @deliberate: (b) Sub-graph's terminal-error outcome propagates to
	// the parent. THE LOAD-BEARING ASSERTION for this story's Falsifier brief
	// ("sub-graph's terminal outcome doesn't propagate to the parent").
	// PropagateFromChildState walks from inner-mid's run through the
	// sub-graph RunScope's ParentRunID to the calling node's run, and
	// the strict aggregator (default for sub-graphs) projects the
	// calling node's run to 'failed' with strict_failed.
	//
	// Why the run-row state read (not WaitForNodeState): see the
	// file-level docstring — a sub-graph caller's settlement happens
	// via UpdateStateAndOutcome with no terminal-event emit, and
	// WaitForNodeState's Fresh-state gate also won't observe a Failed
	// terminal via the event-required path for Fresh. Reading the
	// run row state column directly is the canonical observation for
	// a sub-graph caller's aggregated settlement.
	require.Eventually(t, func() bool {
		var callerState string
		h.QueryRowSQL(`
			SELECT COALESCE(state, 'fresh')
			  FROM rimsky_node_runs
			 WHERE node_id = $1
			 ORDER BY enqueued_at DESC
			 LIMIT 1
		`, []any{callerNode.ID}, &callerState)
		return callerState == string(cascade.NodeStateFailed)
	}, 90*time.Second, 100*time.Millisecond,
		"calling node's run row must aggregate to 'failed' — the sub-graph's terminal-error "+
			"outcome must propagate to the parent via strict aggregation "+
			"(runtime/state_propagation.go::walkUpwards + runtime/run_tree.go::aggregateStrict)")

	// @deliberate: Pin the SIGNAL on the caller's run row: strict aggregator
	// projects settling_signal_type=terminal/error/aggregate/strict_failed
	// from a child Failed. Without this, the parent might fail for an
	// unrelated reason (e.g. an internal-error during the cascade-fire
	// path) and the outcome wouldn't actually be the sub-graph's.
	var callerSettlingSig string
	h.QueryRowSQL(`
		SELECT COALESCE(settling_signal_type, '')
		  FROM rimsky_node_runs
		 WHERE node_id = $1
		 ORDER BY enqueued_at DESC
		 LIMIT 1
	`, []any{callerNode.ID}, &callerSettlingSig)
	require.Equal(t,
		"terminal/error/aggregate/strict_failed", callerSettlingSig,
		"caller run row's settling_signal_type must carry the strict_failed aggregate signal "+
			"(aggregateStrict's projection from inner-mid's failure → parent Failed)")
}
