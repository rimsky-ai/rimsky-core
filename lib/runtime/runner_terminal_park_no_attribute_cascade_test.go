// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestApplyTerminalPark_DoesNotEmitAttributeChangedCascade(t *testing.T) {
	t.Parallel()
	args, acq, tables := seedRunningNodeForParkFixture(t)
	ctx := context.Background()

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.NodeAttributes().Upsert(ctx, acq.NodeRunID, acq.NodeID,
			map[string]any{"foo": "bar"}, tx)
	}))

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalPark(ctx, args, acq, terminalEvent{
			Kind:         terminalKindPark,
			ParkResumeAt: time.Now().Add(time.Hour),
		}, tx)
		return err
	}))

	require.Equal(t, 0, countSignalAudits(t, tables, acq.NodeID, "attribute/foo/changed"),
		"park is dispatch-internal and writes no attributes; it must not fire attribute/<key>/changed "+
			"cascade signals (those diffs would double-emit when the run later actually settles)")
}

func TestApplyTerminalPark_DoesNotDrainWaitSet(t *testing.T) {
	t.Parallel()
	args, acq, tables := seedRunningNodeForParkFixture(t)
	ctx := context.Background()

	receiverRunID := seedReceiverRunInSameFrame(t, args, acq)

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           acq.FrameID,
			ReceiverNodeRunID: receiverRunID,
			SenderNodeRunID:   acq.NodeRunID,
			TopicKind:         "attribute",
		}, tx)
	}))

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalPark(ctx, args, acq, terminalEvent{
			Kind:         terminalKindPark,
			ParkResumeAt: time.Now().Add(time.Hour),
		}, tx)
		return err
	}))

	var rows []persistence.WaitSetRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		rows, err = tables.WaitSet().ListForReceiver(ctx, acq.FrameID, receiverRunID, tx)
		return err
	}))
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].DrainedAt,
		"parked is in-flight per concept:node-run (settled=false); the wait-set settled-state drain "+
			"must not fire when the sender merely parks, or downstream receivers would gate-evaluate "+
			"against an in-flight sender")
}

func seedReceiverRunInSameFrame(t *testing.T, args RunArgs, acq *acquisition) shared.UUID {
	t.Helper()
	ctx := context.Background()
	receiverNodeID := shared.UUID(uuid.New())
	var receiverRunID shared.UUID
	require.NoError(t, args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if _, err := args.Persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: receiverNodeID, InstanceID: acq.InstanceID, NodeType: "receiver", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 receiverNodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                acq.FrameID,
			RunScopeID:             acq.RunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := args.Queue.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == receiverNodeID {
				receiverRunID = c.NodeRunID
			}
		}
		if receiverRunID == (shared.UUID{}) {
			t.Fatalf("seedReceiverRunInSameFrame: candidate not surfaced for %s", receiverNodeID)
		}
		return nil
	}))
	return receiverRunID
}
