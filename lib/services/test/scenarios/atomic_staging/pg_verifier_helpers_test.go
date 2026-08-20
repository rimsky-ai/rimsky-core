// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package atomicstaging

import (
	"context"
	"net"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	pgstoreserver "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/postgres/server"
)

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
		<-done
	}
}
