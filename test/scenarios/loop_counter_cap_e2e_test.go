// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: loop-counter-cap
// @concept: terminal-tag
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
		ExtraExecutors: map[string]executor.Endpoint{
			loopSinkURL: {Transport: "inproc", URL: loopSinkURL},
			doneSinkURL: {Transport: "inproc", URL: doneSinkURL},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "loop-counter-cap-terminal-atomic-commit", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "counter",
					Executor: loop_counter.ExecutorAlias,
					Subscribes: []tmplspec.SubscriptionEntry{
						{
							Node:                 "counter",
							Type:                 "terminal/success",
							When:                 `"loop" in payload.tags`,
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
				Subscribes: []tmplspec.SubscriptionEntry{
					{
						Node:                 "counter",
						Type:                 "terminal/success",
						When:                 `"loop" in payload.tags`,
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

	h.WaitForNodeState(doneSinkNode.ID, cascade.NodeStateFresh)

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

	require.Equal(t, int64(1), doneSink.Count(),
		"done_sink invocations: got %d, want 1", doneSink.Count())

	require.Eventually(t, func() bool { return loopSink.Count() == 1 }, 5*time.Second, 20*time.Millisecond,
		"loop_sink invocations: got %d, want 1 (default most-recent cascade mode coalesces "+
			"counter's two same-frame loop-tagged dispatches into loop_sink's latest pending only)",
		loopSink.Count())
}

// @concept: error-policy
func TestLoopCounterDispatch_InvalidMaxAttributeFailsCleanly(t *testing.T) {
	t.Parallel()

	h := scenario.Start(t, scenario.HarnessOpts{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "loop-counter-invalid-max-errors", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "counter", Executor: loop_counter.ExecutorAlias},
				scenario.WithAttributes(map[string]any{
					"type":     "object",
					"required": []any{"max"},
					"properties": map[string]any{
						"max": map[string]any{"type": "integer", "minimum": 1, "source": "{{params.max}}"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-loop-counter-invalid-max", map[string]any{"max": 0})

	counter := h.FindNode(iid, "counter")
	require.NotNil(t, counter, "counter missing")
	h.WaitForNodeState(counter.ID, cascade.NodeStateFailed)

	var errorClass string
	h.QueryRowSQL(`
		SELECT payload->>'error_class' FROM rimsky_events
		 WHERE node_id = $1 AND kind LIKE 'terminal/error/%'
		 ORDER BY id DESC LIMIT 1`,
		[]any{counter.ID}, &errorClass)

	require.NotEmpty(t, errorClass,
		"a loop_counter dispatch with an attribute bag violating its own schema (max=0, minimum=1) "+
			"must terminate with a populated error_class, not silently succeed")
}
