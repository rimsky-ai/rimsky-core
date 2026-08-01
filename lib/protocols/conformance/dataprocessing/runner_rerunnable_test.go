// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package dataprocessing

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type rerunCandidateState struct {
	claimHandleID string
	subScope      []byte
}

type rerunVersionRecord struct {
	versionID string
	subScope  []byte
}

type honestDataProcessingFake struct {
	genv1.DataProcessingClient

	mu          sync.Mutex
	seq         int
	candidates  map[string]rerunCandidateState
	idempotency map[string]string
	versions    map[string][]rerunVersionRecord
}

func newHonestDataProcessingFake() *honestDataProcessingFake {
	return &honestDataProcessingFake{
		candidates:  map[string]rerunCandidateState{},
		idempotency: map[string]string{},
		versions:    map[string][]rerunVersionRecord{},
	}
}

func (f *honestDataProcessingFake) Capabilities(context.Context, *emptypb.Empty, ...grpc.CallOption) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{
		DataShapes:       []string{"parquet"},
		Materializations: []string{"full"},
	}, nil
}

func (f *honestDataProcessingFake) BeginCandidate(_ context.Context, req *genv1.BeginCandidateRequest, _ ...grpc.CallOption) (*genv1.BeginCandidateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idemKey := req.GetClaimHandleId() + "|" + req.GetIdempotencyKey()
	if h, ok := f.idempotency[idemKey]; ok {
		return &genv1.BeginCandidateResponse{CandidateHandle: []byte(h)}, nil
	}
	f.seq++
	handle := fmt.Sprintf("cand-%d", f.seq)
	f.candidates[handle] = rerunCandidateState{claimHandleID: req.GetClaimHandleId(), subScope: req.GetSubScopeDescriptor()}
	f.idempotency[idemKey] = handle
	return &genv1.BeginCandidateResponse{CandidateHandle: []byte(handle)}, nil
}

func (f *honestDataProcessingFake) CommitCandidate(_ context.Context, req *genv1.CommitCandidateRequest, _ ...grpc.CallOption) (*genv1.CommitCandidateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	handle := string(req.GetCandidateHandle())
	st, ok := f.candidates[handle]
	if !ok {
		return nil, fmt.Errorf("fake.CommitCandidate: unknown candidate_handle %q", handle)
	}
	delete(f.candidates, handle)
	f.seq++
	versionID := fmt.Sprintf("v-%d", f.seq)
	f.versions[st.claimHandleID] = append(f.versions[st.claimHandleID], rerunVersionRecord{versionID: versionID, subScope: st.subScope})
	return &genv1.CommitCandidateResponse{}, nil
}

func (f *honestDataProcessingFake) AbandonCandidate(_ context.Context, req *genv1.AbandonCandidateRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	handle := string(req.GetCandidateHandle())
	if _, ok := f.candidates[handle]; !ok {
		return nil, fmt.Errorf("fake.AbandonCandidate: unknown candidate_handle %q", handle)
	}
	delete(f.candidates, handle)
	return &emptypb.Empty{}, nil
}

func (f *honestDataProcessingFake) ListVersions(_ context.Context, req *genv1.ListVersionsRequest, _ ...grpc.CallOption) (*genv1.ListVersionsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	recs := f.versions[req.GetClaimHandleId()]
	out := make([]*genv1.VersionMetadata, 0, len(recs))
	for _, r := range recs {
		out = append(out, &genv1.VersionMetadata{VersionId: r.versionID})
	}
	return &genv1.ListVersionsResponse{Versions: out}, nil
}

func (f *honestDataProcessingFake) ListPartitions(_ context.Context, req *genv1.ListPartitionsRequest, _ ...grpc.CallOption) (*genv1.ListPartitionsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	var out []*genv1.PartitionDescriptor
	for _, r := range f.versions[req.GetClaimHandleId()] {
		var m map[string]any
		_ = json.Unmarshal(r.subScope, &m)
		key, _ := m["partition_key"].(string)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, &genv1.PartitionDescriptor{PartitionKey: key})
	}
	return &genv1.ListPartitionsResponse{Partitions: out}, nil
}

func (f *honestDataProcessingFake) GetVersionSchema(context.Context, *genv1.GetVersionSchemaRequest, ...grpc.CallOption) (*genv1.GetVersionSchemaResponse, error) {
	return &genv1.GetVersionSchemaResponse{Schema: []byte(`{"type":"object"}`)}, nil
}

func TestRun_IsRerunnableAgainstAStatefulProducer(t *testing.T) {
	fake := newHonestDataProcessingFake()
	for attempt := 1; attempt <= 2; attempt++ {
		results := Run(context.Background(), fake)
		for _, r := range results {
			if r.Err != nil {
				t.Errorf("attempt %d: row %q failed against a stateful producer that accumulates state "+
					"across invocations (constant claim_handle_id/idempotency_key would collide on rerun): %v",
					attempt, r.Name, r.Err)
			}
		}
	}
}

type emptySchemaDataProcessingFake struct {
	*honestDataProcessingFake
}

func (f *emptySchemaDataProcessingFake) GetVersionSchema(context.Context, *genv1.GetVersionSchemaRequest, ...grpc.CallOption) (*genv1.GetVersionSchemaResponse, error) {
	return &genv1.GetVersionSchemaResponse{Schema: nil}, nil
}

func TestCheckGetVersionSchemaSmoke_EmptySchemaIsNotAHardFailure(t *testing.T) {
	fake := &emptySchemaDataProcessingFake{honestDataProcessingFake: newHonestDataProcessingFake()}
	row := checkGetVersionSchemaSmoke(context.Background(), fake, "empty-schema-fixture")
	if row.Err != nil {
		t.Fatalf("GetVersionSchemaSmoke: empty schema bytes must not be a hard failure "+
			"(data_processing.proto documents schema as opaque with no non-empty requirement), got: %v", row.Err)
	}
}
