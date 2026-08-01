// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/store"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Server struct {
	genv1.UnimplementedClaimProducerServer
	Store *store.Store
}

func New(st *store.Store) *Server {
	return &Server{Store: st}
}

func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
		},
	}, nil
}

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

func (s *Server) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.Store.Commit(req.GetClaimId()); err != nil {
		return nil, err
	}
	return &genv1.CommitResponse{}, nil
}

func (s *Server) Abandon(_ context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.Store.Abandon(req.GetClaimId()); err != nil {
		return nil, err
	}
	return &genv1.AbandonResponse{}, nil
}

func (s *Server) Release(_ context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.Store.Release(req.GetClaimId()); err != nil {
		return nil, err
	}
	return &genv1.ReleaseResponse{}, nil
}
