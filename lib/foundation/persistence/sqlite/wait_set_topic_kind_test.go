// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

func TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy(t *testing.T) {
	t.Parallel()
	d := openSQLite(t)
	ctx := context.Background()
	rawDB, ok := sqlitedrv.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	scopeID := uuid.New().String()
	frameID := uuid.New().String()
	receiverNodeID := uuid.New().String()
	senderNodeID := uuid.New().String()
	receiverNodeRunID := uuid.New().String()
	senderNodeRunID := uuid.New().String()

	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	stx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash) VALUES (?, ?)`,
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
		frameID, instanceID, msgID, scopeID,
	); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	for _, nID := range []string{receiverNodeID, senderNodeID} {
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES (?, ?, 'fixture')`,
			nID, instanceID,
		); err != nil {
			t.Fatalf("seed node %s: %v", nID, err)
		}
	}
	runs := []struct{ runID, nodeID string }{
		{receiverNodeRunID, receiverNodeID},
		{senderNodeRunID, senderNodeID},
	}
	for i, r := range runs {
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_node_runs
			   (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
			 VALUES (?, ?, 'stub', '[]', datetime('now'), 'running', 'cascade', ?, ?, ?)`,
			r.runID, r.nodeID, i+1, frameID, scopeID,
		); err != nil {
			t.Fatalf("seed node-run %s: %v", r.runID, err)
		}
	}

	for _, topicKind := range []string{"transient", "terminal"} {
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_wait_set
			   (frame_id, receiver_run_id, sender_run_id, topic_kind)
			 VALUES (?, ?, ?, ?)`,
			frameID, receiverNodeRunID, senderNodeRunID, topicKind,
		); err != nil {
			t.Fatalf("insert wait_set row topic_kind=%q: %v; "+
				"the topic_kind CHECK must admit 'transient' and 'terminal'", topicKind, err)
		}
	}

	var count int
	if err := rawDB.QueryRowContext(ctx,
		`SELECT count(*) FROM rimsky_wait_set
		 WHERE frame_id = ? AND topic_kind IN ('transient','terminal')`,
		frameID,
	).Scan(&count); err != nil {
		t.Fatalf("count wait_set rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 wait-set rows with broadened topic_kind values, got %d; "+
			"the topic_kind CHECK must admit 'transient','terminal'", count)
	}

	for _, rejected := range []string{"message", "event"} {
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_wait_set
			   (frame_id, receiver_run_id, sender_run_id, topic_kind)
			 VALUES (?, ?, ?, ?)`,
			frameID, receiverNodeRunID, senderNodeRunID, rejected,
		); err == nil {
			t.Fatalf("insert wait_set row topic_kind=%q returned nil error; "+
				"the topic_kind CHECK must REJECT %q (retired value)", rejected, rejected)
		}
	}
}
