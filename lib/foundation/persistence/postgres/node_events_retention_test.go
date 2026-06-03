// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// node_events_retention_test.go — postgres mirror of the SQLite
// TestNodeEventDeleteOlderThanQueuesSpilledBlobOrphans gate. Proves the
// postgres NodeEvents().DeleteOlderThan reaps a time-aged named-event row
// AND atomically queues its spilled payload handle into rimsky_blob_orphans,
// so the durable-instance trace-retention path cannot leak the blob bytes.

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestPGNodeEventDeleteOlderThanQueuesSpilledBlobOrphans(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	store := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	mainRunScopeID := uuid.New()
	nodeID := uuid.New()

	tmpl := spec.TemplateSpec{
		Name:                "node-event-retention-fixture",
		Version:             "1",
		FrameResolutionMode: spec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
		Nodes:               []spec.TemplateNodeDef{{Type: "fixture-node-type", Executor: "test-executor"}},
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
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	now := time.Now().UTC()
	cutoff := now.Add(-time.Hour)
	oldTime := now.Add(-24 * time.Hour)

	// Old spilled named event (handle set, emitted_at before cutoff): must be
	// reaped and its handle queued. A recent inline event: survives.
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_node_events
		   (instance_id, emitter_node_id, event_name, payload_handle, payload_handle_backend, emitted_at)
		 VALUES ($1::uuid, $2, 'progress', 'blob-handle-pg', 'filesystem', $3)`,
		instanceID, nodeID.String(), oldTime,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_node_events
		   (instance_id, emitter_node_id, event_name, payload_inline, emitted_at)
		 VALUES ($1::uuid, $2, 'progress', '\x00', $3)`,
		instanceID, nodeID.String(), now.Add(-time.Minute),
	)

	deleted, orphans, err := store.NodeEvents().DeleteOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d rows, want 1 (only the old row is past the cutoff)", deleted)
	}
	if len(orphans) != 1 {
		t.Fatalf("returned %d orphans, want 1 (spilled handle must be surfaced)", len(orphans))
	}
	if orphans[0].Handle != "blob-handle-pg" || orphans[0].Backend != "filesystem" {
		t.Fatalf("orphan = %+v, want {Handle:blob-handle-pg Backend:filesystem}", orphans[0])
	}

	// The handle must be durably queued in the SAME transaction as the
	// delete — surfacing it in the return slice is not the durable guarantee;
	// SweepOrphanedBlobs reaps only what is persisted in rimsky_blob_orphans.
	var queued int
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT COUNT(*) FROM rimsky_blob_orphans WHERE handle = 'blob-handle-pg'`,
		nil, &queued,
	)
	if queued != 1 {
		t.Fatalf("spilled handle must be persisted in rimsky_blob_orphans by DeleteOlderThan, found %d", queued)
	}

	// The recent inline row survives the cutoff.
	var remaining int
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT COUNT(*) FROM rimsky_node_events WHERE instance_id = $1::uuid`,
		[]any{instanceID}, &remaining,
	)
	if remaining != 1 {
		t.Fatalf("recent inline named event must survive; %d rows remain", remaining)
	}
}
