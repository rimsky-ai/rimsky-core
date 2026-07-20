// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// @concept: executor
type Client interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, string, error)
	Close() error
}

type grpcClient struct {
	conn *grpc.ClientConn
	api  genv1.ExecutorClient
}

func NewGRPCClient(endpoint Endpoint) (Client, error) {
	if endpoint.Transport != "grpc" {
		return nil, fmt.Errorf("executor.NewGRPCClient: transport=%q not grpc", endpoint.Transport)
	}
	conn, err := grpc.NewClient(endpoint.URL, peer.UnaryDialOptions(endpoint.URL, endpoint.TLS)...)
	if err != nil {
		return nil, fmt.Errorf("executor.NewGRPCClient: dial %s: %w", endpoint.URL, err)
	}
	return &grpcClient{conn: conn, api: genv1.NewExecutorClient(conn)}, nil
}

func (c *grpcClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, string, error) {
	var pr grpcpeer.Peer
	outcome, err := c.api.Execute(ctx, req, grpc.Peer(&pr))
	if err != nil {
		return nil, "", err
	}
	return outcome, verifiedPeerPrincipal(pr.AuthInfo), nil
}

func verifiedPeerPrincipal(authInfo credentials.AuthInfo) string {
	tlsInfo, ok := authInfo.(credentials.TLSInfo)
	if !ok {
		return ""
	}
	principal, err := pki.PrincipalFromVerifiedChains(&tlsInfo.State)
	if err != nil {
		return ""
	}
	return principal
}

func (c *grpcClient) Close() error { return c.conn.Close() }

type ClientPool struct {
	mu       sync.Mutex
	clients  map[string]Client
	registry *InProcessRegistry
	newHctx  HandlerContextFactory
}

func NewClientPool() *ClientPool { return &ClientPool{clients: map[string]Client{}} }

func NewClientPoolWithInProcess(registry *InProcessRegistry, newHctx HandlerContextFactory) *ClientPool {
	return &ClientPool{
		clients:  map[string]Client{},
		registry: registry,
		newHctx:  newHctx,
	}
}

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
