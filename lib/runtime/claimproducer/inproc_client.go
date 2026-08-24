// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claimproducer

import (
	"context"
	"errors"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	protocol "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
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
	return c.caps.EnforceOpenWriteSemantics("in-process producer", c.name, out)
}

func (c *Client) Commit(ctx context.Context, claimID protocol.ClaimID, scope, address []byte, leaseToken string) (protocol.CommitResult, error) {
	res, err := c.handler.Commit(ctx, claimID, scope, address, leaseToken)
	if err != nil {
		return protocol.CommitResult{}, callError(c.name, "Commit", err)
	}
	return res, nil
}

func (c *Client) Abandon(ctx context.Context, claimID protocol.ClaimID, scope, address []byte, leaseToken string) error {
	if err := c.handler.Abandon(ctx, claimID, scope, address, leaseToken); err != nil {
		return callError(c.name, "Abandon", err)
	}
	return nil
}

func (c *Client) Release(ctx context.Context, claimID protocol.ClaimID, scope, address []byte, leaseToken string) error {
	if err := c.handler.Release(ctx, claimID, scope, address, leaseToken); err != nil {
		return callError(c.name, "Release", err)
	}
	return nil
}

func (c *Client) SplitScope(ctx context.Context, req protocol.SplitClaimScopeRequest) (protocol.SplitClaimScopeResponse, error) {
	if err := service.GateSplitScope(c.caps); err != nil {
		return protocol.SplitClaimScopeResponse{}, err
	}
	resp, err := c.handler.SplitScope(ctx, req)
	if err != nil {
		return protocol.SplitClaimScopeResponse{}, callError(c.name, "SplitScope", err)
	}
	return resp, nil
}

func (c *Client) ScopesConflict(ctx context.Context, a, b []byte) (bool, error) {
	if fallback, gated := service.GateScopesConflict(c.caps, a, b); gated {
		return fallback, nil
	}
	conflicts, err := c.handler.ScopesConflict(ctx, a, b)
	if err != nil {
		return false, callError(c.name, "ScopesConflict", err)
	}
	return conflicts, nil
}

func callError(name, method string, err error) *service.ProducerCallError {
	var classed protocol.ClassedError
	if errors.As(err, &classed) {
		return &service.ProducerCallError{
			ProducerName: name,
			Method:       method,
			ErrorClass:   classed.ErrorClass(),
			Message:      err.Error(),
			Underlying:   err,
		}
	}
	return service.NewProducerCallError(name, method, err)
}
