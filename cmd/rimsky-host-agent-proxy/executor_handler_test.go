// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"io"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// executorScript replays a Heartbeat then a terminal StreamClose{Success}
// as two serialized ExecuteEvent response frames.
func executorScript(t *testing.T) dispatchHandler {
	t.Helper()
	return func(protocol string, payload []byte) [][]byte {
		var req genv1.ExecuteRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			t.Errorf("agent: unmarshal execute request: %v", err)
		}
		hb, _ := proto.Marshal(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Heartbeat{Heartbeat: &genv1.Heartbeat{TimestampMs: 1}}})
		done, _ := proto.Marshal(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{Changed: true}}},
		}})
		return [][]byte{hb, done}
	}
}

// collectExecute drains an Execute stream into the events received.
func collectExecute(t *testing.T, client genv1.ExecutorClient, ctx context.Context, req *genv1.ExecuteRequest) []*genv1.ExecuteEvent {
	t.Helper()
	stream, err := client.Execute(ctx, req)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var events []*genv1.ExecuteEvent
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		events = append(events, ev)
	}
	return events
}

// terminalErrorClass returns the error_class of the last event if it is a
// StreamClose{Error}, else "".
func terminalErrorClass(events []*genv1.ExecuteEvent) string {
	if len(events) == 0 {
		return ""
	}
	sc := events[len(events)-1].GetStreamClose()
	if sc == nil {
		return ""
	}
	if e := sc.GetError(); e != nil {
		return e.GetErrorClass()
	}
	return ""
}

func cacheReadyInstance(ts *proxyTestServer, instanceID, owner string, bindings map[string]bindingSpec) {
	ts.state.cacheInstance(instanceID, bindings, owner, map[string]any{"cwd": "."})
}

func TestExecuteHappyPath(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "http://127.0.0.1:7777", executorScript(t))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./codegen"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()

	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{
		InstanceId:  "inst-1",
		CallbackUrl: "http://supervisor:8080/v1/callback/ack-1",
	})
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}
	if events[0].GetHeartbeat() == nil {
		t.Fatalf("expected first event to be heartbeat, got %T", events[0].GetEvent())
	}
	sc := events[len(events)-1].GetStreamClose()
	if sc == nil || sc.GetSuccess() == nil {
		t.Fatalf("expected terminal Success, got %v", events[len(events)-1].GetEvent())
	}

	// A spawn was recorded with the original callback URL.
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
		done, _ := proto.Marshal(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{}}},
		}})
		return [][]byte{done}
	}
	connectFakeAgent(t, ts, "owner-1", "http://127.0.0.1:7777", handler)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	_ = collectExecute(t, client, ctx, &genv1.ExecuteRequest{
		InstanceId:  "inst-1",
		CallbackUrl: "http://supervisor:8080/v1/callback/ack-1",
	})
	if seenCallback != "http://127.0.0.1:7777/v1/callback/ack-1" {
		t.Fatalf("callback not rewritten to agent host: %q", seenCallback)
	}
}

func TestExecuteMissingServiceName(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // no header
	defer cancel()
	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(events); got != errClassBindingNotFound {
		t.Fatalf("expected %s, got %q", errClassBindingNotFound, got)
	}
}

func TestExecuteInstanceNotFound(t *testing.T) {
	ts := newProxyTestServer(t, nil) // fetcher always misses
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "missing"})
	if got := terminalErrorClass(events); got != errClassBindingNotFound {
		t.Fatalf("expected %s, got %q", errClassBindingNotFound, got)
	}
}

func TestExecuteOwnerEmpty(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	cacheReadyInstance(ts, "inst-1", "", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(events); got != errClassHostAgentNotConnected {
		t.Fatalf("expected %s, got %q", errClassHostAgentNotConnected, got)
	}
}

func TestExecuteAgentNotConnected(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}}) // no agent

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(events); got != errClassHostAgentNotConnected {
		t.Fatalf("expected %s, got %q", errClassHostAgentNotConnected, got)
	}
}

func TestExecuteBindingNotFound(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"other": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(events); got != errClassBindingNotFound {
		t.Fatalf("expected %s, got %q", errClassBindingNotFound, got)
	}
}

func TestExecuteSpawnTimeout(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	fa.setSpawnDelay(3 * time.Second) // exceeds the 2s spawnTimeout
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 8*time.Second)
	defer cancel()
	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(events); got != errClassSpawnFailed {
		t.Fatalf("expected %s, got %q", errClassSpawnFailed, got)
	}
}

func TestExecuteSpawnFailed(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	fa.setSpawnFail(true)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(events); got != errClassSpawnFailed {
		t.Fatalf("expected %s, got %q", errClassSpawnFailed, got)
	}
}

func TestExecuteDisconnectMidStream(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", nil)
	fa.setDropOnFirst(true)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(events); got != errClassHostAgentDisconnected {
		t.Fatalf("expected %s, got %q", errClassHostAgentDisconnected, got)
	}
}

// TestExecuteSupervisorCancelSendsCancelFrame asserts the proxy relays a
// supervisor-side cancellation to the agent as a CANCEL DispatchFrame, so the
// agent can tear down the child's inner Execute stream rather than leaving it
// running until the child terminates.
func TestExecuteSupervisorCancelSendsCancelFrame(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", nil)
	fa.setStallData(true) // agent never answers the DATA frame
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))

	stream, err := client.Execute(ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Recv in the background so the RPC is live; it will error when we cancel.
	go func() { _, _ = stream.Recv() }()

	// Let the spawn + dispatch reach the stalling agent, then cancel.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-fa.canceled:
		// Good: the proxy relayed a CANCEL frame to the agent.
	case <-time.After(3 * time.Second):
		t.Fatalf("proxy did not send a CANCEL frame to the agent on supervisor cancel")
	}
}

func TestExecuteFetcherFallbackPopulatesCache(t *testing.T) {
	entry := &instanceCacheEntry{
		serviceBindings: map[string]bindingSpec{"codegen": {Path: "./c"}},
		ownerAPIKeyID:   "owner-1",
		params:          map[string]any{"cwd": "."},
	}
	ts := newProxyTestServer(t, staticFetcher("inst-1", entry))
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	events := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if terminalErrorClass(events) != "" {
		t.Fatalf("expected success via fetcher fallback, got error: %q", terminalErrorClass(events))
	}
	if _, ok := ts.state.lookupInstance("inst-1"); !ok {
		t.Fatalf("fetcher fallback should populate the cache")
	}
}

func TestExecutorObsCapabilitiesEmpty(t *testing.T) {
	h := newExecutorObsHandler()
	resp, err := h.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(resp.GetDeclaredEvents()) != 0 || len(resp.GetExpectedAttributesSchema()) != 0 {
		t.Fatalf("expected empty capability envelope")
	}
}
