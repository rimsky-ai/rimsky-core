// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// service_name_interceptor.go — gRPC client-side interceptors that stamp
// the per-call `x-rimsky-service-name` metadata header onto outbound RPCs
// so a host-agent-proxy fronting the protocol can route by service name.
// Lives in runtime/peer/ — the package nearest the gRPC dial code that
// already owns the supervisor's outbound client plumbing — so both the
// claim-producer dial here and the executor dial in runtime/executor/ can
// install the same interceptor without crossing into the pure-DTO
// clientiface package (which deliberately imports no gRPC).

package peer

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type serviceNameKey struct{}

// WithServiceName returns ctx with the given service name attached
// for the next outbound gRPC call. Returns ctx unchanged when name
// is empty (no-op on irrelevant call paths).
func WithServiceName(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, serviceNameKey{}, name)
}

// ServiceNameUnaryInterceptor stamps x-rimsky-service-name from the
// per-call context onto outgoing unary RPCs. No-op when the context
// carries no service name. Hosted (non-proxy) services ignore the
// header; the host-agent-proxy reads it to route.
func ServiceNameUnaryInterceptor(
	ctx context.Context, method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
) error {
	if name, ok := ctx.Value(serviceNameKey{}).(string); ok && name != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-rimsky-service-name", name)
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

// ServiceNameStreamInterceptor is the streaming-RPC equivalent. The
// interceptor fires once at stream creation and the metadata travels
// in the initial HTTP/2 headers; subsequent stream frames inherit
// the same call context (no per-frame handling needed for
// server-streaming RPCs like Executor.Execute).
func ServiceNameStreamInterceptor(
	ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
	method string, streamer grpc.Streamer, opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	if name, ok := ctx.Value(serviceNameKey{}).(string); ok && name != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-rimsky-service-name", name)
	}
	return streamer(ctx, desc, cc, method, opts...)
}
