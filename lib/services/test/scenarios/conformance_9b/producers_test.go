// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance9b

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func stagedAsyncCaps() *genv1.CapabilitiesResponse {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
		},
	}
}

func scopeFor(selector string) []byte {
	if selector == "" {
		return []byte("scope:default")
	}
	return []byte("scope:" + selector)
}

func acquiredFor(req *genv1.OpenRequest) *genv1.OpenResponse {
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				Address:                []byte(req.GetClaimId()),
				ClaimScope:             scopeFor(req.GetSelector()),
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
			},
		},
	}
}

type honestProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func (p *honestProducer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return stagedAsyncCaps(), nil
}

func (p *honestProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	return acquiredFor(req), nil
}

func (p *honestProducer) Commit(_ context.Context, _ *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	return &genv1.CommitResponse{}, nil
}

func (p *honestProducer) Abandon(_ context.Context, _ *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (p *honestProducer) Release(_ context.Context, _ *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	return &genv1.ReleaseResponse{}, nil
}

type dishonestProducer struct {
	genv1.UnimplementedClaimProducerServer

	mu sync.Mutex
	gates map[string]*writerGate
}

type writerGate struct {
	scope    string
	released chan struct{}
}

func newDishonestProducer() *dishonestProducer {
	return &dishonestProducer{gates: map[string]*writerGate{}}
}

func (p *dishonestProducer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return stagedAsyncCaps(), nil
}

func (p *dishonestProducer) openWriterGateFor(scope string) chan struct{} {
	for _, g := range p.gates {
		if g.scope == scope {
			return g.released
		}
	}
	return nil
}

func (p *dishonestProducer) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	scope := string(scopeFor(req.GetSelector()))

	if req.GetIntent() == "rw" {
		p.mu.Lock()
		p.gates[req.GetClaimId()] = &writerGate{
			scope:    scope,
			released: make(chan struct{}),
		}
		p.mu.Unlock()
		return acquiredFor(req), nil
	}

	p.mu.Lock()
	gate := p.openWriterGateFor(scope)
	p.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return acquiredFor(req), nil
}

func (p *dishonestProducer) releaseWriter(claimID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g, ok := p.gates[claimID]; ok {
		close(g.released)
		delete(p.gates, claimID)
	}
}

func (p *dishonestProducer) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	p.releaseWriter(req.GetClaimId())
	return &genv1.CommitResponse{}, nil
}

func (p *dishonestProducer) Abandon(_ context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	p.releaseWriter(req.GetClaimId())
	return &genv1.AbandonResponse{}, nil
}

func (p *dishonestProducer) Release(_ context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	p.releaseWriter(req.GetClaimId())
	return &genv1.ReleaseResponse{}, nil
}

func startProducer(t *testing.T, srvImpl genv1.ClaimProducerServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, srvImpl)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}
