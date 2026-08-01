// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestCountByNamedLock_DrivenSolelyByClaimHandleStateNotNodeRunState(t *testing.T) {
	t.Parallel()
	d := openSQLite(t)
	ctx := context.Background()
	rawDB, ok := sqlitedrv.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}

	const lockName = "running-presentation-lock"
	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	frameID := uuid.New()
	nodeID := uuid.New()
	nodeRunID := uuid.New()
	scopeID := uuid.New().String()

	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, target_routing_identity) VALUES (?, ?, 'test-agent')`,
		instanceID, templateID,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id) VALUES (?, 'main', '', ?)`,
		scopeID, instanceID,
	); err != nil {
		t.Fatalf("seed run_scope: %v", err)
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
		frameID.String(), instanceID, msgID, scopeID,
	); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES (?, ?, 'fixture')`,
		nodeID.String(), instanceID,
	); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_claim_producers, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
		 VALUES (?, ?, 'stub', '[]', datetime('now'), 'held', 'cascade', 1, ?, ?)`,
		nodeRunID.String(), nodeID.String(), frameID.String(), scopeID,
	); err != nil {
		t.Fatalf("seed held node-run: %v", err)
	}

	store := d.Tables()
	nodeRunShared := shared.UUID(nodeRunID)
	nodeShared := shared.UUID(nodeID)
	frameShared := shared.UUID(frameID)
	name := lockName
	claimID := shared.UUID(uuid.New())

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimID,
			NodeRunID:          &nodeRunShared,
			LockKind:           persistence.LockKindNamed,
			LockName:           &name,
			HolderSupervisorID: "running-presentation-supervisor",
			HolderNodeID:       nodeShared,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
			FrameID:            &frameShared,
		}, tx)
	}); err != nil {
		t.Fatalf("insert named-lock claim handle: %v", err)
	}

	countFor := func(t *testing.T) int {
		t.Helper()
		var n int
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			n, err = store.ClaimHandles().CountByNamedLock(ctx, lockName, tx)
			return err
		}); err != nil {
			t.Fatalf("CountByNamedLock: %v", err)
		}
		return n
	}

	if got := countFor(t); got != 1 {
		t.Fatalf("CountByNamedLock while holder node-run state=held: got %d, want 1 -- "+
			"a held node's active named-lock claim must still occupy capacity; the count is driven "+
			"solely by claim_handles.state, never by the holder's node_runs.state", got)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, nodeRunShared, cascade.NodeStateFresh, cascade.ReasonAutoTerminalCommit, nil, tx)
	}); err != nil {
		t.Fatalf("transition node-run to fresh: %v", err)
	}
	if got := countFor(t); got != 1 {
		t.Fatalf("CountByNamedLock after holder node-run settled held->fresh: got %d, want 1 -- "+
			"the count must not react to node_runs.state changes at all, even the run's own terminal "+
			"settlement", got)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Promote(ctx, claimID, "running-presentation-supervisor",
			spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("promote claim handle to committed: %v", err)
	}
	if got := countFor(t); got != 0 {
		t.Fatalf("CountByNamedLock after claim promoted to committed (node-run already settled fresh): got %d, want 0 -- "+
			"the count must track claim_handles.state leaving active, independent of node_runs.state", got)
	}
}
