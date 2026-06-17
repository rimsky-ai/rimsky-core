// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// executor_handler_test.go — supervisor-facing Executor handler tests
// under the unary RPC shape (TD-execute-rpc-unary). The handler resolves
// the binding from the cache, dispatches to the agent over the
// HostAgent.Connect stream, awaits a single DispatchFrame carrying a
// marshaled Outcome, and returns it. Proxy-side failures surface as
// Outcome{Error{error_class}}.

package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// cacheReadyInstance survives from the pre-rewrite executor_handler
// test harness so the sibling protocol-handler tests
// (claim_producer_handler_test.go / lifecycle_handler_test.go) can
// still seed the instance cache.
func cacheReadyInstance(ts *proxyTestServer, instanceID, owner string, bindings map[string]bindingSpec) {
	ts.state.cacheInstance(instanceID, bindings, owner, map[string]any{"cwd": "."})
}

// executorScript returns a dispatchHandler that answers each relayed
// ExecuteRequest with a single serialized Outcome{Success}.
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

// collectExecute drives a unary Execute against the proxy and returns
// the settling Outcome.
func collectExecute(t *testing.T, client genv1.ExecutorClient, ctx context.Context, req *genv1.ExecuteRequest) *genv1.Outcome {
	t.Helper()
	outcome, err := client.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return outcome
}

// terminalErrorClass returns the error_class on a settling Outcome, or "".
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
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
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

	// @constraint: a spawn was recorded with the original callback URL.
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

// TestExecuteErrorPreserved verifies that an Outcome{Error} returned by
// the child is relayed end-to-end (proxy doesn't mask it as a
// proxy-synthesized error_class).
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
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// @constraint: no service-name metadata header → binding_not_found.
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassBindingNotFound {
		t.Fatalf("expected %s, got %q", errClassBindingNotFound, got)
	}
}

func TestExecuteInstanceNotFound(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	connectFakeAgent(t, ts, "owner-1", "", executorScript(t))

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
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
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
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
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
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
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassSpawnFailed {
		t.Fatalf("expected %s, got %q", errClassSpawnFailed, got)
	}
}

func TestExecuteSpawnTimeout(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	fa.setSpawnDelay(3 * time.Second) // @constraint: exceeds the 2s spawnTimeout configured in the harness
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 8*time.Second)
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassSpawnFailed {
		t.Fatalf("expected %s, got %q", errClassSpawnFailed, got)
	}
}

func TestExecuteDisconnectMidDispatch(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", nil)
	fa.setDropOnFirst(true)
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if got := terminalErrorClass(outcome); got != errClassHostAgentDisconnected {
		t.Fatalf("expected %s, got %q", errClassHostAgentDisconnected, got)
	}
}

// TestExecuteSupervisorCancelSendsCancelFrame asserts the proxy relays a
// supervisor-side cancellation to the agent as a CANCEL DispatchFrame.
func TestExecuteSupervisorCancelSendsCancelFrame(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", nil)
	fa.setStallData(true) // @deliberate: agent never answers the DATA frame
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))

	// @deliberate: kick off Execute in background; it will return when ctx is cancelled.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.Execute(ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	}()

	// @deliberate: Let the spawn + dispatch reach the stalling agent, then cancel.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-fa.canceled:
	case <-time.After(3 * time.Second):
		t.Fatalf("proxy did not send a CANCEL frame to the agent on supervisor cancel")
	}
	<-done
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
	outcome := collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})
	if terminalErrorClass(outcome) != "" {
		t.Fatalf("expected success via fetcher fallback, got error: %q", terminalErrorClass(outcome))
	}
	if _, ok := ts.state.lookupInstance("inst-1"); !ok {
		t.Fatalf("fetcher fallback should populate the cache")
	}
}
