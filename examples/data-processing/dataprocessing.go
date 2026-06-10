// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package main is a minimal, copy-and-modify DataProcessing service: a mix-in
// protocol a ClaimProducer implementation advertises when it materializes
// content (Parquet files, PostGIS tables, etc.) against partitioned writes. The
// supervisor calls BeginCandidate at fan-out leaf dispatch time and
// CommitCandidate / AbandonCandidate at leaf terminal.
//
// This example advertises a sample capability set and implements the candidate
// lifecycle (BeginCandidate → CommitCandidate) against an in-memory candidate
// map.
//
// It is NOT a test double and NOT a deployable service. Copy this directory,
// rename the module in go.mod, and replace the in-memory map with whatever
// stages and finalizes your materializations.
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

// DataProcessing implements genv1.DataProcessingServer with an in-memory
// candidate + version registry. Embedding the generated Unimplemented server
// keeps it forward-compatible.
//
// The example tracks two pieces of state:
//
//   - `candidates` — a staging map keyed by idempotency_key, holding the
//     opaque candidate_handle bytes returned to rimsky from BeginCandidate.
//     CommitCandidate moves the entry out of staging into `versions`;
//     AbandonCandidate drops it.
//   - `versions` — committed versions, keyed by `claim_handle_id`. Each
//     entry records the version_id rimsky's CommitCandidate response named,
//     the timestamp at commit, opaque producer metadata, and a per-version
//     partition list and schema. ListVersions / ListPartitions /
//     GetVersionSchema read from this map.
//
// State is cleared on Abandon and on a process restart. A real producer
// would persist versions durably (Postgres, the object store's own
// metadata table, etc.); the example keeps things in memory so the
// copy-and-modify surface stays minimal.
type DataProcessing struct {
	genv1.UnimplementedDataProcessingServer

	mu         sync.Mutex
	candidates map[string]*candidate
	versions   map[string][]*versionRecord // claim_handle_id → versions in commit order

	// versionSeq is incremented monotonically to mint a unique
	// version_id per Commit. A real producer would generate something
	// the upstream consumer can navigate (e.g. an Iceberg snapshot ID
	// or an object-store version tag); the example keeps it simple.
	versionSeq atomic.Uint64

	// commitCount / abandonCount are exposed via the testable accessors
	// CommitCount / AbandonCount so the cross-stack proof can assert the
	// verbs really landed on the producer's handler (the
	// "CommitCandidate is called but the producer's effect is canned"
	// falsifier fails if these counters don't grow against a real
	// dispatch).
	commitCount  atomic.Uint64
	abandonCount atomic.Uint64
}

// candidate tracks staging-side state until a CommitCandidate /
// AbandonCandidate. claimHandleID is captured at Begin so Commit can
// route the resulting version into the right per-claim version list
// without a second round-trip; subScope is captured so each committed
// version carries the partition key the leaf wrote into.
type candidate struct {
	handle        []byte
	claimHandleID string
	subScope      []byte
}

// versionRecord is the producer-side projection of a finalized
// CommitCandidate. ListVersions/ListPartitions/GetVersionSchema read
// from this row.
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

// CommitCount returns the running total of CommitCandidate calls that
// reached this producer's handler and returned successfully. Used by
// cross-stack proofs to assert the producer's effect is not canned.
func (d *DataProcessing) CommitCount() uint64 { return d.commitCount.Load() }

// AbandonCount returns the running total of AbandonCandidate calls
// that reached this producer's handler and returned successfully.
func (d *DataProcessing) AbandonCount() uint64 { return d.abandonCount.Load() }

// Capabilities advertises the data shapes, materializations, partition kinds,
// and aggregators this producer supports. rimsky caches the result at startup
// and validates a template's data-processing requirements against it; an empty
// set means "I materialize nothing," so a real producer always names at least
// one shape it can write.
func (d *DataProcessing) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{
		DataShapes:       []string{"parquet"},
		Materializations: []string{"partitioned"},
		PartitionKinds:   []string{"date_range"},
		Aggregators:      []string{"union"},
	}, nil
}

// BeginCandidate allocates a staging candidate keyed by idempotency_key and
// returns an opaque candidate_handle rimsky persists on the claim-handle row.
// The handle is the producer's private cursor into the staged write; a real
// producer would stage a unique object-store prefix or staging schema here.
//
// idempotency_key makes Begin replay-safe: a re-Begin under the same key MUST
// return the same handle (rimsky may retry the acquisition tx), so this example
// dedupes on it rather than allocating a fresh staging area per call.
func (d *DataProcessing) BeginCandidate(_ context.Context, req *genv1.BeginCandidateRequest) (*genv1.BeginCandidateResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := req.GetIdempotencyKey()
	if existing, ok := d.candidates[key]; ok {
		return &genv1.BeginCandidateResponse{CandidateHandle: existing.handle}, nil
	}

	// The handle is opaque to rimsky; this example derives it from the claim
	// handle + idempotency key so it is stable and human-legible in logs.
	handle := []byte(fmt.Sprintf("candidate:%s:%s", req.GetClaimHandleId(), key))
	d.candidates[key] = &candidate{
		handle:        handle,
		claimHandleID: req.GetClaimHandleId(),
		// Copy the sub-scope so callers may reuse their request buffer.
		subScope: append([]byte(nil), req.GetSubScopeDescriptor()...),
	}
	return &genv1.BeginCandidateResponse{CandidateHandle: handle}, nil
}

// CommitCandidate finalizes the candidate at leaf-run success and returns
// opaque-to-rimsky candidate metadata (e.g. row count, byte size) that surfaces
// via the parent's writeback. A real producer would atomically swap its staged
// write into the canonical view here. Committing an unknown handle is a
// FailedPrecondition — rimsky never invented the handle.
//
// On commit, the staged candidate is moved into the per-claim version list
// (keyed by the candidate's `claim_handle_id`), with a fresh monotonic
// version_id, the wall-clock commit time, the producer metadata, a
// single-partition descriptor recording the sub-scope the leaf wrote, and
// an illustrative JSON schema. ListVersions / ListPartitions /
// GetVersionSchema read from this list — that path is the load-bearing
// observable for STORY-data-processing-author's "version history surfacing"
// acceptance.
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
	// The example surfaces one partition per committed candidate, keyed by
	// the sub-scope the leaf received from rimsky. A real producer
	// materializing N partitions per Commit would surface N descriptors
	// here; the shape of the list is the same.
	partition := &genv1.PartitionDescriptor{
		PartitionKey:      string(cand.subScope),
		PartitionMetadata: []byte(fmt.Sprintf(`{"sub_scope":%q}`, string(cand.subScope))),
	}
	// An illustrative JSON schema describing the row layout the parquet
	// candidate wrote. Opaque to rimsky.
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

// AbandonCandidate discards a staged candidate at leaf-run failure or
// strict-cancel-siblings cancellation — the producer GCs its staged write
// without finalizing it. Abandoning an unknown handle is idempotent (the
// candidate is already gone), so it returns success rather than erroring; this
// keeps the failure path safe under supervisor retries.
func (d *DataProcessing) AbandonCandidate(_ context.Context, req *genv1.AbandonCandidateRequest) (*emptypb.Empty, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if key, ok := d.findKeyByHandle(req.GetCandidateHandle()); ok {
		delete(d.candidates, key)
		d.abandonCount.Add(1)
	}
	return &emptypb.Empty{}, nil
}

// ListVersions returns the finalized version records for a claim handle in
// commit order. An unknown claim_handle_id returns an empty list (not an
// error) — a dashboard polling before any commit lands should not see an
// error, just an empty history.
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

// ListPartitions returns the partition descriptors for a (claim_handle_id,
// version_id) pair. A missing version is a FailedPrecondition so the caller
// can distinguish "no such version" from "version exists but has no
// partitions" — the former is a wiring error, the latter is a producer
// that emitted nothing.
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

// GetVersionSchema returns the producer-declared schema bytes for a given
// version. A missing version is a FailedPrecondition for the same reason as
// ListPartitions.
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

// findKeyByHandle locates the idempotency key whose staged candidate matches the
// given opaque handle. Callers must hold d.mu.
func (d *DataProcessing) findKeyByHandle(handle []byte) (string, bool) {
	for key, stored := range d.candidates {
		if string(stored.handle) == string(handle) {
			return key, true
		}
	}
	return "", false
}

// findVersion locates a committed version by (claim_handle_id, version_id).
// Callers must hold d.mu.
func (d *DataProcessing) findVersion(claimHandleID, versionID string) (*versionRecord, bool) {
	for _, r := range d.versions[claimHandleID] {
		if r.versionID == versionID {
			return r, true
		}
	}
	return nil, false
}
