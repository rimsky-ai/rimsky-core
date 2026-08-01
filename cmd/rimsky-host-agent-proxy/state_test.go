// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"fmt"
	"sync"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestRegisterAgentDisplacesPrior(t *testing.T) {
	s := newProxyState()

	first, prior, collided := s.registerAgent("key-1", "host-a", "http://127.0.0.1:5001", registerDisplacePrior)
	if collided || prior != nil {
		t.Fatalf("first register should not collide: collided=%v prior=%v", collided, prior)
	}

	second, prior2, collided2 := s.registerAgent("key-1", "host-b", "http://127.0.0.1:5002", registerDisplacePrior)
	if collided2 {
		t.Fatalf("displace-mode register must never signal collision; got collided2=true")
	}
	if prior2 != first {
		t.Fatalf("displaced prior should be the first connection")
	}
	if got, ok := s.lookupAgent("key-1"); !ok || got != second {
		t.Fatalf("lookupAgent should return the latest connection")
	}
}

func TestRegisterAgentRejectOnCollisionKeepsPrior(t *testing.T) {
	s := newProxyState()

	first, _, collided := s.registerAgent("sparkling-wombat", "alpha", "", registerRejectOnCollision)
	if collided || first == nil {
		t.Fatalf("first register must succeed: collided=%v first=%v", collided, first)
	}

	second, prior, collided2 := s.registerAgent("sparkling-wombat", "beta", "", registerRejectOnCollision)
	if !collided2 {
		t.Fatalf("second register with same routing identity must report collision under reject mode")
	}
	if second != nil {
		t.Fatalf("reject-mode collision must not return a new connection; got %v", second)
	}
	if prior != first {
		t.Fatalf("reject-mode collision must surface the prior connection")
	}
	if got, ok := s.lookupAgent("sparkling-wombat"); !ok || got != first {
		t.Fatalf("prior connection must still be the registered agent after a rejected collision")
	}
}

func TestRegisterAgentDropsPriorSpawnsOnDisplacement(t *testing.T) {
	s := newProxyState()
	s.registerAgent("key-1", "host-a", "", registerDisplacePrior)
	s.recordSpawn("spawn-1", "key-1", "inst-1", "codegen", nil, "")
	s.recordClaimRoute("claim-1", "key-1", "spawn-1")
	if _, ok := s.lookupSpawn("spawn-1"); !ok {
		t.Fatalf("precondition: spawn should be recorded")
	}

	s.registerAgent("key-1", "host-b", "", registerDisplacePrior)

	if _, ok := s.lookupSpawn("spawn-1"); ok {
		t.Fatalf("displacement should drop the prior connection's spawn")
	}
	if _, ok := s.lookupSpawnByRunScopeBinding("inst-1", "codegen"); ok {
		t.Fatalf("displacement should drop the prior connection's dedup index entry")
	}
	if _, ok := s.lookupClaimRoute("claim-1"); ok {
		t.Fatalf("displacement should purge the prior connection's claim route")
	}
}

func TestDropAgentOnlyEvictsCurrent(t *testing.T) {
	s := newProxyState()
	first, _, _ := s.registerAgent("key-1", "a", "", registerDisplacePrior)
	second, _, _ := s.registerAgent("key-1", "b", "", registerDisplacePrior)

	s.dropAgent("key-1", first)
	if got, ok := s.lookupAgent("key-1"); !ok || got != second {
		t.Fatalf("dropping prior must not evict successor")
	}

	s.dropAgent("key-1", second)
	if _, ok := s.lookupAgent("key-1"); ok {
		t.Fatalf("dropping current should evict")
	}
}

func TestSpawnDedupByScopeBinding(t *testing.T) {
	s := newProxyState()
	s.recordSpawn("spawn-1", "key-1", "inst-1", "codegen", nil, "http://sup/cb")

	got, ok := s.lookupSpawnByRunScopeBinding("inst-1", "codegen")
	if !ok || got != "spawn-1" {
		t.Fatalf("dedup lookup miss: ok=%v got=%q", ok, got)
	}

	if _, ok := s.lookupSpawnByRunScopeBinding("inst-1", "other"); ok {
		t.Fatalf("different binding name should not dedup")
	}
	if _, ok := s.lookupSpawnByRunScopeBinding("inst-2", "codegen"); ok {
		t.Fatalf("different scope should not dedup")
	}
}

func TestDropSpawnsForRunScope(t *testing.T) {
	s := newProxyState()
	s.recordSpawn("spawn-1", "key-1", "inst-1", "codegen", nil, "")
	s.recordSpawn("spawn-2", "key-1", "inst-1", "fmt", nil, "")
	s.recordSpawn("spawn-3", "key-1", "inst-2", "codegen", nil, "")

	dropped := s.dropSpawnsForRunScope("inst-1")
	if len(dropped) != 2 {
		t.Fatalf("expected 2 dropped spawns, got %d", len(dropped))
	}
	if _, ok := s.lookupSpawn("spawn-1"); ok {
		t.Fatalf("spawn-1 should be dropped")
	}
	if _, ok := s.lookupSpawn("spawn-3"); !ok {
		t.Fatalf("spawn-3 (different scope) should survive")
	}
	if _, ok := s.lookupSpawnByRunScopeBinding("inst-1", "codegen"); ok {
		t.Fatalf("dedup index for dropped scope should be cleared")
	}
}

func TestInstanceCacheHitMiss(t *testing.T) {
	s := newProxyState()
	if _, ok := s.lookupInstance("inst-x"); ok {
		t.Fatalf("expected cache miss on empty state")
	}
	s.cacheInstance("inst-x", map[string]bindingSpec{"codegen": {Path: "./bin"}}, "owner-1", map[string]any{"cwd": "."})
	entry, ok := s.lookupInstance("inst-x")
	if !ok {
		t.Fatalf("expected cache hit after cacheInstance")
	}
	if entry.targetRoutingIdentity != "owner-1" {
		t.Fatalf("routing identity mismatch: %q", entry.targetRoutingIdentity)
	}
	if entry.serviceBindings["codegen"].Path != "./bin" {
		t.Fatalf("binding path mismatch")
	}
	if entry.params["cwd"] != "." {
		t.Fatalf("params cwd mismatch")
	}
}

func TestDropInstanceEvictsCache(t *testing.T) {
	s := newProxyState()
	s.cacheInstance("inst-x", map[string]bindingSpec{"codegen": {Path: "./bin"}}, "owner-1", nil)
	if _, ok := s.lookupInstance("inst-x"); !ok {
		t.Fatalf("expected cache hit after cacheInstance")
	}
	s.dropInstance("inst-x")
	if _, ok := s.lookupInstance("inst-x"); ok {
		t.Fatalf("expected cache miss after dropInstance")
	}
}

func TestDeliverDispatchOverflowDoesNotBlock(t *testing.T) {
	conn := newAgentConnection("owner-1", "label", "")
	streamID := "stream-1"
	ch := conn.registerStream(streamID)

	done := make(chan struct{})
	go func() {
		for i := 0; i < cap(ch)+4; i++ {
			conn.deliverDispatch(&genv1.DispatchFrame{StreamId: streamID})
		}
		close(done)
	}()
	<-done

	otherID := "stream-2"
	otherCh := conn.registerStream(otherID)
	otherDone := make(chan struct{})
	go func() {
		conn.deliverDispatch(&genv1.DispatchFrame{StreamId: otherID})
		close(otherDone)
	}()
	<-otherDone

	select {
	case <-otherCh:
	default:
		t.Fatalf("expected the other stream's frame to be delivered")
	}
}

func TestClaimRouteLifecycle(t *testing.T) {
	s := newProxyState()
	s.recordSpawn("spawn-1", "key-1", "inst-1", "fs-claims", nil, "")
	s.recordClaimRoute("claim-1", "key-1", "spawn-1")

	route, ok := s.lookupClaimRoute("claim-1")
	if !ok || route.spawnID != "spawn-1" || route.routingIdentity != "key-1" {
		t.Fatalf("claim route lookup miss: ok=%v route=%+v", ok, route)
	}

	s.dropSpawn("spawn-1")
	if _, ok := s.lookupClaimRoute("claim-1"); ok {
		t.Fatalf("claim route should be purged when its spawn is dropped")
	}
}

func TestConcurrentAccessSafety(t *testing.T) {
	s := newProxyState()
	var wg sync.WaitGroup
	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i%5)
			scope := fmt.Sprintf("inst-%d", i%5)
			conn, _, _ := s.registerAgent(key, "label", "", registerDisplacePrior)
			s.recordSpawn(fmt.Sprintf("spawn-%d", i), key, scope, "codegen", nil, "")
			s.cacheInstance(scope, map[string]bindingSpec{"codegen": {Path: "./b"}}, key, nil)
			s.recordClaimRoute(fmt.Sprintf("claim-%d", i), key, fmt.Sprintf("spawn-%d", i))
			_, _ = s.lookupAgent(key)
			_, _ = s.lookupInstance(scope)
			_, _ = s.lookupSpawnByRunScopeBinding(scope, "codegen")
			_ = s.dropSpawnsForRunScope(scope)
			s.dropAgent(key, conn)
		}(i)
	}
	wg.Wait()
}
