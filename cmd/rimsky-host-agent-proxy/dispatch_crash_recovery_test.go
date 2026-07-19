// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestExecute_ExecutorCrashDropsSpawnAndForcesFreshSpawnOnNextDispatch(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "http://127.0.0.1:7777", executorScript(t))
	fa.setCrashOnDispatch(2)

	var mu sync.Mutex
	var spawnIDs []string
	fa.setSpawnObserver(func(sp *genv1.Spawn) {
		mu.Lock()
		defer mu.Unlock()
		spawnIDs = append(spawnIDs, sp.GetSpawnId())
	})

	cacheReadyInstance(ts, "inst-crash", "owner-1", map[string]bindingSpec{"codegen": {Path: "./codegen"}})
	client := genv1.NewExecutorClient(ts.supConn)

	dispatch := func() *genv1.Outcome {
		ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
		defer cancel()
		return collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-crash"})
	}

	first := dispatch()
	if first.GetSuccess() == nil {
		t.Fatalf("first dispatch: expected Success, got %T", first.GetOutcome())
	}

	second := dispatch()
	if class := terminalErrorClass(second); class != errClassExecutorCrashed {
		t.Fatalf("second dispatch (agent cancels the call): error class = %q, want %q", class, errClassExecutorCrashed)
	}

	if _, ok := ts.state.lookupSpawnByRunScopeBinding("inst-crash", "codegen"); ok {
		t.Fatal("a crashed spawn must be dropped from the run_scope/binding cache, not reused")
	}

	third := dispatch()
	if third.GetSuccess() == nil {
		t.Fatalf("third dispatch: expected Success (fresh spawn after crash), got %T", third.GetOutcome())
	}

	mu.Lock()
	got := append([]string(nil), spawnIDs...)
	mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("expected exactly 2 spawns (initial + fresh-after-crash; the crashed call must reuse the dead spawn, not spawn again), got %d: %v", len(got), got)
	}
	if got[0] == got[1] {
		t.Fatalf("the post-crash dispatch must spawn a fresh child with a new spawn_id, got the same id twice: %v", got)
	}
}
