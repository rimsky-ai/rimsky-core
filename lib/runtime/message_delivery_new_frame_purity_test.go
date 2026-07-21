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
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestSweepDeliverMessages_NewFrameReceiverRunNeverProbesPriorFrameParkedRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "new-frame-purity", Version: "1",
	})
	ck := "ck-new-frame-purity"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var receiverNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, &ck, tx)
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

	var endedWhileParked bool
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		result, err := backend.Frames().EndFrameIfSettled(ctx, frame1ID, tx)
		endedWhileParked = result.Transitioned
		return err
	}))
	require.False(t, endedWhileParked,
		"the frame-end predicate must count a parked run as unresolved work; a frame holding a parked run must not end")

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.Nodes().UpdateState(ctx, parkedRunID, cascade.NodeStateStale, cascade.ReasonDeadlineResume, nil, tx); err != nil {
			return err
		}
		if err := backend.Nodes().UpdateState(ctx, parkedRunID, cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx); err != nil {
			return err
		}
		return backend.Nodes().UpdateState(ctx, parkedRunID, cascade.NodeStateFresh, cascade.ReasonHandlerComplete, nil, tx)
	}))

	var endedAfterResolve bool
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		result, err := backend.Frames().EndFrameIfSettled(ctx, frame1ID, tx)
		endedAfterResolve = result.Transitioned
		return err
	}))
	require.True(t, endedAfterResolve,
		"once the formerly parked run resolves, the frame must be free to end")

	var msgID, frame2ID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID = shared.UUID(uuid.New())
		if err := backend.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: inst.ID,
			Type:       "notify-webhook",
			Sender:     "test",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := backend.Frames().InsertRunningFrame(ctx, inst.ID, msgID, mainScopeID, tx)
		frame2ID = fid
		return err
	}))
	require.NotEqual(t, frame1ID, frame2ID)

	require.NoError(t, runtime.SweepDeliverTriggeringMessagesForRunningFrames(ctx, backend, shared.SilentLogger{}, time.Now()))

	var priorRunAfter *persistence.NodeRunForGate
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		g, err := backend.Nodes().GetRunForGate(ctx, parkedRunID, tx)
		priorRunAfter = g
		return err
	}))
	require.NotNil(t, priorRunAfter)
	require.Equal(t, cascade.NodeStateFresh, priorRunAfter.State,
		"delivery into a newly started frame must never mutate the resolved run left over from a prior frame")
	require.Equal(t, frame1ID, priorRunAfter.FrameID,
		"the prior-frame run must keep its original frame binding")

	var latest *persistence.NodeRunLatest
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		l, err := backend.Nodes().GetLatestRunForNode(ctx, receiverNode.ID, tx)
		latest = l
		return err
	}))
	require.NotNil(t, latest)
	require.NotEqual(t, parkedRunID, latest.NodeRunID,
		"the new frame's delivery must create a fresh run rather than reuse or observe the prior frame's run")
	require.Equal(t, cascade.NodeStateStale, latest.State,
		"a receiver run created in a newly started frame must land fresh/stale, never inherit the prior frame's run")
	require.Equal(t, frame2ID, latest.FrameID,
		"the new receiver run must bind to the newly started frame, not the frame holding the formerly parked run")
}
