// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	sqlitedrv "github.com/fallguy/rimsky/foundation/persistence/sqlite"
	"github.com/fallguy/rimsky/foundation/shared"
)

// TestSQLiteParkResumeRoundTrip exercises the SQLite park / load-resume /
// clear sequence end-to-end. Regression coverage for the
// `parkedAt sql.NullTime` bug: modernc/sqlite v1.50.0 onward refuses to
// scan a TEXT column (RFC3339Nano) into sql.NullTime with
// `unsupported Scan, storing driver.Value type string into type
// *time.Time`. Before the fix, LoadResumeMetadataInTx silently failed
// (the runner's `rerr == nil && rm != nil` short-circuit at
// runtime/runner_acquire.go:382 swallowed it), so resume
// metadata was lost and the dispatch proceeded as a fresh dispatch —
// effectively breaking park-resume on SQLite.
func TestSQLiteParkResumeRoundTrip(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()

	rawDB := sqlitedrv.DBFromDatabase(d)

	// Seed the FK chain (template → instance → frame → node → node-run).
	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	frameID := uuid.New()
	nodeID := uuid.New()
	dispatchID := shared.UUID(uuid.New())

	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash) VALUES (?, ?)`,
		instanceID, templateID,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	// rimsky_frames requires frame_timeout_ms >= 60000 (CHECK). Use the
	// minimum permitted value; the test never trips the timeout.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, frame_timeout_ms, started_at)
		 VALUES (?, ?, 'serial_queue', 'running', '[]', 60000, datetime('now'))`,
		frameID.String(), instanceID,
	); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	// Post-stage-3 cutover: state lives on rimsky_node_runs; the
	// rimsky_nodes row carries only identity + frame_id.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id)
		 VALUES (?, ?, 'fixture', ?)`,
		nodeID.String(), instanceID, frameID.String(),
	); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// node-run starts in phase='active' so ParkActiveInTx accepts it.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, claimed_by, claimed_at, last_heartbeat_at, phase, frame_id)
		 VALUES (?, ?, 'stub', '[]', datetime('now'), 'sup-1', datetime('now'), datetime('now'), 'active', ?)`,
		uuid.UUID(dispatchID).String(), nodeID.String(), frameID.String(),
	); err != nil {
		t.Fatalf("seed node-run: %v", err)
	}

	queue := d.Queue()
	store := d.Tables()

	parkedAt := time.Now().UTC().Truncate(time.Microsecond)
	resumeAt := parkedAt.Add(2 * time.Second)
	parkInput := persistence.ParkActiveInput{
		DispatchID:        dispatchID,
		ExpectedClaimedBy: "sup-1",
		ParkedAt:          parkedAt,
		ResumeAt:          resumeAt,
		Reason:            "rate_limit",
		SessionToken:      "session-abc",
		PayloadInline:     []byte(`{"hint":"backoff"}`),
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return queue.ParkActiveInTx(ctx, tx, parkInput)
	}); err != nil {
		t.Fatalf("ParkActiveInTx: %v", err)
	}

	// Resume the row to phase='pending' so the resume-dispatch path's
	// LoadResumeMetadataInTx call would fire (in production this happens
	// inside the runner's per-candidate acquisition tx). The wake_reason
	// arg is the WakeReason enum string.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := queue.ResumeParkedInTx(ctx, tx, dispatchID, "deadline_elapsed")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ResumeParkedInTx: ok=false")
		}
		return nil
	}); err != nil {
		t.Fatalf("ResumeParkedInTx: %v", err)
	}

	// LoadResumeMetadataInTx is the bug surface. Before the fix this
	// returned an error trying to scan parked_at TEXT into sql.NullTime;
	// the runner's short-circuit swallowed the error and treated the
	// dispatch as fresh.
	var rm *persistence.ResumeMetadataRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := queue.LoadResumeMetadataInTx(ctx, tx, dispatchID)
		rm = out
		return err
	}); err != nil {
		t.Fatalf("LoadResumeMetadataInTx: %v", err)
	}
	if rm == nil {
		t.Fatalf("LoadResumeMetadataInTx: returned nil row, want resume metadata")
	}
	if rm.Reason != "rate_limit" {
		t.Fatalf("Reason: got %q, want rate_limit", rm.Reason)
	}
	if rm.SessionToken != "session-abc" {
		t.Fatalf("SessionToken: got %q, want session-abc", rm.SessionToken)
	}
	if string(rm.PayloadInline) != `{"hint":"backoff"}` {
		t.Fatalf("PayloadInline: got %q, want {\"hint\":\"backoff\"}", rm.PayloadInline)
	}
	if rm.WakeReason != "deadline_elapsed" {
		t.Fatalf("WakeReason: got %q, want deadline_elapsed", rm.WakeReason)
	}
	// ParkedAt round-trip: SQLite stores RFC3339Nano text; parseTime
	// handles the parse. We expect equality at microsecond precision.
	if rm.ParkedAt.IsZero() {
		t.Fatalf("ParkedAt: got zero, want %v (regression: TEXT → sql.NullTime scan failure)",
			parkedAt)
	}
	if !rm.ParkedAt.Equal(parkedAt) {
		t.Fatalf("ParkedAt: got %v, want %v", rm.ParkedAt, parkedAt)
	}

	// ClearResumeMetadataInTx wipes the metadata; a second load returns
	// nil. Confirms the post-resume path empties cleanly.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return queue.ClearResumeMetadataInTx(ctx, tx, dispatchID)
	}); err != nil {
		t.Fatalf("ClearResumeMetadataInTx: %v", err)
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := queue.LoadResumeMetadataInTx(ctx, tx, dispatchID)
		rm = out
		return err
	}); err != nil {
		t.Fatalf("LoadResumeMetadataInTx (post-clear): %v", err)
	}
	if rm != nil {
		t.Fatalf("LoadResumeMetadataInTx (post-clear): got %+v, want nil", rm)
	}
}
