// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

func TestParkedNodeRunHoldsFrameOpen(t *testing.T) {
	t.Parallel()
	d := openSQLite(t)
	ctx := context.Background()

	rawDB, ok := sqlitedrv.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	frameID := uuid.New()
	nodeID := uuid.New()
	nodeRunID := uuid.New()

	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	scopeID := uuid.New().String()
	stx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, target_routing_identity) VALUES (?, ?, 'test-agent')`,
		instanceID, templateID,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id) VALUES (?, 'main', '', ?)`,
		scopeID, instanceID,
	); err != nil {
		t.Fatalf("seed run_scope: %v", err)
	}
	if err := stx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	msgID := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'fixture/message', 'operator', 'operator')`,
		msgID, instanceID,
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, root_run_scope_id, started_at)
		 VALUES (?, ?, ?, ?, datetime('now'))`,
		frameID.String(), instanceID, msgID, scopeID,
	); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES (?, ?, 'fixture')`,
		nodeID.String(), instanceID,
	); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_claim_producers, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
		 VALUES (?, ?, 'stub', '[]', datetime('now'), 'parked', 'cascade', 1, ?, ?)`,
		nodeRunID.String(), nodeID.String(), frameID.String(), scopeID,
	); err != nil {
		t.Fatalf("seed parked node-run: %v", err)
	}

	store := d.Tables()
	var pending []persistence.FramePending
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := store.Frames().ListRunningFramesNoPendingNodes(ctx, tx)
		pending = out
		return err
	}); err != nil {
		t.Fatalf("ListRunningFramesNoPendingNodes: %v", err)
	}

	for _, p := range pending {
		if p.FrameID == frameID {
			t.Fatalf("frame %s was reported as ended (no-pending) while a node_run is parked; "+
				"a parked run is unresolved work and must hold the frame open", frameID)
		}
	}
}
