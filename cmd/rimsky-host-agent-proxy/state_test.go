// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"fmt"
	"sync"
	"testing"
)

func TestRegisterAgentDisplacesPrior(t *testing.T) {
	s := newProxyState()

	first, prior, displaced := s.registerAgent("key-1", "host-a", "http://127.0.0.1:5001")
	if displaced || prior != nil {
		t.Fatalf("first register should not displace: displaced=%v prior=%v", displaced, prior)
	}

	second, prior2, displaced2 := s.registerAgent("key-1", "host-b", "http://127.0.0.1:5002")
	if !displaced2 {
		t.Fatalf("second register for same key should report displaced")
	}
	if prior2 != first {
		t.Fatalf("displaced prior should be the first connection")
	}
	if got, ok := s.lookupAgent("key-1"); !ok || got != second {
		t.Fatalf("lookupAgent should return the latest connection")
	}
}

func TestRegisterAgentDropsPriorSpawnsOnDisplacement(t *testing.T) {
	s := newProxyState()
	s.registerAgent("key-1", "host-a", "")
	// The prior connection lazily spawned a child and recorded a claim route.
	s.recordSpawn("spawn-1", "key-1", "inst-1", "codegen", nil, "")
	s.recordClaimRoute("claim-1", "key-1", "spawn-1")
	if _, ok := s.lookupSpawn("spawn-1"); !ok {
		t.Fatalf("precondition: spawn should be recorded")
	}

	// A second connection for the same key displaces the prior. The prior's
	// spawns must be dropped so a dispatch can't resolve a spawn the new
	// agent has no child for (which would surface a spurious executor_crashed).
	s.registerAgent("key-1", "host-b", "")

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
	first, _, _ := s.registerAgent("key-1", "a", "")
	second, _, _ := s.registerAgent("key-1", "b", "")

	// Dropping the displaced prior must not evict its successor.
	s.dropAgent("key-1", first)
	if got, ok := s.lookupAgent("key-1"); !ok || got != second {
		t.Fatalf("dropping prior must not evict successor")
	}

	// Dropping the current connection evicts it.
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

	// Different binding name in the same scope is a distinct spawn.
	if _, ok := s.lookupSpawnByRunScopeBinding("inst-1", "other"); ok {
		t.Fatalf("different binding name should not dedup")
	}
	// Different scope is a distinct spawn.
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
	if entry.ownerAPIKeyID != "owner-1" {
		t.Fatalf("owner mismatch: %q", entry.ownerAPIKeyID)
	}
	if entry.serviceBindings["codegen"].Path != "./bin" {
		t.Fatalf("binding path mismatch")
	}
	if entry.params["cwd"] != "." {
		t.Fatalf("params cwd mismatch")
	}
}

func TestClaimRouteLifecycle(t *testing.T) {
	s := newProxyState()
	s.recordSpawn("spawn-1", "key-1", "inst-1", "fs-claims", nil, "")
	s.recordClaimRoute("claim-1", "key-1", "spawn-1")

	route, ok := s.lookupClaimRoute("claim-1")
	if !ok || route.spawnID != "spawn-1" || route.apiKeyID != "key-1" {
		t.Fatalf("claim route lookup miss: ok=%v route=%+v", ok, route)
	}

	// Dropping the spawn purges its claim routes.
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
			conn, _, _ := s.registerAgent(key, "label", "")
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
