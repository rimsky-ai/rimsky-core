// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package server adapts the atomic-staging Store to the rimsky
// ClaimProducer gRPC interface. The four verbs + Capabilities are
// implemented; this binary does not register a LifecycleSubscriber.
package server

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/store"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Server implements the gRPC ClaimProducerServer.
type Server struct {
	genv1.UnimplementedClaimProducerServer
	Store *store.Store
}

// New constructs a Server wrapping the given Store.
func New(st *store.Store) *Server {
	return &Server{Store: st}
}

// Capabilities returns the producer's advertised capability struct.
// This producer supports staged_async only — the staging area gives
// downstream verifiers something to inspect before Commit fires.
func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
		},
	}, nil
}

// Open creates the staging area for the (scope, claim_id) pair and
// returns its path as the claim address. The scope is the selector
// passed by the template (after substitution); rimsky compares scopes
// byte-equal across claims to detect conflicts.
func (s *Server) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if req.GetClaimId() == "" {
		return nil, fmt.Errorf("atomic-staging.Open: missing claim_id")
	}
	if req.GetSelector() == "" {
		return nil, fmt.Errorf("atomic-staging.Open: missing selector")
	}
	entry, err := s.Store.Open(req.GetClaimId(), req.GetSelector())
	if err != nil {
		return nil, err
	}
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				Address:                []byte(entry.StagingPath),
				ClaimScope:             []byte(req.GetSelector()),
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
			},
		},
	}, nil
}

// Commit fires the two-rename atomic swap.
func (s *Server) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.Store.Commit(req.GetClaimId()); err != nil {
		return nil, err
	}
	return &genv1.CommitResponse{}, nil
}

// Abandon drops the staging area without firing the swap.
func (s *Server) Abandon(_ context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.Store.Abandon(req.GetClaimId()); err != nil {
		return nil, err
	}
	return &genv1.AbandonResponse{}, nil
}

// Release. For `r` claims this is a no-op; for `rw` claims that never
// committed it equates to Abandon. The supervisor passes the intent
// in the request so the producer can route accordingly.
func (s *Server) Release(_ context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	// The proto's ReleaseRequest doesn't carry intent today; producers
	// that need it must track it from Open. This simple example treats
	// every Release as a no-op (it's safe for `r` and idempotent for
	// already-cleaned `rw`).
	_ = req
	return &genv1.ReleaseResponse{}, nil
}
