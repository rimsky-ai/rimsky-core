// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claimproducer

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	protocol "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// @concept: claim-producer
// @decision: parallel-inproc-claim-producer-registry
type Client struct {
	name    string
	caps    protocol.Capabilities
	handler protocol.ClaimProducer
}

var _ locks.ClaimProducer = (*Client)(nil)

func (c *Client) Name() string { return c.name }

func (c *Client) Capabilities(_ context.Context) (protocol.Capabilities, error) {
	return c.caps, nil
}

func (c *Client) Open(ctx context.Context, claimID protocol.ClaimID, spec protocol.ClaimSpec) (protocol.OpenOutcome, error) {
	out, err := c.handler.Open(ctx, claimID, spec)
	if err != nil {
		return protocol.OpenOutcome{}, callError(c.name, "Open", err)
	}
	if !out.Available {
		return out, nil
	}
	rws := out.Result.RealizedWriteSemantics
	if rws == protocol.WriteSemanticsUnknown {
		return protocol.OpenOutcome{}, fmt.Errorf("in-process producer %q: Open: realized_write_semantics is UNKNOWN (producer must declare a concrete value)", c.name)
	}
	if !c.caps.Contains(rws) {
		return protocol.OpenOutcome{}, fmt.Errorf("in-process producer %q: Open: realized_write_semantics %q not in advertised envelope %v", c.name, rws, c.caps.WriteSemanticsAllowed)
	}
	return out, nil
}

func (c *Client) Commit(ctx context.Context, claimID protocol.ClaimID, scope, address []byte) (protocol.CommitResult, error) {
	res, err := c.handler.Commit(ctx, claimID, scope, address)
	if err != nil {
		return protocol.CommitResult{}, callError(c.name, "Commit", err)
	}
	return res, nil
}

func (c *Client) Abandon(ctx context.Context, claimID protocol.ClaimID, scope, address []byte) error {
	if err := c.handler.Abandon(ctx, claimID, scope, address); err != nil {
		return callError(c.name, "Abandon", err)
	}
	return nil
}

func (c *Client) Release(ctx context.Context, claimID protocol.ClaimID, scope, address []byte) error {
	if err := c.handler.Release(ctx, claimID, scope, address); err != nil {
		return callError(c.name, "Release", err)
	}
	return nil
}

func (c *Client) SplitScope(ctx context.Context, req protocol.SplitClaimScopeRequest) (protocol.SplitClaimScopeResponse, error) {
	if !c.caps.SupportsSplitScope {
		return protocol.SplitClaimScopeResponse{}, protocol.ErrSplitScopeUnsupported
	}
	resp, err := c.handler.SplitScope(ctx, req)
	if err != nil {
		return protocol.SplitClaimScopeResponse{}, callError(c.name, "SplitScope", err)
	}
	return resp, nil
}

func (c *Client) ScopesConflict(ctx context.Context, a, b []byte) (bool, error) {
	if !c.caps.SupportsScopesConflict {
		return protocol.ErrScopesConflictUnsupportedFallback(a, b), nil
	}
	conflicts, err := c.handler.ScopesConflict(ctx, a, b)
	if err != nil {
		return false, callError(c.name, "ScopesConflict", err)
	}
	return conflicts, nil
}

func callError(name, method string, err error) *peer.ProducerCallError {
	class := ""
	var classed protocol.ClassedError
	if errors.As(err, &classed) {
		class = classed.ErrorClass()
	}
	return &peer.ProducerCallError{
		ProducerName: name,
		Method:       method,
		ErrorClass:   class,
		Message:      err.Error(),
		Underlying:   err,
	}
}
