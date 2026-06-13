// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// DialClaimProducer dials a peer claim-producer over gRPC and returns
// an adapter that satisfies the
// `pkg:protocols/claimproducer.ClaimProducer` Go interface — the form
// the protocols conformance runner consumes.
//
// The adapter mirrors rimsky-internal `pkg:runtime/peer.Dial` —
// reaching for that package directly is barred by the
// `consumption-side-isolation` depguard. The duplicate lives here
// (rather than in `pkg:protocols`) because the protocols module does
// not currently publish a gRPC ClaimProducer client; the conformance
// runner takes the Go interface, leaving callers to adapt their own
// wire client.
//
// `endpoint` may carry an optional "grpc://" prefix (the convention
// used in rimsky.yml); the prefix is stripped before dial.
//
// The caller is responsible for calling Close on the returned adapter
// to release the gRPC channel.
//
// @source: runtime/peer/dial.go::Dial
// @diverged: true
// @reason: consumption-side-isolation bars runtime/peer; the
// lib/services test harness owns its own wire-adapter copy until the
// protocols module publishes one.
func DialClaimProducer(ctx context.Context, name, endpoint string) (*ClaimProducerClient, error) {
	target := strings.TrimPrefix(endpoint, "grpc://")
	if target == "" {
		return nil, fmt.Errorf("dial %q: empty endpoint", name)
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %q: %w", name, err)
	}
	rpc := genv1.NewClaimProducerClient(conn)
	resp, err := rpc.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("dial %q: Capabilities: %w", name, err)
	}
	envelope := make([]claimproducer.WriteSemantics, 0, len(resp.GetWriteSemanticsAllowed()))
	for _, ws := range resp.GetWriteSemanticsAllowed() {
		mapped := writeSemanticsFromProto(ws)
		if mapped == claimproducer.WriteSemanticsUnknown {
			_ = conn.Close()
			return nil, fmt.Errorf("dial %q: Capabilities advertises UNKNOWN write_semantics", name)
		}
		envelope = append(envelope, mapped)
	}
	return &ClaimProducerClient{
		name: name,
		conn: conn,
		rpc:  rpc,
		caps: claimproducer.Capabilities{
			WriteSemanticsAllowed:    envelope,
			SupportsSplitScope:       resp.GetSupportsSplitScope(),
			SupportsScopesConflict:   resp.GetSupportsScopesConflict(),
			Protocols:                resp.GetProtocols(),
			ValidationSupportedRoles: resp.GetValidationSupportedRoles(),
		},
	}, nil
}

// ClaimProducerClient is a gRPC-backed adapter satisfying the Go
// `claimproducer.ClaimProducer` interface.
type ClaimProducerClient struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.ClaimProducerClient
	caps claimproducer.Capabilities
}

// Close releases the underlying gRPC channel.
func (c *ClaimProducerClient) Close() error { return c.conn.Close() }

// Name returns the operator-configured producer name.
func (c *ClaimProducerClient) Name() string { return c.name }

// Capabilities returns the cached startup-handshake capabilities.
func (c *ClaimProducerClient) Capabilities(_ context.Context) (claimproducer.Capabilities, error) {
	return c.caps, nil
}

// Open dispatches to the wire.
func (c *ClaimProducerClient) Open(ctx context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
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
		return claimproducer.OpenOutcome{}, err
	}
	if u := resp.GetUnavailable(); u != nil {
		return claimproducer.OpenOutcome{Available: false}, nil
	}
	acq := resp.GetAcquired()
	if acq == nil {
		return claimproducer.OpenOutcome{}, errors.New("Open: response carries neither Acquired nor Unavailable")
	}
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                acq.GetAddress(),
			Payload:                acq.GetPayload(),
			ClaimScope:             acq.GetClaimScope(),
			RealizedWriteSemantics: writeSemanticsFromProto(acq.GetRealizedWriteSemantics()),
		},
	}, nil
}

// Commit dispatches to the wire and returns the response body fields
// (version_id + producer_metadata) per the base-protocol contract.
func (c *ClaimProducerClient) Commit(ctx context.Context, claimID claimproducer.ClaimID, scope []byte, address []byte) (claimproducer.CommitResult, error) {
	resp, err := c.rpc.Commit(ctx, &genv1.CommitRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	if err != nil {
		return claimproducer.CommitResult{}, err
	}
	return claimproducer.CommitResult{
		VersionID:        resp.GetVersionId(),
		ProducerMetadata: resp.GetProducerMetadata(),
	}, nil
}

// Abandon dispatches to the wire.
func (c *ClaimProducerClient) Abandon(ctx context.Context, claimID claimproducer.ClaimID, scope []byte, address []byte) error {
	_, err := c.rpc.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	return err
}

// Release dispatches to the wire.
func (c *ClaimProducerClient) Release(ctx context.Context, claimID claimproducer.ClaimID, scope []byte, address []byte) error {
	_, err := c.rpc.Release(ctx, &genv1.ReleaseRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	return err
}

// SplitScope dispatches to the wire when the producer advertises
// supports_split_scope. Otherwise returns ErrSplitScopeUnsupported.
func (c *ClaimProducerClient) SplitScope(ctx context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	if !c.caps.SupportsSplitScope {
		return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
	}
	resp, err := c.rpc.SplitScope(ctx, &genv1.SplitScopeRequest{
		ClaimHandleId:    req.ClaimHandleID,
		PartitionRequest: req.PartitionRequest,
	})
	if err != nil {
		return claimproducer.SplitClaimScopeResponse{}, err
	}
	subs := make([]claimproducer.SubClaimScopeDescriptor, 0, len(resp.GetSubScopes()))
	for _, s := range resp.GetSubScopes() {
		subs = append(subs, claimproducer.SubClaimScopeDescriptor{
			ClaimScopeData:   s.GetClaimScopeData(),
			PartitionKey:     s.GetPartitionKey(),
			ProducerMetadata: s.GetProducerMetadata(),
		})
	}
	return claimproducer.SplitClaimScopeResponse{SubClaimScopes: subs}, nil
}

// ScopesConflict dispatches to the wire when the producer advertises
// supports_scopes_conflict. Otherwise returns
// ErrScopesConflictUnsupported per the protocol's documented sentinel.
func (c *ClaimProducerClient) ScopesConflict(ctx context.Context, a, b []byte) (bool, error) {
	if !c.caps.SupportsScopesConflict {
		return false, claimproducer.ErrScopesConflictUnsupported
	}
	resp, err := c.rpc.ScopesConflict(ctx, &genv1.ClaimScopesConflictRequest{
		ClaimScopeA: a,
		ClaimScopeB: b,
	})
	if err != nil {
		return false, err
	}
	return resp.GetConflicts(), nil
}

// writeSemanticsFromProto maps the proto enum to the Go enum.
func writeSemanticsFromProto(ws genv1.WriteSemantics) claimproducer.WriteSemantics {
	switch ws {
	case genv1.WriteSemantics_WRITE_SEMANTICS_SYNC:
		return claimproducer.WriteSemanticsSync
	case genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC:
		return claimproducer.WriteSemanticsStagedAsync
	default:
		return claimproducer.WriteSemanticsUnknown
	}
}
