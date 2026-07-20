// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peer

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	bridge "github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

type Client struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.ClaimProducerClient
	caps claimproducer.Capabilities
}

var _ locks.ClaimProducer = (*Client)(nil)

func (c *Client) Name() string { return c.name }

func (c *Client) Capabilities(_ context.Context) (claimproducer.Capabilities, error) {
	return c.caps, nil
}

func (c *Client) Open(ctx context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	resp, err := c.rpc.Open(ctx, bridge.OpenRequestFromSpec(claimID, spec))
	if err != nil {
		return claimproducer.OpenOutcome{}, NewProducerCallError(c.name, "Open", err)
	}
	out, err := bridge.OpenOutcomeFromProto(resp)
	if err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("remote producer %q: %w", c.name, err)
	}
	if !out.Available {
		return out, nil
	}
	rws := out.Result.RealizedWriteSemantics
	if rws == claimproducer.WriteSemanticsUnknown {
		return claimproducer.OpenOutcome{}, fmt.Errorf("remote producer %q: Open: realized_write_semantics is UNKNOWN (producer must declare a concrete value)", c.name)
	}
	if !c.caps.Contains(rws) {
		return claimproducer.OpenOutcome{}, fmt.Errorf("remote producer %q: Open: realized_write_semantics %q not in advertised envelope %v", c.name, rws, c.caps.WriteSemanticsAllowed)
	}
	return out, nil
}

func (c *Client) Commit(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) (claimproducer.CommitResult, error) {
	resp, err := c.rpc.Commit(ctx, bridge.CommitRequestFromArgs(claimID, scope, address, leaseToken))
	if err != nil {
		return claimproducer.CommitResult{}, NewProducerCallError(c.name, "Commit", err)
	}
	return bridge.CommitResultFromProto(resp), nil
}

func (c *Client) Abandon(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	_, err := c.rpc.Abandon(ctx, bridge.AbandonRequestFromArgs(claimID, scope, address, leaseToken))
	if err != nil {
		return NewProducerCallError(c.name, "Abandon", err)
	}
	return nil
}

func (c *Client) Release(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	_, err := c.rpc.Release(ctx, bridge.ReleaseRequestFromArgs(claimID, scope, address, leaseToken))
	if err != nil {
		return NewProducerCallError(c.name, "Release", err)
	}
	return nil
}

func (c *Client) SplitScope(ctx context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	if !c.caps.SupportsSplitScope {
		return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
	}
	return c.SplitScopeWire(ctx, req)
}

func (c *Client) ScopesConflict(ctx context.Context, a, b []byte) (bool, error) {
	if !c.caps.SupportsScopesConflict {
		return claimproducer.ErrScopesConflictUnsupportedFallback(a, b), nil
	}
	return c.ScopesConflictWire(ctx, a, b)
}

func (c *Client) SplitScopeWire(ctx context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	resp, err := c.rpc.SplitScope(ctx, bridge.SplitScopeRequestToProto(req))
	if err != nil {
		return claimproducer.SplitClaimScopeResponse{}, NewProducerCallError(c.name, "SplitScope", err)
	}
	return bridge.SplitScopeResponseFromProto(resp), nil
}

func (c *Client) ScopesConflictWire(ctx context.Context, a, b []byte) (bool, error) {
	resp, err := c.rpc.ScopesConflict(ctx, &genv1.ClaimScopesConflictRequest{
		ClaimScopeA: a,
		ClaimScopeB: b,
	})
	if err != nil {
		return false, NewProducerCallError(c.name, "ScopesConflict", err)
	}
	return resp.GetConflicts(), nil
}

func (c *Client) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func (c *Client) ValidateCapabilities(declared claimproducer.Capabilities) error {
	if len(declared.WriteSemanticsAllowed) == 0 {
		return fmt.Errorf("remote producer %q: operator-declared write_semantics_allowed is empty", c.name)
	}
	for _, want := range declared.WriteSemanticsAllowed {
		if !c.caps.Contains(want) {
			return fmt.Errorf("remote producer %q: capabilities mismatch — operator declared %v, producer advertised %v",
				c.name, declared.WriteSemanticsAllowed, c.caps.WriteSemanticsAllowed)
		}
	}
	return nil
}
