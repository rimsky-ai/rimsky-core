// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type DataProcessing struct {
	genv1.UnimplementedDataProcessingServer

	mu         sync.Mutex
	candidates map[string]*candidate
	versions   map[string][]*versionRecord

	versionSeq atomic.Uint64

	commitCount  atomic.Uint64
	abandonCount atomic.Uint64
}

type candidate struct {
	handle        []byte
	claimHandleID string
	subScope      []byte
}

type versionRecord struct {
	versionID   string
	committedAt time.Time
	metadata    []byte
	partitions  []*genv1.PartitionDescriptor
	schema      []byte
}

func newDataProcessing() *DataProcessing {
	return &DataProcessing{
		candidates: map[string]*candidate{},
		versions:   map[string][]*versionRecord{},
	}
}

func (d *DataProcessing) CommitCount() uint64 { return d.commitCount.Load() }

func (d *DataProcessing) AbandonCount() uint64 { return d.abandonCount.Load() }

func (d *DataProcessing) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{
		DataShapes:       []string{"parquet"},
		Materializations: []string{"partitioned"},
		PartitionKinds:   []string{"date_range"},
		Aggregators:      []string{"union"},
	}, nil
}

func (d *DataProcessing) BeginCandidate(_ context.Context, req *genv1.BeginCandidateRequest) (*genv1.BeginCandidateResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := req.GetIdempotencyKey()
	if existing, ok := d.candidates[key]; ok {
		return &genv1.BeginCandidateResponse{CandidateHandle: existing.handle}, nil
	}

	handle := []byte(fmt.Sprintf("candidate:%s:%s", req.GetClaimHandleId(), key))
	d.candidates[key] = &candidate{
		handle:        handle,
		claimHandleID: req.GetClaimHandleId(),
		subScope:      append([]byte(nil), req.GetSubScopeDescriptor()...),
	}
	return &genv1.BeginCandidateResponse{CandidateHandle: handle}, nil
}

func (d *DataProcessing) CommitCandidate(_ context.Context, req *genv1.CommitCandidateRequest) (*genv1.CommitCandidateResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key, ok := d.findKeyByHandle(req.GetCandidateHandle())
	if !ok {
		return nil, status.Errorf(codes.FailedPrecondition, "commit: unknown candidate handle")
	}
	cand := d.candidates[key]
	delete(d.candidates, key)

	versionID := fmt.Sprintf("v%d", d.versionSeq.Add(1))
	metadata := []byte(fmt.Sprintf(`{"shape":"parquet","status":"committed","version_id":%q}`, versionID))
	partition := &genv1.PartitionDescriptor{
		PartitionKey:      string(cand.subScope),
		PartitionMetadata: []byte(fmt.Sprintf(`{"sub_scope":%q}`, string(cand.subScope))),
	}
	schema := []byte(`{"type":"object","properties":{"ts":{"type":"string","format":"date-time"},"value":{"type":"number"}}}`)
	rec := &versionRecord{
		versionID:   versionID,
		committedAt: time.Now().UTC(),
		metadata:    metadata,
		partitions:  []*genv1.PartitionDescriptor{partition},
		schema:      schema,
	}
	d.versions[cand.claimHandleID] = append(d.versions[cand.claimHandleID], rec)
	d.commitCount.Add(1)

	return &genv1.CommitCandidateResponse{
		CandidateMetadata: metadata,
	}, nil
}

func (d *DataProcessing) AbandonCandidate(_ context.Context, req *genv1.AbandonCandidateRequest) (*emptypb.Empty, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if key, ok := d.findKeyByHandle(req.GetCandidateHandle()); ok {
		delete(d.candidates, key)
		d.abandonCount.Add(1)
	}
	return &emptypb.Empty{}, nil
}

func (d *DataProcessing) ListVersions(_ context.Context, req *genv1.ListVersionsRequest) (*genv1.ListVersionsResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	recs := d.versions[req.GetClaimHandleId()]
	out := &genv1.ListVersionsResponse{}
	for _, r := range recs {
		out.Versions = append(out.Versions, &genv1.VersionMetadata{
			VersionId:        r.versionID,
			CommittedAt:      timestamppb.New(r.committedAt),
			ProducerMetadata: r.metadata,
		})
	}
	return out, nil
}

func (d *DataProcessing) ListPartitions(_ context.Context, req *genv1.ListPartitionsRequest) (*genv1.ListPartitionsResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rec, ok := d.findVersion(req.GetClaimHandleId(), req.GetVersionId())
	if !ok {
		return nil, status.Errorf(codes.FailedPrecondition, "list_partitions: unknown (claim_handle_id=%q, version_id=%q)",
			req.GetClaimHandleId(), req.GetVersionId())
	}
	return &genv1.ListPartitionsResponse{Partitions: rec.partitions}, nil
}

func (d *DataProcessing) GetVersionSchema(_ context.Context, req *genv1.GetVersionSchemaRequest) (*genv1.GetVersionSchemaResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rec, ok := d.findVersion(req.GetClaimHandleId(), req.GetVersionId())
	if !ok {
		return nil, status.Errorf(codes.FailedPrecondition, "get_version_schema: unknown (claim_handle_id=%q, version_id=%q)",
			req.GetClaimHandleId(), req.GetVersionId())
	}
	return &genv1.GetVersionSchemaResponse{Schema: rec.schema}, nil
}

func (d *DataProcessing) findKeyByHandle(handle []byte) (string, bool) {
	for key, stored := range d.candidates {
		if string(stored.handle) == string(handle) {
			return key, true
		}
	}
	return "", false
}

func (d *DataProcessing) findVersion(claimHandleID, versionID string) (*versionRecord, bool) {
	for _, r := range d.versions[claimHandleID] {
		if r.versionID == versionID {
			return r, true
		}
	}
	return nil, false
}
