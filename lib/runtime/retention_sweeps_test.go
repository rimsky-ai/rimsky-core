// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestSweepRunTreeRetention_TraceTrailingOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tables := d.Tables()
	rawDB, ok := sqlitedrv.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	scopeID := uuid.New().String()
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

	now := time.Now().UTC()
	oldTime := now.Add(-24 * time.Hour)
	rfc := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

	seedTerminalFrame := func(endedAt time.Time) (string, string) {
		frameID := uuid.New().String()
		nodeID := uuid.New().String()
		runID := uuid.New().String()
		msgID := uuid.New().String()
		frameScopeID := uuid.New().String()
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id) VALUES (?, 'main', '', ?)`,
			frameScopeID, instanceID,
		); err != nil {
			t.Fatalf("seed frame run_scope: %v", err)
		}
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_messages
			   (id, instance_id, type, sender, sender_kind, received_at)
			 VALUES (?, ?, 'test/seed', 'test', 'operator', ?)`,
			msgID, instanceID, rfc(endedAt),
		); err != nil {
			t.Fatalf("seed message: %v", err)
		}
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_frames
			   (frame_id, instance_id, triggering_message_id, root_run_scope_id, started_at, ended_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			frameID, instanceID, msgID, frameScopeID, rfc(endedAt), rfc(endedAt),
		); err != nil {
			t.Fatalf("seed frame: %v", err)
		}
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES (?, ?, 'fixture')`,
			nodeID, instanceID,
		); err != nil {
			t.Fatalf("seed node: %v", err)
		}
		if _, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_node_runs
			   (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
			 VALUES (?, ?, 'stub', '[]', ?, 'failed', 'cascade', 1, ?, ?)`,
			runID, nodeID, rfc(endedAt), frameID, frameScopeID,
		); err != nil {
			t.Fatalf("seed node_run: %v", err)
		}
		return frameID, runID
	}
	oldFrame, oldRun := seedTerminalFrame(oldTime)
	recentFrame, recentRun := seedTerminalFrame(now.Add(-time.Minute))

	insertEvent := func(when time.Time) int64 {
		res, err := rawDB.ExecContext(ctx,
			`INSERT INTO rimsky_events (instance_id, kind, payload, occurred_at) VALUES (?, 'work_started', '{}', ?)`,
			instanceID, rfc(when),
		)
		if err != nil {
			t.Fatalf("seed event: %v", err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	oldEventID := insertEvent(oldTime)
	recentEventID := insertEvent(now.Add(-time.Minute))

	if _, err := runtime.SweepRunTreeRetention(
		ctx, runtime.RetentionConfig{TraceTrailing: time.Hour},
		tables, now, shared.SilentLogger{},
	); err != nil {
		t.Fatalf("SweepRunTreeRetention: %v", err)
	}

	count := func(table, col string, val any) int {
		var n int
		if err := rawDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+table+" WHERE "+col+" = ?", val,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}

	if count("rimsky_frames", "frame_id", oldFrame) != 0 {
		t.Errorf("old terminal frame should be reaped by the trailing window")
	}
	if count("rimsky_node_runs", "id", oldRun) != 0 {
		t.Errorf("old frame's node_run should be cascade-deleted")
	}
	if count("rimsky_frames", "frame_id", recentFrame) != 1 {
		t.Errorf("recent terminal frame must survive")
	}
	if count("rimsky_node_runs", "id", recentRun) != 1 {
		t.Errorf("recent frame's node_run must survive")
	}
	if count("rimsky_events", "id", oldEventID) != 0 {
		t.Errorf("old audit event should be reaped by the trailing window")
	}
	if count("rimsky_events", "id", recentEventID) != 1 {
		t.Errorf("recent audit event must survive")
	}
}
