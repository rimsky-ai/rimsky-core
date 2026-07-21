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

const ServiceNameMetadataKey = "x-rimsky-service-name"

const ServiceNameHTTPHeader = "X-Rimsky-Service-Name"

func WithServiceName(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, serviceNameKey{}, name)
}

func ServiceNameFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(serviceNameKey{}).(string)
	return name, ok && name != ""
}

func ServiceNameUnaryInterceptor(
	ctx context.Context, method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
) error {
	if name, ok := ServiceNameFromContext(ctx); ok {
		ctx = metadata.AppendToOutgoingContext(ctx, ServiceNameMetadataKey, name)
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

func ServiceNameStreamInterceptor(
	ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
	method string, streamer grpc.Streamer, opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	if name, ok := ServiceNameFromContext(ctx); ok {
		ctx = metadata.AppendToOutgoingContext(ctx, ServiceNameMetadataKey, name)
	}
	return streamer(ctx, desc, cc, method, opts...)
}
