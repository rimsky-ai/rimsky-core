// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package executor

import (
	"context"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// Client wraps a generated gRPC ExecutorClient for rimsky's supervisor.
// One Client per (transport, endpoint). Cached by endpoint URL inside the
// supervisor so connections are reused across dispatches.
//
// @concept: executor (the in-repo Go-side surface of the Executor.Execute
// wire protocol; reference executor impls are carved out to the
// rimsky-services sibling repo)
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

// NewGRPCClient dials the endpoint. v1 ships with insecure credentials by
// default; TLS support is a post-v1 Plan C concern.
func NewGRPCClient(endpoint Endpoint) (Client, error) {
	if endpoint.Transport != "grpc" {
		return nil, fmt.Errorf("executor.NewGRPCClient: transport=%q not grpc", endpoint.Transport)
	}
	conn, err := grpc.NewClient(endpoint.URL, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

// ClientPool caches Clients by (transport, URL). Thread-safe.
type ClientPool struct {
	mu      sync.Mutex
	clients map[string]Client
}

func NewClientPool() *ClientPool { return &ClientPool{clients: map[string]Client{}} }

func (p *ClientPool) GetOrCreate(ep Endpoint) (Client, error) {
	key := ep.Transport + "://" + ep.URL
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
