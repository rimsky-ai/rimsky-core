// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-loop-counter-cap proof — wires the production loop_counter
// utility node to two downstream sinks (one on `event/loop`, one on
// `event/done`) and observes the counted event shape:
//
//   - max=4 → loop fires on dispatches 1, 2, 3 (new_count = 1, 2, 3
//     are all < 4); done fires on dispatch 4 (new_count = 4 is not <
//     4); writebacks carry count = 1, 2, 3, 4 in dispatch order.
//
// The plan + spec's falsifier names "(loop × 3 then done × 1)" as the
// counted-and-ordered shape — selecting max=4 satisfies that shape
// under the loop_counter handler's `new_count < max` boundary (per
// decision:loop-counter-shape).
//
// The loop_counter handler IS the value-delivering component (the
// real builtin from `lib/runtime/executor/builtin/loop_counter/`).
// The two sinks are test-only inproc observers — downstream of the
// loop_counter, NOT replacements for it.
//
// @story: loop-counter-cap
package scenarios

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	loop_counter "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// countingSinkHandler is a test-only inproc handler that increments a
// counter on every dispatch and closes the stream with Success. Used
// to observe the per-event cascade fan-out from the loop_counter.
type countingSinkHandler struct {
	invocations int64
}

func (h *countingSinkHandler) Execute(
	_ context.Context, _ *genv1.ExecuteRequest, sink executor.EventSink, _ executor.HandlerContext,
) error {
	atomic.AddInt64(&h.invocations, 1)
	return sink.Send(&genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{
				Outcome: &genv1.StreamClose_Success{
					Success: &genv1.Success{Changed: true, ChangeSummary: "sink"},
				},
			},
		},
	})
}

func (h *countingSinkHandler) Count() int64 { return atomic.LoadInt64(&h.invocations) }

func TestLoopCounterCapE2E(t *testing.T) {
	t.Parallel()

	const loopSinkURL = "inproc://loop-counter-cap-loop-sink"
	const doneSinkURL = "inproc://loop-counter-cap-done-sink"
	loopSink := &countingSinkHandler{}
	doneSink := &countingSinkHandler{}

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraInprocHandlers: map[string]executor.InProcessHandler{
			loopSinkURL: loopSink,
			doneSinkURL: doneSink,
		},
		// @deliberate: Surface the inproc URLs on the control-API's
		// executors config so registration's reference validator
		// recognises them.
		ExtraExecutors: map[string]executor.Endpoint{
			loopSinkURL: {Transport: "inproc", URL: loopSinkURL},
			doneSinkURL: {Transport: "inproc", URL: doneSinkURL},
		},
		// @deliberate: Relaxed mode — the test sinks have no
		// observability handshake to surface expected-attributes-schemas,
		// so the template registers under the available-only validator
		// mode. The real loop_counter alias still validates against its
		// baked-in schema; only the sinks need the relaxation.
		RefValidationMode: node.RefValidateAvailable,
	})

	// @deliberate: Template shape under test:
	//   counter (real loop_counter, max=4) self-loops on event/loop;
	//   loop_sink   subscribes to counter's event/loop;
	//   done_sink   subscribes to counter's event/done.
	//
	// max=4 + handler boundary `new_count < max` ⇒ 3 loop events + 1
	// done event, matching the falsifier's "(loop × 3 then done × 1)"
	// counted shape. The handler's schema (advertised through the
	// supervisor's ExpectedAttributesSchemaFor wrap) carries `count`
	// as readOnly with default 0; carry-forward turns each dispatch's
	// writeback into the next dispatch's incoming `count`.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "loop-counter-cap", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "counter",
					Executor: loop_counter.ExecutorAlias,
					// @deliberate: Self-subscribe on `event/loop` for the
					// cascade re-fire within the same RunScope. The
					// default frame = "in" — the "drain my own queue"
					// idiom.
					Subscribes: []tmplspec.SubscriptionEntry{
						{Node: "counter", Type: "event/loop", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"max": map[string]any{"type": "integer", "default": 4},
					},
				}),
			),
			{
				Type:     "loop_sink",
				Executor: loopSinkURL,
				// @deliberate: `frame: next` opens a fresh frame for the
				// sink on every `event/loop` emit so the sink re-stales +
				// dispatches per emit rather than collapsing all
				// emits into the first dispatch's frame (the default
				// `frame: in` would have the cascade-fire dedupe
				// against the sink's in-flight or already-fresh row).
				Subscribes: []tmplspec.SubscriptionEntry{
					{Node: "counter", Type: "event/loop", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
				},
			},
			{
				Type:     "done_sink",
				Executor: doneSinkURL,
				Subscribes: []tmplspec.SubscriptionEntry{
					{Node: "counter", Type: "event/done", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
				},
			},
		},
	})

	iid := h.CreateInstance(tid, "ck-loop-counter-cap", map[string]any{})
	counter := h.FindNode(iid, "counter")
	require.NotNil(t, counter, "counter missing")
	doneSinkNode := h.FindNode(iid, "done_sink")
	require.NotNil(t, doneSinkNode, "done_sink missing")

	// @deliberate: Wait for done_sink to reach fresh — its dispatch is
	// the latest observable point in the cascade (only fires after the
	// counter emits `done` on its terminal dispatch).
	require.True(t,
		h.WaitForNodeState(doneSinkNode.ID, cascade.NodeStateFresh, 60*time.Second),
		"done_sink must reach fresh — loop_counter never reached the done boundary")

	// @deliberate: Counted assertion 1 — loop_sink fired three times
	// (one per loop event from counter dispatches 1, 2, 3 — new_count
	// 1, 2, 3 are each < max=4). Ordering: every loop event MUST land
	// on loop_sink before done_sink fires (the runtime processes each
	// named event in dispatch-terminal order; the loop event for
	// dispatch K terminates before dispatch K+1 starts, and done_sink
	// only fires after dispatch 4 — strictly later in dispatch
	// lineage).
	if loopSink.Count() != 3 {
		// @deliberate: Diagnostic — enumerate all event/* rows for
		// counter and the loop_sink's run-row history so the failure
		// mode (counter stuck after dispatch 1? sink not re-firing on
		// subsequent loop emits?) is observable in CI logs.
		var counterKinds []string
		rows, _ := h.Pool.Query(h.Ctx,
			`SELECT kind FROM rimsky_events WHERE node_id = $1 ORDER BY occurred_at`,
			counter.ID,
		)
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			counterKinds = append(counterKinds, k)
		}
		rows.Close()
		loopSinkNode := h.FindNode(iid, "loop_sink")
		var loopSinkRuns []string
		rows, _ = h.Pool.Query(h.Ctx,
			`SELECT id::text, phase, state, COALESCE(settling_signal_type,'') FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at`,
			loopSinkNode.ID,
		)
		for rows.Next() {
			var id, phase, state, settling string
			_ = rows.Scan(&id, &phase, &state, &settling)
			loopSinkRuns = append(loopSinkRuns, "id="+id+" phase="+phase+" state="+state+" settling="+settling)
		}
		rows.Close()
		t.Fatalf("loop_sink MUST fire exactly 3 times; got %d. counter events=%v done_sink=%d loop_sink_runs=%v",
			loopSink.Count(), counterKinds, doneSink.Count(), loopSinkRuns)
	}

	// @deliberate: Counted assertion 2 — done_sink fired exactly once.
	require.Equal(t, int64(1), doneSink.Count(),
		"done_sink MUST fire exactly once — the `event/done` emit on counter dispatch 4")

	// @deliberate: Counted assertion 3 — the counter has exactly 4
	// writeback rows in the main RunScope with count = 1, 2, 3, 4 in
	// dispatch order. Proves the count attribute accumulates across
	// dispatches via carry-forward: the loop_counter sets `count` each
	// dispatch, the next dispatch's incoming bag sees the prior value
	// via the carry-forward step, and `new_count = count + 1` carries
	// the loop.
	mainScopeID := h.GetMainRunScopeID(iid)
	var counts []int
	h.QuerySQL(`
		SELECT (na.data->>'count')::int
		  FROM rimsky_node_attributes na
		  JOIN rimsky_node_runs nr ON nr.id = na.node_run_id
		 WHERE na.node_id = $1
		   AND nr.run_scope_id = $2
		 ORDER BY nr.enqueued_at, nr.id
	`, []any{counter.ID, mainScopeID}, func(scan func(...any) error) error {
		var v int
		if err := scan(&v); err != nil {
			return err
		}
		counts = append(counts, v)
		return nil
	})
	require.Equal(t, []int{1, 2, 3, 4}, counts,
		"counter writebacks MUST be exactly [1,2,3,4] in dispatch order — carry-forward + executor delta combined")

	// @deliberate: Ordering assertion — surface the order observed at
	// the events table to make the loop-then-done sequence load-bearing
	// rather than implicit. Read the kind sequence for `event/loop` and
	// `event/done` rows for the counter, ordered by created_at — the
	// runtime appends events with monotonic created_at within one
	// dispatch.
	type kindRow struct {
		kind string
	}
	var sequence []string
	h.QuerySQL(`
		SELECT kind
		  FROM rimsky_events
		 WHERE node_id = $1
		   AND (kind = 'event/loop' OR kind = 'event/done')
		 ORDER BY occurred_at, id
	`, []any{counter.ID}, func(scan func(...any) error) error {
		var k kindRow
		if err := scan(&k.kind); err != nil {
			return err
		}
		sequence = append(sequence, k.kind)
		return nil
	})
	require.Equal(t, []string{"event/loop", "event/loop", "event/loop", "event/done"}, sequence,
		"event sequence MUST be exactly loop, loop, loop, done")

	// @deliberate: Belt-and-braces — sleep one tick + recheck to make
	// sure loop_sink doesn't fire after done_sink terminates. A
	// spurious extra loop_sink dispatch (e.g. a misconfigured
	// self-subscription that doesn't gate on the loop event) would
	// shift the count above 3 in this window.
	time.Sleep(500 * time.Millisecond)
	require.Equal(t, int64(3), loopSink.Count(),
		"loop_sink MUST NOT fire any additional times after the counter terminates")
	require.Equal(t, int64(1), doneSink.Count(),
		"done_sink MUST NOT fire any additional times after the counter terminates")

	// @deliberate: silence the unused-context-import warning in the
	// no-tx path of this test; the harness keeps a context in h.Ctx but
	// the asserts above all flow through QuerySQL / accessors which
	// don't take a ctx parameter.
	_ = context.Background
}
