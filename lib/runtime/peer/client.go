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
)

// Client is a remote-gRPC implementation of the rimsky-side
// ClaimProducer interface. One Client per registered producer
// (operator-chosen name in rimsky.yml). Per spec §2.
type Client struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.ClaimProducerClient
	caps claimproducer.Capabilities
}

// Compile-time interface check.
var _ locks.ClaimProducer = (*Client)(nil)

// Name returns the operator-configured producer name supplied at Dial.
func (c *Client) Name() string { return c.name }

// Capabilities returns the cached capability struct populated by Dial's
// startup handshake. Returns the cached value without making another
// RPC; rimsky calls Capabilities exactly once per producer-service per
// process at startup.
func (c *Client) Capabilities(_ context.Context) (claimproducer.Capabilities, error) {
	return c.caps, nil
}

// Open RPCs to the remote producer. Maps the OpenResponse oneof to
// OpenOutcome: Acquired → {Available: true, Result: ...};
// Unavailable → {Available: false}. Producer-side faults flow as
// gRPC errors and are surfaced to the caller.
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
		// Carry the producer-declared acquisition-failure class (when the
		// producer named one) so the rimsky-side routing keys the operator's
		// `error_types:` chain on it rather than only the synthetic
		// "acquire/unavailable". Empty when the producer named no class.
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

// Commit RPCs to the remote producer and returns the response body.
// The base-protocol CommitResponse fields are honored, not discarded:
// version_id flows to the claim-handle row and producer_metadata to
// the fan-out parent's writeback (both wired by the unified resolution
// engine in runtime). Inert in rimsky per @blessed-invariant 20.
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

// Abandon RPCs to the remote producer. address may be nil when Open's
// response was lost — the producer identifies state by claim_id.
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

// Release RPCs to the remote producer.
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

// SplitScope RPCs to the remote producer. Per spec §Fan-out template
// DSL — used inside the rimsky-side acquisition tx for fan-out nodes.
// Producers that do not advertise SupportsSplitScope return
// ErrSplitScopeUnsupported; rimsky validates at registration so this
// path is normally unreachable.
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
		return false, NewProducerCallError(c.name, "ScopesConflict", err)
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

// writeSemanticsFromProto maps the proto enum to the Go-side string
// constant. Returns WriteSemanticsUnknown for the proto zero value.
func writeSemanticsFromProto(ws genv1.WriteSemantics) claimproducer.WriteSemantics {
	switch ws {
	case genv1.WriteSemantics_WRITE_SEMANTICS_SYNC:
		return claimproducer.WriteSemanticsSync
	case genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC:
		return claimproducer.WriteSemanticsStagedAsync
	case genv1.WriteSemantics_WRITE_SEMANTICS_BLOCKING_ASYNC:
		return claimproducer.WriteSemanticsBlockingAsync
	case genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY:
		return claimproducer.WriteSemanticsReadOnly
	default:
		return claimproducer.WriteSemanticsUnknown
	}
}
