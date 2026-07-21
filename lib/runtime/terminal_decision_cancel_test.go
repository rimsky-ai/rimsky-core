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

type parentOnlyClaimHandles struct {
	persistence.ClaimHandleTable
	parent *persistence.ClaimHandleRow
}

func (f parentOnlyClaimHandles) Get(_ context.Context, id shared.UUID, _ persistence.Tx) (*persistence.ClaimHandleRow, error) {
	if id == f.parent.ID {
		return f.parent, nil
	}
	return nil, nil
}

// @concept: cancel-siblings
func TestCancelInFlightSiblings_MalformedAggregationPolicyWarnsAndNoOps(t *testing.T) {
	parentID := shared.UUID(uuid.New())
	triggerID := shared.UUID(uuid.New())
	parent := &persistence.ClaimHandleRow{
		ID:                parentID,
		State:             spec.ClaimHandleStateActive,
		AggregationPolicy: []byte(`not-json`),
	}
	capLog := shared.NewCapturingLogger()
	args := RunArgs{
		ClaimHandles: parentOnlyClaimHandles{parent: parent},
		Logger:       capLog,
	}

	post, err := cancelInFlightSiblings(context.Background(), args, nil, parentID, triggerID)
	if err != nil {
		t.Fatalf("expected no error for a malformed aggregation_policy (warn + non-strict fallback), got %v", err)
	}
	if post != nil {
		t.Fatalf("expected no post-commit cascade for a malformed aggregation_policy, got %v", post)
	}
	foundWarn := false
	for _, rec := range capLog.Records() {
		if rec.Level == "warn" {
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Fatalf("expected a Warn log for the malformed aggregation_policy, got records=%#v", capLog.Records())
	}
}

// @concept: cancel-siblings
func TestAggregateParentOutcome_MalformedAggregationPolicyFallsBackToStrict(t *testing.T) {
	parent := &persistence.ClaimHandleRow{
		ExpectedChildrenCount:  2,
		CommittedChildrenCount: 1,
		AbandonedChildrenCount: 1,
		AggregationPolicy:      []byte(`not-json`),
	}
	got := aggregateParentOutcome(parent, OutcomeCommit)
	if got != OutcomeAbandon {
		t.Fatalf("malformed aggregation_policy with an abandoned child must fall back to strict "+
			"(any abandonment fails the parent): got %v, want %v", got, OutcomeAbandon)
	}
}

func TestAggregateParentOutcome_MalformedAggregationPolicyStrictAllCommittedSucceeds(t *testing.T) {
	parent := &persistence.ClaimHandleRow{
		ExpectedChildrenCount:  2,
		CommittedChildrenCount: 2,
		AbandonedChildrenCount: 0,
		AggregationPolicy:      []byte(`not-json`),
	}
	got := aggregateParentOutcome(parent, OutcomeAbandon)
	if got != OutcomeCommit {
		t.Fatalf("malformed aggregation_policy with no abandoned children must fall back to strict "+
			"commit: got %v, want %v", got, OutcomeCommit)
	}
}
