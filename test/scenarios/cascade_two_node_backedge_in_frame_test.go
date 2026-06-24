// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: cascade
// @decision: walker-rule-per-sender-node
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	loop_counter "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestCascadeTwoNodeBackedgeInFrame(t *testing.T) {
	t.Parallel()

	const starterURL = "inproc://cascade-2cycle-starter"
	const pongURL = "inproc://cascade-2cycle-pong"
	const doneSinkURL = "inproc://cascade-2cycle-done-sink"

	starter := &countingSinkHandler{}
	pong := &countingSinkHandler{}
	doneSink := &countingSinkHandler{}

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraInprocHandlers: map[string]executor.InProcessHandler{
			starterURL:  starter,
			pongURL:     pong,
			doneSinkURL: doneSink,
		},
		ExtraExecutors: map[string]executor.Endpoint{
			starterURL:  {Transport: "inproc", URL: starterURL},
			pongURL:     {Transport: "inproc", URL: pongURL},
			doneSinkURL: {Transport: "inproc", URL: doneSinkURL},
		},
		RefValidationMode: node.RefValidateAvailable,
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-two-node-backedge-in-frame", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type:     "starter",
				Executor: starterURL,
			},
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "ping",
					Executor: loop_counter.ExecutorAlias,
					Subscribes: []tmplspec.SubscriptionEntry{
						{
							Node:                 "starter",
							Type:                 "terminal/success",
							ForceUpstreamRefresh: tmplspec.BoolPtr(false),
						},
						{
							Node:                 "pong",
							Type:                 "terminal/success",
							ForceUpstreamRefresh: tmplspec.BoolPtr(false),
						},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"max": map[string]any{"type": "integer", "default": 2},
					},
				}),
			),
			{
				Type:     "pong",
				Executor: pongURL,
				Subscribes: []tmplspec.SubscriptionEntry{
					{
						Node:                 "ping",
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
						Node:                 "ping",
						Type:                 "terminal/success",
						When:                 `"done" in payload.tags`,
						ForceUpstreamRefresh: tmplspec.BoolPtr(false),
					},
				},
			},
		},
	})

	iid := h.CreateInstance(tid, "ck-cascade-2cycle", map[string]any{})
	pingNode := h.FindNode(iid, "ping")
	require.NotNil(t, pingNode, "ping missing")
	doneSinkNode := h.FindNode(iid, "done_sink")
	require.NotNil(t, doneSinkNode, "done_sink missing")

	require.True(t,
		h.WaitForNodeState(doneSinkNode.ID, cascade.NodeStateFresh, 60*time.Second),
		"done_sink never reached fresh — ping was not re-dispatched on pong's "+
			"terminal/success back-edge (the cascade walker dropped the back-edge "+
			"to an already-completed receiver, contradicting "+
			"decision:walker-rule-per-sender-node)")

	var pingLoopCount, pingDoneCount int
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_events
		 WHERE node_id = $1 AND kind = 'terminal/success'
		   AND payload->'tags' @> '["loop"]'::jsonb`,
		[]any{pingNode.ID}, &pingLoopCount)
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_events
		 WHERE node_id = $1 AND kind = 'terminal/success'
		   AND payload->'tags' @> '["done"]'::jsonb`,
		[]any{pingNode.ID}, &pingDoneCount)

	require.Equal(t, 1, pingLoopCount,
		"ping's loop-tag terminal/success emissions: got %d, want 1 "+
			"(dispatch 1, fired by starter)", pingLoopCount)
	require.Equal(t, 1, pingDoneCount,
		"ping's done-tag terminal/success emissions: got %d, want 1 "+
			"(dispatch 2, fired by pong's terminal/success back-edge — "+
			"the load-bearing assertion)", pingDoneCount)

	require.Equal(t, int64(1), starter.Count(),
		"starter dispatches: got %d, want 1", starter.Count())
	require.Equal(t, int64(1), pong.Count(),
		"pong dispatches: got %d, want 1 (pong fires only on ping's "+
			"loop-tagged terminal/success; ping's done-tagged success "+
			"breaks pong's when: predicate and terminates the cycle)",
		pong.Count())
	require.Equal(t, int64(1), doneSink.Count(),
		"done_sink invocations: got %d, want 1", doneSink.Count())
}
