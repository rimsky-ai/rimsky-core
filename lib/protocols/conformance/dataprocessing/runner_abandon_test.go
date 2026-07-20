// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package dataprocessing

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type abandonFakeClient struct {
	genv1.DataProcessingClient

	honest bool

	mu         sync.Mutex
	candidates map[string]bool
	versions   map[string]int
}

func newAbandonFakeClient(honest bool) *abandonFakeClient {
	return &abandonFakeClient{
		honest:     honest,
		candidates: make(map[string]bool),
		versions:   make(map[string]int),
	}
}

func (f *abandonFakeClient) BeginCandidate(_ context.Context, req *genv1.BeginCandidateRequest, _ ...grpc.CallOption) (*genv1.BeginCandidateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	handle := "cand:" + req.GetClaimHandleId() + ":" + req.GetIdempotencyKey()
	f.candidates[handle] = true
	return &genv1.BeginCandidateResponse{CandidateHandle: []byte(handle)}, nil
}

func (f *abandonFakeClient) CommitCandidate(_ context.Context, req *genv1.CommitCandidateRequest, _ ...grpc.CallOption) (*genv1.CommitCandidateResponse, error) {
	handle := string(req.GetCandidateHandle())
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.honest {
		delete(f.candidates, handle)
		return &genv1.CommitCandidateResponse{}, nil
	}
	if !f.candidates[handle] {
		return nil, fmt.Errorf("fake.CommitCandidate: candidate_handle %q not found", handle)
	}
	delete(f.candidates, handle)
	f.versions[handle]++
	return &genv1.CommitCandidateResponse{}, nil
}

func (f *abandonFakeClient) AbandonCandidate(_ context.Context, req *genv1.AbandonCandidateRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	handle := string(req.GetCandidateHandle())
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.honest {
		delete(f.candidates, handle)
		return &emptypb.Empty{}, nil
	}
	if !f.candidates[handle] {
		return nil, fmt.Errorf("fake.AbandonCandidate: candidate_handle %q not found", handle)
	}
	delete(f.candidates, handle)
	return &emptypb.Empty{}, nil
}

func (f *abandonFakeClient) ListVersions(_ context.Context, req *genv1.ListVersionsRequest, _ ...grpc.CallOption) (*genv1.ListVersionsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for handle, n := range f.versions {
		if handle == "cand:"+req.GetClaimHandleId()+":abandon-1" {
			count += n
		}
	}
	out := make([]*genv1.VersionMetadata, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, &genv1.VersionMetadata{VersionId: fmt.Sprintf("v%d", i+1)})
	}
	return &genv1.ListVersionsResponse{Versions: out}, nil
}

func findAbandonRow(t *testing.T, results []CheckResult, name string) CheckResult {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no CheckResult named %q in %+v", name, results)
	return CheckResult{}
}

func TestCheckAbandonCandidate_Honest_AllPass(t *testing.T) {
	fake := newAbandonFakeClient(true)
	results := checkAbandonCandidate(context.Background(), fake)
	for _, name := range []string{
		"AbandonCandidateExcludedFromListVersions",
		"AbandonCandidateRejectsCommitAfterAbandon",
		"AbandonCandidateUnknownHandleFailsCleanly",
	} {
		row := findAbandonRow(t, results, name)
		if row.Err != nil {
			t.Errorf("%s: expected PASS for an honest producer, got Err: %v", name, row.Err)
		}
	}
}

func TestCheckAbandonCandidate_BrokenNoOp_CommitAfterAbandonDetected(t *testing.T) {
	fake := newAbandonFakeClient(false)
	results := checkAbandonCandidate(context.Background(), fake)
	row := findAbandonRow(t, results, "AbandonCandidateRejectsCommitAfterAbandon")
	if row.Err == nil {
		t.Fatalf("AbandonCandidateRejectsCommitAfterAbandon: expected non-nil Err when CommitCandidate silently succeeds after AbandonCandidate, got PASS")
	}
}

func TestCheckAbandonCandidate_BrokenNoOp_UnknownHandleDetected(t *testing.T) {
	fake := newAbandonFakeClient(false)
	results := checkAbandonCandidate(context.Background(), fake)
	row := findAbandonRow(t, results, "AbandonCandidateUnknownHandleFailsCleanly")
	if row.Err == nil {
		t.Fatalf("AbandonCandidateUnknownHandleFailsCleanly: expected non-nil Err when AbandonCandidate silently succeeds on an unknown handle, got PASS")
	}
}
