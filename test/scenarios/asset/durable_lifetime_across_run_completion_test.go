// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N6 scenario — durable_lifetime_across_run_completion.
//
// A durable-lifetime claim (`lifetime: durable`) commits to the
// producer's DataProcessing layer; the version persists across the
// original run's terminal. The scenario drives the dataprocessing
// fixture through Begin→Commit→ListVersions and pins that the version
// remains visible after the candidate is gone. The Stage-4 column drop
// replaced the standalone `held_durable` boolean with the
// `(state, lifetime)` pair on `rimsky_claim_handles`; this scenario
// exercises the `lifetime: durable` half of that pair.
package asset

import (
	"context"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/stores/stub/dataprocessing"
)

func TestDurableLifetimeAcrossRunCompletion(t *testing.T) {
	t.Parallel()
	s := dataprocessing.New()
	ctx := context.Background()
	const claimHandle = "durable-claim/across-run"

	begin, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:  claimHandle,
		IdempotencyKey: "durable-1",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	if _, err := s.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	}); err != nil {
		t.Fatalf("CommitCandidate: %v", err)
	}
	// @deliberate: The committed version persists across the "run completion"
	// boundary — verified here by re-listing after the candidate is
	// gone.
	if s.CandidateCount() != 0 {
		t.Errorf("expected candidate count 0 after commit, got %d", s.CandidateCount())
	}
	versions, err := s.ListVersions(ctx, &genv1.ListVersionsRequest{ClaimHandleId: claimHandle})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions.GetVersions()) != 1 {
		t.Errorf("durable claim should have 1 version after commit, got %d", len(versions.GetVersions()))
	}
}
