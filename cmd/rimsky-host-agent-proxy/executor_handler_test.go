// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func cacheReadyInstance(ts *proxyTestServer, instanceID, routingIdentity string, bindings map[string]bindingSpec) {
	ts.state.cacheInstance(instanceID, bindings, routingIdentity, map[string]any{"cwd": "."})
}

func executorScript(t *testing.T) dispatchHandler {
	t.Helper()
	return func(protocol string, payload []byte) [][]byte {
		var req genv1.ExecuteRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			t.Errorf("agent: unmarshal execute request: %v", err)
		}
		done, _ := proto.Marshal(&genv1.Outcome{
			Outcome: &genv1.Outcome_Success{Success: &genv1.Success{Changed: true}},
		})
		return [][]byte{done}
	}
}

func collectExecute(t *testing.T, client genv1.ExecutorClient, ctx context.Context, req *genv1.ExecuteRequest) *genv1.Outcome {
	t.Helper()
	outcome, err := client.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return outcome
}

func terminalErrorClass(outcome *genv1.Outcome) string {
	if e := outcome.GetError(); e != nil {
		return e.GetErrorClass()
	}
	return ""
}

func TestExecuteHappyPath(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "http://127.0.0.1:7777", executorScript(t))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./codegen"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()

	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{
		InstanceId:  "inst-1",
		CallbackUrl: "http://supervisor:8080/v1/callback/ack-1",
	})
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Outcome{Success}, got %T", outcome.GetOutcome())
	}
	if !outcome.GetSuccess().GetChanged() {
		t.Fatalf("expected Changed=true on Success")
	}

	spawnID, ok := ts.state.lookupSpawnByRunScopeBinding("inst-1", "codegen")
	if !ok {
		t.Fatalf("expected a recorded spawn")
	}
	sp, _ := ts.state.lookupSpawn(spawnID)
	if sp.originalCallback != "http://supervisor:8080/v1/callback/ack-1" {
		t.Fatalf("original callback not recorded: %q", sp.originalCallback)
	}
}

func TestExecuteCallbackRewrite(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	var seenCallback string
	handler := func(protocol string, payload []byte) [][]byte {
		var req genv1.ExecuteRequest
		_ = proto.Unmarshal(payload, &req)
		seenCallback = req.GetCallbackUrl()
		done, _ := proto.Marshal(&genv1.Outcome{
			Outcome: &genv1.Outcome_Success{Success: &genv1.Success{}},
		})
		return [][]byte{done}
	}
	connectFakeAgent(t, ts, "owner-1", "http://127.0.0.1:7777", handler)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()
	_ = collectExecute(t, client, ctx, &genv1.ExecuteRequest{
		InstanceId:  "inst-1",
		CallbackUrl: "http://supervisor:8080/v1/callback/ack-1",
	})
	if seenCallback != "http://127.0.0.1:7777/v1/callback/ack-1" {
		t.Fatalf("callback not rewritten to agent host: %q", seenCallback)
	}
}

func TestExecuteErrorPreserved(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	handler := func(protocol string, payload []byte) [][]byte {
		done, _ := proto.Marshal(&genv1.Outcome{
			Outcome: &genv1.Outcome_Error{Error: &genv1.Error{ErrorClass: "child_thing_broke"}},
		})
		return [][]byte{done}
	}
	connectFakeAgent(t, ts, "owner-1", "", handler)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != "child_thing_broke" {
		t.Fatalf("expected error_class %q, got %q", "child_thing_broke", got)
	}
}

func TestExecuteMissingServiceName(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassBindingNotFound {
		t.Fatalf("expected %s, got %q", errClassBindingNotFound, got)
	}
}

func TestExecuteInstanceNotFound(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "missing"})
	if got := terminalErrorClass(outcome); got != errClassBindingNotFound {
		t.Fatalf("expected %s, got %q", errClassBindingNotFound, got)
	}
}

func TestExecuteAgentNotConnected(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassHostAgentNotConnected {
		t.Fatalf("expected %s, got %q", errClassHostAgentNotConnected, got)
	}
}

func TestExecuteBindingNotFound(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"other": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassBindingNotFound {
		t.Fatalf("expected %s, got %q", errClassBindingNotFound, got)
	}
}

func TestExecuteSpawnFailed(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	fa.setSpawnFail(true)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassSpawnFailed {
		t.Fatalf("expected %s, got %q", errClassSpawnFailed, got)
	}
}

func TestExecuteFailsSpawnWhenTheAgentNeverAcks(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	fa.setSpawnSilent(true)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	outcome := collectExecute(t, client, callCtx("codegen"), &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassSpawnFailed {
		t.Fatalf("expected %s, got %q", errClassSpawnFailed, got)
	}
}

func TestSpawnAckDeadlineOutlastsTheReadinessDeadlineTheAgentIsGiven(t *testing.T) {
	cases := []struct {
		name      string
		fallback  time.Duration
		binding   bindingSpec
		wantReady time.Duration
	}{
		{"binding sets its own readiness deadline", 30 * time.Second, bindingSpec{TimeoutSeconds: 3}, 3 * time.Second},
		{"binding declares none, so the proxy default stands", 30 * time.Second, bindingSpec{}, 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ready, ack := spawnDeadlines(tc.fallback, tc.binding)
			if ready != tc.wantReady {
				t.Fatalf("readiness deadline = %s, want %s", ready, tc.wantReady)
			}
			if ack <= ready {
				t.Fatalf("ack deadline %s does not outlast the readiness deadline %s the agent is given — the "+
					"proxy would abandon the wait as the agent reports why the child never bound, and the "+
					"operator would read the proxy's own timeout instead of the real reason", ack, ready)
			}
		})
	}
}

func TestExecuteDisconnectMidDispatch(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", nil)
	fa.setDropOnFirst(true)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassHostAgentDisconnected {
		t.Fatalf("expected %s, got %q", errClassHostAgentDisconnected, got)
	}
}

func TestExecuteSupervisorCancelSendsCancelFrame(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", nil)
	fa.setStallData(true)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.Execute(ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	}()

	waitFor(t, "the agent to receive the stalled dispatch, so the cancel lands mid-stream",
		func() bool { return fa.dispatches() > 0 })
	cancel()

	<-fa.canceled
	<-done
}

func TestExecuteFetcherFallbackPopulatesCache(t *testing.T) {
	entry := &instanceCacheEntry{
		serviceBindings:       map[string]bindingSpec{"codegen": {Path: "./c"}},
		targetRoutingIdentity: "owner-1",
		params:                map[string]any{"cwd": "."},
	}
	ts := newProxyTestServer(t, staticFetcher("inst-1", entry))
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if terminalErrorClass(outcome) != "" {
		t.Fatalf("expected success via fetcher fallback, got error: %q", terminalErrorClass(outcome))
	}
	if _, ok := ts.state.lookupInstance("inst-1"); !ok {
		t.Fatalf("fetcher fallback should populate the cache")
	}
}
