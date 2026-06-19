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
	resp, err := c.rpc.Open(ctx, &genv1.OpenRequest{
		ClaimId:      string(claimID),
		ProducerName: spec.ProducerName,
		Selector:     spec.Selector,
		Intent:       string(spec.Intent),
		Alias:        spec.Alias,
		TemplateId:   spec.TemplateID,
		InstanceId:   spec.InstanceID,
		RunScopeId:   spec.RunScopeID,
	})
	if err != nil {
		return claimproducer.OpenOutcome{}, NewProducerCallError(c.name, "Open", err)
	}
	if u := resp.GetUnavailable(); u != nil {
		return claimproducer.OpenOutcome{Available: false, UnavailableClass: u.GetErrorClass()}, nil
	}
	acq := resp.GetAcquired()
	if acq == nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("remote producer %q: Open: response carries neither Acquired nor Unavailable", c.name)
	}
	rws := writeSemanticsFromProto(acq.GetRealizedWriteSemantics())
	if rws == claimproducer.WriteSemanticsUnknown {
		return claimproducer.OpenOutcome{}, fmt.Errorf("remote producer %q: Open: realized_write_semantics is UNKNOWN (producer must declare a concrete value)", c.name)
	}
	if !c.caps.Contains(rws) {
		return claimproducer.OpenOutcome{}, fmt.Errorf("remote producer %q: Open: realized_write_semantics %q not in advertised envelope %v", c.name, rws, c.caps.WriteSemanticsAllowed)
	}
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                acq.GetAddress(),
			Payload:                acq.GetPayload(),
			ClaimScope:             acq.GetClaimScope(),
			RealizedWriteSemantics: rws,
		},
	}, nil
}

func (c *Client) Commit(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte) (claimproducer.CommitResult, error) {
	resp, err := c.rpc.Commit(ctx, &genv1.CommitRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	if err != nil {
		return claimproducer.CommitResult{}, NewProducerCallError(c.name, "Commit", err)
	}
	return claimproducer.CommitResult{
		VersionID:        resp.GetVersionId(),
		ProducerMetadata: resp.GetProducerMetadata(),
	}, nil
}

func (c *Client) Abandon(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte) error {
	_, err := c.rpc.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	if err != nil {
		return NewProducerCallError(c.name, "Abandon", err)
	}
	return nil
}

func (c *Client) Release(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte) error {
	_, err := c.rpc.Release(ctx, &genv1.ReleaseRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	if err != nil {
		return NewProducerCallError(c.name, "Release", err)
	}
	return nil
}

func (c *Client) SplitScope(ctx context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	if !c.caps.SupportsSplitScope {
		return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
	}
	resp, err := c.rpc.SplitScope(ctx, &genv1.SplitScopeRequest{
		ClaimHandleId:    req.ClaimHandleID,
		PartitionRequest: req.PartitionRequest,
	})
	if err != nil {
		return claimproducer.SplitClaimScopeResponse{}, NewProducerCallError(c.name, "SplitScope", err)
	}
	out := claimproducer.SplitClaimScopeResponse{}
	for _, sub := range resp.GetSubScopes() {
		out.SubClaimScopes = append(out.SubClaimScopes, claimproducer.SubClaimScopeDescriptor{
			ClaimScopeData:   sub.GetClaimScopeData(),
			PartitionKey:     sub.GetPartitionKey(),
			ProducerMetadata: sub.GetProducerMetadata(),
			Address:          sub.GetAddress(),
			Payload:          sub.GetPayload(),
		})
	}
	return out, nil
}

func (c *Client) ScopesConflict(ctx context.Context, a, b []byte) (bool, error) {
	if !c.caps.SupportsScopesConflict {
		return claimproducer.ErrScopesConflictUnsupportedFallback(a, b), nil
	}
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

func writeSemanticsFromProto(ws genv1.WriteSemantics) claimproducer.WriteSemantics {
	return claimproducer.WriteSemantics(bridge.WriteSemanticsFromProto(ws))
}
