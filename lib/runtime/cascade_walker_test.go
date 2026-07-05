// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: cascade
// @decision: walker-rule-per-sender-node
func TestEnsureCascadePending_PerSenderNodeRule(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))

	tables := d.Tables()
	q := d.Queue()
	rawDB := sqlitedrv.DBFromDatabase(d)
	args := RunArgs{
		Persist: tables,
		Queue:   q,
		Clock:   shared.SystemClock{},
		Logger:  shared.SilentLogger{},
	}

	templateHash := "sha256-cascade-walker-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	receiverNodeID := shared.UUID(uuid.New())
	senderANodeID := shared.UUID(uuid.New())
	senderBNodeID := shared.UUID(uuid.New())

	senderARunID := shared.UUID(uuid.New())
	senderBRunID := shared.UUID(uuid.New())
	senderASecondRunID := shared.UUID(uuid.New())

	messageID := shared.UUID(uuid.New())
	stx, err := rawDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = stx.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateHash,
	)
	require.NoError(t, err)
	_, err = stx.ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id) VALUES (?, 'main', '', ?)`,
		mainScopeID.String(), instanceID.String(),
	)
	require.NoError(t, err)
	_, err = stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash) VALUES (?, ?)`,
		instanceID.String(), templateHash,
	)
	require.NoError(t, err)
	_, err = stx.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'cascade-walker-test', 'test', 'operator')`,
		messageID.String(), instanceID.String(),
	)
	require.NoError(t, err)
	_, err = stx.ExecContext(ctx,
		`INSERT INTO rimsky_frames (frame_id, instance_id, triggering_message_id, root_run_scope_id, state, started_at, frame_timeout_ms)
		 VALUES (?, ?, ?, ?, 'running', datetime('now'), 600000)`,
		frameID.String(), instanceID.String(), messageID.String(), mainScopeID.String(),
	)
	require.NoError(t, err)
	for _, n := range []struct {
		id   shared.UUID
		ntyp string
	}{
		{receiverNodeID, "receiver"},
		{senderANodeID, "sender-a"},
		{senderBNodeID, "sender-b"},
	} {
		_, err = stx.ExecContext(ctx,
			`INSERT INTO rimsky_nodes (id, instance_id, node_type, executor, frame_id, tags, cascade_mode)
			 VALUES (?, ?, ?, 'stub', ?, '[]', 'most-recent')`,
			n.id.String(), instanceID.String(), n.ntyp, frameID.String(),
		)
		require.NoError(t, err)
	}
	require.NoError(t, stx.Commit())

	var firstPendingID shared.UUID
	t.Run("case1_no_pending_creates_new", func(t *testing.T) {
		require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			id, err := ensureCascadePending(ctx, args, tx,
				receiverNodeID, mainScopeID, frameID, senderANodeID, senderARunID)
			firstPendingID = id
			return err
		}))
		require.NotEqual(t, shared.UUID{}, firstPendingID,
			"case 1: should create a new pending receiver run when none exists")
	})

	t.Run("case2_pending_no_sender_node_accumulates", func(t *testing.T) {
		var after shared.UUID
		require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			id, err := ensureCascadePending(ctx, args, tx,
				receiverNodeID, mainScopeID, frameID, senderBNodeID, senderBRunID)
			after = id
			return err
		}))
		require.Equal(t, firstPendingID, after,
			"case 2: sender-b (different node) accumulates into the existing pending (diamond accumulation)")
	})

	t.Run("case3_pending_sender_node_already_covered_creates_new", func(t *testing.T) {
		_, err = rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_node_runs
			   (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id)
			 VALUES (?, ?, 'stub', '[]', datetime('now'), 'fresh', 100, 'cascade', ?, ?)`,
			senderARunID.String(), senderANodeID.String(), frameID.String(), mainScopeID.String(),
		)
		require.NoError(t, err)
		drainedAt := time.Now()
		require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return tables.WaitSet().Insert(ctx, persistence.WaitSetRow{
				FrameID:       frameID,
				ReceiverRunID: firstPendingID,
				SenderRunID:   senderARunID,
				TopicKind:     "terminal",
				DrainedAt:     &drainedAt,
			}, tx)
		}))

		var after shared.UUID
		require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			id, err := ensureCascadePending(ctx, args, tx,
				receiverNodeID, mainScopeID, frameID, senderANodeID, senderASecondRunID)
			after = id
			return err
		}))
		require.NotEqual(t, firstPendingID, after,
			"case 3: sender-a's node is already in pending's wait-set → new pending created (same-upstream re-cascade)")
	})
}
