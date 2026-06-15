// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-one-message-per-frame acceptance proof.
//
// As a template author, I can rely on substitution from the message
// body always being well-defined in a node that's reacting to a message,
// so that no template ever has to refuse a multi-message coalesced
// frame at runtime.
//
// Acceptance shape (per spec):
//   - N messages posted in close succession produce N distinct frames.
//   - Each frame carries exactly one message in its
//     rimsky_messages join.
//   - Each frame has a distinct triggering_message_id.
//   - Each subscribed receiver runs once per frame.
//
// The proof boots the real rimsky stack, POSTs N typed messages of a
// declared type to the same instance within one outer tick, then polls
// the cascade-graph endpoint until all N frames have settled and
// asserts the one-message-per-frame invariant against the persistence
// layer.
//
// Falsifier this test pins:
//
//	"Two messages share a frame; OR a template that substitutes from a
//	 message body fails at substitution time with a 'multiple
//	 messages' error."
//
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

	// @deliberate: the receiver runs through the stub executor; its
	// terminal/success count must equal N (one run per delivered
	// message).
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
						// @deliberate: substitution from the message body
						// — this surface fails LOUDLY ("multiple
						// messages error") if a frame ever carried more
						// than one delivered message. The spec
						// falsifier pins exactly this.
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

	// @deliberate: POST N messages in quick succession. The handler
	// enqueues each envelope AND its delivering frame inside the
	// request tx. With the one-message-per-frame guarantee, each
	// envelope opens its own queued frame; the supervisor's frame
	// engine promotes one at a time and the delivery sweep stamps
	// delivered_at on exactly the running frame's envelope.
	//
	// @deliberate: N = 10 is large enough to expose any "two-messages-
	// share-a-frame" coalescence regression while staying within
	// scenario-test timing budgets.
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

	// @deliberate: poll until all N frames have settled. Exit when
	// rimsky_frames has N completed rows for this instance AND the
	// receiver has run N times. Bound the wait so a stuck cascade fails
	// the test rather than hanging.
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

	// @deliberate: persistence-layer falsifier-killing checks below —
	// the spec's "two messages share a frame" failure mode lands
	// exactly here.

	// @deliberate: (1) exactly N message envelopes exist for this
	// instance with the declared type.
	var msgCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1 AND type = 'ping/recheck'`,
		[]any{iid}, &msgCount)
	require.Equal(t, N, msgCount,
		"exactly %d ping/recheck envelopes must exist; got %d", N, msgCount)

	// @deliberate: (2) at least N rimsky_frames rows exist for this
	// instance, and the distinct triggering_message_id count equals at
	// least N. The "at least" wording leaves room for any synthetic
	// wake frame the frame engine might insert as a tail-housekeeping
	// step; the load-bearing property is that no two of OUR posted
	// message IDs share a triggering_message_id.
	var distinctTriggers int
	h.QueryRowSQL(
		`SELECT count(DISTINCT triggering_message_id) FROM rimsky_frames WHERE instance_id = $1`,
		[]any{iid}, &distinctTriggers)
	require.GreaterOrEqual(t, distinctTriggers, N,
		"each posted message must produce a distinct frame; got %d distinct triggering_message_id values for %d posted messages",
		distinctTriggers, N)

	// @deliberate: (3) every posted message_id appears as some frame's
	// triggering_message_id. Pin the per-message frame mapping.
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

	// @deliberate: (4) the load-bearing one-message-per-frame property
	// at the message-ledger join — every frame in the in-flight cohort
	// carries at most one delivered message. Assert this with a GROUP
	// BY frame_id query against rimsky_messages.
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

	// @deliberate: (5) receiver dispatch count matches frame count —
	// one frame, one dispatch. This pins the spec's "two messages
	// posted in close succession produce two frames (one each)"
	// wording at the observable-dispatch level.
	require.Equal(t, N, receiverRuns,
		"receiver must run exactly %d times (one per frame); got %d",
		N, receiverRuns)
}
