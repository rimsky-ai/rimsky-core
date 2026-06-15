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

// TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy inserts a
// rimsky_wait_set row for each of the three signal classes the legacy
// CHECK rejects — 'transient', 'message', 'terminal' — against a
// freshly-migrated SQLite and asserts every insert succeeds. RED until
// 006-waitset-topic-kind-taxonomy rebuilds the table with the broadened
// CHECK; today each insert is rejected by
// CHECK (topic_kind IN ('state','attribute','event')).
func TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()
	rawDB := sqlitedrv.DBFromDatabase(d)

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	scopeID := uuid.New().String()
	frameID := uuid.New().String()
	receiverNodeID := uuid.New().String()
	senderNodeID := uuid.New().String()
	receiverRunID := uuid.New().String()
	senderRunID := uuid.New().String()

	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	// @constraint: rimsky_instances and rimsky_run_scopes mutually reference each other via FK, so both rows must be inserted inside a single transaction to satisfy deferred FK checks at commit.
	stx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, main_run_scope_id) VALUES (?, ?, ?)`,
		instanceID, templateID, scopeID,
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
	// @constraint: rimsky_frames CHECK enforces frame_timeout_ms >= 60000, so the seed value must clear the floor.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, frame_timeout_ms, started_at)
		 VALUES (?, ?, 'serial_queue', 'running', '[]', 60000, datetime('now'))`,
		frameID, instanceID,
	); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	// @constraint: uq_node_runs_in_flight_per_run_scope forbids two in-flight runs sharing (node_id, run_scope_id), so the receiver and sender runs each need their own node.
	for _, nID := range []string{receiverNodeID, senderNodeID} {
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id) VALUES (?, ?, 'fixture', ?)`,
			nID, instanceID, frameID,
		); err != nil {
			t.Fatalf("seed node %s: %v", nID, err)
		}
	}
	runs := []struct{ runID, nodeID string }{
		{receiverRunID, receiverNodeID},
		{senderRunID, senderNodeID},
	}
	for _, r := range runs {
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_node_runs
			   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
			 VALUES (?, ?, 'stub', '[]', datetime('now'), 'active', 'running', ?, ?)`,
			r.runID, r.nodeID, frameID, scopeID,
		); err != nil {
			t.Fatalf("seed node-run %s: %v", r.runID, err)
		}
	}

	// @deliberate: each of the three broadened topic_kind values must be admitted by the rebuilt CHECK; the legacy CHECK rejects all three, which is what migration 006 fixes.
	for _, topicKind := range []string{"transient", "message", "terminal"} {
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_wait_set
			   (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
			 VALUES (?, ?, ?, ?, 'direct')`,
			frameID, receiverRunID, senderRunID, topicKind,
		); err != nil {
			t.Fatalf("insert wait_set row topic_kind=%q: %v; "+
				"the topic_kind CHECK must admit the full 5-value signal taxonomy", topicKind, err)
		}
	}

	var count int
	if err := rawDB.QueryRowContext(ctx,
		`SELECT count(*) FROM rimsky_wait_set
		 WHERE frame_id = ? AND topic_kind IN ('transient','message','terminal')`,
		frameID,
	).Scan(&count); err != nil {
		t.Fatalf("count wait_set rows: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 wait-set rows with broadened topic_kind values, got %d; "+
			"the topic_kind CHECK must admit 'transient','message','terminal'", count)
	}
}
