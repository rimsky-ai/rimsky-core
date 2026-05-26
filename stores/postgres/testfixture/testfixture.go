// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package testfixture stands up an in-process loopback claim-producer
// store-service for tests that want to drive the postgres-store
// surface over the wire.
//
// Post-2026-05-24 reorganization: the production postgres-store
// implementation lives in `pkg:github.com/fallguyconsulting/rimsky-services`.
// To preserve the in-rimsky test-fixture surface without dragging in
// the production code, this testfixture now wraps `pkg:stores/stub`
// (deterministic in-memory) while exposing the same `Start`
// signature callers had against the postgres-backed fixture. For
// tests that want postgres-specific behaviour (atomic-staging schema
// swap, fused verifier-role Execute, items-table pick semantics) the
// rimsky-services repo's own fixture wraps the published store image.
//
// Per spec `2026-05-24-repo-reorganization-design` §P3.1.
package testfixture

import (
	"context"
	"net"
	"testing"
	"time"

	claimproducer "github.com/fallguyconsulting/rimsky/protocols/claimproducer"
	"github.com/fallguyconsulting/rimsky/sdk/go/stores/action"
	stubserver "github.com/fallguyconsulting/rimsky/stores/stub/server"
	stubstore "github.com/fallguyconsulting/rimsky/stores/stub/store"
)

// PickPolicy mirrors the configuration shape that the production
// postgres-store's PickPolicy used to expose, so call-site test code
// can construct fixture configs without importing the production-
// side store package. Only `OnCommit` and `OnGiveUp` are honoured by
// the stub-backed translation; `ItemsTable` and `VisibilityTimeout`
// are accepted for source compatibility and ignored. Postgres-
// specific tests that rely on the items-table semantics or
// visibility-timeout dynamics live in rimsky-services.
type PickPolicy struct {
	ItemsTable        string
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
}

// Config is the per-test store config. `Connection`,
// `WriteSemantics`, `SweepInterval`, `WithAdmin`, and
// `EnableExecutor` are accepted for source compatibility with the
// pre-reorg postgres-backed fixture and ignored by the stub-backed
// translation — the stub-store substrate has no executor role and
// no admin surface. Tests that need those behaviours belong in
// rimsky-services.
type Config struct {
	Connection     string
	WriteSemantics claimproducer.WriteSemantics
	PickPolicies   map[string]*PickPolicy
	SweepInterval  time.Duration
	WithAdmin      bool
	EnableExecutor bool
}

// Start spawns the loopback store-service on ephemeral listeners.
// Returns (grpcEndpoint, adminEndpoint, teardown). adminEndpoint is
// always empty — the stub-backed fixture exposes no admin surface;
// tests that need admin seeding use the stub's `*stubstore.Store`
// handle directly (see `pkg:stores/stub/testfixture` for that
// surface).
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

	stubCfg := translateConfig(cfg)
	st := stubstore.New(stubCfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = stubserver.RunWithStore(ctx, stubserver.Config{
			Substrate: stubCfg,
		}, st, grpcLis, httpLis)
		close(done)
	}()
	return grpcLis.Addr().String(), "", func() {
		cancel()
		<-done
	}
}

// translateConfig converts the postgres-shape Config into a
// stub-store config. Pick policies preserve OnCommit / OnGiveUp
// kinds; queues start empty (tests that need items seeded use
// `stubstore.Store.SeedPickPolicyItem` via the stub-store
// testfixture's *Store handle).
func translateConfig(cfg Config) stubstore.Config {
	out := stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{writeSemanticsOrDefault(cfg.WriteSemantics)},
		},
		PickPolicies: make(map[string]stubstore.PickPolicyConfig, len(cfg.PickPolicies)),
	}
	for selector, pp := range cfg.PickPolicies {
		if pp == nil {
			continue
		}
		out.PickPolicies[selector] = stubstore.PickPolicyConfig{
			OnCommit: pp.OnCommit,
			OnGiveUp: pp.OnGiveUp,
		}
	}
	return out
}

func writeSemanticsOrDefault(ws claimproducer.WriteSemantics) claimproducer.WriteSemantics {
	if ws == "" {
		return claimproducer.WriteSemanticsSync
	}
	return ws
}
