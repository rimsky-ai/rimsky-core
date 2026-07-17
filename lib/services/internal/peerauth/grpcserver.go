// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peerauth

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
		slog.Error(label+" peer-auth renewal", "error", err.Error())
	})
}
