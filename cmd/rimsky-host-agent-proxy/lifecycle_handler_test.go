// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestOnInstanceCreatedPopulatesCache(t *testing.T) {
	state := newProxyState()
	h := newLifecycleHandler(state, Config{ReapTimeout: time.Second})

	_, err := h.OnInstanceCreated(context.Background(), &genv1.OnInstanceCreatedRequest{
		InstanceId:            "inst-1",
		TargetRoutingIdentity: "owner-1",
		ServiceBindings:       []byte(`{"codegen":{"path":"./codegen"},"fs":{"path":"./fs"}}`),
		Params:                []byte(`{"cwd":"/work"}`),
	})
	if err != nil {
		t.Fatalf("OnInstanceCreated: %v", err)
	}
	entry, ok := state.lookupInstance("inst-1")
	if !ok {
		t.Fatalf("instance not cached")
	}
	if entry.targetRoutingIdentity != "owner-1" {
		t.Fatalf("routing identity mismatch: %q", entry.targetRoutingIdentity)
	}
	if entry.serviceBindings["codegen"].Path != "./codegen" {
		t.Fatalf("binding parse failed: %+v", entry.serviceBindings)
	}
	if entry.params["cwd"] != "/work" {
		t.Fatalf("params parse failed: %+v", entry.params)
	}
}

func TestOnRunScopeTerminalReapsSpawns(t *testing.T) {
	ts := newProxyTestServer(t, nil)
	fa := connectFakeAgent(t, ts, "owner-1", "", executorScript(t))
	cacheReadyInstance(ts, "inst-1", "owner-1", map[string]bindingSpec{"codegen": {Path: "./c"}})

	const runScopeID = "run-scope-xyz"
	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithCancel(callCtx("codegen"))
	defer cancel()
	_ = collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1", RunScopeId: runScopeID})

	spawnID, ok := ts.state.lookupSpawnByRunScopeBinding(runScopeID, "codegen")
	if !ok {
		t.Fatalf("expected a spawn keyed by run_scope_id before reap")
	}
	if _, instKeyed := ts.state.lookupSpawnByRunScopeBinding("inst-1", "codegen"); instKeyed {
		t.Fatalf("spawn must be keyed by run_scope_id, not instance id")
	}

	lc := genv1.NewLifecycleSubscriberClient(ts.supConn)
	if _, err := lc.OnRunScopeTerminal(context.Background(), &genv1.OnRunScopeTerminalRequest{
		RunScopeId: runScopeID,
		InstanceId: "inst-1",
	}); err != nil {
		t.Fatalf("OnRunScopeTerminal: %v", err)
	}

	if got := <-fa.reaped; got != spawnID {
		t.Fatalf("reaped wrong spawn: got %q want %q", got, spawnID)
	}

	if _, ok := ts.state.lookupSpawn(spawnID); ok {
		t.Fatalf("spawn should be dropped after reap")
	}
}

func TestOnRunScopeTerminalReapsConcurrently(t *testing.T) {
	state := newProxyState()
	h := newLifecycleHandler(state, Config{ReapTimeout: 3 * time.Second})

	conn := newAgentConnection("owner-1", "label", "")
	state.agents["owner-1"] = conn

	const scopeID = "scope-1"
	state.recordSpawn("spawn-1", "owner-1", scopeID, "codegen", nil, "")
	state.recordSpawn("spawn-2", "owner-1", scopeID, "fs", nil, "")

	done := make(chan struct{})
	go func() {
		_, _ = h.OnRunScopeTerminal(context.Background(), &genv1.OnRunScopeTerminalRequest{RunScopeId: scopeID})
		close(done)
	}()

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		frame := <-conn.sendCh
		reap := frame.GetReap()
		if reap == nil {
			t.Fatalf("expected Reap frame, got %T", frame.GetBody())
		}
		seen[reap.GetSpawnId()] = true
	}
	if !seen["spawn-1"] || !seen["spawn-2"] {
		t.Fatalf("expected reap frames for both spawns, got %v", seen)
	}

	for spawnID := range seen {
		conn.deliverReaped(&genv1.Reaped{SpawnId: spawnID})
	}

	<-done
}

func TestOnInstanceTerminatedEvictsCache(t *testing.T) {
	state := newProxyState()
	h := newLifecycleHandler(state, Config{ReapTimeout: time.Second})

	if _, err := h.OnInstanceCreated(context.Background(), &genv1.OnInstanceCreatedRequest{
		InstanceId:            "inst-1",
		TargetRoutingIdentity: "owner-1",
		ServiceBindings:       []byte(`{"codegen":{"path":"./codegen"}}`),
	}); err != nil {
		t.Fatalf("OnInstanceCreated: %v", err)
	}
	if _, ok := state.lookupInstance("inst-1"); !ok {
		t.Fatalf("instance not cached before termination")
	}

	if _, err := h.OnInstanceTerminated(context.Background(), &genv1.OnInstanceTerminatedRequest{
		InstanceId: "inst-1",
	}); err != nil {
		t.Fatalf("OnInstanceTerminated: %v", err)
	}

	if _, ok := state.lookupInstance("inst-1"); ok {
		t.Fatalf("instance cache entry should be evicted after termination")
	}
}

func TestNoOpLifecycleMethods(t *testing.T) {
	h := newLifecycleHandler(newProxyState(), Config{})
	ctx := context.Background()
	if _, err := h.OnTemplateRegistered(ctx, &genv1.OnTemplateRegisteredRequest{}); err != nil {
		t.Fatalf("OnTemplateRegistered: %v", err)
	}
	if _, err := h.OnTemplateDeployed(ctx, &genv1.OnTemplateDeployedRequest{}); err != nil {
		t.Fatalf("OnTemplateDeployed: %v", err)
	}
	if _, err := h.OnTemplateUndeployed(ctx, &genv1.OnTemplateUndeployedRequest{}); err != nil {
		t.Fatalf("OnTemplateUndeployed: %v", err)
	}
	if _, err := h.OnTemplateDeregistered(ctx, &genv1.OnTemplateDeregisteredRequest{}); err != nil {
		t.Fatalf("OnTemplateDeregistered: %v", err)
	}
	if _, err := h.OnInstanceTerminated(ctx, &genv1.OnInstanceTerminatedRequest{}); err != nil {
		t.Fatalf("OnInstanceTerminated: %v", err)
	}
}

func TestLocalHttpForwardRoundTrip(t *testing.T) {
	var gotBody atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotBody.Store(string(buf))
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("upstream-ack"))
	}))
	t.Cleanup(upstream.Close)

	state := newProxyState()
	state.recordSpawn("spawn-1", "owner-1", "inst-1", "codegen", nil, upstream.URL+"/v1/callback/ack-1")
	conn := newAgentConnection("owner-1", "label", "http://127.0.0.1:7777")

	fwd := newHTTPForwarder(state)
	fwd.handle(conn, &genv1.LocalHttpForward{
		ForwardId: "fwd-1",
		Method:    http.MethodPost,
		Url:       "http://127.0.0.1:7777/v1/callback/ack-1",
		Body:      []byte(`{"hello":"world"}`),
		SpawnId:   "spawn-1",
	})

	resp := (<-conn.sendCh).GetHttpResponse()
	if resp == nil {
		t.Fatal("expected LocalHttpResponse on the connection's send channel")
	}
	if resp.GetForwardId() != "fwd-1" {
		t.Fatalf("forward id mismatch: %q", resp.GetForwardId())
	}
	if resp.GetStatus() != http.StatusAccepted {
		t.Fatalf("status mismatch: %d", resp.GetStatus())
	}
	if string(resp.GetBody()) != "upstream-ack" {
		t.Fatalf("body mismatch: %q", resp.GetBody())
	}

	if gotBody.Load() != `{"hello":"world"}` {
		t.Fatalf("upstream did not receive forwarded body: %v", gotBody.Load())
	}
}

func TestLocalHttpForwardReusedSpawnPerCallbackPath(t *testing.T) {
	var gotPath atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(upstream.Close)

	state := newProxyState()
	state.recordSpawn("spawn-1", "owner-1", "inst-1", "codegen", nil, upstream.URL+"/v1/callback/ack-1")
	conn := newAgentConnection("owner-1", "label", "http://127.0.0.1:7777")
	fwd := newHTTPForwarder(state)

	fwd.handle(conn, &genv1.LocalHttpForward{
		ForwardId: "fwd-2",
		Method:    http.MethodPost,
		Url:       "http://127.0.0.1:7777/v1/callback/ack-2",
		SpawnId:   "spawn-1",
	})

	<-conn.sendCh

	if got := gotPath.Load(); got != "/v1/callback/ack-2" {
		t.Fatalf("callback routed to wrong ack path: got %v, want /v1/callback/ack-2", got)
	}
}

func TestLocalHttpForwardNoSpawn(t *testing.T) {
	state := newProxyState()
	conn := newAgentConnection("owner-1", "label", "")
	fwd := newHTTPForwarder(state)
	fwd.handle(conn, &genv1.LocalHttpForward{ForwardId: "fwd-1", SpawnId: "unknown"})

	if resp := (<-conn.sendCh).GetHttpResponse(); resp.GetStatus() != http.StatusBadGateway {
		t.Fatalf("expected BadGateway for unknown spawn, got %d", resp.GetStatus())
	}
}
