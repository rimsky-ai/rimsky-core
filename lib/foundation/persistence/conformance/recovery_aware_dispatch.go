// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope

// @constraint: RecoveryAwareDispatch conformance area.
// Covers the persistence-layer round-trip of the recovery-aware fields
// (PriorDispatchID + PriorDispatchDisposition) introduced in spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Recovery-aware executor protocol".
//
// The wire-level proto field surfacing is exercised by the runtime's
// callback / dispatch code paths and the TS executor's recovery_aware
// test. This conformance test focuses on what persistence guarantees:
// EnqueueInTx persists the two fields, and SelectCandidates returns
// them on the corresponding Candidate.
//
// @concept: run-scope
package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// testRecoveryAwareDispatch seeds a fan-out parent + one partition,
// enqueues a recovery-aware child dispatch that carries
// PriorDispatchID + PriorDispatchDisposition = "stale_recovery",
// then asserts both fields round-trip through SelectCandidates.
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

	// @deliberate: use a fresh child node so the in-flight run does not
	// collide with the fixture node already occupying the parent scope.
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
			NodeID:         childNodeID,
			ExecutorName:   "test-executor",
			RequiredStores: []string{},
			EnqueuedAt:     time.Now().Add(-2 * time.Second),
			FrameID:        fix.FrameID,
			RunScopeID:     partitionScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("EnqueueInTx (original): %v", err)
	}

	var originalDispatchID shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             16,
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

	// @deliberate: write scratch onto the original dispatch row before
	// retiring it. The recovery enqueue below loads this scratch and
	// copies it onto the new dispatch row so the executor's in-flight
	// state survives stale-recovery — the opaque-executor-scratch
	// round-trip this conformance test pins.
	scratchFixture := []byte("scratch-bytes-fixture")

	// @deliberate: simulate stale-recovery — claim the original row,
	// stamp its scratch, then remove via the claimant-guarded path so a
	// successor enqueue can name the original as its prior.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		// @constraint: RemoveForNodeInTx's claimant guard requires the
		// caller to hold the claim, so claim the row first.
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
		return q.RemoveForNodeInTx(ctx, childNodeID, partitionScopeID, "sup-stale", tx)
	}); err != nil {
		t.Fatalf("Remove original: %v", err)
	}

	// @deliberate: recovery enqueue — load prior scratch, then enqueue
	// with the carry-forward triple populated. The recovery production
	// sites (conductor.go::SweepExecutorDeadlines, cascade_recalculate.go,
	// on_error.go, runner_error_policy.go::applyResolvedAction) follow
	// this same load → enqueue shape. EnqueuedAt is set slightly in the
	// past so the SelectCandidates time filter (enqueued_at <= now)
	// surfaces the recovery row in the same wall-clock instant.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		scratchInline, scratchHandle, scratchBackend, lerr := q.LoadScratchInTx(ctx, tx, originalDispatchID)
		if lerr != nil {
			return lerr
		}
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                      childNodeID,
			ExecutorName:                "test-executor",
			RequiredStores:              []string{},
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
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             16,
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

	// @constraint: scratch round-trip — the recovery row's scratch must
	// match the bytes written to the original. Without this the
	// executor's in-flight state silently vanishes on stale-recovery;
	// the opaque-executor-scratch contract this conformance test pins.
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

// testScratchMissingRowContract pins the deliberate asymmetry between
// LoadScratchInTx and WriteScratchInTx for a dispatch_id that addresses
// no row:
//
//   - LoadScratchInTx degrades to (nil, "", "", nil) so recovery-enqueue
//     load sites (conductor.go::SweepExecutorDeadlines,
//     cascade_recalculate.go, on_error.go, runner_error_policy.go) treat
//     a retired prior row as "no carry-forward state" and the successor
//     dispatch begins with empty scratch.
//   - WriteScratchInTx surfaces persistence.ErrRunRowMissing so the
//     executor's mid-dispatch checkpoint contract
//     (STORY-opaque-executor-scratch) is preserved: the HTTP scratch
//     callback handler maps the sentinel to 410 Gone and in-process
//     callers see it directly.
//
// Regression pin: a future refactor that "normalizes" either side would
// either bite recovery-enqueue paths (write side made tolerant) or
// silently swallow the executor's checkpoint loss (load side made
// strict). Both function-level comments call out the asymmetry; this
// test holds it down at the persistence layer.
func testScratchMissingRowContract(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	q := d.Queue()

	missingID := shared.UUID(uuid.New())

	// @constraint: load against a missing row degrades to empty scratch
	// + no error (asymmetric with write).
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

	// @constraint: write against a missing row surfaces
	// ErrRunRowMissing (asymmetric with load).
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
