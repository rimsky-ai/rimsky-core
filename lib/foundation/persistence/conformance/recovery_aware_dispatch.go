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
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// testRecoveryAwareDispatch seeds a fan-out parent + one partition,
// enqueues a recovery-aware child dispatch that carries
// PriorDispatchID + PriorDispatchDisposition = "heartbeat_stale",
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

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		// @constraint: RemoveForNodeInTx's claimant guard requires the
		// caller to hold the claim, so set the heartbeat claimant first.
		if err := store.Nodes().UpdateHeartbeat(ctx, childNodeID, partitionScopeID, time.Now(), "sup-stale", tx); err != nil {
			return err
		}
		return q.RemoveForNodeInTx(ctx, childNodeID, partitionScopeID, "sup-stale", tx)
	}); err != nil {
		t.Fatalf("Remove original: %v", err)
	}

	// @deliberate: EnqueuedAt is set slightly in the past so the
	// SelectCandidates time filter (enqueued_at <= now) surfaces the
	// recovery row in the same wall-clock instant.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                   childNodeID,
			ExecutorName:             "test-executor",
			RequiredStores:           []string{},
			EnqueuedAt:               time.Now().Add(-time.Second),
			FrameID:                  fix.FrameID,
			RunScopeID:               partitionScopeID,
			PriorDispatchID:          &originalDispatchID,
			PriorDispatchDisposition: "heartbeat_stale",
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
	if got.PriorDispatchDisposition != "heartbeat_stale" {
		t.Fatalf("Candidate.PriorDispatchDisposition = %q; want heartbeat_stale", got.PriorDispatchDisposition)
	}
}
