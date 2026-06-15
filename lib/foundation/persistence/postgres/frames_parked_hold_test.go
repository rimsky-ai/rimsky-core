// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// seedFrameParkedFixture creates template → main run scope → instance via
// the public accessors, then raw-inserts a frame (in the given state), a
// node, and a single parked node_run (phase='parked', state='parked').
// Returns (instanceID, frameID). The parked run is the only run for the
// frame — there are no stale/running runs. The instance carries
// terminate_after_run = true so the durable-by-default gate is satisfied
// and the instance-terminated assertions hinge solely on the parked guard.
func seedFrameParkedFixture(
	t *testing.T, ctx context.Context, d persistence.Database, frameState string,
) (shared.UUID, shared.UUID) {
	t.Helper()
	store := d.Tables()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	mainRunScopeID := uuid.New()
	frameID := uuid.New()
	nodeID := uuid.New()
	dispatchID := uuid.New()

	tmpl := spec.TemplateSpec{
		Name:                "parked-hold-fixture",
		Version:             "1",
		FrameResolutionMode: spec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainRunScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash, MainRunScopeID: mainRunScopeID,
			TerminateAfterRun: true,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedFrameParkedFixture: %v", err)
	}

	// @constraint: raw-insert frame/node/parked-run through the test escape hatch
	// because the persistence interface does not surface a "force a parked run"
	// seed path; source_node_ids has a CHECK (length >= 1); a terminal frame
	// requires ended_at, a running frame requires started_at.
	endedClause := "NULL"
	startedClause := "now()"
	if frameState == "completed" || frameState == "failed" {
		endedClause = "now()"
	}
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, frame_resolution_mode, state, source_node_ids,
		    frame_timeout_ms, started_at, ended_at)
		 VALUES ($1, $2, 'serial_queue', $3, ARRAY[$4]::uuid[], 60000, `+startedClause+`, `+endedClause+`)`,
		frameID, instanceID, frameState, nodeID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id)
		 VALUES ($1, $2, 'fixture-node-type', $3)`,
		nodeID, instanceID, frameID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, phase, state, frame_id, run_scope_id)
		 VALUES ($1, $2, 'test-executor', 'parked', 'parked', $3, $4)`,
		dispatchID, nodeID, frameID, mainRunScopeID,
	)
	return shared.UUID(instanceID), shared.UUID(frameID)
}

// TestPGParkedNodeRunHoldsFrameOpen: a running frame whose only node_run
// is parked must NOT appear in ListRunningFramesNoPendingNodes (a parked
// run is unresolved work and holds the frame open).
func TestPGParkedNodeRunHoldsFrameOpen(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	store := d.Tables()
	_, frameID := seedFrameParkedFixture(t, ctx, d, "running")

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

// TestPGMarkInstanceTerminatedIfDoneHoldsForParkedRun: with the owning
// frame already terminal (so the frames-in-flight guard does not block
// termination), a parked node_run must still prevent the instance from
// being marked terminated.
func TestPGMarkInstanceTerminatedIfDoneHoldsForParkedRun(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	store := d.Tables()
	instanceID, _ := seedFrameParkedFixture(t, ctx, d, "completed")

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Frames().MarkInstanceTerminatedIfDone(ctx, instanceID, tx)
	}); err != nil {
		t.Fatalf("MarkInstanceTerminatedIfDone: %v", err)
	}

	var row *persistence.InstanceRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := store.Instances().Get(ctx, instanceID, tx)
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

// seedResolvedFrameInstancePG creates an instance (terminate_after_run set
// per the flag) with one terminal (completed) frame and a single fresh node
// — all work resolved, no parked/in-flight run. This is the shape
// transitionFrameEnd sees at a real frame-end; the only thing deciding the
// terminal predicate is the terminate_after_run gate.
func seedResolvedFrameInstancePG(
	t *testing.T, ctx context.Context, d persistence.Database, terminateAfterRun bool,
) shared.UUID {
	t.Helper()
	store := d.Tables()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	mainRunScopeID := uuid.New()
	frameID := uuid.New()
	nodeID := uuid.New()

	tmpl := spec.TemplateSpec{
		Name:                "resolved-frame-fixture",
		Version:             "1",
		FrameResolutionMode: spec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainRunScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash, MainRunScopeID: mainRunScopeID,
			TerminateAfterRun: terminateAfterRun,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedResolvedFrameInstancePG: %v", err)
	}

	// @deliberate: terminal frame + fresh node (no run row) means all work resolved.
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, frame_resolution_mode, state, source_node_ids,
		    frame_timeout_ms, started_at, ended_at)
		 VALUES ($1, $2, 'serial_queue', 'completed', ARRAY[$3]::uuid[], 60000, now(), now())`,
		frameID, instanceID, nodeID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id)
		 VALUES ($1, $2, 'fixture-node-type', $3)`,
		nodeID, instanceID, frameID,
	)
	return shared.UUID(instanceID)
}

func markTerminatedAndGetPG(
	t *testing.T, ctx context.Context, d persistence.Database, instanceID shared.UUID,
) *persistence.InstanceRow {
	t.Helper()
	store := d.Tables()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Frames().MarkInstanceTerminatedIfDone(ctx, instanceID, tx)
	}); err != nil {
		t.Fatalf("MarkInstanceTerminatedIfDone: %v", err)
	}
	var row *persistence.InstanceRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := store.Instances().Get(ctx, instanceID, tx)
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

// TestPGMarkInstanceTerminatedIfDoneFiresForTerminateAfterRun is the
// positive companion to the parked-hold test: a terminate_after_run
// instance whose frame has fully resolved (no parked/in-flight run) IS
// marked terminated, proving the parked-hold test is meaningful.
func TestPGMarkInstanceTerminatedIfDoneFiresForTerminateAfterRun(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	instanceID := seedResolvedFrameInstancePG(t, ctx, d, true)
	row := markTerminatedAndGetPG(t, ctx, d, instanceID)
	if row.TerminatedAt == nil {
		t.Fatalf("terminate_after_run instance with a fully-resolved frame was NOT terminated; "+
			"the strict terminal predicate must fire at frame-end for instance %s", instanceID)
	}
}

// TestPGMarkInstanceTerminatedIfDoneSkipsDurableDefault pins durable-by-
// default at the predicate level: an instance created without the flag is
// never self-terminated even when all its work has resolved.
func TestPGMarkInstanceTerminatedIfDoneSkipsDurableDefault(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	instanceID := seedResolvedFrameInstancePG(t, ctx, d, false)
	row := markTerminatedAndGetPG(t, ctx, d, instanceID)
	if row.TerminatedAt != nil {
		t.Fatalf("durable-by-default instance (terminate_after_run=false) was self-terminated "+
			"(terminated_at=%v); a durable instance must survive its own drain", *row.TerminatedAt)
	}
}
