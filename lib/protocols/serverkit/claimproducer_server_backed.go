// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: claim-producer
type ServerBackedClaimProducer struct {
	name string
	caps claimproducer.Capabilities
	srv  genv1.ClaimProducerServer
}

var _ claimproducer.ClaimProducer = (*ServerBackedClaimProducer)(nil)

func NewServerBackedClaimProducer(ctx context.Context, name string, srv genv1.ClaimProducerServer) (*ServerBackedClaimProducer, error) {
	if name == "" {
		return nil, errors.New("serverkit.NewServerBackedClaimProducer: name is required")
	}
	if srv == nil {
		return nil, fmt.Errorf("serverkit.NewServerBackedClaimProducer: producer %q: server is required", name)
	}
	resp, err := srv.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		return nil, fmt.Errorf("producer %q: Capabilities: %w", name, err)
	}
	caps, err := ClaimProducerCapabilitiesFromProto(resp)
	if err != nil {
		return nil, fmt.Errorf("producer %q: %w", name, err)
	}
	return &ServerBackedClaimProducer{name: name, caps: caps, srv: srv}, nil
}

func (p *ServerBackedClaimProducer) Name() string { return p.name }

func (p *ServerBackedClaimProducer) Capabilities(_ context.Context) (claimproducer.Capabilities, error) {
	return p.caps, nil
}

func (p *ServerBackedClaimProducer) Open(ctx context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	resp, err := p.srv.Open(ctx, OpenRequestFromSpec(claimID, spec))
	if err != nil {
		return claimproducer.OpenOutcome{}, err
	}
	return OpenOutcomeFromProto(resp)
}

func (p *ServerBackedClaimProducer) Commit(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) (claimproducer.CommitResult, error) {
	resp, err := p.srv.Commit(ctx, CommitRequestFromArgs(claimID, scope, address, leaseToken))
	if err != nil {
		return claimproducer.CommitResult{}, err
	}
	return CommitResultFromProto(resp), nil
}

func (p *ServerBackedClaimProducer) Abandon(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	_, err := p.srv.Abandon(ctx, AbandonRequestFromArgs(claimID, scope, address, leaseToken))
	return err
}

func (p *ServerBackedClaimProducer) Release(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	_, err := p.srv.Release(ctx, ReleaseRequestFromArgs(claimID, scope, address, leaseToken))
	return err
}

func (p *ServerBackedClaimProducer) SplitScope(ctx context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	resp, err := p.srv.SplitScope(ctx, SplitScopeRequestToProto(req))
	if err != nil {
		return claimproducer.SplitClaimScopeResponse{}, err
	}
	return SplitScopeResponseFromProto(resp), nil
}

func (p *ServerBackedClaimProducer) ScopesConflict(ctx context.Context, a, b []byte) (bool, error) {
	resp, err := p.srv.ScopesConflict(ctx, &genv1.ClaimScopesConflictRequest{ClaimScopeA: a, ClaimScopeB: b})
	if err != nil {
		return false, err
	}
	return resp.GetConflicts(), nil
}
