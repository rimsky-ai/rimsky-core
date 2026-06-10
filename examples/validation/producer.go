// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"encoding/json"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Producer is a minimal ClaimProducer companion to the Validation
// service. Its only job is to host the Capabilities handshake that
// advertises the `validation` mix-in alongside its primary protocol —
// rimsky reads ValidationSupportedRoles from this response and uses it
// to decide which roles the Validation service is willing to validate.
//
// A real service that needs validation alongside a substantive primary
// protocol (a true claim-producer fronting a store, an executor running
// real work, a publisher emitting messages) would advertise the
// validation mix-in from THAT primary protocol's Capabilities. This
// example chooses claim-producer because:
//
//   - rimsky's startup wiring only reads `validation_supported_roles`
//     from the claim-producer Capabilities (see
//     `lib/control/config/publishers.go::DialPublisherAndValidationRegistries`),
//     so a claim-producer-hosted mix-in is the path the validation
//     pipeline actually exercises end-to-end today;
//   - the Open / Commit / Abandon / Release lifecycle is small enough
//     to fit a self-contained example without obscuring the validation
//     story.
//
// Copy this directory, replace the body of Validate with your own
// validator, and either keep this minimal producer (if your service is
// just a validator) or merge the Validation server into your existing
// primary-protocol binary.
type Producer struct {
	genv1.UnimplementedClaimProducerServer
}

func newProducer() *Producer { return &Producer{} }

// Capabilities advertises this service as a read-only claim-producer
// that also implements the `validation` mix-in for the executor and
// claim-producer roles. The two arms map to the two role checks the
// rimsky validation pipeline performs at template registration:
//
//   - "executor": runExecutorRoleCheck dispatches when a template node
//     references this peer as its executor.
//   - "claim_producer": runClaimProducerRoleChecks dispatches when a
//     template node carries a `stores:` binding to this peer.
//
// The example's cross-stack proof exercises the claim-producer arm
// (the role bindings carry per-claim selectors the validator can route
// on); the executor arm is exercised by the in-process
// validation_test.go.
func (p *Producer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY,
		},
		Protocols: []string{"validation"},
		ValidationSupportedRoles: []string{
			"executor",
			"claim_producer",
		},
	}, nil
}

// Open returns a trivially-acquired claim. The address is a JSON-quoted
// claim_id — opaque to rimsky (`@blessed-invariant 20`) but persisted
// as `json.RawMessage` on the rimsky side, so the bytes MUST be a
// syntactically-valid JSON value. A producer fronting a real store
// would encode something structurally meaningful (a path, row-key,
// staging-dir descriptor).
func (p *Producer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
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

// Commit / Abandon / Release are no-op acknowledgements for this
// read-only validator-companion producer. A real producer would settle
// or release backing-store state here.

func (p *Producer) Commit(_ context.Context, _ *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	return &genv1.CommitResponse{}, nil
}

func (p *Producer) Abandon(_ context.Context, _ *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (p *Producer) Release(_ context.Context, _ *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	return &genv1.ReleaseResponse{}, nil
}
