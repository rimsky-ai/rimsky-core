// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: transition-reason
func testReleaseClaimReadsTheStateTheCompetingWriterCommitted(t *testing.T, d persistence.Database) {
	releaseAgainstAnOpenSettle(t, d, "ReleaseClaim", "supervisor-release-contended",
		func(ctx context.Context, runID shared.UUID, supID string) error {
			return d.Queue().ReleaseClaim(ctx, runID, supID)
		})
}

// @concept: transition-reason
func testReleaseClaimWithDispositionReadsTheStateTheCompetingWriterCommitted(t *testing.T, d persistence.Database) {
	releaseAgainstAnOpenSettle(t, d, "ReleaseClaimWithDisposition", "supervisor-release-disposition-contended",
		func(ctx context.Context, runID shared.UUID, supID string) error {
			return d.Queue().ReleaseClaimWithDisposition(ctx, runID, supID, "stale_recovery")
		})
}

func releaseAgainstAnOpenSettle(
	t *testing.T, d persistence.Database, op string, supID string,
	release func(ctx context.Context, runID shared.UUID, supID string) error,
) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	runID := seedClaimedGuardRun(ctx, t, d, fix, supID)

	settleWritten := make(chan struct{})
	settleCommit := make(chan struct{})
	settleDone := make(chan error, 1)
	sig := "terminal/error/test_failure"
	go func() {
		settleDone <- d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := d.Tables().Nodes().UpdateState(ctx, runID,
				cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &sig, tx); err != nil {
				return err
			}
			close(settleWritten)
			<-settleCommit
			return nil
		})
	}()

	<-settleWritten
	releaseStarted := make(chan struct{})
	releaseDone := make(chan error, 1)
	go func() {
		close(releaseStarted)
		releaseDone <- release(ctx, runID, supID)
	}()
	<-releaseStarted
	close(settleCommit)

	if err := <-settleDone; err != nil {
		t.Fatalf("%s: the competing settle must commit: %v", op, err)
	}
	releaseErr := <-releaseDone

	if !errors.Is(releaseErr, cascade.ErrIllegalTransition) {
		t.Fatalf("%s ran against a row a committing writer already held, so it must read the committed terminal state and report the switch's refusal; got %v",
			op, releaseErr)
	}
	owner, err := d.Queue().GetClaimedBy(ctx, runID)
	if err != nil {
		t.Fatalf("%s: GetClaimedBy: %v", op, err)
	}
	if owner.Kind != persistence.ClaimOwnershipKindClaimedBy || owner.SupervisorID != supID {
		t.Fatalf("%s refused the transition, so it must have written nothing; got claim %s/%s", op, owner.Kind, owner.SupervisorID)
	}
}
