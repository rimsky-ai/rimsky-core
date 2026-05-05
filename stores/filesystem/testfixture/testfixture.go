// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package testfixture starts the filesystem store-service on ephemeral
// listeners for in-process loopback tests. Per spec §9.2.
package testfixture

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/fallguy/rimsky/stores/filesystem/server"
	fsstore "github.com/fallguy/rimsky/stores/filesystem/store"
)

// Config configures the loopback store-service. Only Root is required;
// omit PickPolicies for a regional-only store-service.
type Config struct {
	Root          string
	PickPolicies  map[string]*fsstore.PickPolicy
	SweepInterval time.Duration
	WithAdmin     bool
}

// Start spawns the filesystem store-service on ephemeral listeners.
// Returns (grpcEndpoint, adminEndpoint, teardown). adminEndpoint is
// empty when WithAdmin is false.
func Start(t *testing.T, cfg Config) (grpcEndpoint, adminEndpoint string, teardown func()) {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("filesystem testfixture: grpc listen: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLis.Close()
		t.Fatalf("filesystem testfixture: http listen: %v", err)
	}
	var adminLis net.Listener
	if cfg.WithAdmin {
		adminLis, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			_ = grpcLis.Close()
			_ = httpLis.Close()
			t.Fatalf("filesystem testfixture: admin listen: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = server.Run(ctx, server.Config{
			Root:          cfg.Root,
			PickPolicies:  cfg.PickPolicies,
			SweepInterval: cfg.SweepInterval,
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
