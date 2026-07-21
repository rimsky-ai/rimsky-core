// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type fakeClaimHandlesByNodeRun struct {
	rows map[shared.UUID][]persistence.ClaimHandleRow
}

func (f *fakeClaimHandlesByNodeRun) Insert(context.Context, persistence.ClaimHandleInsertInput, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) UpdateAddress(context.Context, shared.UUID, string, json.RawMessage, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) UpdatePayload(context.Context, shared.UUID, string, json.RawMessage, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) Get(context.Context, shared.UUID, persistence.Tx) (*persistence.ClaimHandleRow, error) {
	return nil, nil
}
func (f *fakeClaimHandlesByNodeRun) ListByHolderNode(context.Context, shared.UUID, persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	return nil, nil
}
func (f *fakeClaimHandlesByNodeRun) ListByNodeRun(_ context.Context, nodeRunID shared.UUID, _ persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	return f.rows[nodeRunID], nil
}
func (f *fakeClaimHandlesByNodeRun) ListExpired(context.Context, persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	return nil, nil
}
func (f *fakeClaimHandlesByNodeRun) RenewExpiryForHolderRun(context.Context, shared.UUID, time.Time, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) Delete(context.Context, shared.UUID, string, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) CountByNamedLock(context.Context, string, persistence.Tx) (int, error) {
	return 0, nil
}
func (f *fakeClaimHandlesByNodeRun) ListByProducerClaimScope(context.Context, string, persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	return nil, nil
}
func (f *fakeClaimHandlesByNodeRun) DeleteIfExpired(context.Context, shared.UUID, string, persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeClaimHandlesByNodeRun) LockForUpdate(context.Context, shared.UUID, persistence.Tx) (*persistence.ClaimHandleRow, error) {
	return nil, nil
}
func (f *fakeClaimHandlesByNodeRun) UpdateClaimScope(context.Context, shared.UUID, string, json.RawMessage, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) UpdateNodeRunID(context.Context, shared.UUID, shared.UUID, string, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) ReassignHolderSupervisor(context.Context, shared.UUID, string, string, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) UpdateRealizedWriteSemantics(context.Context, shared.UUID, string, string, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) ListForObservability(context.Context, persistence.ClaimHandleListFilter, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.ClaimHandleRow], error) {
	return persistence.PaginatedListResult[persistence.ClaimHandleRow]{}, nil
}
func (f *fakeClaimHandlesByNodeRun) GetByFrameAndNode(context.Context, shared.UUID, shared.UUID, persistence.Tx) (*persistence.ClaimHandleRow, error) {
	return nil, nil
}
func (f *fakeClaimHandlesByNodeRun) ListChildClaimHandles(context.Context, shared.UUID, persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	return nil, nil
}
func (f *fakeClaimHandlesByNodeRun) SetVersionID(context.Context, shared.UUID, string, string, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) DeleteResolvedOlderThan(context.Context, time.Time) (int, error) {
	return 0, nil
}
func (f *fakeClaimHandlesByNodeRun) DeleteResolved(context.Context, shared.UUID, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) DeleteResolvedIfNoActiveHolders(context.Context, shared.UUID, persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeClaimHandlesByNodeRun) Promote(context.Context, shared.UUID, string, spec.ClaimHandleState, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) ListByState(context.Context, spec.ClaimHandleState, persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	return nil, nil
}
func (f *fakeClaimHandlesByNodeRun) ListByInstanceAndState(context.Context, shared.UUID, spec.ClaimHandleState, spec.ClaimLifetime, persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	return nil, nil
}
func (f *fakeClaimHandlesByNodeRun) SetAggregationPolicy(context.Context, shared.UUID, string, json.RawMessage, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) BumpExpectedChildrenCount(context.Context, shared.UUID, string, int, persistence.Tx) error {
	return nil
}
func (f *fakeClaimHandlesByNodeRun) BumpChildOutcomeCount(context.Context, shared.UUID, string, string, int, persistence.Tx) error {
	return nil
}

// @concept: fan-out
// @concept: claim-tree
func TestBindLeafCandidateHandles_MatchesByParentClaimHandleIDNotProducerNameAlone(t *testing.T) {
	nodeRunID := shared.UUID(uuid.New())
	claimAID := shared.UUID(uuid.New())
	claimBID := shared.UUID(uuid.New())
	producerName := "shared-producer"
	handleForA := []byte("candidate-handle-for-claim-a")

	ch := &fakeClaimHandlesByNodeRun{
		rows: map[shared.UUID][]persistence.ClaimHandleRow{
			nodeRunID: {
				{
					ID:                      shared.UUID(uuid.New()),
					ParentClaimHandleID:     &claimAID,
					ProducerName:            &producerName,
					ProducerCandidateHandle: handleForA,
				},
			},
		},
	}
	args := RunArgs{ClaimHandles: ch, Logger: shared.SilentLogger{}}
	out := &acquisition{
		Locks: []AcquiredLock{
			{ClaimHandleID: claimBID, Spec: claimproducer.ClaimSpec{ProducerName: producerName, Alias: "claim_b"}},
			{ClaimHandleID: claimAID, Spec: claimproducer.ClaimSpec{ProducerName: producerName, Alias: "claim_a"}},
		},
	}
	cand := persistence.Candidate{NodeRunID: nodeRunID}

	bindLeafCandidateHandles(context.Background(), args, nil, out, cand)

	if len(out.Locks[0].ProducerCandidateHandle) != 0 {
		t.Fatalf("claim_b (an unrelated claim from the same producer, iterated first) must NOT receive "+
			"claim_a's candidate handle; matching by producer name alone (and taking the first empty slot) "+
			"would wrongly bind it here: got %q", out.Locks[0].ProducerCandidateHandle)
	}
	if string(out.Locks[1].ProducerCandidateHandle) != string(handleForA) {
		t.Fatalf("claim_a (the sub-claim's actual parent, iterated second) did not receive the candidate "+
			"handle: got %q", out.Locks[1].ProducerCandidateHandle)
	}
}
