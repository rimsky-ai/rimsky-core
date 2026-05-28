// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package dataprocessing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestCapabilities pins the stub's advertised capability set: one
// data shape, full materialization, attribute_value partitioning,
// union aggregator.
func TestCapabilities(t *testing.T) {
	s := New()
	caps, err := s.Capabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if got, want := caps.GetDataShapes(), []string{"stub"}; !equalStrings(got, want) {
		t.Errorf("data_shapes: got %v want %v", got, want)
	}
	if got, want := caps.GetMaterializations(), []string{"full"}; !equalStrings(got, want) {
		t.Errorf("materializations: got %v want %v", got, want)
	}
	if got, want := caps.GetPartitionKinds(), []string{"attribute_value"}; !equalStrings(got, want) {
		t.Errorf("partition_kinds: got %v want %v", got, want)
	}
	if got, want := caps.GetAggregators(), []string{"union"}; !equalStrings(got, want) {
		t.Errorf("aggregators: got %v want %v", got, want)
	}
}

// TestBeginCommitRoundTrip drives a single candidate through
// Begin → Commit and asserts the version is observable via
// ListVersions / ListPartitions / GetVersionSchema.
func TestBeginCommitRoundTrip(t *testing.T) {
	fixedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	s := New().WithClock(func() time.Time { return fixedAt })
	ctx := context.Background()

	subScope, _ := json.Marshal(map[string]string{"partition_key": "region-a"})
	begin, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:      "claim-1",
		SubScopeDescriptor: subScope,
		IdempotencyKey:     "idem-1",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	if len(begin.GetCandidateHandle()) == 0 {
		t.Fatal("BeginCandidate: empty candidate_handle")
	}

	commit, err := s.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	})
	if err != nil {
		t.Fatalf("CommitCandidate: %v", err)
	}
	if len(commit.GetCandidateMetadata()) == 0 {
		t.Fatal("CommitCandidate: empty candidate_metadata")
	}

	versions, err := s.ListVersions(ctx, &genv1.ListVersionsRequest{ClaimHandleId: "claim-1"})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions.GetVersions()) != 1 {
		t.Fatalf("ListVersions: expected 1 version, got %d", len(versions.GetVersions()))
	}
	v := versions.GetVersions()[0]
	if v.GetVersionId() != "v1" {
		t.Errorf("version_id: got %q want v1", v.GetVersionId())
	}
	if !v.GetCommittedAt().AsTime().Equal(fixedAt) {
		t.Errorf("committed_at: got %v want %v", v.GetCommittedAt().AsTime(), fixedAt)
	}

	partitions, err := s.ListPartitions(ctx, &genv1.ListPartitionsRequest{
		ClaimHandleId: "claim-1",
		VersionId:     "v1",
	})
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	if len(partitions.GetPartitions()) != 1 {
		t.Fatalf("ListPartitions: expected 1 partition, got %d", len(partitions.GetPartitions()))
	}
	if partitions.GetPartitions()[0].GetPartitionKey() != "region-a" {
		t.Errorf("partition_key: got %q want region-a", partitions.GetPartitions()[0].GetPartitionKey())
	}

	schema, err := s.GetVersionSchema(ctx, &genv1.GetVersionSchemaRequest{
		ClaimHandleId: "claim-1",
		VersionId:     "v1",
	})
	if err != nil {
		t.Fatalf("GetVersionSchema: %v", err)
	}
	if len(schema.GetSchema()) == 0 {
		t.Error("GetVersionSchema: empty schema bytes")
	}
}

// TestBeginCandidateIdempotent re-issues BeginCandidate with the
// same (claim_handle_id, idempotency_key) and asserts the same
// candidate_handle is returned.
func TestBeginCandidateIdempotent(t *testing.T) {
	s := New()
	ctx := context.Background()
	req := &genv1.BeginCandidateRequest{
		ClaimHandleId:  "claim-2",
		IdempotencyKey: "idem-same",
	}
	first, err := s.BeginCandidate(ctx, req)
	if err != nil {
		t.Fatalf("BeginCandidate #1: %v", err)
	}
	second, err := s.BeginCandidate(ctx, req)
	if err != nil {
		t.Fatalf("BeginCandidate #2: %v", err)
	}
	if string(first.GetCandidateHandle()) != string(second.GetCandidateHandle()) {
		t.Errorf("candidate_handle drift: %q vs %q",
			first.GetCandidateHandle(), second.GetCandidateHandle())
	}
	if s.CandidateCount() != 1 {
		t.Errorf("CandidateCount: got %d want 1", s.CandidateCount())
	}
}

// TestAbandonCandidate covers the abandon path: the candidate is
// removed without flipping onto versions; subsequent commit is a
// no-op.
func TestAbandonCandidate(t *testing.T) {
	s := New()
	ctx := context.Background()
	begin, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:  "claim-3",
		IdempotencyKey: "idem-3",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	if _, err := s.AbandonCandidate(ctx, &genv1.AbandonCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	}); err != nil {
		t.Fatalf("AbandonCandidate: %v", err)
	}
	if s.CandidateCount() != 0 {
		t.Errorf("CandidateCount after abandon: got %d want 0", s.CandidateCount())
	}
	versions, err := s.ListVersions(ctx, &genv1.ListVersionsRequest{ClaimHandleId: "claim-3"})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions.GetVersions()) != 0 {
		t.Errorf("ListVersions: expected 0 versions after abandon, got %d", len(versions.GetVersions()))
	}
	// Idempotent commit on abandoned handle.
	if _, err := s.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	}); err != nil {
		t.Errorf("CommitCandidate on abandoned handle: got error %v want nil", err)
	}
}

// TestSplitScopeDefaultDecoder asserts the default decoder produces
// one SubScopeDescriptor per partition_key, each carrying
// scope_data = {"partition_key": "<key>"}.
func TestSplitScopeDefaultDecoder(t *testing.T) {
	s := New()
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "claim-4",
		PartitionRequest: []byte(`{"partition_keys":["a","b","c"]}`),
	}
	resp, err := s.SplitScope(context.Background(), req)
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if len(resp.GetSubScopes()) != 3 {
		t.Fatalf("SplitScope: expected 3 sub-scopes, got %d", len(resp.GetSubScopes()))
	}
	wantKeys := []string{"a", "b", "c"}
	for i, sub := range resp.GetSubScopes() {
		if sub.GetPartitionKey() != wantKeys[i] {
			t.Errorf("sub[%d].partition_key: got %q want %q", i, sub.GetPartitionKey(), wantKeys[i])
		}
		var decoded struct {
			PartitionKey string `json:"partition_key"`
		}
		if err := json.Unmarshal(sub.GetClaimScopeData(), &decoded); err != nil {
			t.Errorf("sub[%d].claim_scope_data not JSON-decodable: %v", i, err)
		}
		if decoded.PartitionKey != wantKeys[i] {
			t.Errorf("sub[%d].claim_scope_data.partition_key: got %q want %q", i, decoded.PartitionKey, wantKeys[i])
		}
	}
}

// TestSplitScopeRejectsEmpty covers the partition_request validation
// gate.
func TestSplitScopeRejectsEmpty(t *testing.T) {
	s := New()
	_, err := s.SplitScope(context.Background(), &genv1.SplitScopeRequest{
		PartitionRequest: []byte(`{"partition_keys":[]}`),
	})
	if err == nil {
		t.Fatal("SplitScope: expected error on empty partition_keys, got nil")
	}
}

// TestSplitScopeCustomFunc overrides the decoder via WithSplitScope.
func TestSplitScopeCustomFunc(t *testing.T) {
	s := New().WithSplitScope(func(req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
		return &genv1.SplitScopeResponse{
			SubScopes: []*genv1.SubScopeDescriptor{
				{PartitionKey: "custom-1", ClaimScopeData: []byte(`{"x":1}`)},
			},
		}, nil
	})
	resp, err := s.SplitScope(context.Background(), &genv1.SplitScopeRequest{})
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if len(resp.GetSubScopes()) != 1 || resp.GetSubScopes()[0].GetPartitionKey() != "custom-1" {
		t.Fatalf("SplitScope custom: unexpected response %+v", resp)
	}
}

// TestMultipleCommitsPreserveOrder pushes three candidates through
// the loop and asserts ListVersions returns them in commit order.
func TestMultipleCommitsPreserveOrder(t *testing.T) {
	s := New()
	ctx := context.Background()
	for i, key := range []string{"k1", "k2", "k3"} {
		sub, _ := json.Marshal(map[string]string{"partition_key": key})
		begin, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
			ClaimHandleId:      "claim-5",
			SubScopeDescriptor: sub,
			IdempotencyKey:     key,
		})
		if err != nil {
			t.Fatalf("BeginCandidate[%d]: %v", i, err)
		}
		if _, err := s.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
			CandidateHandle: begin.GetCandidateHandle(),
		}); err != nil {
			t.Fatalf("CommitCandidate[%d]: %v", i, err)
		}
	}
	versions, err := s.ListVersions(ctx, &genv1.ListVersionsRequest{ClaimHandleId: "claim-5"})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions.GetVersions()) != 3 {
		t.Fatalf("ListVersions: expected 3 versions, got %d", len(versions.GetVersions()))
	}
	wantIDs := []string{"v1", "v2", "v3"}
	for i, v := range versions.GetVersions() {
		if v.GetVersionId() != wantIDs[i] {
			t.Errorf("versions[%d].version_id: got %q want %q", i, v.GetVersionId(), wantIDs[i])
		}
	}
}

// TestGetVersionSchemaMissingVersionID rejects an unknown version_id.
func TestGetVersionSchemaMissingVersionID(t *testing.T) {
	s := New()
	ctx := context.Background()
	begin, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:  "claim-6",
		IdempotencyKey: "idem-6",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	if _, err := s.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	}); err != nil {
		t.Fatalf("CommitCandidate: %v", err)
	}
	if _, err := s.GetVersionSchema(ctx, &genv1.GetVersionSchemaRequest{
		ClaimHandleId: "claim-6",
		VersionId:     "v-missing",
	}); err == nil {
		t.Fatal("GetVersionSchema: expected error on missing version_id")
	}
}

// TestRequiredFields covers the per-RPC required-field gates.
func TestRequiredFields(t *testing.T) {
	s := New()
	ctx := context.Background()
	cases := []struct {
		name string
		fn   func() error
	}{
		{"BeginCandidate.claim_handle_id", func() error {
			_, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{IdempotencyKey: "k"})
			return err
		}},
		{"BeginCandidate.idempotency_key", func() error {
			_, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{ClaimHandleId: "c"})
			return err
		}},
		{"CommitCandidate.candidate_handle", func() error {
			_, err := s.CommitCandidate(ctx, &genv1.CommitCandidateRequest{})
			return err
		}},
		{"AbandonCandidate.candidate_handle", func() error {
			_, err := s.AbandonCandidate(ctx, &genv1.AbandonCandidateRequest{})
			return err
		}},
		{"ListVersions.claim_handle_id", func() error {
			_, err := s.ListVersions(ctx, &genv1.ListVersionsRequest{})
			return err
		}},
		{"ListPartitions.claim_handle_id", func() error {
			_, err := s.ListPartitions(ctx, &genv1.ListPartitionsRequest{})
			return err
		}},
		{"GetVersionSchema.claim_handle_id", func() error {
			_, err := s.GetVersionSchema(ctx, &genv1.GetVersionSchemaRequest{})
			return err
		}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if err := c.fn(); err == nil {
				t.Errorf("%s: expected error, got nil", c.name)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
