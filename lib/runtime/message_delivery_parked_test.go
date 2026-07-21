// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func TestSweepDeliverMessages_TypedMessageAfterParkDoesNotTransitionTheParkedRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "parked-message-noop", Version: "1",
	})
	ck := "ck-parked-msg"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var receiverNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "notify-webhook", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		receiverNode = n
		return nil
	}))

	frame1ID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parkedRunID := seedRunForNode(ctx, t, backend, d.Queue(), receiverNode.ID, frame1ID)

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.Nodes().UpdateState(ctx, parkedRunID, cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx); err != nil {
			return err
		}
		return backend.Nodes().UpdateState(ctx, parkedRunID, cascade.NodeStateParked, cascade.ReasonHandlerPark, nil, tx)
	}))

	require.NoError(t, runtime.SweepDeliverMessagesForRunningFrames(ctx, backend, shared.SilentLogger{}, time.Now()))

	var msg2ID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msg2ID = shared.UUID(uuid.New())
		return backend.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msg2ID,
			InstanceID: inst.ID,
			Type:       "notify-webhook",
			Sender:     "test",
			SenderKind: "operator",
		})
	}))

	require.NoError(t, runtime.SweepDeliverMessagesForRunningFrames(ctx, backend, shared.SilentLogger{}, time.Now()))

	var gateAfter *persistence.NodeRunForGate
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		g, err := backend.Nodes().GetRunForGate(ctx, tx, parkedRunID)
		gateAfter = g
		return err
	}))
	require.NotNil(t, gateAfter)
	require.Equal(t, cascade.NodeStateParked, gateAfter.State,
		"a typed message posted after park must not transition the parked run's state")
	require.Equal(t, frame1ID, gateAfter.FrameID,
		"the parked run must stay bound to its original frame; message delivery must not reassign it")

	var msg2After *persistence.MessageRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		m, err := backend.Messages().GetInTx(ctx, tx, msg2ID)
		msg2After = m
		return err
	}))
	require.NotNil(t, msg2After)
	require.Nil(t, msg2After.DeliveredAt,
		"a message posted while the instance's only open frame already parked a run must not be delivered; "+
			"the sweep only probes each frame's own trigger message, never a parked run's frame for new arrivals")

	var latest *persistence.NodeRunLatest
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		l, err := backend.Nodes().GetLatestRunForNode(ctx, tx, receiverNode.ID)
		latest = l
		return err
	}))
	require.NotNil(t, latest)
	require.Equal(t, parkedRunID, latest.NodeRunID,
		"no fresh run may be created for the receiver node from the undelivered message; the parked run remains the only run")
}
