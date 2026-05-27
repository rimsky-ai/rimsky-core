// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

func TestOnInstanceCreatedPopulatesCache(t *testing.T) {
	state := newProxyState()
	h := newLifecycleHandler(state, Config{ReapTimeout: time.Second})

	_, err := h.OnInstanceCreated(context.Background(), &genv1.OnInstanceCreatedRequest{
		InstanceId:      "inst-1",
		OwnerApiKeyId:   "owner-1",
		ServiceBindings: []byte(`{"codegen":{"path":"./codegen"},"fs":{"path":"./fs"}}`),
		Params:          []byte(`{"cwd":"/work"}`),
	})
	if err != nil {
		t.Fatalf("OnInstanceCreated: %v", err)
	}
	entry, ok := state.lookupInstance("inst-1")
	if !ok {
		t.Fatalf("instance not cached")
	}
	if entry.ownerAPIKeyID != "owner-1" {
		t.Fatalf("owner mismatch: %q", entry.ownerAPIKeyID)
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

	// Drive one Execute to spawn the child (scope == instance id).
	client := genv1.NewExecutorClient(ts.supConn)
	ctx, cancel := context.WithTimeout(callCtx("codegen"), 5*time.Second)
	defer cancel()
	_ = collectExecute(t, client, ctx, &genv1.ExecuteRequest{InstanceId: "inst-1"})

	spawnID, ok := ts.state.lookupSpawnByRunScopeBinding("inst-1", "codegen")
	if !ok {
		t.Fatalf("expected a spawn before reap")
	}

	// Fire the run-scope-terminal lifecycle event with a real run-scope id
	// distinct from the instance id. The proxy keys spawns by instance id
	// (its v1 dispatch-observable scope), so the reap must match on
	// instance_id — NOT run_scope_id. Passing a run-scope id that differs
	// from the instance id is exactly what production does (control-api and
	// the supervisor both pass a real run-scope id ≠ instance id), so this
	// guards against the divergence where the reap silently matched nothing.
	lc := genv1.NewLifecycleSubscriberClient(ts.supConn)
	if _, err := lc.OnRunScopeTerminal(context.Background(), &genv1.OnRunScopeTerminalRequest{
		RunScopeId: "run-scope-xyz",
		InstanceId: "inst-1",
	}); err != nil {
		t.Fatalf("OnRunScopeTerminal: %v", err)
	}

	// The agent should have received a Reap for the spawn.
	select {
	case got := <-fa.reaped:
		if got != spawnID {
			t.Fatalf("reaped wrong spawn: got %q want %q", got, spawnID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("agent did not receive a Reap")
	}

	// The spawn should be gone from state.
	if _, ok := ts.state.lookupSpawn(spawnID); ok {
		t.Fatalf("spawn should be dropped after reap")
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
	// Upstream supervisor callback receiver.
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
	// Record a spawn whose original callback is the upstream server.
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

	// The forwarder enqueued a LocalHttpResponse onto the connection's
	// sendCh; drain it.
	select {
	case frame := <-conn.sendCh:
		resp := frame.GetHttpResponse()
		if resp == nil {
			t.Fatalf("expected LocalHttpResponse, got %T", frame.GetBody())
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
	case <-time.After(3 * time.Second):
		t.Fatalf("no LocalHttpResponse enqueued")
	}

	if gotBody.Load() != `{"hello":"world"}` {
		t.Fatalf("upstream did not receive forwarded body: %v", gotBody.Load())
	}
}

// TestLocalHttpForwardReusedSpawnPerCallbackPath guards the multi-dispatch
// callback-routing fix: a spawn that serves more than one dispatch in a
// run-scope records its supervisor callback base once (at first dispatch),
// but the supervisor builds a distinct /v1/callback/{ack_id} path per
// dispatch. The forwarder must un-rewrite to the supervisor host + the
// *current* forward's path — not the path baked into the recorded callback —
// so the second dispatch's callback lands on its own ack-id, not the first's.
func TestLocalHttpForwardReusedSpawnPerCallbackPath(t *testing.T) {
	var gotPath atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(upstream.Close)

	state := newProxyState()
	// The spawn's recorded callback encodes ack-1 (the FIRST dispatch's path).
	state.recordSpawn("spawn-1", "owner-1", "inst-1", "codegen", nil, upstream.URL+"/v1/callback/ack-1")
	conn := newAgentConnection("owner-1", "label", "http://127.0.0.1:7777")
	fwd := newHTTPForwarder(state)

	// A SECOND dispatch on the same spawn fires a callback for ack-2. The
	// child POSTed to the agent's local listener at /v1/callback/ack-2; the
	// forward carries that path.
	fwd.handle(conn, &genv1.LocalHttpForward{
		ForwardId: "fwd-2",
		Method:    http.MethodPost,
		Url:       "http://127.0.0.1:7777/v1/callback/ack-2",
		SpawnId:   "spawn-1",
	})

	select {
	case <-conn.sendCh: // drain the LocalHttpResponse
	case <-time.After(3 * time.Second):
		t.Fatalf("no LocalHttpResponse enqueued")
	}

	if got := gotPath.Load(); got != "/v1/callback/ack-2" {
		t.Fatalf("callback routed to wrong ack path: got %v, want /v1/callback/ack-2", got)
	}
}

func TestLocalHttpForwardNoSpawn(t *testing.T) {
	state := newProxyState()
	conn := newAgentConnection("owner-1", "label", "")
	fwd := newHTTPForwarder(state)
	fwd.handle(conn, &genv1.LocalHttpForward{ForwardId: "fwd-1", SpawnId: "unknown"})

	select {
	case frame := <-conn.sendCh:
		resp := frame.GetHttpResponse()
		if resp.GetStatus() != http.StatusBadGateway {
			t.Fatalf("expected BadGateway for unknown spawn, got %d", resp.GetStatus())
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no response for unknown spawn")
	}
}

func TestUnimplementedHandlers(t *testing.T) {
	ctx := context.Background()
	if _, err := newUnimplementedValidation().Validate(ctx, &genv1.ValidateRequest{}); status_(err) == "" {
		t.Fatalf("validation should be unimplemented")
	}
}

// status_ returns the gRPC code string of err, or "" if OK.
func status_(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
