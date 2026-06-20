// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtree

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/dataprocessing"
)

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
