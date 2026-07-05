// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: cascade-emit
// @concept: message-emitter-node
package scenarios

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStoryQueueDrainConverges_TerminatesViaCELGate(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").Success(map[string]any{
		"counter":     1,
		"should_loop": false,
	}, true, "a ran")
	h.Stub.WhenType("b").Success(map[string]any{
		"counter":     2,
		"should_loop": false,
	}, true, "b ran")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-queue-drain-converges-cycle", Version: "1",
		Messages: []spec.MessageSchema{
			{
				Type: "loop/wake",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {
						"trip_counter": {"type": "integer"}
					}
				}`),
			},
			{
				Type: "loop/iterate",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {
						"trip_counter": {"type": "integer"},
						"should_loop":  {"type": "boolean"}
					},
					"required": ["trip_counter", "should_loop"]
				}`),
			},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "a",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{Node: "loop/wake", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
						{Node: "loop/iterate", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"counter":     map[string]any{"type": "integer"},
						"should_loop": map[string]any{"type": "boolean"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "b",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{Node: "a", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"counter":     map[string]any{"type": "integer"},
						"should_loop": map[string]any{"type": "boolean"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:         "emitter",
					EmitsMessage: "loop/iterate",
					Subscribes: []node.SubscriptionEntry{
						{
							Node:                 "b",
							Type:                 "terminal/success",
							When:                 `payload.attributes_delta.should_loop`,
							ForceUpstreamRefresh: node.BoolPtr(false),
						},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"trip_counter": map[string]any{"type": "integer", "default": 1},
						"should_loop":  map[string]any{"type": "boolean", "default": false},
					},
					"required": []any{"trip_counter", "should_loop"},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-qdc-cycle", map[string]any{})
	require.NotEqual(t, shared.UUID{}, iid)

	resp := postMessage(t, h.ControlBase, iid, map[string]any{
		"type":    "loop/wake",
		"payload": map[string]any{"trip_counter": 0},
	}, "key-cycle-wake-"+uuid.NewString())
	require.Truef(t, resp.status == http.StatusOK || resp.status == http.StatusCreated,
		"initial wake POST must succeed; status=%d body=%s", resp.status, string(resp.raw))

	aNode := h.FindNode(iid, "a")
	bNode := h.FindNode(iid, "b")
	require.NotNil(t, aNode)
	require.NotNil(t, bNode)

	deadline := time.Now().Add(20 * time.Second)
	var aRuns, bRuns int
	for time.Now().Before(deadline) {
		h.QueryRowSQL(
			`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = 'terminal/success'`,
			[]any{aNode.ID}, &aRuns)
		h.QueryRowSQL(
			`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = 'terminal/success'`,
			[]any{bNode.ID}, &bRuns)
		if aRuns >= 1 && bRuns >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.GreaterOrEqual(t, aRuns, 1, "A must run at least once on the initial wake")
	require.GreaterOrEqual(t, bRuns, 1, "B must run at least once driven by A's terminal/success")

	time.Sleep(5 * time.Second)
	var iterateMsgs, finalARuns int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1 AND type = 'loop/iterate'`,
		[]any{iid}, &iterateMsgs)
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = 'terminal/success'`,
		[]any{aNode.ID}, &finalARuns)
	require.Equal(t, 0, iterateMsgs,
		"emit-node's CEL on b.terminal/success evaluates false against should_loop=false; "+
			"no loop/iterate can emit and the queue drains to empty. got %d loop/iterate messages", iterateMsgs)
	require.Equal(t, 1, finalARuns,
		"A must run exactly once — the initial wake — because no back-edge iterate was emitted. got %d", finalARuns)
}

func TestStoryQueueDrainConverges_LoopsWithoutGate(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").Success(map[string]any{
		"counter":     1,
		"should_loop": true,
	}, true, "a ran")
	h.Stub.WhenType("b").Success(map[string]any{
		"counter":     2,
		"should_loop": true,
	}, true, "b ran")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-queue-drain-converges-loops", Version: "1",
		Messages: []spec.MessageSchema{
			{
				Type: "loop/wake",
				BodySchema: []byte(`{
					"type": "object",
					"properties": { "trip_counter": {"type": "integer"} }
				}`),
			},
			{
				Type: "loop/iterate",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {
						"trip_counter": {"type": "integer"}
					},
					"required": ["trip_counter"]
				}`),
			},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "a",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{Node: "loop/wake", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
						{Node: "loop/iterate", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"counter": map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "b",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{Node: "a", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"counter": map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:         "emitter",
					EmitsMessage: "loop/iterate",
					Subscribes: []node.SubscriptionEntry{
						{
							Node:                 "b",
							Type:                 "terminal/success",
							ForceUpstreamRefresh: node.BoolPtr(false),
						},
						{Node: "b", Type: "attribute/counter/changed", ForceUpstreamRefresh: node.BoolPtr(false)},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"trip_counter": map[string]any{
							"type":   "integer",
							"source": "{{nodes.b.attribute.counter}}",
						},
					},
					"required": []any{"trip_counter"},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-qdc-loops", map[string]any{})
	require.NotEqual(t, shared.UUID{}, iid)

	resp := postMessage(t, h.ControlBase, iid, map[string]any{
		"type":    "loop/wake",
		"payload": map[string]any{"trip_counter": 0},
	}, "key-loops-wake-"+uuid.NewString())
	require.Truef(t, resp.status == http.StatusOK || resp.status == http.StatusCreated,
		"initial wake must succeed; status=%d body=%s", resp.status, string(resp.raw))

	aNode := h.FindNode(iid, "a")
	require.NotNil(t, aNode)

	deadline := time.Now().Add(30 * time.Second)
	var aRuns int
	for time.Now().Before(deadline) {
		h.QueryRowSQL(
			`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = 'terminal/success'`,
			[]any{aNode.ID}, &aRuns)
		if aRuns >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.GreaterOrEqual(t, aRuns, 2,
		"A must run at least twice — once on wake, then again on the back-edge feedback frame; got %d",
		aRuns)

	var iterateBody []byte
	h.QueryRowSQL(
		`SELECT payload FROM rimsky_messages
		   WHERE instance_id = $1 AND type = 'loop/iterate'
		   ORDER BY received_at ASC LIMIT 1`,
		[]any{iid}, &iterateBody)
	require.NotEmpty(t, iterateBody,
		"at least one loop/iterate envelope must land in the ledger — the back-edge wire")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(iterateBody, &decoded))
	require.Equal(t, float64(2), decoded["trip_counter"],
		"the body's trip_counter must reflect B's counter attribute via substitution; got %v",
		decoded)

	frames := getFrames(t, h.ControlBase, iid, "")
	require.GreaterOrEqual(t, len(frames), 2,
		"at least two frames must exist (wake + at least one loop)")
	seen := map[string]bool{}
	for _, fr := range frames {
		require.NotEmpty(t, fr.TriggeringMessageID,
			"every frame must have a non-empty triggering_message_id; got %+v", fr)
		seen[fr.TriggeringMessageID] = true
	}
	require.GreaterOrEqual(t, len(seen), 2,
		"distinct triggering_message_id values must exist across frames")
}
