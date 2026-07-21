// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testFramePruneEnrollsScratchAndAttributeBlobOrphans(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()
	orphans := store.BlobOrphans()

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 8, time.Hour)

	scratchHandle, err := mem.Write(ctx, persistence.BlobKey{NodeID: fix.NodeID.String(), AttributeName: "scratch"}, []byte("scratch-payload"))
	if err != nil {
		t.Fatalf("seed scratch blob: %v", err)
	}

	var runID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := d.Queue().Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                      fix.NodeID,
			ExecutorName:                "test-executor",
			RequiredClaimProducers:      []string{},
			EnqueuedAt:                  time.Now().Add(-1 * time.Second),
			FrameID:                     fix.FrameID,
			RunScopeID:                  fix.MainRunScopeID,
			InitialScratchHandle:        string(scratchHandle),
			InitialScratchHandleBackend: mem.Name(),
		}, tx); err != nil {
			return err
		}
		cands, err := d.Queue().SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == fix.NodeID {
				runID = c.NodeRunID
				return nil
			}
		}
		t.Fatalf("candidate not surfaced for seeded run")
		return nil
	}); err != nil {
		t.Fatalf("enqueue run with scratch handle: %v", err)
	}

	spilledPayload := map[string]any{"big": `{"padding":"0123456789012345678901234567890123456789"}`}
	raw, err := json.Marshal(spilledPayload)
	if err != nil {
		t.Fatalf("marshal spilled payload: %v", err)
	}
	if len(raw) <= 8 {
		t.Fatalf("test fixture payload too small to exceed the spill threshold: %d bytes", len(raw))
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.NodeAttributes().Upsert(ctx, runID, fix.NodeID, spilledPayload, tx)
	}); err != nil {
		t.Fatalf("Upsert (spill attribute blob): %v", err)
	}

	farFuture := time.Now().Add(48 * time.Hour)
	before, err := orphans.DueBefore(ctx, farFuture, mem.Name(), 1000)
	if err != nil {
		t.Fatalf("orphans.DueBefore(before prune): %v", err)
	}
	beforeSet := map[string]bool{}
	for _, r := range before {
		beforeSet[r.Handle] = true
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		transitioned, err := frames.MarkFrameEnded(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkFrameEnded did not transition the fixture frame")
		}
		return nil
	}); err != nil {
		t.Fatalf("end fixture frame: %v", err)
	}

	pruned, err := frames.PruneTraceForRetention(ctx, 0, farFuture)
	if err != nil {
		t.Fatalf("PruneTraceForRetention: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("PruneTraceForRetention pruned %d frames, want 1", pruned)
	}

	after, err := orphans.DueBefore(ctx, farFuture, mem.Name(), 1000)
	if err != nil {
		t.Fatalf("orphans.DueBefore(after prune): %v", err)
	}
	var newHandles []persistence.BlobOrphanRow
	for _, r := range after {
		if !beforeSet[r.Handle] {
			newHandles = append(newHandles, r)
		}
	}
	if len(newHandles) != 2 {
		t.Fatalf("frame prune enrolled %d new orphan handles, want exactly 2 (scratch + attribute value): %+v", len(newHandles), newHandles)
	}

	sawScratch := false
	var attrHandle string
	for _, r := range newHandles {
		if r.Handle == string(scratchHandle) {
			sawScratch = true
			continue
		}
		attrHandle = r.Handle
	}
	if !sawScratch {
		t.Fatalf("frame prune did not enroll the node_run.scratch_handle %q as an orphan: %+v", scratchHandle, newHandles)
	}
	if attrHandle == "" {
		t.Fatalf("frame prune did not enroll a second orphan handle for the spilled node_attributes.value_handle: %+v", newHandles)
	}
	attrBytes, err := mem.Read(ctx, persistence.Handle(attrHandle))
	if err != nil {
		t.Fatalf("read enrolled attribute blob handle %q: %v", attrHandle, err)
	}
	if string(attrBytes) != string(raw) {
		t.Fatalf("enrolled attribute blob handle %q content = %q, want %q", attrHandle, attrBytes, raw)
	}
}
