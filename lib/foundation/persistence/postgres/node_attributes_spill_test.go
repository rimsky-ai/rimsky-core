// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func seedNodeAndRunPG(
	t *testing.T, ctx context.Context, d persistence.Database, sequence int,
) (nodeID, runID, scopeID uuid.UUID) {
	t.Helper()
	store := d.Tables()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	mainRunScopeID := uuid.New()
	frame := uuid.New()
	node := uuid.New()
	run := uuid.New()

	tmpl := spec.TemplateSpec{
		Name:    "node-attr-spill-fixture",
		Version: "1",
		Nodes:   []spec.TemplateNodeDef{{Type: "fixture-node-type", Executor: "test-executor"}},
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainRunScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedNodeAndRunPG: %v", err)
	}

	messageID := uuid.New()
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES ($1, $2, 'fixture/message', 'operator', 'operator')`,
		messageID, instanceID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, root_run_scope_id, started_at)
		 VALUES ($1, $2, $3, $4, now())`,
		frame, instanceID, messageID, mainRunScopeID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES ($1, $2, 'fixture-node-type')`,
		node, instanceID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, state, sequence, creation_reason, enqueued_at, frame_id, run_scope_id)
		 VALUES ($1, $2, 'test-executor', 'stale', $3, 'cascade', now(), $4, $5)`,
		run, node, sequence, frame, mainRunScopeID,
	)
	return node, run, mainRunScopeID
}

func seedSecondRunPG(
	t *testing.T, ctx context.Context, d persistence.Database,
	nodeID, scopeID uuid.UUID, priorRunID uuid.UUID, sequence int,
) uuid.UUID {
	t.Helper()
	var frame uuid.UUID
	pgtest.QueryRowForTest(ctx, t, d, `SELECT frame_id FROM rimsky_node_runs WHERE id = $1`, []any{&frame}, priorRunID)
	run := uuid.New()
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, state, sequence, creation_reason, enqueued_at, frame_id, run_scope_id)
		 VALUES ($1, $2, 'test-executor', 'stale', $3, 'cascade', now(), $4, $5)`,
		run, nodeID, sequence, frame, scopeID,
	)
	return run
}

func spillHandlePG(t *testing.T, ctx context.Context, d persistence.Database, runID uuid.UUID) string {
	t.Helper()
	var handle *string
	pgtest.QueryRowForTest(ctx, t, d, `SELECT value_handle FROM rimsky_node_attributes WHERE node_run_id = $1`, []any{&handle}, runID)
	if handle == nil {
		return ""
	}
	return *handle
}

func TestPGNodeAttributesBackendMismatchErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 256, time.Hour)

	store := d.Tables()
	attrs := store.NodeAttributes()
	nodeID, runID, _ := seedNodeAndRunPG(t, ctx, d, 1)

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, shared.UUID(runID), shared.UUID(nodeID), map[string]any{"k": "v"}, tx)
	}); err != nil {
		t.Fatalf("Upsert seed: %v", err)
	}

	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_node_attributes
		    SET data                 = $1,
		        value_handle         = $2,
		        value_handle_backend = $3
		  WHERE node_run_id = $4`,
		`{"inline_survivor":"yes"}`, "handle-under-other-backend", "other-backend", runID,
	)

	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := attrs.GetByRun(ctx, shared.UUID(runID), tx)
		return err
	})
	if err == nil {
		t.Fatalf("GetByRun with mismatched value_handle_backend must error, not silently degrade to inline/empty data")
	}
}

func TestPGUpsertSpillDoesNotLeakLargeObjectWhenEnclosingTxRollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("PoolFromDatabaseForTest: not a postgres driver")
	}
	blob := pgpersist.NewPgLargeObjectBackend(pool)
	d.SetBlobBackend(blob, 8, time.Hour)

	store := d.Tables()
	attrs := store.NodeAttributes()
	nodeID, runID, _ := seedNodeAndRunPG(t, ctx, d, 1)

	sentinelErr := errors.New("force rollback")
	var handle string
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := attrs.Upsert(ctx, shared.UUID(runID), shared.UUID(nodeID),
			map[string]any{"big": strings.Repeat("x", 500)}, tx); err != nil {
			return err
		}
		pgT, err := pgpersist.UnwrapTxForTest(tx)
		if err != nil {
			return err
		}
		var h *string
		if err := pgT.QueryRow(ctx,
			`SELECT value_handle FROM rimsky_node_attributes WHERE node_run_id = $1`, runID,
		).Scan(&h); err != nil {
			return err
		}
		if h == nil || *h == "" {
			t.Fatalf("expected the large payload to spill to a blob handle before rollback")
		}
		handle = *h
		return sentinelErr
	})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("store.Transaction: got %v, want sentinelErr", err)
	}

	if _, err := blob.Read(ctx, persistence.Handle(handle)); !errors.Is(err, persistence.ErrBlobNotFound) {
		t.Fatalf("large object written by a rolled-back Upsert survived: Read(%s) = %v, want ErrBlobNotFound", handle, err)
	}
}

func TestPGSnapshotBagCarriesForwardSpilledBlobWithoutAliasing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 256, time.Hour)

	store := d.Tables()
	attrs := store.NodeAttributes()
	orphans := store.BlobOrphans()

	nodeID, priorRunID, scopeID := seedNodeAndRunPG(t, ctx, d, 1)

	bigVal := strings.Repeat("z", 500)
	priorBag := map[string]any{"big": bigVal, "tag": "prior"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, shared.UUID(priorRunID), shared.UUID(nodeID), priorBag, tx)
	}); err != nil {
		t.Fatalf("Upsert prior spilled: %v", err)
	}
	priorHandle := spillHandlePG(t, ctx, d, priorRunID)
	if priorHandle == "" {
		t.Fatalf("prior run did not spill")
	}

	newRunID := seedSecondRunPG(t, ctx, d, nodeID, scopeID, priorRunID, 2)
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.SnapshotBagForNewRun(ctx, shared.UUID(newRunID), shared.UUID(nodeID), shared.UUID(scopeID), tx)
	}); err != nil {
		t.Fatalf("SnapshotBagForNewRun: %v", err)
	}

	newHandle := spillHandlePG(t, ctx, d, newRunID)
	if newHandle == "" {
		t.Fatalf("carry-forward did not spill on the new run")
	}
	if newHandle == priorHandle {
		t.Fatalf("carry-forward aliased the blob handle across runs: both = %q", newHandle)
	}

	var snap map[string]any
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		bag, err := attrs.GetDispatchInputBag(ctx, shared.UUID(newRunID), tx)
		snap = bag
		return err
	}); err != nil {
		t.Fatalf("GetDispatchInputBag(new): %v", err)
	}
	if snap["big"] != bigVal || snap["tag"] != "prior" {
		t.Fatalf("carried dispatch_input_bag lost spilled content: got %+v", snap)
	}

	newBag := map[string]any{"big": strings.Repeat("q", 500), "tag": "new"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, shared.UUID(newRunID), shared.UUID(nodeID), newBag, tx)
	}); err != nil {
		t.Fatalf("Upsert on new run: %v", err)
	}

	orphRows, err := orphans.DueBefore(ctx, time.Now().Add(48*time.Hour), mem.Name(), 100)
	if err != nil {
		t.Fatalf("orphans.DueBefore: %v", err)
	}
	for _, r := range orphRows {
		if r.Handle == priorHandle {
			t.Fatalf("Upsert on new run enrolled the prior run's still-referenced blob %q as an orphan", priorHandle)
		}
		if err := mem.Delete(ctx, persistence.Handle(r.Handle)); err != nil {
			t.Fatalf("reap orphan %q: %v", r.Handle, err)
		}
	}

	var got *persistence.NodeAttributesRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := attrs.GetByRun(ctx, shared.UUID(priorRunID), tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("GetByRun(prior): %v", err)
	}
	if got == nil || got.Data["big"] != bigVal || got.Data["tag"] != "prior" {
		t.Fatalf("prior run's spilled value was destroyed by new run's Upsert+reap: got %+v", got)
	}
}
