// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: event-log-read
// @decision: event-log-kind-enum
// @concept: event-log
func TestEventLog_SettlingRunWritesStateTransitionAndAttributesCommitted(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "event-kinds-settle", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-event-kinds-settle", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	transitions := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, Kind: "state_transition", MinCount: 2})
	byReason := map[string]map[string]any{}
	for _, row := range transitions {
		payload := row.Payload.Map()
		reason, _ := payload["reason"].(string)
		byReason[reason] = payload
	}

	gate := byReason[cascade.ReasonGateCleared.Kind]
	require.NotNil(t, gate, "the receiver's gate clearing writes its own transition row: %v", byReason)
	require.Equal(t, string(cascade.NodeStatePending), gate["from"])
	require.Equal(t, string(cascade.NodeStateStale), gate["to"])

	settled := byReason[cascade.ReasonHandlerComplete.Kind]
	require.NotNil(t, settled, "the settling terminal writes its own transition row: %v", byReason)
	require.Equal(t, string(cascade.NodeStateRunning), settled["from"])
	require.Equal(t, string(cascade.NodeStateFresh), settled["to"])

	committed := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, Kind: "attributes_committed"})
	require.Equal(t, true, committed[0].Payload.Map()["changed"],
		"the executor reported a change, so the committed row records one")

	sent := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{InstanceID: &iid, Kind: "message_sent"})
	require.NotEmpty(t, sent, "every message enqueued for the instance writes a message_sent row")
}

// @story: event-log-read
// @decision: event-log-kind-enum
func TestEventLog_UnchangedTerminalWritesNoOpCommit(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("idle").Success(map[string]any{}, false, "nothing to do")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "event-kinds-noop", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "idle", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-event-kinds-noop", map[string]any{})

	n := h.FindNode(iid, "idle")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	rows := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, Kind: "no_op_commit"})
	require.Equal(t, "nothing to do", rows[0].Payload.Map()["reason"],
		"the no-op row carries the executor's own change summary")
}

// @story: event-log-read
// @decision: event-log-kind-enum
// @concept: claim
func TestEventLog_ClaimingRunWritesClaimAcquiredAndClaimResolved(t *testing.T) {
	t.Parallel()
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("claimer").Success(map[string]any{}, true, "claimed")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "event-kinds-claim", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "claimer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.ClaimRef("queue-store", "/event-kinds")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-event-kinds-claim", map[string]any{})

	n := h.FindNode(iid, "claimer")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	acquired := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, Kind: "claim_acquired"})
	require.Equal(t, "queue-store", acquired[0].Payload.Map()["producer_name"])
	require.NotEmpty(t, acquired[0].Payload.Map()["claim_id"])

	resolved := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &n.ID, Kind: "claim_resolved"})
	require.Equal(t, "queue-store", resolved[0].Payload.Map()["producer_name"])
	require.NotEmpty(t, resolved[0].Payload.Map()["action"],
		"the resolved row names the producer verb rimsky fired")
}

// @story: event-log-read
// @decision: event-log-kind-enum
// @concept: claim-co-holdership
func TestEventLog_HeldClaimWritesClaimHeld(t *testing.T) {
	t.Parallel()
	h, acquirer, coHolder := startHandoffHarness(t, handoffOpts{
		alias:        "schema",
		coHolderType: "held-event-reader",
	})

	h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh)

	held := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &acquirer.ID, Kind: "claim_held"})
	require.Equal(t, "queue-store", held[0].Payload.Map()["producer_name"])
	require.NotEmpty(t, held[0].Payload.Map()["claim_id"])
}

// @story: event-log-read
// @decision: event-log-kind-enum
// @concept: event-log
func TestEventLog_DispatchBagViolatingExecutorSchemaWritesWorkRejected(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("producer").Success(map[string]any{"text": "far-too-long"}, true, "produced")
	h.Stub.WhenType("consumer").Success(map[string]any{}, true, "consumed")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "event-kinds-rejected", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "producer", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{"type": "string"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "consumer", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "producer", Type: "attribute/text/changed", ForceUpstreamRefresh: spec.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":      "string",
							"source":    "{{nodes.producer.attribute.text}}",
							"maxLength": 3,
						},
					},
					"required": []any{"text"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-event-kinds-rejected", map[string]any{})

	consumer := h.FindNode(iid, "consumer")
	require.NotNil(t, consumer)

	rejected := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &consumer.ID, Kind: "work_rejected"})
	require.Equal(t, "dispatch_bag_violates_executor_schema", rejected[0].Payload.Map()["reason"],
		"the rejected row names why rimsky refused to dispatch the run")
}
