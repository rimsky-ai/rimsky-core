// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// client.go declares the minimal Executor wire-client surface the
// conformance suite needs to dial a peer executor over gRPC or the
// HTTP+JSON bridge. The types are intentionally local to the
// conformance library — rimsky's supervisor uses an equivalent
// (`pkg:runtime/executor`) for production dispatch, but the protocols
// module cannot import that package (`protocols-purity` denies it).
// The two surfaces are kept in semantic
// lockstep; divergence is recorded with `@diverged: true`.
//
// External Go service authors invoking the conformance library from
// their own tests construct an Endpoint, build a Client via
// NewClientPool, and pass it into Run. The conformance runner reaches
// the wire through the generated stubs at `pkg:protocols/proto/v1/gen`
// directly.

package conformance

import (
	"context"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// Endpoint identifies one peer executor's wire address.
//
// @source: runtime/executor/resolver.go::Endpoint
type Endpoint struct {
	Transport string // "grpc" | "http"
	URL       string
	TLS       string // "off" | "optional" | "required"
}

// Client wraps a generated gRPC ExecutorClient.
//
// @source: runtime/executor/client.go::Client
type Client interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest) (EventStream, error)
	Close() error
}

// EventStream abstracts gRPC streaming + HTTP-bridge newline-delimited
// JSON so scenarios are transport-agnostic.
//
// @source: runtime/executor/client.go::EventStream
type EventStream interface {
	Recv() (*genv1.ExecuteEvent, error) // returns io.EOF when stream ends
	Close() error
}

type grpcClient struct {
	conn *grpc.ClientConn
	api  genv1.ExecutorClient
}

// NewGRPCClient dials endpoint over gRPC. v1 ships with insecure
// credentials by default.
//
// @source: runtime/executor/client.go::NewGRPCClient
func NewGRPCClient(endpoint Endpoint) (Client, error) {
	if endpoint.Transport != "grpc" {
		return nil, fmt.Errorf("conformance.NewGRPCClient: transport=%q not grpc", endpoint.Transport)
	}
	conn, err := grpc.NewClient(endpoint.URL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("conformance.NewGRPCClient: dial %s: %w", endpoint.URL, err)
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

// ClientPool caches Clients by (transport, URL). Thread-safe.
//
// @source: runtime/executor/client.go::ClientPool
type ClientPool struct {
	mu      sync.Mutex
	clients map[string]Client
}

// NewClientPool constructs an empty pool.
func NewClientPool() *ClientPool { return &ClientPool{clients: map[string]Client{}} }

// GetOrCreate returns the cached Client for ep or constructs one.
// HTTP transport is not yet supported in the conformance client;
// the production rimsky surface (`runtime/executor`) ships a bridge
// for the HTTP+JSON wire and external service authors who need it can
// dial via their own helper.
func (p *ClientPool) GetOrCreate(ep Endpoint) (Client, error) {
	key := ep.Transport + "://" + ep.URL
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[key]; ok {
		return c, nil
	}
	switch ep.Transport {
	case "grpc":
		c, err := NewGRPCClient(ep)
		if err != nil {
			return nil, err
		}
		p.clients[key] = c
		return c, nil
	default:
		return nil, fmt.Errorf("conformance.ClientPool: unknown transport %q (protocols conformance supports grpc only; use rimsky-internal runtime/executor for HTTP bridges)", ep.Transport)
	}
}

// Close shuts down every cached Client.
func (p *ClientPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		_ = c.Close()
	}
	p.clients = map[string]Client{}
	return nil
}
