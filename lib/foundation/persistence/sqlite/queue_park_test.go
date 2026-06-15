// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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

	// @constraint: FK chain order — template → instance → frame → node → node-run.
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
	// @constraint: instance ↔ main_run_scope mutually FK each other post-RunScope-first cutover; both rows must land in one tx with deferred constraints.
	scopeID := uuid.New().String()
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
	// @constraint: rimsky_frames CHECK requires frame_timeout_ms >= 60000; use the minimum permitted value (the test never trips the timeout).
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, frame_timeout_ms, started_at)
		 VALUES (?, ?, 'serial_queue', 'running', '[]', 60000, datetime('now'))`,
		frameID.String(), instanceID,
	); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	// @constraint: post-stage-3 cutover — state lives on rimsky_node_runs; rimsky_nodes row carries only identity + frame_id.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id)
		 VALUES (?, ?, 'fixture', ?)`,
		nodeID.String(), instanceID, frameID.String(),
	); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// @constraint: node-run must start in phase='active' so ParkActiveInTx accepts it.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, claimed_by, claimed_at, last_heartbeat_at, phase, frame_id, run_scope_id)
		 VALUES (?, ?, 'stub', '[]', datetime('now'), 'sup-1', datetime('now'), datetime('now'), 'active', ?, ?)`,
		uuid.UUID(dispatchID).String(), nodeID.String(), frameID.String(), scopeID,
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

	// @deliberate: drive row to phase='pending' here so LoadResumeMetadataInTx fires the resume-dispatch path; in production the runner does this inside its per-candidate acquisition tx. The wake_reason arg is the WakeReason enum string.
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

	// @constraint: LoadResumeMetadataInTx must scan parked_at TEXT into time.Time without error — regression surface where sql.NullTime previously failed and the runner's short-circuit swallowed it, treating the dispatch as fresh.
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
	// @constraint: ParkedAt round-trip — SQLite stores RFC3339Nano text and parseTime handles the parse; equality holds at microsecond precision.
	if rm.ParkedAt.IsZero() {
		t.Fatalf("ParkedAt: got zero, want %v (regression: TEXT → sql.NullTime scan failure)",
			parkedAt)
	}
	if !rm.ParkedAt.Equal(parkedAt) {
		t.Fatalf("ParkedAt: got %v, want %v", rm.ParkedAt, parkedAt)
	}

	// @constraint: ClearResumeMetadataInTx must leave a subsequent LoadResumeMetadataInTx returning nil — the post-resume path empties metadata cleanly.
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
