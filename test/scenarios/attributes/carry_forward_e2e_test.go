// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-attribute-carry-forward proof — drives three sequential
// dispatches of one stateful node in a single RunScope and observes the
// schema-default-then-prior-writeback shape (count 0 → 1 → 2 on the
// incoming bag, count 1, 2, 3 on the writeback). Then invokes a
// sub-graph and observes the same node-kind starts fresh (count 0) in
// the sub-graph RunScope — the RunScope boundary blocks carry-forward.
//
// The real loop_counter handler IS the value-delivering component; the
// test wraps it with a thin recorder so the per-dispatch incoming
// attribute bag is observable without modifying the loop_counter
// itself. This is a downstream observer of the dispatch's
// ExecuteRequest — the loop_counter still does the real work.
//
// @story: attribute-carry-forward
package attributes

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	loop_counter "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// recordingLoopCounter wraps the real loop_counter handler and records
// the incoming attribute bag of each dispatch — the READ path the
// carry-forward step must populate before substitution. The real
// loop_counter still produces every event, so this is a pure observer
// (not a stub of the value-delivering component).
//
// @story: attribute-carry-forward
type recordingLoopCounter struct {
	inner    *loop_counter.Handler
	mu       sync.Mutex
	captures []dispatchCapture
}

// dispatchCapture records one observed dispatch's identity + the
// incoming attribute bag. Keyed on NodeID + RunScopeID so the test
// can partition observations into "main counter" vs "inner counter
// in the sub-graph scope".
type dispatchCapture struct {
	nodeID     string
	runScopeID string
	attrs      map[string]any
}

func (r *recordingLoopCounter) Execute(
	ctx context.Context, req *genv1.ExecuteRequest, sink executor.EventSink, hctx executor.HandlerContext,
) error {
	bag := map[string]any{}
	if req.Attributes != nil {
		bag = req.Attributes.AsMap()
	}
	// @deliberate: Defensive copy: the map AsMap returns is fresh per call, but if
	// any downstream consumer were to mutate it, the record would
	// drift; copy-on-record makes the assertion read what the dispatch
	// saw at entry.
	copied := make(map[string]any, len(bag))
	for k, v := range bag {
		copied[k] = v
	}
	r.mu.Lock()
	r.captures = append(r.captures, dispatchCapture{
		nodeID:     req.NodeId,
		runScopeID: req.RunScopeId,
		attrs:      copied,
	})
	r.mu.Unlock()
	return r.inner.Execute(ctx, req, sink, hctx)
}

func (r *recordingLoopCounter) snapshot() []dispatchCapture {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]dispatchCapture, len(r.captures))
	copy(out, r.captures)
	return out
}

// TestCarryForwardE2E exercises the carry-forward READ path AND the
// RunScope boundary in one harness:
//
//  1. Three sequential dispatches of `counter` in the main RunScope.
//     Each dispatch's incoming bag MUST carry the prior dispatch's
//     count (0 on the first dispatch — schema default; 1 on the
//     second; 2 on the third).
//  2. The three writeback rows MUST carry count = 1, 2, 3 in dispatch
//     order — proves the writeback half of the cycle (without it, the
//     read half cannot be tested).
//  3. A sub-graph (`worker`) containing the same node kind MUST see
//     count = 0 on its first dispatch — proves the RunScope boundary
//     blocks carry-forward.
//
// Cascade re-fire: the `counter` node self-subscribes to its own
// `loop` named event so the runtime re-enqueues `counter` after each
// dispatch in the same RunScope.
func TestCarryForwardE2E(t *testing.T) {
	t.Parallel()

	const recordingURL = "inproc://carry-forward-recording-loop-counter"
	recorder := &recordingLoopCounter{inner: loop_counter.New()}

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraInprocHandlers: map[string]executor.InProcessHandler{
			recordingURL: recorder,
		},
		// @deliberate: Surface the inproc executor URL on the control-API's
		// executors config so registration's reference-validation
		// recognises it. The supervisor already wires the inproc
		// dispatch path via ExtraInprocHandlers (handler + resolver
		// alias); this entry is the symmetric control-API surface.
		ExtraExecutors: map[string]executor.Endpoint{
			recordingURL: {Transport: "inproc", URL: recordingURL},
		},
		// @deliberate: Relax to "available" so the test inproc URL (which has no
		// observability handshake to surface declared events or an
		// expected_attributes_schema) is accepted at registration
		// without the test having to fake those capabilities. The
		// loop_counter alias hard-coded into the control-API path
		// still gets its schema for free; the wrapper handler
		// delegates value delivery to the real loop_counter.
		RefValidationMode: node.RefValidateAvailable,
	})

	// @deliberate: Stub scripts for every non-inproc node-type the template
	// references. The default stub mode is "succeed with empty
	// delta" but the harness's stub Resolver returns a permissive
	// `{"type":"object"}` schema for any executor in the
	// `executors` map; when a node-type's dispatch arrives, the
	// stub looks up its per-type script and runs it. Without an
	// explicit `WhenType` the stub waits forever on the call (no
	// script registered → no Success response). See
	// subgraph_exit_carry_e2e_test.go for the same pattern.
	h.Stub.WhenType("caller").Success(map[string]any{}, true, "caller-ok")
	h.Stub.WhenType("inner-trigger").Success(map[string]any{}, true, "inner-trigger-ok")

	// @deliberate: Template: main-graph `counter` self-loops via its `loop` named
	// event; the sub-graph `worker` has a single `inner-counter` node
	// dispatched once when the main caller delegates to it. The main
	// caller (`main-caller`) only fires `delegation` after we've
	// observed three counter dispatches by deferring the delegation
	// onto a downstream `gate` node that fires once we've seen the
	// `done` event from `counter`.
	//
	// Implementation note on the loop terminator: loop_counter emits
	// `done` only when count == max. To bound the loop at three
	// dispatches we set max=3; the third dispatch emits `done` and
	// the self-subscription on `event/loop` stops firing. The sub-
	// graph caller is then dispatched on `done` so the sub-graph
	// runs after the main-graph loop terminates.
	//
	// Counter attribute schema: `count` is the writeback property the
	// executor sets; `max` is the static-default input that gates
	// loop vs done. Both flow through the carry-forward READ path —
	// `max` is unchanged across dispatches (its source is the static
	// default) and `count` accumulates dispatch-over-dispatch.
	counterSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"max":   map[string]any{"type": "integer", "default": 3},
			"count": map[string]any{"type": "integer", "default": 0, "readOnly": true},
		},
	}

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "carry-forward-demo", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{
							Type:     "counter",
							Executor: recordingURL,
							// @deliberate: Self-subscribe to `loop` so cascade re-fires
							// the node within the SAME RunScope on each
							// loop emit (not on `done`, which only fires
							// at the final dispatch). The default frame
							// = "in" — the cascade walker's "drain my own
							// queue" idiom (validated in
							// TestValidateSubscribes_SelfWithFrameInOK)
							// — keeps each re-enqueue tied to the same
							// frame so the carry-forward step on the
							// next dispatch finds the prior run.
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "counter", Type: "event/loop", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						scenario.WithAttributes(counterSchema),
					),
					// @deliberate: `caller` delegates to the sub-graph at instance
					// creation (no Subscribes → runs in the initial
					// frame). The sub-graph runs in parallel with the
					// main-counter loop. Inner-counter inside the sub-
					// graph dispatches in the worker RunScope; the
					// recorder observes its incoming `count` value —
					// which MUST be the schema default (0), not the
					// main scope's accumulated value (which would be
					// the falsifier).
					//
					// Open attribute schema mirrors the
					// `subgraph_exit_carry_e2e_test.go` setup: the
					// caller carries the exit's writeback into the
					// main scope. Empty schema here just affords the
					// carry-rule a non-nil attributes surface to
					// write into.
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						scenario.WithAttributes(map[string]any{
							"type":       "object",
							"properties": map[string]any{},
						}),
					),
				},
			},
			{
				Name:  "worker",
				Entry: "inner-trigger",
				Exit:  "inner-counter",
				Nodes: []node.TemplateNodeDef{
					// @deliberate: inner-trigger is the entry; it absorbs into the
					// caller (per the sub-graph identity-and-absorption
					// canonicalization). Using a separate, dedicated
					// stub entry leaves inner-counter free to be
					// dispatched as a standalone sub-graph node (the
					// SubgraphInternalCascade helper filters out the
					// entry but NOT non-entry sub-graph nodes).
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-trigger", Executor: "stub"},
					),
					// @deliberate: inner-counter runs in the sub-graph RunScope on
					// the inner-trigger terminal cascade. Recording
					// handler captures its incoming bag — the
					// carry-forward boundary assertion reads that
					// bag's `count` field.
					scenario.MakeNode(
						node.TemplateNodeDef{
							Type:     "inner-counter",
							Executor: recordingURL,
							Subscribes: []tmplspec.SubscriptionEntry{
								{Node: "inner-trigger", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
							},
						},
						scenario.WithAttributes(counterSchema),
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-carry-forward", map[string]any{})

	mainCounter := h.FindNode(iid, "counter")
	require.NotNil(t, mainCounter, "main counter node missing")
	caller := h.FindNode(iid, "caller")
	require.NotNil(t, caller, "caller (sub-graph entry) missing")
	innerCounter := h.FindNode(iid, "inner-counter")
	require.NotNil(t, innerCounter, "inner-counter (sub-graph node) missing")

	// @deliberate: First wait for the main counter to complete all three
	// dispatches — that gates the caller's event/done subscription
	// firing, which in turn delegates into the sub-graph.
	require.True(t,
		h.WaitForNodeState(mainCounter.ID, cascade.NodeStateFresh, 30*time.Second),
		"main counter must reach fresh after three dispatches; loop self-subscription did not drive the cascade")

	// @deliberate: Then wait for the inner counter to terminate — that is
	// downstream of all three main-graph counter dispatches and the
	// sub-graph dispatch, so it's the latest observable point in
	// the cascade.
	if !h.WaitForNodeState(innerCounter.ID, cascade.NodeStateFresh, 60*time.Second) {
		// @deliberate: Diagnostic: surface the node-runs row state for every node
		// + the run-scope count so the failure mode is observable in
		// CI logs.
		var mainPhase, mainState string
		_ = h.Pool.QueryRow(h.Ctx,
			`SELECT phase, state FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
			mainCounter.ID,
		).Scan(&mainPhase, &mainState)
		var callerPhase, callerState string
		_ = h.Pool.QueryRow(h.Ctx,
			`SELECT phase, state FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
			caller.ID,
		).Scan(&callerPhase, &callerState)
		var innerPhase, innerState string
		_ = h.Pool.QueryRow(h.Ctx,
			`SELECT phase, state FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
			innerCounter.ID,
		).Scan(&innerPhase, &innerState)
		innerTrigger := h.FindNode(iid, "inner-trigger")
		var triggerPhase, triggerState, triggerNodeID string
		if innerTrigger != nil {
			triggerNodeID = innerTrigger.ID.String()
			_ = h.Pool.QueryRow(h.Ctx,
				`SELECT COALESCE(phase, ''), COALESCE(state, '') FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
				innerTrigger.ID,
			).Scan(&triggerPhase, &triggerState)
		}
		var workerScopes int
		_ = h.Pool.QueryRow(h.Ctx,
			`SELECT COUNT(*) FROM rimsky_run_scopes WHERE instance_id = $1 AND graph_name = 'worker'`,
			iid,
		).Scan(&workerScopes)
		// @deliberate: Also list all event kinds the main counter emitted in
		// chronological order — that surfaces whether `event/done`
		// fired at all.
		var allKinds []string
		rows, _ := h.Pool.Query(h.Ctx,
			`SELECT kind FROM rimsky_events WHERE node_id = $1 ORDER BY occurred_at`,
			mainCounter.ID,
		)
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			allKinds = append(allKinds, k)
		}
		rows.Close()
		t.Fatalf("inner counter never reached fresh; main_counter latest run phase=%q state=%q kinds=%v; caller latest run phase=%q state=%q; inner_counter latest run phase=%q state=%q; inner_trigger (id=%s) latest run phase=%q state=%q; worker run-scopes=%d",
			mainPhase, mainState, allKinds, callerPhase, callerState, innerPhase, innerState, triggerNodeID, triggerPhase, triggerState, workerScopes)
	}

	// @deliberate: Assertion 1: the main-graph counter ran exactly three dispatches
	// (the loop_counter's loop boundary is `new_count < max`, so on
	// max=3 the third dispatch transitions from `loop` to `done` and
	// the self-subscription stops firing). The three runs sit in the
	// main RunScope.
	mainScopeID := h.GetMainRunScopeID(iid)
	type writebackRow struct {
		dispatchID string
		count      int
	}
	var mainWritebacks []writebackRow
	h.QuerySQL(`
		SELECT na.node_run_id::text, (na.data->>'count')::int
		  FROM rimsky_node_attributes na
		  JOIN rimsky_node_runs nr ON nr.id = na.node_run_id
		 WHERE na.node_id = $1
		   AND nr.run_scope_id = $2
		 ORDER BY nr.enqueued_at, nr.id
	`, []any{mainCounter.ID, mainScopeID}, func(scan func(...any) error) error {
		var row writebackRow
		if err := scan(&row.dispatchID, &row.count); err != nil {
			return err
		}
		mainWritebacks = append(mainWritebacks, row)
		return nil
	})
	require.Len(t, mainWritebacks, 3,
		"expected exactly three writebacks for the main-graph counter (one per dispatch); got %v", mainWritebacks)
	require.Equal(t, 1, mainWritebacks[0].count, "dispatch 1 writeback must be count=1")
	require.Equal(t, 2, mainWritebacks[1].count, "dispatch 2 writeback must be count=2 (carry-forward + executor delta)")
	require.Equal(t, 3, mainWritebacks[2].count, "dispatch 3 writeback must be count=3 (carry-forward + executor delta)")

	// @deliberate: Assertion 2: the per-dispatch incoming bag carries the prior
	// writeback — the READ side of the carry-forward step. Partition
	// the recorder's captures by (NodeID, RunScopeID). Main-counter
	// captures live under (mainCounter.ID, mainScopeID). The three
	// expected count values for those dispatches are 0, 1, 2 (the
	// dispatch n+1 sees dispatch n's writeback via carry-forward;
	// dispatch 1 sees the schema default).
	captures := recorder.snapshot()
	require.NotEmpty(t, captures, "recorder must have captured at least one dispatch")

	var mainCaptures []dispatchCapture
	for _, c := range captures {
		if c.nodeID == mainCounter.ID.String() && c.runScopeID == mainScopeID.String() {
			mainCaptures = append(mainCaptures, c)
		}
	}
	require.Len(t, mainCaptures, 3,
		"expected exactly three main-counter captures (one per dispatch); got %d (%+v)", len(mainCaptures), mainCaptures)
	for i, want := range []int{0, 1, 2} {
		bag := mainCaptures[i].attrs
		got, hasCount := bag["count"]
		require.True(t, hasCount,
			"main-counter dispatch %d incoming bag must carry `count` (want=%d); got %v", i+1, want, bag)
		switch v := got.(type) {
		case float64:
			require.Equal(t, want, int(v),
				"main-counter dispatch %d incoming bag must carry count=%d; got %v", i+1, want, v)
		case int:
			require.Equal(t, want, v,
				"main-counter dispatch %d incoming bag must carry count=%d; got %v", i+1, want, v)
		default:
			t.Fatalf("main-counter dispatch %d: count of unexpected type %T (%v)", i+1, v, v)
		}
	}

	// @deliberate: Assertion 3: the sub-graph's inner-counter sees the schema
	// default — NOT count=3 carried from the parent scope. Partition
	// the captures by NodeID = innerCounter.ID. The first such
	// capture's count MUST be 0 (the schema default) — anything else
	// (especially a parent-scope value like 3) is the falsifier.
	var innerCaptures []dispatchCapture
	for _, c := range captures {
		if c.nodeID == innerCounter.ID.String() {
			innerCaptures = append(innerCaptures, c)
		}
	}
	require.NotEmpty(t, innerCaptures,
		"recorder must have captured at least one inner-counter dispatch (the sub-graph never ran?)")
	firstInner := innerCaptures[0]
	require.NotEqual(t, mainScopeID.String(), firstInner.runScopeID,
		"inner-counter MUST run in the sub-graph RunScope, not the main RunScope (got %s)", firstInner.runScopeID)
	if v, ok := firstInner.attrs["count"]; ok {
		switch x := v.(type) {
		case float64:
			require.Equal(t, 0, int(x),
				"sub-graph inner-counter incoming bag MUST see schema default count=0, NOT a parent-scope carry-forward; got %v", v)
		case int:
			require.Equal(t, 0, x,
				"sub-graph inner-counter incoming bag MUST see schema default count=0, NOT a parent-scope carry-forward; got %v", v)
		default:
			t.Fatalf("sub-graph inner-counter incoming bag: count of unexpected type %T (%v)", v, v)
		}
	}
	// @deliberate: If `count` is absent (schema default not yet projected), the
	// non-leak property still holds — the falsifier is a PRESENT non-
	// zero parent-scope value, not the projection mechanism itself.

	// @deliberate: Cross-check: the inner-counter run row's terminal MUST be
	// terminal/success — proves the executor ran, terminated cleanly,
	// and the carry-rule path fired. The exit-node carve-out routes
	// inner-counter's executor delta onto the parent's writeback row
	// (per `applyTerminalCompleteSubgraphExit`), NOT onto the exit's
	// own attribute row, so the assertion above against
	// captures[3].attrs is the load-bearing observation for the
	// inner-counter's incoming-bag carry-forward (count=0 schema
	// default in a fresh scope, NOT a parent-scope value).
	var innerSettling string
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		SELECT COALESCE(settling_signal_type,'')
		  FROM rimsky_node_runs
		 WHERE node_id = $1
		   AND prior_dispatch_id IS NULL
		 ORDER BY enqueued_at ASC, id ASC
		 LIMIT 1
	`, innerCounter.ID).Scan(&innerSettling))
	require.Equal(t, "terminal/success", innerSettling,
		"inner-counter MUST terminate via terminal/success — proves the recordingURL executor ran in the sub-graph RunScope")

	// @deliberate: Stable order — defensive: the QuerySQL ORDER BY guarantees this,
	// but a sort guards against any test-local reordering in the
	// helper composition.
	sort.SliceStable(mainWritebacks, func(i, j int) bool {
		return mainWritebacks[i].count < mainWritebacks[j].count
	})
	require.Equal(t, []writebackRow{
		{mainWritebacks[0].dispatchID, 1},
		{mainWritebacks[1].dispatchID, 2},
		{mainWritebacks[2].dispatchID, 3},
	}, mainWritebacks, "main-counter writebacks must be exactly count=1,2,3")

	// @deliberate: Belt-and-braces — read the latest main-counter writeback through
	// the persistence accessor as a cross-check on the raw SQL.
	require.NoError(t, h.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.Persist.NodeAttributes().GetLatestByNode(ctx, mainCounter.ID, mainScopeID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row, "GetLatestByNode must return a row for the main counter")
		count, ok := row.Data["count"]
		require.True(t, ok, "latest writeback must carry `count`")
		switch v := count.(type) {
		case float64:
			require.Equal(t, 3, int(v), "latest writeback count must be 3")
		case int:
			require.Equal(t, 3, v, "latest writeback count must be 3")
		default:
			t.Fatalf("latest writeback count of unexpected type %T (%v)", v, v)
		}
		return nil
	}))
}
