// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: one-message-per-frame
// @concept: frame
package scenarios

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
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
						{Node: "ping/recheck", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
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

	deadline := time.Now().Add(60 * time.Second)
	var frameCount, receiverRuns int
	for time.Now().Before(deadline) {
		h.QueryRowSQL(
			`SELECT count(*) FROM rimsky_frames WHERE instance_id = $1 AND state IN ('completed','failed')`,
			[]any{iid}, &frameCount)
		h.QueryRowSQL(
			`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = 'terminal/success'`,
			[]any{receiver.ID}, &receiverRuns)
		if frameCount >= N && receiverRuns >= N {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.GreaterOrEqual(t, frameCount, N,
		"need at least %d settled frames; got %d (cascade did not converge)", N, frameCount)
	require.GreaterOrEqual(t, receiverRuns, N,
		"receiver must run %d times; got %d (one-message-per-frame broken)", N, receiverRuns)

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
	require.GreaterOrEqual(t, distinctTriggers, N,
		"each posted message must produce a distinct frame; got %d distinct triggering_message_id values for %d posted messages",
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
