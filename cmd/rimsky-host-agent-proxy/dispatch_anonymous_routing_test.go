// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestResolveAndSpawn_OwnerlessInstanceRoutesToAnonymousAgent(t *testing.T) {
	state := newProxyState()

	const (
		instanceID  = "inst-anon-routing"
		bindingName = "verifier"
	)
	state.cacheInstance(instanceID, map[string]bindingSpec{
		bindingName: {Path: "/usr/local/bin/verifier"},
	}, "", map[string]any{})

	agentConn, _, _ := state.registerAgent(anonymousRoutingIdentity, "anon-agent", "")

	go func() {
		frame := <-agentConn.sendCh
		spawn := frame.GetSpawn()
		agentConn.deliverSpawnAck(&genv1.SpawnAck{
			SpawnId: spawn.GetSpawnId(),
			Status:  genv1.SpawnAck_SPAWN_STATUS_READY,
		})
	}()

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(serviceNameHeader, bindingName))

	fetchNeverCalled := func(context.Context, string) (*instanceCacheEntry, bool, error) {
		t.Fatalf("fetch must not be called: the instance is already cached")
		return nil, false, nil
	}

	resolved, rerr := resolveAndSpawn(ctx, state, fetchNeverCalled, nil, instanceID, "", "", 5*time.Second)
	if rerr != nil {
		t.Fatalf("resolveAndSpawn: got error class=%s msg=%s, want success routed to the %q agent",
			rerr.class, rerr.msg, anonymousRoutingIdentity)
	}
	if resolved.agent != agentConn {
		t.Fatalf("resolveAndSpawn resolved to a different agent connection than the one registered under %q",
			anonymousRoutingIdentity)
	}
	if resolved.agent.apiKeyID != anonymousRoutingIdentity {
		t.Fatalf("resolved.agent.apiKeyID = %q, want %q "+
			"(an owner-less instance's routing key must resolve to the anonymous agent, not host_agent_not_connected)",
			resolved.agent.apiKeyID, anonymousRoutingIdentity)
	}
}

func TestResolveAndSpawn_OwnerlessInstanceDoesNotRouteToNamedOwnerAgent(t *testing.T) {
	state := newProxyState()

	const (
		instanceID  = "inst-anon-routing-mismatch"
		bindingName = "verifier"
	)
	state.cacheInstance(instanceID, map[string]bindingSpec{
		bindingName: {Path: "/usr/local/bin/verifier"},
	}, "", map[string]any{})

	state.registerAgent("key-owner-1", "owner-agent", "")

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(serviceNameHeader, bindingName))
	fetchNeverCalled := func(context.Context, string) (*instanceCacheEntry, bool, error) {
		t.Fatalf("fetch must not be called: the instance is already cached")
		return nil, false, nil
	}

	_, rerr := resolveAndSpawn(ctx, state, fetchNeverCalled, nil, instanceID, "", "", 5*time.Second)
	if rerr == nil {
		t.Fatalf("resolveAndSpawn: got success, want %s "+
			"(no agent is registered under the anonymous routing key, only under an unrelated owner key)",
			errClassHostAgentNotConnected)
	}
	if rerr.class != errClassHostAgentNotConnected {
		t.Fatalf("resolveAndSpawn error class: got %q want %q", rerr.class, errClassHostAgentNotConnected)
	}
}
