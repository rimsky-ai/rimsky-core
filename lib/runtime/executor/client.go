// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// Client wraps a generated gRPC ExecutorClient for rimsky's supervisor.
// One Client per (transport, TLS mode, endpoint). Cached inside the
// supervisor so connections are reused across dispatches.
//
// @concept: executor (the in-repo Go-side surface of the Executor.Execute
// wire protocol; reference executor impls are not part of this repo)
type Client interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest) (EventStream, error)
	Close() error
}

// EventStream abstracts gRPC streaming + HTTP-bridge newline-delimited
// JSON so the supervisor loop is transport-agnostic.
type EventStream interface {
	Recv() (*genv1.ExecuteEvent, error) // returns io.EOF when stream ends
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
		// Stamp x-rimsky-service-name from the per-call context so a
		// host-agent-proxy fronting the executor protocol can route by
		// service name. No-op when the dispatch site set no name.
		// The TLSMode interceptors annotate RPC errors with the peer +
		// mode under tls: required (no-op otherwise).
		grpc.WithChainUnaryInterceptor(peer.ServiceNameUnaryInterceptor, peer.TLSModeUnaryInterceptor(endpoint.URL, endpoint.TLS)),
		grpc.WithChainStreamInterceptor(peer.ServiceNameStreamInterceptor, peer.TLSModeStreamInterceptor(endpoint.URL, endpoint.TLS)),
	)
	if err != nil {
		return nil, fmt.Errorf("executor.NewGRPCClient: dial %s: %w", endpoint.URL, err)
	}
	return &grpcClient{conn: conn, api: genv1.NewExecutorClient(conn)}, nil
}

func (c *grpcClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (EventStream, error) {
	s, err := c.api.Execute(ctx, req)
	if err != nil {
		return nil, err
	}
	return &grpcEventStream{s: s}, nil
}
func (c *grpcClient) Close() error { return c.conn.Close() }

type grpcEventStream struct {
	s genv1.Executor_ExecuteClient
}

func (e *grpcEventStream) Recv() (*genv1.ExecuteEvent, error) {
	ev, err := e.s.Recv()
	if err == io.EOF {
		return nil, io.EOF
	}
	return ev, err
}
func (e *grpcEventStream) Close() error { return nil }

// ClientPool caches Clients by (transport, TLS mode, URL). Thread-safe.
// The TLS mode is part of the key so two entries sharing a URL with
// different `tls:` modes never silently share one client — a
// `tls: required` entry must never ride a plaintext connection created
// for a `tls: off` twin (STORY-peer-tls-enforced falsifier).
type ClientPool struct {
	mu      sync.Mutex
	clients map[string]Client
}

func NewClientPool() *ClientPool { return &ClientPool{clients: map[string]Client{}} }

func (p *ClientPool) GetOrCreate(ep Endpoint) (Client, error) {
	// Normalize the empty mode to "off" — the two dial identically
	// (plaintext), so keying them separately would mint a redundant
	// second connection to the same endpoint. Unreachable via config
	// (parseTLSMode normalizes), but ad-hoc Endpoint literals reach
	// this pool directly.
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
