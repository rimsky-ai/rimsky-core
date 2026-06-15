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
// rimsky_wait_set row for each of the two signal classes the legacy
// CHECK rejects but the new CHECK admits — 'transient' and 'terminal' —
// against a freshly-migrated SQLite and asserts every insert succeeds.
// Also asserts that 'message' is rejected by the post-011 CHECK: the
// virtual-node-settle model has no wait-set rows under that bucket.
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
	// @constraint: rimsky_frames.triggering_message_id is NOT NULL FK into rimsky_messages (post-010 schema), so a triggering message must be seeded before the frame row.
	msgID := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'fixture/message', 'operator', 'operator')`,
		msgID, instanceID,
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	// @constraint: rimsky_frames CHECK enforces frame_timeout_ms >= 60000, so the seed value must clear the floor.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, state, frame_timeout_ms, started_at)
		 VALUES (?, ?, ?, 'running', 60000, datetime('now'))`,
		frameID, instanceID, msgID,
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

	// @deliberate: each of the two broadened topic_kind values must be admitted by the post-006 / post-011 CHECK; the legacy CHECK (001-schema.sql) rejects both, and 'message' is no longer admitted post-011, so it is asserted separately below as a REJECTION.
	for _, topicKind := range []string{"transient", "terminal"} {
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_wait_set
			   (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
			 VALUES (?, ?, ?, ?, 'direct')`,
			frameID, receiverRunID, senderRunID, topicKind,
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

	// @blessed-invariant: wait-set-topic-kind-rejects-message — post-011 the rimsky_wait_set.topic_kind CHECK MUST reject 'message'; the virtual-node-settle model carries no wait-set rows under that bucket, so any INSERT with topic_kind='message' must fail through the CHECK rejection path.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_wait_set
		   (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
		 VALUES (?, ?, ?, ?, 'direct')`,
		frameID, receiverRunID, senderRunID, "message",
	); err == nil {
		t.Fatalf("insert wait_set row topic_kind='message' returned nil error; " +
			"the topic_kind CHECK must REJECT 'message' (post-011 retirement)")
	}
}
