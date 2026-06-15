// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N4 scenario — message_cascade_e2e.
//
// Drives the full message-cascade path against a real Postgres: a
// template with `subscribes: on: message`, an enqueued envelope, the
// `SweepDeliverMessagesForRunningFrames` tick, and an assertion that
// the subscriber's `rimsky_nodes.state` flipped to stale within the
// running frame's frame_id.
//
// @concept: message
// @concept: cascade
// @concept: frame

package messages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func TestMessageCascadeE2E_SubscriberFlipsStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	// @deliberate: Template: one node (`receiver`) subscribes to instance-scoped
	// `on: message` messages with `kind: invalidate` (matches any
	// target). A sibling node (`self_receiver`) subscribes with
	// `target: self` so receiver-relative resolution rejects it on
	// the broadcast / empty-target envelope below. Pinning both in
	// the same scenario closes the coverage gap from cycle 4 issue B
	// (regression-reintroducing `msg.Target != ""` short-circuit in
	// cascadeMessageSubscribersInTx would not be caught by the unit
	// test alone).
	tmplSpec := node.TemplateSpec{
		Name: "msg-cascade-e2e", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type:     "sender",
				Executor: "stub",
			},
			{
				Type:     "receiver",
				Executor: "stub",
				Subscribes: []spec.SubscriptionEntry{
					{
						Instance: true,
						// @deliberate: Match any message envelope with kind=invalidate,
						// regardless of sender_kind / target. Prefix-bind
						// payload as dyn; CEL filter narrows by kind.
						Type:                 "message/invalidate/*",
						Frame:                "in",
						WakeOnChange:         spec.BoolPtr(true),  // today-equivalent
						ForceUpstreamRefresh: spec.BoolPtr(false), // today-equivalent
					},
				},
			},
			{
				Type:     "self_receiver",
				Executor: "stub",
				Subscribes: []spec.SubscriptionEntry{
					{
						Instance: true,
						// @constraint: Match invalidate envelopes; CEL filter binds
						// to receiver's own alias via payload.target.
						// An empty broadcast envelope (payload.target ==
						// "") never matches.
						Type:                 "message/invalidate/operator/self_receiver",
						Frame:                "in",
						WakeOnChange:         spec.BoolPtr(true),  // today-equivalent
						ForceUpstreamRefresh: spec.BoolPtr(false), // today-equivalent
					},
				},
			},
		},
	}
	tmplRow := insertDeployedTemplate(ctx, t, backend, tmplSpec)

	ck := "ck-msg-cascade"
	var inst persistence.InstanceRow
	var senderNode, receiverNode, selfReceiverNode persistence.NodeRow
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}); err != nil {
			return err
		}
		// @constraint: coalesce delivery: this scenario enqueues two envelopes (a
		// targeted invalidate + a broadcast) and asserts BOTH are delivered
		// into one frame so the cascade walker is exercised against both at
		// once. The default flipped to serial_queue (one message per frame)
		// per spec 2026-05-29, so coalesce is now opt-in and must be set
		// explicitly here. The two envelopes carry no payload, so the
		// conflict-aware coalesce path sees no value-disagreement and
		// coalesces them.
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tmplRow.ID,
			InstanceKey: &ck, Params: map[string]any{},
			MainRunScopeID:    mainScopeID,
			FrameDeliveryMode: string(runtime.FrameDeliveryCoalesce),
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		s, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID,
			NodeType: "sender", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		senderNode = s
		r, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID,
			NodeType: "receiver", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		receiverNode = r
		sr, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID,
			NodeType: "self_receiver", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		selfReceiverNode = sr
		return nil
	}))
	_ = senderNode

	// @constraint: Enqueue two `invalidate` messages:
	//   (1) targeted to the receiver alias — exercises the existing
	//       receiver-resolution path.
	//   (2) empty-target broadcast — exercises the regression coverage
	//       for cycle 4 issue B: `target: self` MUST NOT match an empty
	//       envelope target (`cascadeMessageSubscribersInTx` rejects it
	//       at the receiver-resolution stage).
	msgID := shared.UUID(uuid.New())
	broadcastMsgID := shared.UUID(uuid.New())
	now := time.Now().UTC()
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := runtime.EnqueueMessage(ctx, tx, backend.Messages(), persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: inst.ID,
			Kind: "invalidate", Sender: "op-A", SenderKind: "operator",
			Target: "receiver", ReceivedAt: now,
		}); err != nil {
			return err
		}
		return runtime.EnqueueMessage(ctx, tx, backend.Messages(), persistence.EnqueueMessageRequest{
			ID: broadcastMsgID, InstanceID: inst.ID,
			Kind: "invalidate", Sender: "op-A", SenderKind: "operator",
			// @deliberate: Target intentionally empty — broadcast envelope.
			ReceivedAt: now.Add(time.Millisecond),
		})
	}))

	// @deliberate: Open a running frame for the instance. The frame engine creates
	// a queued frame and we promote it to running so the sweep picks
	// it up.
	var frameID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := backend.Frames().EnqueueSerialFrame(ctx, inst.ID, senderNode.ID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := backend.Frames().PromoteQueuedFrameToRunning(ctx, fid, tx); err != nil {
			return err
		}
		frameID = fid
		return nil
	}))

	// @deliberate: Drive the sweep — this dispatches DeliverPendingMessages +
	// cascadeMessageSubscribersInTx for every running frame.
	require.NoError(t, runtime.SweepDeliverMessagesForRunningFrames(
		ctx, backend, d.Queue(), shared.SilentLogger{}, now))

	// @deliberate: Assert: the receiver's rimsky_nodes.state is now 'stale' with
	// frame_id == frameID (the cascade walker fired MarkStaleForCascade).
	var rcv *persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.Nodes().Get(ctx, receiverNode.ID, tx)
		rcv = r
		return err
	}))
	require.NotNil(t, rcv)
	require.Equal(t, cascade.NodeStateStale, rcv.State,
		"receiver must flip to stale after the message cascade walker fires")
	require.NotNil(t, rcv.FrameID,
		"receiver's frame_id must be stamped by the cascade walker")
	require.Equal(t, frameID, *rcv.FrameID,
		"receiver's frame_id must equal the running frame's frame_id")

	// @deliberate: Assert: the message row was stamped delivered_at + frame_id.
	var deliveredMsg *persistence.MessageRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, _ persistence.Tx) error {
		m, err := backend.Messages().Get(ctx, msgID)
		deliveredMsg = m
		return err
	}))
	require.NotNil(t, deliveredMsg)
	require.NotNil(t, deliveredMsg.DeliveredAt,
		"delivered_at must be stamped on the message row")
	require.NotNil(t, deliveredMsg.FrameID,
		"frame_id must be stamped on the message row")
	require.Equal(t, frameID, *deliveredMsg.FrameID,
		"message's frame_id must equal the delivering frame's frame_id")

	// @deliberate: Assert: the broadcast (empty-target) envelope was delivered too.
	// The cascade walker must NOT have stale-marked `self_receiver` —
	// the subscription declared `target: self`, and a `target: self`
	// subscription only matches when the envelope's target equals the
	// receiver's own alias. An empty / broadcast target never equals
	// any receiver alias, so the receiver-resolution stage in
	// cascadeMessageSubscribersInTx must skip self_receiver entirely
	// (cycle 4 issue B regression coverage).
	var broadcastDelivered *persistence.MessageRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, _ persistence.Tx) error {
		m, err := backend.Messages().Get(ctx, broadcastMsgID)
		broadcastDelivered = m
		return err
	}))
	require.NotNil(t, broadcastDelivered)
	require.NotNil(t, broadcastDelivered.DeliveredAt,
		"broadcast (empty-target) envelope must be delivered too")
	require.Equal(t, "", broadcastDelivered.Target,
		"broadcast envelope target must remain empty in storage")

	// @deliberate: self_receiver MUST NOT have been stale-marked: it carries a
	// `target: self` subscription, which rejects the empty-target
	// envelope at receiver-resolution. The receiver's row should remain
	// in the pre-cascade state (`fresh`, the column default).
	var selfRcv *persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.Nodes().Get(ctx, selfReceiverNode.ID, tx)
		selfRcv = r
		return err
	}))
	require.NotNil(t, selfRcv)
	require.NotEqual(t, cascade.NodeStateStale, selfRcv.State,
		"self_receiver must NOT be stale-marked: `target: self` subscription must reject empty-target envelopes (cycle 4 issue B)")
}

// insertDeployedTemplate is a thin helper that inserts a template row in
// 'deployed' state with a deterministic content hash derived from
// name+version. Lives here so the scenario test stays self-contained
// alongside the existing fakes in this package.
func insertDeployedTemplate(ctx context.Context, t *testing.T, backend persistence.Tables, tmpl node.TemplateSpec) persistence.TemplateRow {
	t.Helper()
	sum := sha256.Sum256([]byte(tmpl.Name + ":" + tmpl.Version))
	hash := "sha256-" + hex.EncodeToString(sum[:])
	var row *persistence.TemplateRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: tmpl, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := backend.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		r, err := backend.Templates().GetByHash(ctx, hash, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	return *row
}
