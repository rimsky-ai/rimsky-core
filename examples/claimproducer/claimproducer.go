// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package main is a minimal, copy-and-modify ClaimProducer: a read-only
// producer that hands out a claim address on Open and accepts the
// Commit / Abandon / Release lifecycle as no-ops. It shows the wire shape a
// store/queue producer fills in — in particular how to build the OpenResponse
// `result` oneof (Acquired vs. Unavailable).
//
// Copy this directory, rename the module in go.mod, and replace the bodies with
// real acquisition against your backing store. This example advertises only
// READ_ONLY write semantics and does not implement SplitScope / ScopesConflict
// (its Capabilities reports them unsupported, so rimsky never calls them).
package main

import (
	"context"
	"encoding/json"
	"sync"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Producer implements genv1.ClaimProducerServer. Embedding the generated
// Unimplemented server supplies forward-compatible defaults for the RPCs this
// minimal producer does not implement (SplitScope, ScopesConflict).
//
// The per-RPC counters (capabilitiesCalls / openCalls / commitCalls /
// abandonCalls / releaseCalls) exist for the cross-stack proof in
// main_e2e_test.go to assert that rimsky's startup handshake + dispatch +
// terminal pipeline reach the REAL producer handlers (not a stub). A
// production producer would expose these via its own metrics surface, not
// as struct fields; this example keeps them inline to stay copy-and-modify
// simple. Callers use the Calls() helper to read a consistent snapshot.
type Producer struct {
	genv1.UnimplementedClaimProducerServer

	mu                sync.Mutex
	capabilitiesCalls int
	openCalls         int
	commitCalls       int
	abandonCalls      int
	releaseCalls      int
	// commitClaimIDs / abandonClaimIDs / releaseClaimIDs record the
	// claim_id arguments rimsky passed on each terminal verb so the
	// cross-stack proof can correlate "this Commit landed against THIS
	// instance's claim" and not a different one — proof rimsky's terminal
	// dispatch ran the verb with the claim it acquired, not a canned one.
	commitClaimIDs  []string
	abandonClaimIDs []string
	releaseClaimIDs []string
}

// newProducer returns a fresh in-memory Producer instance. Used by
// claimproducer_test.go (in-process gRPC server) and main_e2e_test.go
// (cross-stack proof against a running rimsky stack); the binary entry
// point in main.go constructs one directly via `&Producer{}` for the
// trivial zero-value path.
func newProducer() *Producer {
	return &Producer{}
}

// CallCounts is the snapshot returned by Producer.Calls(). The
// cross-stack proof asserts on these counters across a complete
// instance dispatch: Capabilities grows on startup; Open grows when an
// instance acquires; Commit/Abandon grow when the instance settles
// success/failure; Release grows when a held durable claim is released
// at instance terminate.
type CallCounts struct {
	Capabilities int
	Open         int
	Commit       int
	Abandon      int
	Release      int
}

// Calls returns a consistent snapshot of the per-RPC call counters. Used
// by the cross-stack proof to assert real RPCs reach this server (the
// falsifier "Commit/Abandon/Release are called but the producer's effect
// is canned" is exactly the wrong-counter case). The mutex guard makes
// the read atomic w.r.t. concurrent RPC handlers running on the gRPC
// server.
func (p *Producer) Calls() CallCounts {
	p.mu.Lock()
	defer p.mu.Unlock()
	return CallCounts{
		Capabilities: p.capabilitiesCalls,
		Open:         p.openCalls,
		Commit:       p.commitCalls,
		Abandon:      p.abandonCalls,
		Release:      p.releaseCalls,
	}
}

// CommitClaimIDs returns a defensive copy of the claim_ids that have
// landed on the Commit handler. The cross-stack proof uses this to
// correlate "Commit ran for THIS instance's claim, not a stale or canned
// claim id" — exhibit the producer's effect is not canned.
func (p *Producer) CommitClaimIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.commitClaimIDs))
	copy(out, p.commitClaimIDs)
	return out
}

// AbandonClaimIDs is the Commit-side helper for the Abandon counter.
func (p *Producer) AbandonClaimIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.abandonClaimIDs))
	copy(out, p.abandonClaimIDs)
	return out
}

// ReleaseClaimIDs is the Commit-side helper for the Release counter.
func (p *Producer) ReleaseClaimIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.releaseClaimIDs))
	copy(out, p.releaseClaimIDs)
	return out
}

// Capabilities is the startup handshake. rimsky caches the result and validates
// it against the operator's declared envelope. This producer is read-only and
// implements neither SplitScope nor ScopesConflict, so both stay at their false
// zero value.
func (p *Producer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	p.mu.Lock()
	p.capabilitiesCalls++
	p.mu.Unlock()
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY},
	}, nil
}

// Open returns the producer-supplied address the executor will use on the data
// path. A producer with nothing to give right now returns
// OpenResponse_Unavailable instead (rimsky rolls back the dispatch and may
// retry on the next tick); a producer-side fault returns a gRPC error status,
// not Unavailable.
//
// The Acquired.address bytes are opaque to rimsky (`@blessed-invariant 20`)
// but persisted as `json.RawMessage` on the rimsky side
// (`pkg:lib/foundation/persistence/postgres/claim_handles.go::UpdateAddress`),
// so the bytes a producer emits MUST be a syntactically-valid JSON value —
// rimsky's INSERT will fail with SQLSTATE 22P02 otherwise. The example
// JSON-encodes the claim_id as a quoted string; producers fronting a real
// store typically encode a structured value (e.g. a path, row-key,
// staging-dir descriptor). The producer chooses its own canonical encoding,
// but invalid JSON is not a free choice.
func (p *Producer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	p.mu.Lock()
	p.openCalls++
	p.mu.Unlock()
	// Marshal the claim_id as a JSON-quoted string so the resulting bytes
	// are valid JSON. json.Marshal of a string never returns an error.
	addressJSON, _ := json.Marshal(req.GetClaimId())
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				Address:                addressJSON,
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY,
			},
		},
	}, nil
}

// Commit signals the consumer succeeded; Abandon that it failed; Release that
// the claim is being dropped without a verdict. A read-only producer holds no
// state to settle, so each is a no-op acknowledgement — but the cross-stack
// proof relies on the call landing here at all (the "canned effect"
// falsifier guard), so each handler bumps its counter and records the
// claim_id rimsky passed before returning.

func (p *Producer) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	p.mu.Lock()
	p.commitCalls++
	p.commitClaimIDs = append(p.commitClaimIDs, req.GetClaimId())
	p.mu.Unlock()
	return &genv1.CommitResponse{}, nil
}

func (p *Producer) Abandon(_ context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	p.mu.Lock()
	p.abandonCalls++
	p.abandonClaimIDs = append(p.abandonClaimIDs, req.GetClaimId())
	p.mu.Unlock()
	return &genv1.AbandonResponse{}, nil
}

func (p *Producer) Release(_ context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	p.mu.Lock()
	p.releaseCalls++
	p.releaseClaimIDs = append(p.releaseClaimIDs, req.GetClaimId())
	p.mu.Unlock()
	return &genv1.ReleaseResponse{}, nil
}
