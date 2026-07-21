// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type casLostClaimHandles struct {
	persistence.ClaimHandleTable
	rows []persistence.ClaimHandleRow
}

func (f casLostClaimHandles) ListByNodeRun(_ context.Context, _ shared.UUID, _ persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	return f.rows, nil
}

func (f casLostClaimHandles) ReassignHolderSupervisor(_ context.Context, _ shared.UUID, _, _ string, _ persistence.Tx) error {
	return spec.ErrIllegalClaimHandleTransition
}

// @concept: claim-tree
// @concept: fan-out
func TestRestampLinkedSubClaimHolders_CASLostSurfacesSentinelError(t *testing.T) {
	otherSupervisor := "sup-other"
	fake := casLostClaimHandles{rows: []persistence.ClaimHandleRow{
		{
			ID:                  shared.UUID(uuid.New()),
			ParentClaimHandleID: &shared.UUID{},
			State:               spec.ClaimHandleStateActive,
			HolderSupervisorID:  &otherSupervisor,
		},
	}}
	args := RunArgs{
		ClaimHandles: fake,
		SupervisorID: "sup-mine",
	}
	err := restampLinkedSubClaimHolders(context.Background(), args, nil, persistence.Candidate{
		NodeRunID: shared.UUID(uuid.New()),
	})
	if err != errAcquireRestampLost {
		t.Fatalf("restampLinkedSubClaimHolders: err = %v, want the errAcquireRestampLost sentinel", err)
	}
}
