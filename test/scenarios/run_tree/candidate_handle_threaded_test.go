// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N1 scenario — candidate_handle_threaded.
//
// At fan-out dispatch, the supervisor calls
// proto:data_processing.proto::DataProcessing.BeginCandidate per
// sub-claim and persists the producer-returned candidate_handle on
// col:rimsky_claim_handles.producer_candidate_handle. At leaf
// success the supervisor reads the candidate_handle back from the
// row and calls CommitCandidate with matching bytes.
//
// The N1 contract this scenario pins is the threading shape: the
// per-sub-claim candidate_handle is unique per partition_key, is
// stable across the dispatch → terminal boundary (an idempotent
// re-call returns the same handle), and survives a round-trip
// through the dataprocessing fixture.
package runtree

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
	"github.com/fallguy/rimsky/stores/stub/dataprocessing"
)

// TestCandidateHandleThreaded_PerPartitionKeyUnique asserts each
// fan-out leaf gets a distinct candidate_handle (the producer must
// not return the same handle for distinct partition_keys).
func TestCandidateHandleThreaded_PerPartitionKeyUnique(t *testing.T) {
	t.Parallel()
	s := dataprocessing.New()
	ctx := context.Background()
	const claimHandle = "claim/candidate-thread"
	handles := make(map[string][]byte)
	for _, key := range []string{"a", "b", "c"} {
		sub, _ := json.Marshal(map[string]string{"partition_key": key})
		resp, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
			ClaimHandleId:      claimHandle,
			SubScopeDescriptor: sub,
			IdempotencyKey:     key,
		})
		if err != nil {
			t.Fatalf("BeginCandidate[%s]: %v", key, err)
		}
		for prevKey, prevHandle := range handles {
			if bytes.Equal(prevHandle, resp.GetCandidateHandle()) {
				t.Fatalf("partition_key %q and %q returned identical candidate_handle %q",
					key, prevKey, string(prevHandle))
			}
		}
		handles[key] = resp.GetCandidateHandle()
	}
}

// TestCandidateHandleThreaded_IdempotentReBegin asserts a retried
// BeginCandidate with the same idempotency_key returns the same
// candidate_handle. The supervisor relies on this for at-least-once
// dispatch retries.
func TestCandidateHandleThreaded_IdempotentReBegin(t *testing.T) {
	t.Parallel()
	s := dataprocessing.New()
	ctx := context.Background()
	req := &genv1.BeginCandidateRequest{
		ClaimHandleId:  "claim/idempotent",
		IdempotencyKey: "stable-key",
	}
	first, err := s.BeginCandidate(ctx, req)
	if err != nil {
		t.Fatalf("BeginCandidate #1: %v", err)
	}
	second, err := s.BeginCandidate(ctx, req)
	if err != nil {
		t.Fatalf("BeginCandidate #2: %v", err)
	}
	if !bytes.Equal(first.GetCandidateHandle(), second.GetCandidateHandle()) {
		t.Errorf("retried BeginCandidate returned drift: %q vs %q",
			string(first.GetCandidateHandle()), string(second.GetCandidateHandle()))
	}
}

// TestCandidateHandleThreaded_CommitMetadataPropagates asserts the
// candidate_metadata returned by CommitCandidate is non-empty —
// production producers thread row counts / partition stats through
// here so the parent's writeback can surface them per the N1 spec
// pin.
func TestCandidateHandleThreaded_CommitMetadataPropagates(t *testing.T) {
	t.Parallel()
	s := dataprocessing.New()
	ctx := context.Background()
	begin, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:  "claim/metadata",
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	commit, err := s.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	})
	if err != nil {
		t.Fatalf("CommitCandidate: %v", err)
	}
	if len(commit.GetCandidateMetadata()) == 0 {
		t.Error("CommitCandidate.candidate_metadata is empty; producers should thread per-candidate stats through")
	}
}
