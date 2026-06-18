// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"encoding/json"
	"sync"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Producer struct {
	genv1.UnimplementedClaimProducerServer

	mu                sync.Mutex
	capabilitiesCalls int
	openCalls         int
	commitCalls       int
	abandonCalls      int
	releaseCalls      int
	commitClaimIDs    []string
	abandonClaimIDs   []string
	releaseClaimIDs   []string
}

func newProducer() *Producer {
	return &Producer{}
}

type CallCounts struct {
	Capabilities int
	Open         int
	Commit       int
	Abandon      int
	Release      int
}

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

func (p *Producer) CommitClaimIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.commitClaimIDs))
	copy(out, p.commitClaimIDs)
	return out
}

func (p *Producer) AbandonClaimIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.abandonClaimIDs))
	copy(out, p.abandonClaimIDs)
	return out
}

func (p *Producer) ReleaseClaimIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.releaseClaimIDs))
	copy(out, p.releaseClaimIDs)
	return out
}

func (p *Producer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	p.mu.Lock()
	p.capabilitiesCalls++
	p.mu.Unlock()
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY},
	}, nil
}

func (p *Producer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	p.mu.Lock()
	p.openCalls++
	p.mu.Unlock()
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
