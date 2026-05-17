// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N6 scenario — held_durable_across_run_completion.
//
// A durable claim (`lifetime: durable`) commits to the producer's
// DataProcessing layer; the version persists across the original
// run's terminal. The scenario drives the dataprocessing fixture
// through Begin→Commit→ListVersions and pins that the version
// remains visible after the candidate is gone.
package asset

import (
	"context"
	"testing"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
	"github.com/fallguy/rimsky/stores/stub/dataprocessing"
)

func TestHeldDurableAcrossRunCompletion(t *testing.T) {
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
	// The committed version persists across the "run completion"
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
