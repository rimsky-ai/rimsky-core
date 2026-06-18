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

// @source: lib/runtime/peer/dial.go::Dial
// @diverged: true
// @reason: consumption-side-isolation bars runtime/peer; the
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

type ClaimProducerClient struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.ClaimProducerClient
	caps claimproducer.Capabilities
}

func (c *ClaimProducerClient) Close() error { return c.conn.Close() }

func (c *ClaimProducerClient) Name() string { return c.name }

func (c *ClaimProducerClient) Capabilities(_ context.Context) (claimproducer.Capabilities, error) {
	return c.caps, nil
}

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

func (c *ClaimProducerClient) Abandon(ctx context.Context, claimID claimproducer.ClaimID, scope []byte, address []byte) error {
	_, err := c.rpc.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	return err
}

func (c *ClaimProducerClient) Release(ctx context.Context, claimID claimproducer.ClaimID, scope []byte, address []byte) error {
	_, err := c.rpc.Release(ctx, &genv1.ReleaseRequest{
		ClaimId:    string(claimID),
		ClaimScope: scope,
		Address:    address,
	})
	return err
}

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
