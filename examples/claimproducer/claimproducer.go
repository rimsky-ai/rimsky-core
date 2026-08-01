// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Producer struct {
	genv1.UnimplementedClaimProducerServer
}

func newProducer() *Producer {
	return &Producer{}
}

func (p *Producer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY},
	}, nil
}

func (p *Producer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	addressJSON, err := json.Marshal(req.GetClaimId())
	if err != nil {
		return nil, fmt.Errorf("marshal claim_id address: %w", err)
	}
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				Address:                addressJSON,
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY,
			},
		},
	}, nil
}

func (p *Producer) Commit(_ context.Context, _ *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	return &genv1.CommitResponse{}, nil
}

func (p *Producer) Abandon(_ context.Context, _ *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (p *Producer) Release(_ context.Context, _ *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	return &genv1.ReleaseResponse{}, nil
}
