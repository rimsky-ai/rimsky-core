// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serviceauth

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
)

func (id *Identity) GRPCServer(extra ...grpc.ServerOption) *grpc.Server {
	return grpc.NewServer(append(id.GRPCServerOptions(), extra...)...)
}

func NewGRPCServer(ctx context.Context, label string, extra ...grpc.ServerOption) (*grpc.Server, *Identity, error) {
	id, err := LoadFromEnv(ctx, label)
	if err != nil {
		return nil, nil, err
	}
	return id.GRPCServer(extra...), id, nil
}

func (id *Identity) StartMaintain(ctx context.Context, label string) {
	if !id.Enabled() {
		return
	}
	go id.Maintain(ctx, DefaultRenewCheckInterval, func(err error) {
		slog.Error("SERVICEAUTH.LEAF.RENEWFAILED", "service", label, "error", err.Error())
	})
}
