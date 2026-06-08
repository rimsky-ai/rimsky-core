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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// DataProcessing implements genv1.DataProcessingServer with an in-memory
// candidate registry. Embedding the generated Unimplemented server keeps it
// forward-compatible.
type DataProcessing struct {
	genv1.UnimplementedDataProcessingServer

	mu         sync.Mutex
	candidates map[string][]byte
}

func newDataProcessing() *DataProcessing {
	return &DataProcessing{candidates: map[string][]byte{}}
}

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
		return &genv1.BeginCandidateResponse{CandidateHandle: existing}, nil
	}

	// The handle is opaque to rimsky; this example derives it from the claim
	// handle + idempotency key so it is stable and human-legible in logs.
	handle := []byte(fmt.Sprintf("candidate:%s:%s", req.GetClaimHandleId(), key))
	d.candidates[key] = handle
	return &genv1.BeginCandidateResponse{CandidateHandle: handle}, nil
}

// CommitCandidate finalizes the candidate at leaf-run success and returns
// opaque-to-rimsky candidate metadata (e.g. row count, byte size) that surfaces
// via the parent's writeback. A real producer would atomically swap its staged
// write into the canonical view here. Committing an unknown handle is a
// FailedPrecondition — rimsky never invented the handle.
func (d *DataProcessing) CommitCandidate(_ context.Context, req *genv1.CommitCandidateRequest) (*genv1.CommitCandidateResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key, ok := d.findKeyByHandle(req.GetCandidateHandle())
	if !ok {
		return nil, status.Errorf(codes.FailedPrecondition, "commit: unknown candidate handle")
	}
	delete(d.candidates, key)

	// Opaque-to-rimsky producer-side metadata about the finalized candidate.
	return &genv1.CommitCandidateResponse{
		CandidateMetadata: []byte(`{"shape":"parquet","status":"committed"}`),
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
	}
	return &emptypb.Empty{}, nil
}

// findKeyByHandle locates the idempotency key whose staged candidate matches the
// given opaque handle. Callers must hold d.mu.
func (d *DataProcessing) findKeyByHandle(handle []byte) (string, bool) {
	for key, stored := range d.candidates {
		if string(stored) == string(handle) {
			return key, true
		}
	}
	return "", false
}
