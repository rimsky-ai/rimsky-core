// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type HandlerContextFactory func(ctx context.Context, dispatchID, nodeID shared.UUID) HandlerContext

// @concept: executor
type InProcessClient struct {
	registry *InProcessRegistry
	url      string
	newHctx  HandlerContextFactory
}

func NewInProcessClient(endpoint Endpoint, registry *InProcessRegistry, newHctx HandlerContextFactory) (Client, error) {
	if endpoint.Transport != "inproc" {
		return nil, fmt.Errorf("executor.NewInProcessClient: transport=%q not inproc", endpoint.Transport)
	}
	if registry == nil {
		return nil, errors.New("executor.NewInProcessClient: registry required")
	}
	if _, ok := registry.Lookup(endpoint.URL); !ok {
		return nil, fmt.Errorf("executor.NewInProcessClient: no handler registered for %q", endpoint.URL)
	}
	return &InProcessClient{registry: registry, url: endpoint.URL, newHctx: newHctx}, nil
}

// @decision: three-dispatch-deadlines
func (c *InProcessClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	h, ok := c.registry.Lookup(c.url)
	if !ok {
		return nil, fmt.Errorf("InProcessClient.Execute: no handler for %q", c.url)
	}
	dispatchID, err := uuid.Parse(req.DispatchId)
	if err != nil {
		return nil, fmt.Errorf("InProcessClient.Execute: parse dispatch_id %q: %w", req.DispatchId, err)
	}
	nodeID, err := uuid.Parse(req.NodeId)
	if err != nil {
		return nil, fmt.Errorf("InProcessClient.Execute: parse node_id %q: %w", req.NodeId, err)
	}
	hctx := HandlerContext{}
	if c.newHctx != nil {
		hctx = c.newHctx(ctx, shared.UUID(dispatchID), shared.UUID(nodeID))
	}
	var outcome *genv1.Outcome
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("inproc handler panic: %v", p)
			}
		}()
		outcome, err = h.Execute(ctx, req, hctx)
	}()
	if err != nil {
		return nil, err
	}
	return outcome, nil
}

func (c *InProcessClient) Close() error { return nil }
