// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package testfixture

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubserver "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/server"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
)

type PickPolicy struct {
	ItemsTable        string
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
}

type Config struct {
	Connection     string
	WriteSemantics claimproducer.WriteSemantics
	PickPolicies   map[string]*PickPolicy
	SweepInterval  time.Duration
	WithAdmin      bool
	EnableExecutor bool
}

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
