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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Producer implements genv1.ClaimProducerServer. Embedding the generated
// Unimplemented server supplies forward-compatible defaults for the RPCs this
// minimal producer does not implement (SplitScope, ScopesConflict).
type Producer struct {
	genv1.UnimplementedClaimProducerServer
}

// Capabilities is the startup handshake. rimsky caches the result and validates
// it against the operator's declared envelope. This producer is read-only and
// implements neither SplitScope nor ScopesConflict, so both stay at their false
// zero value.
func (p *Producer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY},
	}, nil
}

// Open returns the producer-supplied address the executor will use on the data
// path. A producer with nothing to give right now returns
// OpenResponse_Unavailable instead (rimsky rolls back the dispatch and may
// retry on the next tick); a producer-side fault returns a gRPC error status,
// not Unavailable.
func (p *Producer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				Address:                []byte(req.GetClaimId()),
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY,
			},
		},
	}, nil
}

// Commit signals the consumer succeeded; Abandon that it failed; Release that
// the claim is being dropped without a verdict. A read-only producer holds no
// state to settle, so each is a no-op acknowledgement.

func (p *Producer) Commit(_ context.Context, _ *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	return &genv1.CommitResponse{}, nil
}

func (p *Producer) Abandon(_ context.Context, _ *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (p *Producer) Release(_ context.Context, _ *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	return &genv1.ReleaseResponse{}, nil
}
