// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package dataprocessing implements the DataProcessing mix-in
// service-protocol for the stub store-service. In-memory state;
// deterministic; no external dependencies. Per the H-cut block in
// plan:2026-05-15-data-platform-extensions-plan.md (M1 prep) and the
// stub-store DataProcessing extension surfaced in the O1 smoke
// fan-out coverage.
//
// The extension advertises a minimal `data_shapes: ["stub"]` shape,
// a single `full` materialization, an `attribute_value` partition
// kind, and the `union` aggregator. The fixture is the M1 / N6 / N7 /
// O1 self-test target; production data formats live elsewhere
// (parquet / geo-parquet / postgis-table were cut alongside
// Section H).
//
// @concept: data-processing
package dataprocessing

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// Server is the in-memory DataProcessing server. Stateless from the
// caller's perspective beyond the keyed candidates map; concurrency-
// safe via a single mutex.
type Server struct {
	genv1.UnimplementedDataProcessingServer

	mu sync.Mutex
	// candidates keyed by candidate_handle. Candidates linger until
	// CommitCandidate or AbandonCandidate; CommitCandidate flips them
	// onto the per-claim_handle versions slice.
	candidates map[string]*candidateRow
	// versions keyed by claim_handle_id, sorted by committed_at asc.
	versions map[string][]VersionRow

	clock func() time.Time
	// splitFunc returns N SubScopeDescriptors per partition_request.
	// nil = use the default decoder that reads a JSON object
	// {"partition_keys": ["a","b","c"]} and emits one descriptor per
	// key.
	splitFunc func(req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error)
}

// candidateRow is the per-BeginCandidate state. idempotency_key →
// candidate_handle is enforced so a retried BeginCandidate against
// the same (claim_handle_id, idempotency_key) returns the same
// handle.
type candidateRow struct {
	ClaimHandleID   string
	CandidateHandle string
	IdempotencyKey  string
	SubScopeBytes   []byte
	PartitionKey    string
}

// VersionRow is the committed version snapshot. VersionID is a
// deterministic counter scoped to the claim_handle_id ("v1", "v2",
// ...) so tests can pin against the format.
type VersionRow struct {
	VersionID    string
	CommittedAt  time.Time
	Metadata     []byte
	PartitionKey string
}

// New constructs a fresh in-memory DataProcessing server. Clock
// defaults to time.Now.
func New() *Server {
	return &Server{
		candidates: make(map[string]*candidateRow),
		versions:   make(map[string][]VersionRow),
		clock:      time.Now,
	}
}

// WithClock returns s; mutates the clock seam in place. Test helper.
func (s *Server) WithClock(c func() time.Time) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = c
	return s
}

// WithSplitScope overrides the default partition_request decoder.
// Test helper.
func (s *Server) WithSplitScope(fn func(req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error)) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.splitFunc = fn
	return s
}

// Capabilities advertises the stub's data shape, materializations,
// partition kinds, and aggregators. Stable across calls.
func (s *Server) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{
		DataShapes:       []string{"stub"},
		Materializations: []string{"full"},
		PartitionKinds:   []string{"attribute_value"},
		Aggregators:      []string{"union"},
	}, nil
}

// BeginCandidate allocates a staging candidate keyed by
// (claim_handle_id, idempotency_key). Re-issuing BeginCandidate with
// the same idempotency_key returns the existing candidate_handle.
// Per proto:data_processing.proto::BeginCandidateRequest.idempotency_key.
func (s *Server) BeginCandidate(_ context.Context, req *genv1.BeginCandidateRequest) (*genv1.BeginCandidateResponse, error) {
	if req.GetClaimHandleId() == "" {
		return nil, fmt.Errorf("stub.BeginCandidate: claim_handle_id required")
	}
	if req.GetIdempotencyKey() == "" {
		return nil, fmt.Errorf("stub.BeginCandidate: idempotency_key required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for handle, row := range s.candidates {
		if row.ClaimHandleID == req.GetClaimHandleId() && row.IdempotencyKey == req.GetIdempotencyKey() {
			return &genv1.BeginCandidateResponse{CandidateHandle: []byte(handle)}, nil
		}
	}
	// Allocate a fresh candidate_handle. Format is
	// "cand:<claim_handle_id>:<idempotency_key>" — opaque to rimsky;
	// stable so test assertions can pin it.
	handle := fmt.Sprintf("cand:%s:%s", req.GetClaimHandleId(), req.GetIdempotencyKey())
	row := &candidateRow{
		ClaimHandleID:   req.GetClaimHandleId(),
		CandidateHandle: handle,
		IdempotencyKey:  req.GetIdempotencyKey(),
		SubScopeBytes:   cloneBytes(req.GetSubScopeDescriptor()),
	}
	// Sniff a partition_key from the sub_scope_descriptor when it
	// JSON-decodes as {"partition_key": "..."}. Best-effort.
	var sniff struct {
		PartitionKey string `json:"partition_key"`
	}
	if len(req.GetSubScopeDescriptor()) > 0 {
		_ = json.Unmarshal(req.GetSubScopeDescriptor(), &sniff)
	}
	row.PartitionKey = sniff.PartitionKey
	s.candidates[handle] = row
	return &genv1.BeginCandidateResponse{CandidateHandle: []byte(handle)}, nil
}

// CommitCandidate finalizes the candidate; flips it onto the
// versions slice for the claim_handle. Idempotent on already-
// committed / already-abandoned candidate_handle.
func (s *Server) CommitCandidate(_ context.Context, req *genv1.CommitCandidateRequest) (*genv1.CommitCandidateResponse, error) {
	handle := string(req.GetCandidateHandle())
	if handle == "" {
		return nil, fmt.Errorf("stub.CommitCandidate: candidate_handle required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.candidates[handle]
	if !ok {
		return &genv1.CommitCandidateResponse{}, nil
	}
	delete(s.candidates, handle)
	versions := s.versions[row.ClaimHandleID]
	versionID := fmt.Sprintf("v%d", len(versions)+1)
	committedAt := s.clock()
	metadata, _ := json.Marshal(map[string]any{
		"version_id":    versionID,
		"partition_key": row.PartitionKey,
		"sub_scope":     string(row.SubScopeBytes),
		"row_count":     1,
	})
	s.versions[row.ClaimHandleID] = append(versions, VersionRow{
		VersionID:    versionID,
		CommittedAt:  committedAt,
		Metadata:     metadata,
		PartitionKey: row.PartitionKey,
	})
	return &genv1.CommitCandidateResponse{CandidateMetadata: metadata}, nil
}

// AbandonCandidate GCs the candidate without flipping it onto
// versions. Idempotent on unknown candidate_handle.
func (s *Server) AbandonCandidate(_ context.Context, req *genv1.AbandonCandidateRequest) (*emptypb.Empty, error) {
	handle := string(req.GetCandidateHandle())
	if handle == "" {
		return nil, fmt.Errorf("stub.AbandonCandidate: candidate_handle required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.candidates, handle)
	return &emptypb.Empty{}, nil
}

// ListVersions returns the per-claim_handle version slice in commit
// order.
func (s *Server) ListVersions(_ context.Context, req *genv1.ListVersionsRequest) (*genv1.ListVersionsResponse, error) {
	if req.GetClaimHandleId() == "" {
		return nil, fmt.Errorf("stub.ListVersions: claim_handle_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.versions[req.GetClaimHandleId()]
	out := make([]*genv1.VersionMetadata, 0, len(rows))
	for _, r := range rows {
		out = append(out, &genv1.VersionMetadata{
			VersionId:        r.VersionID,
			CommittedAt:      timestamppb.New(r.CommittedAt),
			ProducerMetadata: r.Metadata,
		})
	}
	return &genv1.ListVersionsResponse{Versions: out}, nil
}

// ListPartitions returns one PartitionDescriptor per version row
// matching version_id. When version_id is empty, all partitions for
// the claim_handle are returned in partition_key order.
func (s *Server) ListPartitions(_ context.Context, req *genv1.ListPartitionsRequest) (*genv1.ListPartitionsResponse, error) {
	if req.GetClaimHandleId() == "" {
		return nil, fmt.Errorf("stub.ListPartitions: claim_handle_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.versions[req.GetClaimHandleId()]
	out := make([]*genv1.PartitionDescriptor, 0)
	for _, r := range rows {
		if req.GetVersionId() != "" && r.VersionID != req.GetVersionId() {
			continue
		}
		out = append(out, &genv1.PartitionDescriptor{
			PartitionKey:      r.PartitionKey,
			PartitionMetadata: r.Metadata,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PartitionKey < out[j].PartitionKey })
	return &genv1.ListPartitionsResponse{Partitions: out}, nil
}

// GetVersionSchema returns a fixture JSON Schema for the "stub" data
// shape — a single integer `row_id` column.
func (s *Server) GetVersionSchema(_ context.Context, req *genv1.GetVersionSchemaRequest) (*genv1.GetVersionSchemaResponse, error) {
	if req.GetClaimHandleId() == "" {
		return nil, fmt.Errorf("stub.GetVersionSchema: claim_handle_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.versions[req.GetClaimHandleId()]
	if req.GetVersionId() != "" {
		found := false
		for _, r := range rows {
			if r.VersionID == req.GetVersionId() {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("stub.GetVersionSchema: version_id %q not found for claim_handle %q",
				req.GetVersionId(), req.GetClaimHandleId())
		}
	}
	schema := []byte(`{"type":"object","properties":{"row_id":{"type":"integer"}},"required":["row_id"]}`)
	return &genv1.GetVersionSchemaResponse{Schema: schema}, nil
}

// SplitScope partitions a parent scope into N sub-scopes per the
// decoded partition_request. The default decoder accepts a JSON
// object `{"partition_keys": ["a", "b", "c"]}` and emits one
// SubScopeDescriptor per key; each descriptor carries
// `scope_data: {"partition_key": "<key>"}` so BeginCandidate's
// partition_key sniff round-trips end-to-end.
//
// This method lives on the DataProcessing server purely as a
// convenience helper for tests that don't bring up a full
// ClaimProducer wire. The on-wire SplitScope lives on the
// ClaimProducer service; the equivalent stub-store ClaimProducer
// hook is wired in stores/stub/server (see server.SplitScope).
func (s *Server) SplitScope(_ context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	s.mu.Lock()
	fn := s.splitFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	var decoded struct {
		PartitionKeys []string `json:"partition_keys"`
	}
	if err := json.Unmarshal(req.GetPartitionRequest(), &decoded); err != nil {
		return nil, fmt.Errorf("stub.SplitScope: decode partition_request: %w", err)
	}
	if len(decoded.PartitionKeys) == 0 {
		return nil, fmt.Errorf("stub.SplitScope: partition_request.partition_keys must be non-empty")
	}
	out := make([]*genv1.SubScopeDescriptor, 0, len(decoded.PartitionKeys))
	for _, key := range decoded.PartitionKeys {
		scope, _ := json.Marshal(map[string]string{"partition_key": key})
		out = append(out, &genv1.SubScopeDescriptor{
			ScopeData:    scope,
			PartitionKey: key,
		})
	}
	return &genv1.SplitScopeResponse{SubScopes: out}, nil
}

// Snapshot returns a deep-copy of the per-claim version slice for
// test assertions. Returns nil for unknown claim_handle.
func (s *Server) Snapshot(claimHandleID string) []VersionRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.versions[claimHandleID]
	if len(src) == 0 {
		return nil
	}
	out := make([]VersionRow, len(src))
	copy(out, src)
	return out
}

// CandidateCount returns the live candidate count.
func (s *Server) CandidateCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.candidates)
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
