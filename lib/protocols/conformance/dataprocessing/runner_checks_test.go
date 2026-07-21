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

type dpFake struct {
	genv1.DataProcessingClient

	dishonestIdempotency  bool
	versionsAlwaysEmpty   bool
	partitionsAlwaysEmpty bool
	schemaAlwaysEmpty     bool
	dropConcurrentWrites  bool

	mu          sync.Mutex
	nextHandle  int
	idempotency map[string][]byte
	candidates  map[string]string
	versions    map[string][]string
	partitions  map[string][]string
}

func newDPFake() *dpFake {
	return &dpFake{
		idempotency: map[string][]byte{},
		candidates:  map[string]string{},
		versions:    map[string][]string{},
		partitions:  map[string][]string{},
	}
}

func (f *dpFake) Capabilities(context.Context, *emptypb.Empty, ...grpc.CallOption) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{
		DataShapes:       []string{"table"},
		Materializations: []string{"main"},
	}, nil
}

func (f *dpFake) BeginCandidate(_ context.Context, req *genv1.BeginCandidateRequest, _ ...grpc.CallOption) (*genv1.BeginCandidateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idemKey := req.GetClaimHandleId() + "|" + req.GetIdempotencyKey()
	if !f.dishonestIdempotency {
		if h, ok := f.idempotency[idemKey]; ok {
			return &genv1.BeginCandidateResponse{CandidateHandle: h}, nil
		}
	}
	f.nextHandle++
	handle := []byte(fmt.Sprintf("cand-%d", f.nextHandle))
	f.idempotency[idemKey] = handle
	f.candidates[string(handle)] = req.GetClaimHandleId()
	return &genv1.BeginCandidateResponse{CandidateHandle: handle}, nil
}

func (f *dpFake) CommitCandidate(_ context.Context, req *genv1.CommitCandidateRequest, _ ...grpc.CallOption) (*genv1.CommitCandidateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	handle := string(req.GetCandidateHandle())
	claimHandleID, ok := f.candidates[handle]
	if !ok {
		return nil, fmt.Errorf("fake.CommitCandidate: unknown candidate_handle %q", handle)
	}
	delete(f.candidates, handle)
	if f.dropConcurrentWrites {
		return &genv1.CommitCandidateResponse{}, nil
	}
	versionID := "v-" + handle
	f.versions[claimHandleID] = append(f.versions[claimHandleID], versionID)
	f.partitions[claimHandleID] = append(f.partitions[claimHandleID], "conformance-region")
	return &genv1.CommitCandidateResponse{}, nil
}

func (f *dpFake) AbandonCandidate(_ context.Context, req *genv1.AbandonCandidateRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.candidates, string(req.GetCandidateHandle()))
	return &emptypb.Empty{}, nil
}

func (f *dpFake) ListVersions(_ context.Context, req *genv1.ListVersionsRequest, _ ...grpc.CallOption) (*genv1.ListVersionsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.versionsAlwaysEmpty {
		return &genv1.ListVersionsResponse{}, nil
	}
	out := make([]*genv1.VersionMetadata, 0, len(f.versions[req.GetClaimHandleId()]))
	for _, id := range f.versions[req.GetClaimHandleId()] {
		out = append(out, &genv1.VersionMetadata{VersionId: id})
	}
	return &genv1.ListVersionsResponse{Versions: out}, nil
}

func (f *dpFake) ListPartitions(_ context.Context, req *genv1.ListPartitionsRequest, _ ...grpc.CallOption) (*genv1.ListPartitionsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.partitionsAlwaysEmpty {
		return &genv1.ListPartitionsResponse{}, nil
	}
	out := make([]*genv1.PartitionDescriptor, 0, len(f.partitions[req.GetClaimHandleId()]))
	for _, key := range f.partitions[req.GetClaimHandleId()] {
		out = append(out, &genv1.PartitionDescriptor{PartitionKey: key})
	}
	return &genv1.ListPartitionsResponse{Partitions: out}, nil
}

func (f *dpFake) GetVersionSchema(context.Context, *genv1.GetVersionSchemaRequest, ...grpc.CallOption) (*genv1.GetVersionSchemaResponse, error) {
	if f.schemaAlwaysEmpty {
		return &genv1.GetVersionSchemaResponse{}, nil
	}
	return &genv1.GetVersionSchemaResponse{Schema: []byte(`{"type":"object"}`)}, nil
}

func TestCheckBeginCandidateIdempotent_Honest_Passes(t *testing.T) {
	fake := newDPFake()
	row := checkBeginCandidateIdempotent(context.Background(), fake)
	if row.Err != nil {
		t.Fatalf("expected PASS for an honest idempotent BeginCandidate, got Err: %v", row.Err)
	}
}

func TestCheckBeginCandidateIdempotent_Dishonest_Fails(t *testing.T) {
	fake := newDPFake()
	fake.dishonestIdempotency = true
	row := checkBeginCandidateIdempotent(context.Background(), fake)
	if row.Err == nil {
		t.Fatal("expected non-nil Err when a retried BeginCandidate returns a different candidate_handle, got PASS")
	}
}

func TestCheckListVersionsSmoke_Honest_Passes(t *testing.T) {
	fake := newDPFake()
	row := checkListVersionsSmoke(context.Background(), fake)
	if row.Err != nil {
		t.Fatalf("expected PASS, got Err: %v", row.Err)
	}
}

func TestCheckListVersionsSmoke_AlwaysEmpty_Fails(t *testing.T) {
	fake := newDPFake()
	fake.versionsAlwaysEmpty = true
	row := checkListVersionsSmoke(context.Background(), fake)
	if row.Err == nil {
		t.Fatal("expected non-nil Err when ListVersions returns zero versions after a successful commit, got PASS")
	}
}

func TestCheckListPartitionsSmoke_Honest_Passes(t *testing.T) {
	fake := newDPFake()
	row := checkListPartitionsSmoke(context.Background(), fake)
	if row.Err != nil {
		t.Fatalf("expected PASS, got Err: %v", row.Err)
	}
}

func TestCheckListPartitionsSmoke_AlwaysEmpty_Fails(t *testing.T) {
	fake := newDPFake()
	fake.partitionsAlwaysEmpty = true
	row := checkListPartitionsSmoke(context.Background(), fake)
	if row.Err == nil {
		t.Fatal("expected non-nil Err when ListPartitions returns zero partitions after a successful commit, got PASS")
	}
}

func TestCheckGetVersionSchemaSmoke_Honest_Passes(t *testing.T) {
	fake := newDPFake()
	row := checkGetVersionSchemaSmoke(context.Background(), fake)
	if row.Err != nil {
		t.Fatalf("expected PASS, got Err: %v", row.Err)
	}
}

func TestCheckGetVersionSchemaSmoke_EmptySchema_Fails(t *testing.T) {
	fake := newDPFake()
	fake.schemaAlwaysEmpty = true
	row := checkGetVersionSchemaSmoke(context.Background(), fake)
	if row.Err == nil {
		t.Fatal("expected non-nil Err when GetVersionSchema returns empty schema bytes, got PASS")
	}
}

func TestCheckConcurrentWrites_Honest_Passes(t *testing.T) {
	fake := newDPFake()
	row := checkConcurrentWrites(context.Background(), fake)
	if row.Err != nil {
		t.Fatalf("expected PASS, got Err: %v", row.Err)
	}
}

func TestCheckConcurrentWrites_DroppedWrites_Fails(t *testing.T) {
	fake := newDPFake()
	fake.dropConcurrentWrites = true
	row := checkConcurrentWrites(context.Background(), fake)
	if row.Err == nil {
		t.Fatal("expected non-nil Err when concurrent commits silently drop versions, got PASS")
	}
}

func TestRun_Capabilities_EmptyDataShapesFails(t *testing.T) {
	fake := &emptyCapsFake{}
	results := Run(context.Background(), fake)
	if len(results) != 1 || results[0].Name != "Capabilities" || results[0].Err == nil {
		t.Fatalf("expected a single failing Capabilities row for empty data_shapes, got %+v", results)
	}
}

type emptyCapsFake struct {
	genv1.DataProcessingClient
}

func (emptyCapsFake) Capabilities(context.Context, *emptypb.Empty, ...grpc.CallOption) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{}, nil
}
