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
	"crypto/tls"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Endpoint identifies one peer executor's wire address.
//
// @source: lib/runtime/executor/resolver.go::Endpoint
type Endpoint struct {
	Transport string // "grpc" | "http"
	URL       string
	// TLS is the dial mode: "off" (plaintext, the default) or
	// "required" (verified TLS against system roots).
	TLS string
}

// Client wraps a generated gRPC ExecutorClient.
//
// @source: lib/runtime/executor/client.go::Client
type Client interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest) (EventStream, error)
	Close() error
}

// EventStream abstracts gRPC streaming + HTTP-bridge newline-delimited
// JSON so scenarios are transport-agnostic.
//
// @source: lib/runtime/executor/client.go::EventStream
type EventStream interface {
	Recv() (*genv1.ExecuteEvent, error) // returns io.EOF when stream ends
	Close() error
}

type grpcClient struct {
	conn *grpc.ClientConn
	api  genv1.ExecutorClient
}

// NewGRPCClient dials endpoint over gRPC. Plaintext by default;
// Endpoint.TLS "required" dials with verified TLS (system roots).
//
// @source: lib/runtime/executor/client.go::NewGRPCClient
// @diverged: true
// @reason: the conformance harness keeps the protocols module's
// dependency budget (no lib/runtime import), so it maps TLS to
// credentials inline instead of using runtime/peer.TransportCredentials.
func NewGRPCClient(endpoint Endpoint) (Client, error) {
	if endpoint.Transport != "grpc" {
		return nil, fmt.Errorf("conformance.NewGRPCClient: transport=%q not grpc", endpoint.Transport)
	}
	conn, err := grpc.NewClient(endpoint.URL, grpc.WithTransportCredentials(transportCredsFor(endpoint.TLS)))
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

// transportCredsFor maps an Endpoint.TLS mode to gRPC transport
// credentials: "required" dials verified TLS against system roots;
// anything else (including "" and "off") dials plaintext. Shared by
// every dial in this package (Execute suite, observability probe,
// lifecycle probe) so a TLS conformance run cannot split mid-suite
// between encrypted and plaintext checks.
func transportCredsFor(tlsMode string) credentials.TransportCredentials {
	if tlsMode == "required" {
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	return insecure.NewCredentials()
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
// for a `tls: off` twin.
//
// @source: lib/runtime/executor/client.go::ClientPool
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
	// Normalize the empty mode to "off" — the two dial identically
	// (plaintext; see transportCredsFor), so keying them separately
	// would mint a redundant second connection to the same endpoint.
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
