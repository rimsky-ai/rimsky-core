// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package testfixture starts the stub store-service on ephemeral
// listeners for in-process loopback tests.
package testfixture

import (
	"context"
	"net"
	"testing"

	"github.com/rimsky-ai/rimsky-core/stores/stub/server"
	stubstore "github.com/rimsky-ai/rimsky-core/stores/stub/store"
)

// Start spawns server.RunWithStore on a goroutine bound to ephemeral
// ports. Returns the gRPC endpoint, the in-memory *Store (for test
// assertions), and a teardown closure.
func Start(t *testing.T, cfg stubstore.Config) (endpoint string, store *stubstore.Store, teardown func()) {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("stub testfixture: grpc listen: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLis.Close()
		t.Fatalf("stub testfixture: http listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	st := stubstore.New(cfg)
	done := make(chan struct{})
	go func() {
		// t.Logf is unsafe to call after the test has returned; teardown
		// blocks on `done` so we just discard the return value here.
		_ = server.RunWithStore(ctx, server.Config{
			Substrate:            cfg,
			EnableLifecycle:      true,
			EnableDataProcessing: true,
		}, st, grpcLis, httpLis)
		close(done)
	}()
	return grpcLis.Addr().String(), st, func() {
		cancel()
		<-done
	}
}
