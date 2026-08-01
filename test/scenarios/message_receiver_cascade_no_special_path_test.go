// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestMessageReceiverNodeCascade_NoSpecialCascadePath(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	const msgType = "test/my-message-type"

	h.Stub.WhenType("regular-source").Success(map[string]any{"ran": true}, true, "ran")
	h.Stub.WhenType("downstream-of-message").Success(map[string]any{"d": true}, true, "d")
	h.Stub.WhenType("downstream-of-regular").Success(map[string]any{"d": true}, true, "d")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "message-receiver-cascade-no-special-path", Version: "1",
		Messages: []tmplspec.MessageSchema{
			{Type: msgType},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "regular-source", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "downstream-of-message", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: msgType, Type: "terminal/*",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "downstream-of-regular", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "regular-source", Type: "terminal/*",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-message-receiver-cascade-no-special-path", map[string]any{})

	regularSource := h.FindNode(iid, "regular-source")
	downstreamOfRegular := h.FindNode(iid, "downstream-of-regular")
	require.NotNil(t, regularSource)
	require.NotNil(t, downstreamOfRegular)

	h.WaitForNodeState(regularSource.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(downstreamOfRegular.ID, cascade.NodeStateFresh)

	h.PostInstanceMessage(iid, msgType, nil, "message-receiver-cascade-kick")

	msgReceiver := h.FindNode(iid, msgType)
	downstreamOfMessage := h.FindNode(iid, "downstream-of-message")
	require.NotNil(t, msgReceiver)
	require.NotNil(t, downstreamOfMessage)

	h.WaitForNodeState(msgReceiver.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(downstreamOfMessage.ID, cascade.NodeStateFresh)

	var msgReceiverCreationReason string
	h.QueryRowSQL(`SELECT creation_reason FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{msgReceiver.ID}, &msgReceiverCreationReason)
	require.Equal(t, string(cascade.CreationReasonMessageDelivery), msgReceiverCreationReason,
		"the message-receiver-node's own run must be creation_reason=message_delivery (it is the "+
			"message's direct target)")

	var downstreamOfMessageCreationReason, downstreamOfRegularCreationReason string
	h.QueryRowSQL(`SELECT creation_reason FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{downstreamOfMessage.ID}, &downstreamOfMessageCreationReason)
	h.QueryRowSQL(`SELECT creation_reason FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{downstreamOfRegular.ID}, &downstreamOfRegularCreationReason)

	require.Equal(t, string(cascade.CreationReasonCascade), downstreamOfMessageCreationReason,
		"downstream-of-message must be creation_reason=cascade -- a message-originated upstream "+
			"terminal must cascade through the exact same generic sender/receiver cascade walker "+
			"(ensureCascadePending / ListSenderNodesForReceiver in cascade_walker.go) as any other "+
			"upstream, with no message-specific creation reason or special path")
	require.Equal(t, downstreamOfMessageCreationReason, downstreamOfRegularCreationReason,
		"downstream-of-message (cascaded off a message-receiver-node) and downstream-of-regular "+
			"(cascaded off a plain executor node) must share the identical creation_reason -- "+
			"proving there is no dedicated message-originated cascade path distinguishable from "+
			"the generic one")

	var msgSenderRunID, regularSenderRunID shared.UUID
	h.QueryRowSQL(`
		SELECT w.sender_run_id FROM rimsky_wait_set w
		  JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
		 WHERE r.node_id = $1`,
		[]any{downstreamOfMessage.ID}, &msgSenderRunID)
	h.QueryRowSQL(`
		SELECT w.sender_run_id FROM rimsky_wait_set w
		  JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
		 WHERE r.node_id = $1`,
		[]any{downstreamOfRegular.ID}, &regularSenderRunID)

	var msgReceiverRunID, regularSourceRunID shared.UUID
	h.QueryRowSQL(`SELECT id FROM rimsky_node_runs WHERE node_id = $1`, []any{msgReceiver.ID}, &msgReceiverRunID)
	h.QueryRowSQL(`SELECT id FROM rimsky_node_runs WHERE node_id = $1`, []any{regularSource.ID}, &regularSourceRunID)

	require.Equal(t, msgReceiverRunID, msgSenderRunID,
		"downstream-of-message's wait-set sender_run_id must point at the message-receiver-node's "+
			"own run, tracked through the same rimsky_wait_set mechanism used for any cascade sender")
	require.Equal(t, regularSourceRunID, regularSenderRunID,
		"downstream-of-regular's wait-set sender_run_id must point at regular-source's run (control case)")
}
