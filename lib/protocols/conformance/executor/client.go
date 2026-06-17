// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// client.go declares the minimal Executor wire-client surface the
// conformance suite needs to dial a peer executor over gRPC. Per
// TD-execute-rpc-unary the RPC returns the settling Outcome directly;
// the conformance client wraps that with no transport-specific
// indirection.

package conformance

import (
	"context"
	"crypto/tls"
	"fmt"
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
	Transport string // @constraint: "grpc"
	URL       string
	// @constraint: TLS is the dial mode — "off" (plaintext, the default) or
	// "required" (verified TLS against system roots).
	TLS string
}

// Client wraps a generated gRPC ExecutorClient.
//
// @source: lib/runtime/executor/client.go::Client
type Client interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error)
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
// dependency budget (no lib/runtime import).
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

func (c *grpcClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return c.api.Execute(ctx, req)
}

// transportCredsFor maps an Endpoint.TLS mode to gRPC transport
// credentials: "required" dials verified TLS against system roots;
// anything else (including "" and "off") dials plaintext.
func transportCredsFor(tlsMode string) credentials.TransportCredentials {
	if tlsMode == "required" {
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	return insecure.NewCredentials()
}

func (c *grpcClient) Close() error { return c.conn.Close() }

// ClientPool caches Clients by (transport, TLS mode, URL). Thread-safe.
//
// @source: lib/runtime/executor/client.go::ClientPool
type ClientPool struct {
	mu      sync.Mutex
	clients map[string]Client
}

// NewClientPool constructs an empty pool.
func NewClientPool() *ClientPool { return &ClientPool{clients: map[string]Client{}} }

// GetOrCreate returns the cached Client for ep or constructs one.
func (p *ClientPool) GetOrCreate(ep Endpoint) (Client, error) {
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
		return nil, fmt.Errorf("conformance.ClientPool: unknown transport %q (grpc only)", ep.Transport)
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
