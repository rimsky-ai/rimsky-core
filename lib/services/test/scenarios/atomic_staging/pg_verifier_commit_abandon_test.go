// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// pg_verifier commit/abandon scenario — rewritten in Pass 2 task 26
// alongside the unary RPC migration. Sibling tests in this package
// still reference startPgStore, so the helper survives here as a
// thin wrapper until the rewrite lands.

package atomicstaging

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	pgstoreserver "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/server"
)

// startPgStore boots `stores/postgres/server.Run` in-process bound to
// loopback. Returns the gRPC dial address and a teardown.
func startPgStore(t *testing.T, dsn string, enableExecutor bool) (grpcAddr string, teardown func()) {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pg store grpc listen: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLis.Close()
		t.Fatalf("pg store http listen: %v", err)
	}
	addr := grpcLis.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = pgstoreserver.Run(ctx, pgstoreserver.Config{
			Connection:     dsn,
			WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
			EnableExecutor: enableExecutor,
		}, grpcLis, httpLis, nil)
		close(done)
	}()
	return addr, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}
