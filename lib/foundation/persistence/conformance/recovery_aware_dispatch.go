// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope

// @concept: run-scope
package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testRecoveryAwareDispatch(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	parentRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	partitionScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               partitionScopeID,
			ParentRunScopeID: &fix.MainRunScopeID,
			ParentRunID:      &parentRunID,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "part-recovery",
		})
	}); err != nil {
		t.Fatalf("Create partition scope: %v", err)
	}

	childNodeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         childNodeID,
			InstanceID: fix.InstanceID,
			NodeType:   "fixture-node-type",
			Executor:   "test-executor",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("Create child node: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 childNodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-2 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             partitionScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("EnqueueInTx (original): %v", err)
	}

	var originalDispatchID shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == childNodeID {
				originalDispatchID = c.DispatchID
				return nil
			}
		}
		t.Fatalf("original dispatch not surfaced by SelectCandidates")
		return nil
	}); err != nil {
		t.Fatalf("SelectCandidates (original): %v", err)
	}

	scratchFixture := []byte("scratch-bytes-fixture")

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, originalDispatchID, "sup-stale")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow(original) did not claim")
		}
		if err := q.WriteScratchInTx(ctx, tx, originalDispatchID, scratchFixture, "", ""); err != nil {
			return err
		}
		if err := store.Nodes().UpdateState(ctx, originalDispatchID,
			cascade.NodeStateFailed, cascade.ReasonInstanceKilled, nil, tx); err != nil {
			return err
		}
		return q.RemoveForNodeInTx(ctx, childNodeID, partitionScopeID, "sup-stale", tx)
	}); err != nil {
		t.Fatalf("Remove original: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		scratchInline, scratchHandle, scratchBackend, lerr := q.LoadScratchInTx(ctx, tx, originalDispatchID)
		if lerr != nil {
			return lerr
		}
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                      childNodeID,
			ExecutorName:                "test-executor",
			RequiredClaimProducers:      []string{},
			EnqueuedAt:                  time.Now().Add(-time.Second),
			FrameID:                     fix.FrameID,
			RunScopeID:                  partitionScopeID,
			PriorDispatchID:             &originalDispatchID,
			PriorDispatchDisposition:    "stale_recovery",
			InitialScratchInline:        scratchInline,
			InitialScratchHandle:        scratchHandle,
			InitialScratchHandleBackend: scratchBackend,
		}, tx)
	}); err != nil {
		t.Fatalf("EnqueueInTx (recovery): %v", err)
	}

	var got persistence.Candidate
	var found bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == childNodeID {
				got = c
				found = true
				return nil
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("SelectCandidates (recovery): %v", err)
	}
	if !found {
		t.Fatalf("recovery dispatch not surfaced by SelectCandidates")
	}
	if got.PriorDispatchID == nil {
		t.Fatalf("Candidate.PriorDispatchID = nil; want %v", originalDispatchID)
	}
	if *got.PriorDispatchID != originalDispatchID {
		t.Fatalf("Candidate.PriorDispatchID = %v; want %v", *got.PriorDispatchID, originalDispatchID)
	}
	if got.PriorDispatchDisposition != "stale_recovery" {
		t.Fatalf("Candidate.PriorDispatchDisposition = %q; want stale_recovery", got.PriorDispatchDisposition)
	}

	var gotInline []byte
	var gotHandle, gotBackend string
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var lerr error
		gotInline, gotHandle, gotBackend, lerr = q.LoadScratchInTx(ctx, tx, got.DispatchID)
		return lerr
	}); err != nil {
		t.Fatalf("LoadScratchInTx (recovery): %v", err)
	}
	if string(gotInline) != string(scratchFixture) {
		t.Fatalf("recovery scratch_inline = %q; want %q", string(gotInline), string(scratchFixture))
	}
	if gotHandle != "" {
		t.Fatalf("recovery scratch_handle = %q; want empty", gotHandle)
	}
	if gotBackend != "" {
		t.Fatalf("recovery scratch_handle_backend = %q; want empty", gotBackend)
	}
}

func testScratchMissingRowContract(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	q := d.Queue()

	missingID := shared.UUID(uuid.New())

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		inline, handle, backend, lerr := q.LoadScratchInTx(ctx, tx, missingID)
		if lerr != nil {
			t.Fatalf("LoadScratchInTx (missing): unexpected error %v", lerr)
		}
		if len(inline) != 0 {
			t.Fatalf("LoadScratchInTx (missing): inline = %q; want empty", string(inline))
		}
		if handle != "" {
			t.Fatalf("LoadScratchInTx (missing): handle = %q; want empty", handle)
		}
		if backend != "" {
			t.Fatalf("LoadScratchInTx (missing): backend = %q; want empty", backend)
		}
		return nil
	}); err != nil {
		t.Fatalf("LoadScratchInTx (missing): tx failure %v", err)
	}

	werr := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.WriteScratchInTx(ctx, tx, missingID, []byte("bytes"), "", "")
	})
	if werr == nil {
		t.Fatalf("WriteScratchInTx (missing): want ErrRunRowMissing, got nil")
	}
	if !errors.Is(werr, persistence.ErrRunRowMissing) {
		t.Fatalf("WriteScratchInTx (missing): want ErrRunRowMissing, got %v", werr)
	}
}
