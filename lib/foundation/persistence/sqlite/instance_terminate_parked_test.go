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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// TestMarkInstanceTerminatedIfDoneHoldsForParkedRun pins the instance-
// terminated predicate to the same unresolved-work definition as
// ListRunningFramesNoPendingNodes (the two predicates are documented as
// agreeing, and run in the same tx in frame/engine.go::transitionFrameEnd).
// A parked node_run is unresolved work — the instance must NOT be marked
// terminated while a node sits parked, or the next deadline-elapsed wake
// would resume work against a terminated instance.
//
// The instance is created with terminate_after_run = true so the
// durable-by-default gate is satisfied and the predicate hinges solely on
// the parked-run guard — a durable instance (the default) would never
// terminate here regardless, which would not exercise the parked clause.
// Termination reads nothing about publisher-subscriptions (that coupling
// is gone), so none is seeded. Before the parked-counting fix, the instance
// was wrongly terminated.
func TestMarkInstanceTerminatedIfDoneHoldsForParkedRun(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()

	rawDB := sqlitedrv.DBFromDatabase(d)

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	frameID := uuid.New()
	nodeID := uuid.New()
	dispatchID := uuid.New()

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
		`INSERT INTO rimsky_instances (id, template_hash, main_run_scope_id, terminate_after_run)
		 VALUES (?, ?, ?, 1)`,
		instanceID.String(), templateID, scopeID,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
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
	// @constraint: frame seeded terminal (completed) so the frames-in-flight
	// guard does NOT block termination — only the node-run predicate can.
	// @constraint: triggering message seeded so the
	// rimsky_frames.triggering_message_id NOT NULL FK is satisfied.
	msgID := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'fixture/message', 'operator', 'operator')`,
		msgID, instanceID.String(),
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, state, frame_timeout_ms, started_at, ended_at)
		 VALUES (?, ?, ?, 'completed', 60000, datetime('now'), datetime('now'))`,
		frameID.String(), instanceID.String(), msgID,
	); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id)
		 VALUES (?, ?, 'fixture', ?)`,
		nodeID.String(), instanceID.String(), frameID.String(),
	); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// @constraint: exactly one parked node_run (phase='parked',
	// state='parked') seeded; no stale/running runs, so the predicate hinges
	// solely on the parked clause.
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		 VALUES (?, ?, 'stub', '[]', datetime('now'), 'parked', 'parked', ?, ?)`,
		dispatchID.String(), nodeID.String(), frameID.String(), scopeID,
	); err != nil {
		t.Fatalf("seed parked node-run: %v", err)
	}

	store := d.Tables()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Frames().MarkInstanceTerminatedIfDone(ctx, shared.UUID(instanceID), tx)
	}); err != nil {
		t.Fatalf("MarkInstanceTerminatedIfDone: %v", err)
	}

	var row *persistence.InstanceRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := store.Instances().Get(ctx, shared.UUID(instanceID), tx)
		row = out
		return err
	}); err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}
	if row == nil {
		t.Fatalf("Instances.Get: nil row")
	}
	if row.TerminatedAt != nil {
		t.Fatalf("instance was marked terminated (terminated_at=%v) while a node_run is parked; "+
			"a parked run is unresolved work and must block instance termination", *row.TerminatedAt)
	}
}

// seedResolvedFrameInstance creates a terminate_after_run-flagged instance
// (controlled by terminateAfterRun) with one terminal (completed) frame and
// a single fresh node — i.e. all work resolved, no in-flight or parked run.
// This is the exact shape transitionFrameEnd sees at a real frame-end.
// Returns the instance id.
func seedResolvedFrameInstance(t *testing.T, ctx context.Context, d persistence.Database, terminateAfterRun bool) uuid.UUID {
	t.Helper()
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
	flag := 0
	if terminateAfterRun {
		flag = 1
	}
	stx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, main_run_scope_id, terminate_after_run)
		 VALUES (?, ?, ?, ?)`,
		instanceID.String(), templateID, scopeID, flag,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
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
	// @constraint: frame seeded terminal so the (former) frames-in-flight
	// clause is irrelevant; the node is fresh (no in-flight run row), so
	// the only thing deciding the predicate is the terminate_after_run gate.
	// @constraint: triggering message seeded so the
	// rimsky_frames.triggering_message_id NOT NULL FK is satisfied.
	msgID := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'fixture/message', 'operator', 'operator')`,
		msgID, instanceID.String(),
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, state, frame_timeout_ms, started_at, ended_at)
		 VALUES (?, ?, ?, 'completed', 60000, datetime('now'), datetime('now'))`,
		frameID.String(), instanceID.String(), msgID,
	); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id)
		 VALUES (?, ?, 'fixture', ?)`,
		uuid.New().String(), instanceID.String(), frameID.String(),
	); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return instanceID
}

func markTerminatedAndGet(t *testing.T, ctx context.Context, d persistence.Database, instanceID uuid.UUID) *persistence.InstanceRow {
	t.Helper()
	store := d.Tables()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Frames().MarkInstanceTerminatedIfDone(ctx, shared.UUID(instanceID), tx)
	}); err != nil {
		t.Fatalf("MarkInstanceTerminatedIfDone: %v", err)
	}
	var row *persistence.InstanceRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := store.Instances().Get(ctx, shared.UUID(instanceID), tx)
		row = out
		return err
	}); err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}
	if row == nil {
		t.Fatalf("Instances.Get: nil row")
	}
	return row
}

// TestMarkInstanceTerminatedIfDoneFiresForTerminateAfterRun is the positive
// companion to the parked-hold test: a terminate_after_run instance whose
// frame has fully resolved (no parked/in-flight run) IS marked terminated.
// This proves the parked-hold test is meaningful — the only thing holding
// termination back there is the parked guard, not the flag gate.
func TestMarkInstanceTerminatedIfDoneFiresForTerminateAfterRun(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()
	instanceID := seedResolvedFrameInstance(t, ctx, d, true)
	row := markTerminatedAndGet(t, ctx, d, instanceID)
	if row.TerminatedAt == nil {
		t.Fatalf("terminate_after_run instance with a fully-resolved frame was NOT terminated; "+
			"the strict terminal predicate must fire at frame-end for instance %s", instanceID)
	}
}

// TestMarkInstanceTerminatedIfDoneSkipsDurableDefault pins durable-by-default
// at the predicate level: an instance created without the flag (the default)
// is never self-terminated even when all its work has resolved. It lives
// until force-terminate.
func TestMarkInstanceTerminatedIfDoneSkipsDurableDefault(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()
	instanceID := seedResolvedFrameInstance(t, ctx, d, false)
	row := markTerminatedAndGet(t, ctx, d, instanceID)
	if row.TerminatedAt != nil {
		t.Fatalf("durable-by-default instance (terminate_after_run=false) was self-terminated "+
			"(terminated_at=%v); a durable instance must survive its own drain", *row.TerminatedAt)
	}
}
