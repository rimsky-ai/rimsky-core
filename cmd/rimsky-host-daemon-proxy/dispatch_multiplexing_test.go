// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestConcurrentDispatchesOnSameSpawnNoHeadOfLineBlocking(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	release := make(chan struct{})

	handler := func(_ string, payload []byte) [][]byte {
		var req genv1.ExecuteRequest
		_ = proto.Unmarshal(payload, &req)
		if req.GetNodeId() == "slow" {
			<-release
		}
		done, _ := proto.Marshal(&genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			ChangeSummary: req.GetNodeId(),
		}}})
		return [][]byte{done}
	}
	connectFakeDaemon(t, ts, "owner-1", "", handler)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./codegen"}})

	client := genv1.NewExecutorClient(ts.supConn)

	slowCtx, slowCancel := context.WithCancel(callCtx("codegen"))
	defer slowCancel()
	slowDone := make(chan *genv1.Outcome, 1)
	go func() {
		outcome, err := client.Execute(slowCtx, &genv1.ExecuteRequest{InstanceId: "inst-1", NodeId: "slow"})
		if err != nil {
			t.Errorf("slow Execute: %v", err)
			close(slowDone)
			return
		}
		slowDone <- outcome
	}()

	waitFor(t, "the slow Execute to open its spawn, so the fast one shares it", func() bool {
		_, ok := ts.state.lookupSpawnByRunScopeBinding("inst-1", "codegen")
		return ok
	})

	fastCtx, fastCancel := context.WithCancel(callCtx("codegen"))
	defer fastCancel()
	outcome, err := client.Execute(fastCtx, &genv1.ExecuteRequest{InstanceId: "inst-1", NodeId: "fast"})
	if err != nil {
		t.Fatalf("fast Execute: %v", err)
	}
	if got := outcome.GetSuccess().GetChangeSummary(); got != "fast" {
		t.Fatalf("unexpected fast outcome: %+v", outcome)
	}

	select {
	case <-slowDone:
		t.Fatalf("the slow dispatch settled before the fast one returned; " +
			"a shared spawn must not serialize concurrent stream-ids (head-of-line blocking)")
	default:
	}

	close(release)
	slowOutcome := <-slowDone
	if got := slowOutcome.GetSuccess().GetChangeSummary(); got != "slow" {
		t.Fatalf("unexpected slow outcome: %+v", slowOutcome)
	}
}

func TestConcurrentDispatchesShareOneSpawn(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	release := make(chan struct{})
	spawnCount := make(chan struct{}, 8)

	handler := func(_ string, payload []byte) [][]byte {
		<-release
		done, _ := proto.Marshal(&genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{}}})
		return [][]byte{done}
	}
	fa := connectFakeDaemon(t, ts, "owner-1", "", handler)
	fa.setSpawnObserver(func(_ *genv1.Spawn) {
		select {
		case spawnCount <- struct{}{}:
		default:
		}
	})
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./codegen"}})

	client := genv1.NewExecutorClient(ts.supConn)

	const n = 5
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			ctx, cancel := context.WithCancel(callCtx("codegen"))
			defer cancel()
			_, err := client.Execute(ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
			results <- err
		}()
	}

	close(release)
	for i := 0; i < n; i++ {
		if err := <-results; err != nil {
			t.Fatalf("Execute[%d]: %v", i, err)
		}
	}

	if _, ok := ts.state.lookupSpawnByRunScopeBinding("inst-1", "codegen"); !ok {
		t.Fatalf("expected a recorded spawn after concurrent dispatch")
	}
	if len(spawnCount) != 1 {
		t.Fatalf("expected exactly one Spawn for concurrent first-dispatch on the same (scope, binding), observed %d", len(spawnCount))
	}
}
