// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package testfixture starts the postgres store-service on ephemeral
// listeners for in-process loopback tests. Per spec §9.2.
package testfixture

import (
	"context"
	"net"
	"testing"
	"time"

	corestore "github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/stores/postgres/server"
	pgsstore "github.com/fallguyconsulting/rimsky/stores/postgres/store"
)

// Config is the per-test store config (a thin re-export of
// server.Config so tests don't need to import the server package
// directly).
type Config struct {
	Connection     string
	WriteSemantics corestore.WriteSemantics
	PickPolicies   map[string]*pgsstore.PickPolicy
	SweepInterval  time.Duration
	WithAdmin      bool

	// EnableExecutor registers the Executor protocol alongside
	// ClaimProducer on the same gRPC endpoint, enabling the
	// SQL-substrate verifier role per spec
	// 2026-05-19-multi-instance-template-ergonomics-design §Item 6.
	EnableExecutor bool
}

// Start spawns server.Run on a goroutine bound to ephemeral ports.
// Returns the gRPC endpoint, the admin endpoint (or "" if WithAdmin is
// false), and a teardown closure.
func Start(t *testing.T, cfg Config) (grpcEndpoint, adminEndpoint string, teardown func()) {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("postgres testfixture: grpc listen: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLis.Close()
		t.Fatalf("postgres testfixture: http listen: %v", err)
	}
	var adminLis net.Listener
	if cfg.WithAdmin {
		adminLis, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			_ = grpcLis.Close()
			_ = httpLis.Close()
			t.Fatalf("postgres testfixture: admin listen: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = server.Run(ctx, server.Config{
			Connection:     cfg.Connection,
			WriteSemantics: cfg.WriteSemantics,
			PickPolicies:   cfg.PickPolicies,
			SweepInterval:  cfg.SweepInterval,
			EnableExecutor: cfg.EnableExecutor,
		}, grpcLis, httpLis, adminLis)
		close(done)
	}()
	grpcEndpoint = grpcLis.Addr().String()
	if adminLis != nil {
		adminEndpoint = adminLis.Addr().String()
	}
	return grpcEndpoint, adminEndpoint, func() {
		cancel()
		<-done
	}
}
