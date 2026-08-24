// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"sync"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestSpawnChild_InstanceLevelParamsCwdThreadsOntoSpawnCwd(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeDaemon(t, ts, "owner-1", "http://127.0.0.1:7777", executorScript(t))

	var mu sync.Mutex
	var got *genv1.Spawn
	fa.setSpawnObserver(func(sp *genv1.Spawn) {
		mu.Lock()
		defer mu.Unlock()
		got = sp
	})

	const wantCwd = "/work/instance-level"
	ts.state.cacheInstance("inst-cwd", map[string]bindingSpec{"codegen": {Path: "./codegen"}}, "owner-1", map[string]any{"cwd": wantCwd})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()

	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{
		InstanceId:  "inst-cwd",
		CallbackUrl: "http://supervisor:8080/v1/callback/ack-1",
	})
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Outcome{Success}, got %T", outcome.GetOutcome())
	}

	mu.Lock()
	sp := got
	mu.Unlock()
	if sp == nil {
		t.Fatal("fake daemon never observed a Spawn frame")
	}
	if sp.GetCwd() != wantCwd {
		t.Fatalf("Spawn.Cwd = %q, want %q (spawnChild must thread entry.params[cwd] onto the top-level Spawn.Cwd field)", sp.GetCwd(), wantCwd)
	}
	if sp.GetBinding().GetCwd() != "" {
		t.Fatalf("Binding.Cwd = %q, want empty (the instance-level cwd param must not leak into the binding-level override)", sp.GetBinding().GetCwd())
	}
}
