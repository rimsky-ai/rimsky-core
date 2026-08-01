// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testSelectCandidatesExcludesPureCascadeAdmitsClaimRouting(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	q := d.Queue()

	fix := seedFixtureSet(ctx, t, d)

	pureCascadeNodeID := shared.UUID(uuid.New())
	claimRoutingNodeID := shared.UUID(uuid.New())

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         pureCascadeNodeID,
			InstanceID: fix.InstanceID,
			NodeType:   "pure-cascade-node-type",
			Executor:   "",
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         claimRoutingNodeID,
			InstanceID: fix.InstanceID,
			NodeType:   "claim-routing-node-type",
			Executor:   "",
		}, tx); err != nil {
			return err
		}
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 pureCascadeNodeID,
			ExecutorName:           "",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		return q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 claimRoutingNodeID,
			ExecutorName:           "",
			RequiredClaimProducers: []string{"fixture-store"},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed executor-empty rows: %v", err)
	}

	probeErr := errors.New("rollback probe")
	var sawPureCascade, sawClaimRouting bool
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit: 100,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			switch c.NodeID {
			case pureCascadeNodeID:
				sawPureCascade = true
			case claimRoutingNodeID:
				sawClaimRouting = true
			}
		}
		return probeErr
	})
	if err != nil && !errors.Is(err, probeErr) {
		t.Fatalf("SelectCandidates: %v", err)
	}
	if sawPureCascade {
		t.Errorf("executor-empty row with no required claim producers leaked into SelectCandidates " +
			"(pure-cascade rows are settled natively by the scheduler; the supervisor must not race it)")
	}
	if !sawClaimRouting {
		t.Errorf("executor-empty row with required claim producers missing from SelectCandidates " +
			"(claim-routing rows still need the supervisor to acquire claims)")
	}
}
