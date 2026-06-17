// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// Client wraps a generated gRPC ExecutorClient for rimsky's supervisor.
// One Client per (transport, TLS mode, endpoint). Cached inside the
// supervisor so connections are reused across dispatches.
//
// Per concept:executor / TD-execute-rpc-unary the Execute RPC is
// unary: a single call returns the settling Outcome (Success / Error /
// Park) or AwaitAsyncCallback. The HTTP-bridge client wraps the same
// shape over an HTTP POST.
//
// @concept: executor
type Client interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error)
	Close() error
}

type grpcClient struct {
	conn *grpc.ClientConn
	api  genv1.ExecutorClient
}

// NewGRPCClient dials the endpoint, honoring the entry's validated
// `tls:` mode: "required" → verified TLS (system roots); "off" / empty
// → plaintext. Under required, Execute-channel failures name the peer
// (by endpoint URL — the pool may share one client across executor
// names) and the mode.
func NewGRPCClient(endpoint Endpoint) (Client, error) {
	if endpoint.Transport != "grpc" {
		return nil, fmt.Errorf("executor.NewGRPCClient: transport=%q not grpc", endpoint.Transport)
	}
	conn, err := grpc.NewClient(endpoint.URL,
		grpc.WithTransportCredentials(peer.TransportCredentials(endpoint.TLS)),
		// @deliberate: stamp x-rimsky-service-name from the per-call
		// context so a host-agent-proxy fronting the executor protocol can
		// route by service name (no-op when the dispatch site set no
		// name); the TLSMode interceptors annotate RPC errors with the
		// peer + mode under tls: required (no-op otherwise).
		grpc.WithChainUnaryInterceptor(peer.ServiceNameUnaryInterceptor, peer.TLSModeUnaryInterceptor(endpoint.URL, endpoint.TLS)),
	)
	if err != nil {
		return nil, fmt.Errorf("executor.NewGRPCClient: dial %s: %w", endpoint.URL, err)
	}
	return &grpcClient{conn: conn, api: genv1.NewExecutorClient(conn)}, nil
}

func (c *grpcClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return c.api.Execute(ctx, req)
}
func (c *grpcClient) Close() error { return c.conn.Close() }

// ClientPool caches Clients by (transport, TLS mode, URL). Thread-safe.
// The TLS mode is part of the key so two entries sharing a URL with
// different `tls:` modes never silently share one client — a
// `tls: required` entry must never ride a plaintext connection created
// for a `tls: off` twin (STORY-peer-tls-enforced falsifier).
//
// The optional inproc registry + newHctx hook are populated via
// NewClientPoolWithInProcess; out-of-process-only callers (tests of
// http/grpc paths, the conformance pool) continue to use NewClientPool.
type ClientPool struct {
	mu       sync.Mutex
	clients  map[string]Client
	registry *InProcessRegistry
	newHctx  HandlerContextFactory
}

func NewClientPool() *ClientPool { return &ClientPool{clients: map[string]Client{}} }

// NewClientPoolWithInProcess returns a pool with the inproc registry
// wired. Production startup uses this; tests using only out-of-process
// executors keep NewClientPool().
func NewClientPoolWithInProcess(registry *InProcessRegistry, newHctx HandlerContextFactory) *ClientPool {
	return &ClientPool{
		clients:  map[string]Client{},
		registry: registry,
		newHctx:  newHctx,
	}
}

func (p *ClientPool) GetOrCreate(ep Endpoint) (Client, error) {
	// @deliberate: normalize the empty mode to "off" — the two dial
	// identically (plaintext), so keying them separately would mint a
	// redundant second connection to the same endpoint; unreachable via
	// config (parseTLSMode normalizes), but ad-hoc Endpoint literals
	// reach this pool directly.
	tlsMode := ep.TLS
	if tlsMode == "" {
		tlsMode = "off"
	}
	key := ep.Transport + "|" + tlsMode + "|" + ep.URL
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[key]; ok {
		return c, nil
	}
	var c Client
	var err error
	switch ep.Transport {
	case "grpc":
		c, err = NewGRPCClient(ep)
	case "http":
		c, err = NewHTTPClient(ep)
	case "inproc":
		if p.registry == nil {
			return nil, fmt.Errorf("ClientPool: inproc transport requested but registry is nil")
		}
		c, err = NewInProcessClient(ep, p.registry, p.newHctx)
	default:
		return nil, fmt.Errorf("ClientPool: unknown transport %q", ep.Transport)
	}
	if err != nil {
		return nil, err
	}
	p.clients[key] = c
	return c, nil
}

func (p *ClientPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		_ = c.Close()
	}
	p.clients = map[string]Client{}
	return nil
}
