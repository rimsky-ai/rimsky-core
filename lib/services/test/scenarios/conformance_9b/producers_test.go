// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package conformance9b stands up two in-test `staged_async`-advertising
// claim-producer gRPC servers — one honest, one dishonest — so the
// claim-producer conformance suite can be driven against them over the
// wire and asked whether it detects the reader-lease serialization
// pattern that @blessed-invariant 9b forbids for staged_async.
//
// The honest producer simulates snapshot delegation: every Open returns
// promptly, including a reader Open issued while a writer claim is open
// on the byte-equal scope. The dishonest producer holds a per-scope
// "writer open" gate for the writer's claim lifetime, so a reader Open
// on the same scope blocks until the writer's claim is terminal-ed
// (Release / Commit / Abandon) — exactly the lock-shaped internal
// serialization 9b forbids.
//
// Both producers run as real gRPC servers (mirroring
// examples/claimproducer/main.go) so the probe drives them through the
// same wire path a deployed producer uses.
package conformance9b

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// stagedAsyncCaps is the Capabilities both producers advertise: a single
// staged_async write-semantics entry, no optional RPCs. This is what
// gates the 9b probe to actually run (the probe SKIPs unless the
// producer advertises staged_async).
func stagedAsyncCaps() *genv1.CapabilitiesResponse {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
		},
	}
}

// scopeFor derives a deterministic claim-scope from the producer's
// selector so two Opens with a byte-equal selector yield byte-equal
// ClaimScope bytes — the precondition the conformance suite's
// uniformity check needs, and the byte-equal scope the 9b probe drives
// a writer + two readers against.
func scopeFor(selector string) []byte {
	if selector == "" {
		return []byte("scope:default")
	}
	return []byte("scope:" + selector)
}

// acquiredFor builds the Acquired arm of an OpenResponse for a request,
// advertising staged_async as the realized write-semantics so the
// realized value is a member of the advertised envelope (the
// conformance suite rejects a realized value outside the envelope).
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

// honestProducer simulates snapshot delegation: it holds no internal
// lock across a claim's lifetime, so every Open returns promptly —
// including a reader Open issued against an open writer on the same
// scope. This is the honest staged_async shape 9b requires.
type honestProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func (p *honestProducer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return stagedAsyncCaps(), nil
}

func (p *honestProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	// @deliberate: snapshot delegation — no gate, no wait. Every intent
	// returns immediately, including a reader Open against an open
	// writer; this is the honest staged_async shape @blessed-invariant 9b
	// requires.
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

// dishonestProducer implements the reader-lease serialization pattern
// 9b forbids: while a writer claim is open on a scope, a reader Open on
// the byte-equal scope BLOCKS until the writer's claim is terminal-ed.
//
// The gate is a per-scope channel closed when the writer terminal-s.
// A writer Open (IntentReadWrite) installs an open gate keyed by scope;
// a reader Open (IntentRead) that finds an open gate for its scope waits
// on the gate's channel before returning. Commit / Abandon / Release on
// the writer's claim closes the gate and unblocks waiting readers.
//
// This is precisely the internal lock-shaped serialization a probe must
// detect: the producer answers "is anyone holding a writer on X?"
// itself, instead of delegating to a snapshot / MVCC pass-through, so
// reader and writer are serialized rather than coexisting.
type dishonestProducer struct {
	genv1.UnimplementedClaimProducerServer

	mu sync.Mutex
	// gates maps a writer claim_id to its scope + release channel. A
	// reader Open on a scope with an open writer waits on that channel.
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

// openWriterGateFor returns the release channel of any open writer gate
// for the given scope, or nil if no writer is currently open on it.
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

	// @deliberate: a writer Open installs an open gate keyed by its
	// claim_id and returns promptly; the gate is what a subsequent
	// reader Open on the same scope will park on below, materializing
	// the lock-shaped serialization @blessed-invariant 9b forbids.
	if req.GetIntent() == "rw" {
		p.mu.Lock()
		p.gates[req.GetClaimId()] = &writerGate{
			scope:    scope,
			released: make(chan struct{}),
		}
		p.mu.Unlock()
		return acquiredFor(req), nil
	}

	// @deliberate: a reader Open blocks while a writer is open on the
	// byte-equal scope — the reader-lease serialization
	// @blessed-invariant 9b forbids for staged_async. It returns only
	// once the writer terminal-s (gate closed) or the call's context is
	// cancelled. The probe under test must detect this shape.
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

// releaseWriter closes (and removes) any open gate for the given
// claim_id, unblocking readers parked on that scope.
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

// startProducer stands the given ClaimProducer server up on a fresh
// loopback gRPC listener and returns its dial endpoint. The server is
// stopped when the test finishes. Each producer gets its own port so
// the two can run concurrently (mirroring examples/claimproducer/main.go,
// but on an ephemeral port rather than the example's fixed 9400).
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
