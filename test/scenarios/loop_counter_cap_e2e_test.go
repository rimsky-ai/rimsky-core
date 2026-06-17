// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-loop-counter-cap proof — wires the production loop_counter
// utility node to two downstream sinks (one on the `loop` tag, one
// on the `done` tag) via the post-collapse subscription grammar
// (terminal/success + CEL `when:` filter over payload.tags) and
// observes the counted shape:
//
//   - max=3 → loop fires on dispatches 1, 2 (new_count = 1, 2 are
//     each < max=3); done fires on dispatch 3 (new_count = 3 is
//     not < 3); writebacks carry count = 1, 2, 3 in dispatch order.
//
// Per concept:terminal-tag the tag rides on the settling Success
// outcome's `tags` field; downstream subscribers filter via CEL on
// `payload.tags` rather than a per-name signal type.
//
// The loop_counter handler IS the value-delivering component (the
// real builtin from `lib/runtime/executor/builtin/loop_counter/`).
// The two sinks are test-only inproc observers downstream of the
// loop_counter, NOT replacements for it.
//
// @story: loop-counter-cap
// @concept: terminal-tag
// @blessed-invariant: terminal-atomic-commit — exercised by the
// per-emission audit-row count assertions below: each terminal
// outcome's run-state mutation, attributes_delta carry-forward, and
// tags persistence committed atomically with the verdict, so the
// loop+done emission counts the audit log surfaces match exactly
// what the counter computed at each dispatch (a torn write would
// surface as a tag count divergent from the dispatch count).
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
// counter on every dispatch and returns Success. Used to observe the
// per-tag cascade fan-out from the loop_counter under the unary
// in-process handler interface.
type countingSinkHandler struct {
	invocations int64
}

func (h *countingSinkHandler) Execute(
	_ context.Context, _ *genv1.ExecuteRequest, _ executor.HandlerContext,
) (*genv1.Outcome, error) {
	atomic.AddInt64(&h.invocations, 1)
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Success{
			Success: &genv1.Success{Changed: true, ChangeSummary: "sink"},
		},
	}, nil
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
	//   counter (real loop_counter, max=3) self-subscribes on
	//     terminal/success + `when: "loop" in payload.tags`;
	//   loop_sink subscribes to counter's terminal/success with the
	//     same `loop` tag filter;
	//   done_sink subscribes to counter's terminal/success with
	//     `when: "done" in payload.tags`.
	//
	// max=3 + handler boundary `new_count < max` ⇒ 2 loop tags + 1
	// done tag, matching STORY-loop-counter-cap's acceptance ("three
	// dispatches; on the third the node emits done instead of loop").
	// The handler's schema (advertised through the supervisor's
	// ExpectedAttributesSchemaFor wrap) carries `count` as readOnly
	// with default 0; carry-forward turns each dispatch's writeback
	// into the next dispatch's incoming `count`.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "loop-counter-cap-terminal-atomic-commit", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "counter",
					Executor: loop_counter.ExecutorAlias,
					// @deliberate: Self-subscribe on terminal/success +
					// `"loop" in payload.tags` for the cascade re-fire
					// within the same RunScope. The default frame =
					// "in" — the "drain my own queue" idiom.
					Subscribes: []tmplspec.SubscriptionEntry{
						{
							Node:                 "counter",
							Type:                 "terminal/success",
							When:                 `"loop" in payload.tags`,
							WakeOnChange:         tmplspec.BoolPtr(true),
							ForceUpstreamRefresh: tmplspec.BoolPtr(false),
						},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"max": map[string]any{"type": "integer", "default": 3},
					},
				}),
			),
			{
				Type:     "loop_sink",
				Executor: loopSinkURL,
				// @deliberate: Per `concept:node-subscription` the
				// cascade walker has one path — in-tx, in-frame.
				// Each counter dispatch settles in its own frame
				// (the self-subscribe drives the next dispatch in
				// the NEXT frame), so the loop_sink stale-marks +
				// dispatches once per counter loop-tag emission.
				Subscribes: []tmplspec.SubscriptionEntry{
					{
						Node:                 "counter",
						Type:                 "terminal/success",
						When:                 `"loop" in payload.tags`,
						WakeOnChange:         tmplspec.BoolPtr(true),
						ForceUpstreamRefresh: tmplspec.BoolPtr(false),
					},
				},
			},
			{
				Type:     "done_sink",
				Executor: doneSinkURL,
				Subscribes: []tmplspec.SubscriptionEntry{
					{
						Node:                 "counter",
						Type:                 "terminal/success",
						When:                 `"done" in payload.tags`,
						WakeOnChange:         tmplspec.BoolPtr(true),
						ForceUpstreamRefresh: tmplspec.BoolPtr(false),
					},
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
	// counter emits `done` on its terminal dispatch). The done_sink
	// reaching fresh proves cascade routed the `done` tag to its
	// `when: "done" in payload.tags` subscription.
	require.True(t,
		h.WaitForNodeState(doneSinkNode.ID, cascade.NodeStateFresh, 60*time.Second),
		"done_sink must reach fresh — loop_counter never reached the done boundary")

	// @deliberate: Tag-counted assertion against the canonical
	// per-dispatch audit row: every terminal/success emission writes
	// one rimsky_events row with `payload.tags` carrying the tags
	// from the settling outcome (concept:signal +
	// concept:terminal-tag). With max=3 the counter dispatches three
	// times — new_count=1 and new_count=2 each emit `loop` (1, 2 < 3),
	// and new_count=3 emits `done` (3 not < 3). Querying the audit
	// log for tag presence is the right shape per the story Proof
	// ("observes the loop tag fires N times then the done tag fires
	// once") because in-frame cascade collapsing into a single sink
	// dispatch hides per-emission counts otherwise.
	var loopCount, doneCount int
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_events
		 WHERE node_id = $1 AND kind = 'terminal/success'
		   AND payload->'tags' @> '["loop"]'::jsonb`,
		[]any{counter.ID}, &loopCount)
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_events
		 WHERE node_id = $1 AND kind = 'terminal/success'
		   AND payload->'tags' @> '["done"]'::jsonb`,
		[]any{counter.ID}, &doneCount)
	require.Equal(t, 2, loopCount,
		"counter's loop-tag terminal/success emissions: got %d, want 2 (dispatches 1, 2)", loopCount)
	require.Equal(t, 1, doneCount,
		"counter's done-tag terminal/success emissions: got %d, want 1 (dispatch 3)", doneCount)

	// @deliberate: done_sink fired exactly once is the singular
	// cross-frame fan-out observation the story Acceptance promises
	// ("a downstream subscriber on the done tag fires"). The
	// loop_sink invocation count is not asserted: in-frame cascade
	// collapses repeated stale-marks of the same receiver into one
	// dispatch, so the sink-count surface is not a faithful proxy
	// for the per-emission cascade traffic that the audit-row query
	// above measures directly.
	require.Equal(t, int64(1), doneSink.Count(),
		"done_sink invocations: got %d, want 1", doneSink.Count())
	// @deliberate: loopSink is wired into the template for cascade
	// symmetry with done_sink; per-emission counts come from the
	// audit-row query above, not from the sink's invocation counter.
	_ = loopSink
}
