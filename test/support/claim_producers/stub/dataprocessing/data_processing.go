// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Server struct {
	genv1.UnimplementedDataProcessingServer

	mu         sync.Mutex
	candidates map[string]*candidateRow
	versions   map[string][]VersionRow

	clock     func() time.Time
	splitFunc func(req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error)
}

type candidateRow struct {
	ClaimHandleID   string
	CandidateHandle string
	IdempotencyKey  string
	SubScopeBytes   []byte
	PartitionKey    string
}

type VersionRow struct {
	VersionID    string
	CommittedAt  time.Time
	Metadata     []byte
	PartitionKey string
}

func New() *Server {
	return &Server{
		candidates: make(map[string]*candidateRow),
		versions:   make(map[string][]VersionRow),
		clock:      time.Now,
	}
}

func (s *Server) WithClock(c func() time.Time) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = c
	return s
}

func (s *Server) WithSplitScope(fn func(req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error)) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.splitFunc = fn
	return s
}

func (s *Server) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{
		DataShapes:       []string{"stub"},
		Materializations: []string{"full"},
		PartitionKinds:   []string{"attribute_value"},
		Aggregators:      []string{"union"},
	}, nil
}

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
	handle := fmt.Sprintf("cand:%s:%s", req.GetClaimHandleId(), req.GetIdempotencyKey())
	row := &candidateRow{
		ClaimHandleID:   req.GetClaimHandleId(),
		CandidateHandle: handle,
		IdempotencyKey:  req.GetIdempotencyKey(),
		SubScopeBytes:   cloneBytes(req.GetSubScopeDescriptor()),
	}
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

func (s *Server) CommitCandidate(_ context.Context, req *genv1.CommitCandidateRequest) (*genv1.CommitCandidateResponse, error) {
	handle := string(req.GetCandidateHandle())
	if handle == "" {
		return nil, fmt.Errorf("stub.CommitCandidate: candidate_handle required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.candidates[handle]
	if !ok {
		return nil, fmt.Errorf("stub.CommitCandidate: candidate_handle %q not found (never issued by BeginCandidate, already committed, or already abandoned)", handle)
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

func (s *Server) AbandonCandidate(_ context.Context, req *genv1.AbandonCandidateRequest) (*emptypb.Empty, error) {
	handle := string(req.GetCandidateHandle())
	if handle == "" {
		return nil, fmt.Errorf("stub.AbandonCandidate: candidate_handle required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.candidates[handle]; !ok {
		return nil, fmt.Errorf("stub.AbandonCandidate: candidate_handle %q not found (never issued by BeginCandidate, already committed, or already abandoned)", handle)
	}
	delete(s.candidates, handle)
	return &emptypb.Empty{}, nil
}

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

func (s *Server) SplitScope(_ context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	s.mu.Lock()
	fn := s.splitFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return DecodeSplitScope(req.GetPartitionRequest())
}

func DecodeSplitScope(partitionRequest []byte) (*genv1.SplitScopeResponse, error) {
	var probe struct {
		PartitionKeys []string          `json:"partition_keys"`
		List          []json.RawMessage `json:"list"`
	}
	if err := json.Unmarshal(partitionRequest, &probe); err != nil {
		return nil, fmt.Errorf("stub.SplitScope: decode partition_request: %w", err)
	}
	if probe.List != nil {
		out := make([]*genv1.SubScopeDescriptor, 0, len(probe.List))
		for i, raw := range probe.List {
			var el struct {
				Key     string          `json:"key"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(raw, &el); err != nil {
				return nil, fmt.Errorf("stub.SplitScope: decode list[%d]: %w", i, err)
			}
			if el.Key == "" {
				return nil, fmt.Errorf("stub.SplitScope: list[%d].key must be non-empty", i)
			}
			scope, _ := json.Marshal(map[string]string{"partition_key": el.Key})
			out = append(out, &genv1.SubScopeDescriptor{
				ClaimScopeData: scope,
				PartitionKey:   el.Key,
				Payload:        []byte(el.Payload),
				Address:        []byte{},
			})
		}
		return &genv1.SplitScopeResponse{SubScopes: out}, nil
	}
	if len(probe.PartitionKeys) == 0 {
		return nil, fmt.Errorf("stub.SplitScope: partition_request.partition_keys must be non-empty")
	}
	out := make([]*genv1.SubScopeDescriptor, 0, len(probe.PartitionKeys))
	for _, key := range probe.PartitionKeys {
		scope, _ := json.Marshal(map[string]string{"partition_key": key})
		out = append(out, &genv1.SubScopeDescriptor{
			ClaimScopeData: scope,
			PartitionKey:   key,
		})
	}
	return &genv1.SplitScopeResponse{SubScopes: out}, nil
}

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
