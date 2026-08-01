// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package peer

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	bridge "github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

func UnaryDialOptions(name, tlsMode string) []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithTransportCredentials(TransportCredentials(tlsMode)),
		grpc.WithChainUnaryInterceptor(ServiceNameUnaryInterceptor, TLSModeUnaryInterceptor(name, tlsMode)),
	}
}

func dialOptions(name, tlsMode string) []grpc.DialOption {
	return append(UnaryDialOptions(name, tlsMode),
		grpc.WithChainStreamInterceptor(ServiceNameStreamInterceptor, TLSModeStreamInterceptor(name, tlsMode)))
}

func capabilityDialOptions(name, tlsMode string) []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithTransportCredentials(TransportCredentials(tlsMode)),
		grpc.WithUnaryInterceptor(TLSModeUnaryInterceptor(name, tlsMode)),
	}
}

func Dial(ctx context.Context, name, endpoint, tlsMode string) (*Client, error) {
	target, err := StripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, dialOptions(name, tlsMode)...)
	if err != nil {
		return nil, fmt.Errorf("remote producer %q: dial %q: %w", name, endpoint, err)
	}
	rpc := genv1.NewClaimProducerClient(conn)
	resp, err := rpc.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("remote producer %q: Capabilities handshake: %w", name, err)
	}
	caps, err := bridge.ClaimProducerCapabilitiesFromProto(resp)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("remote producer %q: %w", name, err)
	}
	return &Client{
		name: name,
		conn: conn,
		rpc:  rpc,
		caps: caps,
	}, nil
}

func DialLifecycle(_ context.Context, name, endpoint, tlsMode string) (*LifecycleClient, error) {
	target, err := StripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, dialOptions(name, tlsMode)...)
	if err != nil {
		return nil, fmt.Errorf("lifecycle subscriber %q: dial %q: %w", name, endpoint, err)
	}
	return &LifecycleClient{
		name: name,
		conn: conn,
		rpc:  genv1.NewLifecycleSubscriberClient(conn),
	}, nil
}

func StripScheme(name, endpoint string) (string, error) {
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
