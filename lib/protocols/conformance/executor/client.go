// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Endpoint struct {
	Transport string
	URL       string
	TLS       string
}

type Client interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error)
	Close() error
}

type DeclaredTagsClient interface {
	DeclaredTags(ctx context.Context) ([]string, error)
}

type grpcClient struct {
	conn *grpc.ClientConn
	api  genv1.ExecutorClient
}

func NewGRPCClient(endpoint Endpoint) (Client, error) {
	if endpoint.Transport != "grpc" {
		return nil, fmt.Errorf("conformance.NewGRPCClient: transport=%q not grpc", endpoint.Transport)
	}
	target := stripScheme(endpoint.URL)
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(transportCredsFor(endpoint.TLS)))
	if err != nil {
		return nil, fmt.Errorf("conformance.NewGRPCClient: dial %s: %w", endpoint.URL, err)
	}
	return &grpcClient{conn: conn, api: genv1.NewExecutorClient(conn)}, nil
}

func (c *grpcClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return c.api.Execute(ctx, req)
}

func (c *grpcClient) DeclaredTags(ctx context.Context) ([]string, error) {
	obs := genv1.NewExecutorObservabilityClient(c.conn)
	caps, err := obs.Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
			return nil, nil
		}
		return nil, err
	}
	return caps.GetDeclaredTags(), nil
}

func transportCredsFor(tlsMode string) credentials.TransportCredentials {
	if tlsMode == "required" {
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	return insecure.NewCredentials()
}

func (c *grpcClient) Close() error { return c.conn.Close() }

type ClientPool struct {
	mu      sync.Mutex
	clients map[string]Client
}

func NewClientPool() *ClientPool { return &ClientPool{clients: map[string]Client{}} }

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

func (p *ClientPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		_ = c.Close()
	}
	p.clients = map[string]Client{}
	return nil
}
