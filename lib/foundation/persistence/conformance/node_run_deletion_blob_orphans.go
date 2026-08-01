// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: blob-backend
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testDeletePriorCascadeStalesEnrollsScratchBlobOrphan(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()
	orphans := store.BlobOrphans()

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 8, time.Hour)

	scratchHandle, err := mem.Write(ctx, persistence.BlobKey{NodeID: fix.NodeID.String(), AttributeName: "scratch"}, []byte("prior-cascade-scratch"))
	if err != nil {
		t.Fatalf("seed scratch blob: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                      fix.NodeID,
			ExecutorName:                "test-executor",
			RequiredClaimProducers:      []string{},
			EnqueuedAt:                  time.Now().Add(-2 * time.Second),
			FrameID:                     fix.FrameID,
			RunScopeID:                  fix.MainRunScopeID,
			InitialScratchHandle:        string(scratchHandle),
			InitialScratchHandleBackend: mem.Name(),
		}, tx)
	}); err != nil {
		t.Fatalf("enqueue prior stale run: %v", err)
	}

	var currentSeq int64
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 fix.NodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		latest, err := store.Nodes().GetLatestRunForNode(ctx, fix.NodeID, tx)
		if err != nil {
			return err
		}
		if latest == nil {
			t.Fatalf("latest run not found after second enqueue")
		}
		currentSeq = latest.Sequence
		return nil
	}); err != nil {
		t.Fatalf("enqueue current run: %v", err)
	}

	var deleted int
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		deleted, err = store.Nodes().DeletePriorCascadeStales(ctx, fix.NodeID, fix.MainRunScopeID, currentSeq, tx)
		return err
	}); err != nil {
		t.Fatalf("DeletePriorCascadeStales: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeletePriorCascadeStales deleted %d rows, want 1", deleted)
	}

	after, err := orphans.DueBefore(ctx, time.Now().Add(48*time.Hour), mem.Name(), 1000)
	if err != nil {
		t.Fatalf("orphans.DueBefore: %v", err)
	}
	found := false
	for _, r := range after {
		if r.Handle == string(scratchHandle) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("DeletePriorCascadeStales did not enroll the deleted row's scratch_handle %q as an orphan", scratchHandle)
	}
}

func testDropPendingRunEnrollsScratchBlobOrphan(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	orphans := store.BlobOrphans()

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 8, time.Hour)

	scratchHandle, err := mem.Write(ctx, persistence.BlobKey{NodeID: fix.NodeID.String(), AttributeName: "scratch"}, []byte("pending-run-scratch"))
	if err != nil {
		t.Fatalf("seed scratch blob: %v", err)
	}

	var pendingID shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
		pendingID = id
		return err
	}); err != nil {
		t.Fatalf("CreateCascadePending: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return d.Queue().WriteScratch(ctx, pendingID, nil, string(scratchHandle), mem.Name(), tx)
	}); err != nil {
		t.Fatalf("WriteScratch: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Nodes().DropPendingRun(ctx, pendingID, tx)
	}); err != nil {
		t.Fatalf("DropPendingRun: %v", err)
	}

	after, err := orphans.DueBefore(ctx, time.Now().Add(48*time.Hour), mem.Name(), 1000)
	if err != nil {
		t.Fatalf("orphans.DueBefore: %v", err)
	}
	found := false
	for _, r := range after {
		if r.Handle == string(scratchHandle) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("DropPendingRun did not enroll the dropped row's scratch_handle %q as an orphan", scratchHandle)
	}
}
