// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package remote

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/protocols/claimproducer"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// Client is a remote-gRPC implementation of the rimsky-side
// ClaimProducer interface. One Client per registered producer
// (operator-chosen name in rimsky.yml). Per spec §2.
type Client struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.ClaimProducerClient
	caps locks.Capabilities
}

// Compile-time interface check.
var _ locks.ClaimProducer = (*Client)(nil)

// Name returns the operator-configured producer name supplied at Dial.
func (c *Client) Name() string { return c.name }

// Capabilities returns the cached capability struct populated by Dial's
// startup handshake. Returns the cached value without making another
// RPC; rimsky calls Capabilities exactly once per producer-service per
// process at startup.
func (c *Client) Capabilities(_ context.Context) (locks.Capabilities, error) {
	return c.caps, nil
}

// Open RPCs to the remote producer. Maps the OpenResponse oneof to
// OpenOutcome: Acquired → {Available: true, Result: ...};
// Unavailable → {Available: false}. Producer-side faults flow as
// gRPC errors and are surfaced to the caller.
func (c *Client) Open(ctx context.Context, claimID locks.ClaimID, spec locks.ClaimSpec) (locks.OpenOutcome, error) {
	resp, err := c.rpc.Open(ctx, &genv1.OpenRequest{
		ClaimId:      string(claimID),
		ProducerName: spec.ProducerName,
		Selector:     spec.Selector,
		Intent:       string(spec.Intent),
		Alias:        spec.Alias,
		TemplateId:   spec.TemplateID,
		InstanceId:   spec.InstanceID,
	})
	if err != nil {
		return locks.OpenOutcome{}, fmt.Errorf("remote producer %q: Open: %w", c.name, err)
	}
	if u := resp.GetUnavailable(); u != nil {
		return locks.OpenOutcome{Available: false}, nil
	}
	acq := resp.GetAcquired()
	if acq == nil {
		return locks.OpenOutcome{}, fmt.Errorf("remote producer %q: Open: response carries neither Acquired nor Unavailable", c.name)
	}
	rws := writeSemanticsFromProto(acq.GetRealizedWriteSemantics())
	if rws == locks.WriteSemanticsUnknown {
		return locks.OpenOutcome{}, fmt.Errorf("remote producer %q: Open: realized_write_semantics is UNKNOWN (producer must declare a concrete value)", c.name)
	}
	if !c.caps.Contains(rws) {
		return locks.OpenOutcome{}, fmt.Errorf("remote producer %q: Open: realized_write_semantics %q not in advertised envelope %v", c.name, rws, c.caps.WriteSemanticsAllowed)
	}
	return locks.OpenOutcome{
		Available: true,
		Result: locks.ClaimResult{
			Address:                acq.GetAddress(),
			Payload:                acq.GetPayload(),
			ClaimScope:             acq.GetClaimScope(),
			RealizedWriteSemantics: rws,
		},
	}, nil
}

// Commit RPCs to the remote producer.
func (c *Client) Commit(ctx context.Context, claimID locks.ClaimID, scope, address []byte) error {
	_, err := c.rpc.Commit(ctx, &genv1.CommitRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	if err != nil {
		return fmt.Errorf("remote producer %q: Commit: %w", c.name, err)
	}
	return nil
}

// Abandon RPCs to the remote producer. address may be nil when Open's
// response was lost — the producer identifies state by claim_id.
func (c *Client) Abandon(ctx context.Context, claimID locks.ClaimID, scope, address []byte) error {
	_, err := c.rpc.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	if err != nil {
		return fmt.Errorf("remote producer %q: Abandon: %w", c.name, err)
	}
	return nil
}

// Release RPCs to the remote producer.
func (c *Client) Release(ctx context.Context, claimID locks.ClaimID, scope, address []byte) error {
	_, err := c.rpc.Release(ctx, &genv1.ReleaseRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	if err != nil {
		return fmt.Errorf("remote producer %q: Release: %w", c.name, err)
	}
	return nil
}

// SplitScope RPCs to the remote producer. Per spec §Fan-out template
// DSL — used inside the rimsky-side acquisition tx for fan-out nodes.
// Producers that do not advertise SupportsSplitScope return
// ErrSplitScopeUnsupported; rimsky validates at registration so this
// path is normally unreachable.
func (c *Client) SplitScope(ctx context.Context, req locks.SplitClaimScopeRequest) (locks.SplitClaimScopeResponse, error) {
	if !c.caps.SupportsSplitScope {
		return locks.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
	}
	resp, err := c.rpc.SplitScope(ctx, &genv1.SplitScopeRequest{
		ClaimHandleId:    req.ClaimHandleID,
		PartitionRequest: req.PartitionRequest,
	})
	if err != nil {
		return locks.SplitClaimScopeResponse{}, fmt.Errorf("remote producer %q: SplitScope: %w", c.name, err)
	}
	out := locks.SplitClaimScopeResponse{}
	for _, sub := range resp.GetSubScopes() {
		out.SubClaimScopes = append(out.SubClaimScopes, locks.SubClaimScopeDescriptor{
			ClaimScopeData:   sub.GetClaimScopeData(),
			PartitionKey:     sub.GetPartitionKey(),
			ProducerMetadata: sub.GetProducerMetadata(),
		})
	}
	return out, nil
}

// ScopesConflict RPCs to the remote producer. Per @blessed-invariant
// 4b: when SupportsScopesConflict is false rimsky uses byte-equal as
// the trivial default; callers should consult the cached Capabilities
// rather than relying on this method's fallback.
func (c *Client) ScopesConflict(ctx context.Context, a, b []byte) (bool, error) {
	if !c.caps.SupportsScopesConflict {
		return claimproducer.ErrScopesConflictUnsupportedFallback(a, b), nil
	}
	resp, err := c.rpc.ScopesConflict(ctx, &genv1.ClaimScopesConflictRequest{
		ClaimScopeA: a,
		ClaimScopeB: b,
	})
	if err != nil {
		return false, fmt.Errorf("remote producer %q: ScopesConflict: %w", c.name, err)
	}
	return resp.GetConflicts(), nil
}

// Close releases the gRPC connection. Called by Registry.Close on
// shutdown.
func (c *Client) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// ValidateCapabilities compares the cached capability struct against
// the operator-declared envelope. The operator-declared envelope MUST
// be a non-empty subset of the producer-advertised envelope.
func (c *Client) ValidateCapabilities(declared locks.Capabilities) error {
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

// writeSemanticsFromProto maps the proto enum to the Go-side string
// constant. Returns WriteSemanticsUnknown for the proto zero value.
func writeSemanticsFromProto(ws genv1.WriteSemantics) locks.WriteSemantics {
	switch ws {
	case genv1.WriteSemantics_WRITE_SEMANTICS_SYNC:
		return locks.WriteSemanticsSync
	case genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC:
		return locks.WriteSemanticsStagedAsync
	case genv1.WriteSemantics_WRITE_SEMANTICS_BLOCKING_ASYNC:
		return locks.WriteSemanticsBlockingAsync
	case genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY:
		return locks.WriteSemanticsReadOnly
	default:
		return locks.WriteSemanticsUnknown
	}
}
