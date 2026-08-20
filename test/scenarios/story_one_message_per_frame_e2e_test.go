// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: one-message-per-frame
// @concept: frame
package scenarios

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStoryOneMessagePerFrame_NMessagesProduceNDistinctFrames(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("receiver").Success(map[string]any{"observed": "ok"}, true, "ran")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-one-message-per-frame", Version: "1",
		Messages: []spec.MessageSchema{
			{
				Type: "ping/recheck",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {
						"index": {"type": "integer"}
					},
					"required": ["index"]
				}`),
			},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "receiver",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{Node: "ping/recheck", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"observed_index": map[string]any{
							"type":   "integer",
							"source": "{{messages.ping/recheck.index}}",
						},
					},
					"required": []any{"observed_index"},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-one-msg-per-frame", map[string]any{})
	require.NotEqual(t, shared.UUID{}, iid)

	const N = 10
	postedIDs := make([]string, 0, N)
	for i := 0; i < N; i++ {
		resp := postMessage(t, h.ControlBase, iid, map[string]any{
			"type": "ping/recheck",
			"payload": map[string]any{
				"index": i,
			},
		}, "key-omp-"+uuid.NewString())
		require.Truef(t,
			resp.status == http.StatusOK || resp.status == http.StatusCreated,
			"message %d POST must succeed; status=%d body=%s",
			i, resp.status, string(resp.raw))
		var body struct {
			MessageID string `json:"message_id"`
		}
		require.NoError(t, json.Unmarshal(resp.raw, &body),
			"message %d POST: response body decode", i)
		require.NotEmpty(t, body.MessageID, "message %d: empty message_id", i)
		postedIDs = append(postedIDs, body.MessageID)
	}

	receiver := h.FindNode(iid, "receiver")
	require.NotNil(t, receiver)

	var receiverRuns int
	awaited.Until(t, fmt.Sprintf("%d settled frames and %d receiver runs", N, N), func() bool {
		var frameCount int
		h.QueryRowSQL(
			`SELECT count(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NOT NULL`,
			[]any{iid}, &frameCount)
		h.QueryRowSQL(
			`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = 'terminal/success'`,
			[]any{receiver.ID}, &receiverRuns)
		return frameCount >= N && receiverRuns >= N
	})

	var totalFrames int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_frames WHERE instance_id = $1`,
		[]any{iid}, &totalFrames)
	require.Equal(t, N, totalFrames,
		"exactly %d frames must exist for %d posted messages — one message = one frame, no extra frames; got %d",
		N, N, totalFrames)

	var msgCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1 AND type = 'ping/recheck'`,
		[]any{iid}, &msgCount)
	require.Equal(t, N, msgCount,
		"exactly %d ping/recheck envelopes must exist; got %d", N, msgCount)

	var distinctTriggers int
	h.QueryRowSQL(
		`SELECT count(DISTINCT triggering_message_id) FROM rimsky_frames WHERE instance_id = $1`,
		[]any{iid}, &distinctTriggers)
	require.Equal(t, N, distinctTriggers,
		"each posted message must produce exactly one frame with a distinct trigger; got %d distinct triggering_message_id values for %d posted messages",
		distinctTriggers, N)

	frames := getFrames(t, h.ControlBase, iid, "")
	triggers := make(map[string]frameView, len(frames))
	for _, fr := range frames {
		triggers[fr.TriggeringMessageID] = fr
	}
	sort.Strings(postedIDs)
	for _, mid := range postedIDs {
		fr, ok := triggers[mid]
		require.Truef(t, ok,
			"posted message %s has no frame; one-message-per-frame is broken (the message was either coalesced or dropped)",
			mid)
		require.Equal(t, "ping/recheck", fr.MessageType,
			"frame for message %s carries the wrong joined envelope type: %s",
			mid, fr.MessageType)
	}

	var maxMessagesPerFrame int
	h.QueryRowSQL(
		`SELECT COALESCE(MAX(c), 0) FROM (
			SELECT count(*) AS c
			  FROM rimsky_messages
			 WHERE instance_id = $1 AND frame_id IS NOT NULL
			 GROUP BY frame_id
		   ) per_frame`,
		[]any{iid}, &maxMessagesPerFrame)
	require.Equal(t, 1, maxMessagesPerFrame,
		"no frame may carry more than one delivered message; got max=%d (coalescence regression)",
		maxMessagesPerFrame)

	require.Equal(t, N, receiverRuns,
		"receiver must run exactly %d times (one per frame); got %d",
		N, receiverRuns)
}
