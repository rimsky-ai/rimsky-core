// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package testfixture

import (
	"context"
	"net"
	"testing"

	"github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/server"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
)

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
