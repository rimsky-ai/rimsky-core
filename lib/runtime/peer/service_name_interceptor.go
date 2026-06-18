// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peer

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type serviceNameKey struct{}

func WithServiceName(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, serviceNameKey{}, name)
}

func ServiceNameUnaryInterceptor(
	ctx context.Context, method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
) error {
	if name, ok := ctx.Value(serviceNameKey{}).(string); ok && name != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-rimsky-service-name", name)
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

func ServiceNameStreamInterceptor(
	ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
	method string, streamer grpc.Streamer, opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	if name, ok := ctx.Value(serviceNameKey{}).(string); ok && name != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-rimsky-service-name", name)
	}
	return streamer(ctx, desc, cc, method, opts...)
}
