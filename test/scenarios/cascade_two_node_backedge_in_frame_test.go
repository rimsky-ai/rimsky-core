// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: iterative-workflows-converge
// @concept: cascade
// @decision: walker-rule-per-sender-node
package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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

	h.WaitForNodeState(doneSinkNode.ID, cascade.NodeStateFresh)

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

	var instanceFrameCount int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_frames WHERE instance_id = $1`,
		[]any{iid}, &instanceFrameCount)
	require.Equal(t, 1, instanceFrameCount,
		"exactly one frame must carry the entire back-edge cycle; got %d frames for the instance "+
			"(a successor round opening a new frame violates the intra-frame invariant)",
		instanceFrameCount)

	var pingFrameIDs []shared.UUID
	h.QuerySQL(`
		SELECT frame_id FROM rimsky_node_runs
		 WHERE node_id = $1
		 ORDER BY sequence ASC`,
		[]any{pingNode.ID},
		func(scan func(...any) error) error {
			var fid shared.UUID
			if err := scan(&fid); err != nil {
				return err
			}
			pingFrameIDs = append(pingFrameIDs, fid)
			return nil
		})
	require.Len(t, pingFrameIDs, 2,
		"ping must have exactly two node-runs (round 1 off starter, round 2 off pong's back-edge); got %d",
		len(pingFrameIDs))
	require.Equal(t, pingFrameIDs[0], pingFrameIDs[1],
		"both of ping's dispatches must carry the same frame_id — round 1 frame_id=%s, round 2 (the "+
			"back-edge redispatch) frame_id=%s; a differing frame_id means the successor round opened a "+
			"new frame instead of appending to the current frame's queue",
		pingFrameIDs[0], pingFrameIDs[1])
}
