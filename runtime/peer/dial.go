// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package peer

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/fallguyconsulting/rimsky/protocols/claimproducer"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// Dial connects to a remote producer-service over gRPC, performs the
// startup Capabilities() handshake, and returns a Client that satisfies
// the rimsky-side locks.ClaimProducer interface.
//
// Endpoint may carry a "grpc://" prefix (the convention used in
// rimsky.yml); the prefix is stripped before passing to grpc.NewClient.
//
// Insecure credentials are used by default. Per spec auth is a
// deployment-layer concern (mTLS, service mesh, IAM); mTLS support is a
// follow-up cycle.
//
// On any failure (unreachable, capability RPC error, timeout), Dial
// returns the error without leaking a partial Client. Callers should
// pass a context with a deadline so a non-responsive producer-service
// cannot block startup forever.
func Dial(ctx context.Context, name, endpoint string) (*Client, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// Stamp x-rimsky-service-name from the per-call context so a
		// host-agent-proxy fronting the claim-producer protocol can route
		// by service name. No-op when the dispatch site set no name.
		grpc.WithUnaryInterceptor(ServiceNameUnaryInterceptor),
		grpc.WithStreamInterceptor(ServiceNameStreamInterceptor),
	)
	if err != nil {
		return nil, fmt.Errorf("remote producer %q: dial %q: %w", name, endpoint, err)
	}
	rpc := genv1.NewClaimProducerClient(conn)
	resp, err := rpc.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("remote producer %q: Capabilities handshake: %w", name, err)
	}
	envelope := make([]claimproducer.WriteSemantics, 0, len(resp.GetWriteSemanticsAllowed()))
	for _, ws := range resp.GetWriteSemanticsAllowed() {
		mapped := writeSemanticsFromProto(ws)
		if mapped == claimproducer.WriteSemanticsUnknown {
			_ = conn.Close()
			return nil, fmt.Errorf("remote producer %q: Capabilities advertises UNKNOWN write_semantics value", name)
		}
		envelope = append(envelope, mapped)
	}
	if len(envelope) == 0 {
		_ = conn.Close()
		return nil, fmt.Errorf("remote producer %q: Capabilities returned empty write_semantics_allowed", name)
	}
	return &Client{
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

// DialLifecycle connects to a peer that implements the
// LifecycleSubscriber service. Unlike Dial, no startup handshake is
// performed (the LifecycleSubscriber service has no Capabilities verb);
// the dial succeeds as long as the gRPC channel comes up and the caller
// is responsible for catching unimplemented errors when the first
// lifecycle event fires.
func DialLifecycle(_ context.Context, name, endpoint string) (*LifecycleClient, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	// TODO(host-agent-proxy v2): install ServiceName interceptor here when this protocol gains late-bind support
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("lifecycle subscriber %q: dial %q: %w", name, endpoint, err)
	}
	return &LifecycleClient{
		name: name,
		conn: conn,
		rpc:  genv1.NewLifecycleSubscriberClient(conn),
	}, nil
}

func stripScheme(name, endpoint string) (string, error) {
	if endpoint == "" {
		return "", fmt.Errorf("remote peer %q: endpoint is required", name)
	}
	for _, badScheme := range []string{"http://", "https://", "tcp://", "unix://"} {
		if strings.HasPrefix(endpoint, badScheme) {
			return "", fmt.Errorf("remote peer %q: endpoint scheme must be grpc:// (got %s)", name, badScheme)
		}
	}
	return strings.TrimPrefix(endpoint, "grpc://"), nil
}
