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
		Name:           "parked-hold-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
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

	endedClause := "NULL"
	startedClause := "now()"
	if frameState == "completed" || frameState == "failed" {
		endedClause = "now()"
	}
	messageID := uuid.New()
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES ($1, $2, 'fixture/message', 'operator', 'operator')`,
		messageID, instanceID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, state,
		    frame_timeout_ms, started_at, ended_at)
		 VALUES ($1, $2, $3, $4, 60000, `+startedClause+`, `+endedClause+`)`,
		frameID, instanceID, messageID, frameState,
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
		Name:           "resolved-frame-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
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

	messageID := uuid.New()
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES ($1, $2, 'fixture/message', 'operator', 'operator')`,
		messageID, instanceID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, state,
		    frame_timeout_ms, started_at, ended_at)
		 VALUES ($1, $2, $3, 'completed', 60000, now(), now())`,
		frameID, instanceID, messageID,
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
