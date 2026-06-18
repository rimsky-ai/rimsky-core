// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peer

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func Dial(ctx context.Context, name, endpoint, tlsMode string) (*Client, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(TransportCredentials(tlsMode)),
		grpc.WithChainUnaryInterceptor(ServiceNameUnaryInterceptor, TLSModeUnaryInterceptor(name, tlsMode)),
		grpc.WithChainStreamInterceptor(ServiceNameStreamInterceptor, TLSModeStreamInterceptor(name, tlsMode)),
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
			DeclaredErrorClasses:     resp.GetDeclaredErrorClasses(),
		},
	}, nil
}

func DialLifecycle(_ context.Context, name, endpoint, tlsMode string) (*LifecycleClient, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(TransportCredentials(tlsMode)),
		grpc.WithUnaryInterceptor(TLSModeUnaryInterceptor(name, tlsMode)),
		grpc.WithStreamInterceptor(TLSModeStreamInterceptor(name, tlsMode)),
	)
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
