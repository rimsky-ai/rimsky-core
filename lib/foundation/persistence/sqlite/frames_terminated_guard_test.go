// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

// TestQueuedFrameNotPromotedForTerminatedInstance pins the orphaned-queued-
// frame guard under strict terminal semantics. With durable-by-default,
// a terminate_after_run instance can terminate at frame-end while a frame
// it never ran is still `queued` (a message arrived mid-run). That queued
// frame must NEVER be promoted against a terminated instance — promoting it
// would run work on an instance that has already reached terminal.
//
// We seed a terminated instance (terminated_at non-NULL) with one `queued`
// frame and no running frame, then call ListQueuedFramesReadyToStart. The
// frame must not be returned.
func TestQueuedFrameNotPromotedForTerminatedInstance(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()

	rawDB := sqlitedrv.DBFromDatabase(d)

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	frameID := uuid.New()

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
	// Instance is already terminated: terminated_at non-NULL.
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, main_run_scope_id, terminated_at)
		 VALUES (?, ?, ?, datetime('now'))`,
		instanceID.String(), templateID, scopeID,
	); err != nil {
		t.Fatalf("seed terminated instance: %v", err)
	}
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id) VALUES (?, 'main', '', ?)`,
		scopeID, instanceID.String(),
	); err != nil {
		t.Fatalf("seed run_scope: %v", err)
	}
	if err := stx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// One queued frame, no running frame — eligible to start except for
	// the terminated-instance guard.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, frame_timeout_ms)
		 VALUES (?, ?, 'serial_queue', 'queued', '[]', 60000)`,
		frameID.String(), instanceID.String(),
	); err != nil {
		t.Fatalf("seed queued frame: %v", err)
	}

	store := d.Tables()
	var ready []persistence.FrameQueuedReady
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := store.Frames().ListQueuedFramesReadyToStart(ctx, tx)
		ready = out
		return err
	}); err != nil {
		t.Fatalf("ListQueuedFramesReadyToStart: %v", err)
	}

	for _, r := range ready {
		if r.FrameID == frameID {
			t.Fatalf("queued frame %s for terminated instance %s was reported ready to start; "+
				"a frame must never be promoted against a terminated instance", frameID, instanceID)
		}
	}
}
