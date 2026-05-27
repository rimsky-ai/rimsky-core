// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package stub

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// listenForTest mirrors stubtest.Listen but lives inside package stub so
// the package's own tests can call it without an import cycle.
func listenForTest(t testing.TB, s *Stub) (*grpc.Server, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, s)
	RegisterObservability(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })
	return srv, lis.Addr().String()
}

func dial(t *testing.T, addr string) genv1.ExecutorClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return genv1.NewExecutorClient(conn)
}

func drain(t *testing.T, stream grpc.ServerStreamingClient[genv1.ExecuteEvent]) []*genv1.ExecuteEvent {
	t.Helper()
	var out []*genv1.ExecuteEvent
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out = append(out, ev)
	}
}

func TestScriptedComplete(t *testing.T) {
	s := New()
	s.WhenType("t.complete").Success(map[string]any{"ok": true}, true, "did the thing")
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.complete"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].GetHeartbeat())
	comp := events[1].GetStreamClose().GetSuccess()
	require.NotNil(t, comp)
	require.True(t, comp.GetChanged())
	require.Equal(t, "did the thing", comp.GetChangeSummary())
	// AttributesDelta replaced the legacy `Result` field per spec §12.2.
	require.Equal(t, true, comp.GetAttributesDelta().AsMap()["ok"])
}

func TestScriptedError(t *testing.T) {
	s := New()
	s.WhenType("t.err").Error("CONFIG", map[string]any{"hint": "bad"})
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.err"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].GetHeartbeat())
	e := events[1].GetStreamClose().GetError()
	require.NotNil(t, e)
	require.Equal(t, "stub/CONFIG", e.GetErrorClass())
	require.Equal(t, "bad", e.GetPayload().AsMap()["hint"])
}

func TestScriptedBlocked(t *testing.T) {
	s := New()
	s.WhenType("t.blk").Error("executor_blocked", map[string]any{
		"reason": "waiting for review",
		"ticket": "Z-1",
	})
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.blk"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].GetHeartbeat())
	b := events[1].GetStreamClose().GetError()
	require.NotNil(t, b)
	// Executor-blocked path: Error with the stub-prefixed class
	// `stub/executor_blocked`. Tests construct the payload inline
	// (reason + any context).
	require.Equal(t, "stub/executor_blocked", b.GetErrorClass())
	pl := b.GetPayload().AsMap()
	require.Equal(t, "waiting for review", pl["reason"])
	require.Equal(t, "Z-1", pl["ticket"])
}

func TestScriptedAwaitAsyncCallback(t *testing.T) {
	s := New()
	s.WhenType("t.async").AwaitAsyncCallback("ack-123", 5000)
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.async"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].GetHeartbeat())
	a := events[1].GetStreamClose().GetAwaitAsync()
	require.NotNil(t, a)
	require.Equal(t, "ack-123", a.GetAsyncAckId())
	require.Equal(t, int64(5000), a.GetExpectedCompletionMs())
}

func TestHeartbeatsCount(t *testing.T) {
	s := New()
	s.WhenType("t.hb").Success(nil, false, "").Heartbeats(3)
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.hb"})
	require.NoError(t, err)
	events := drain(t, stream)
	// 1 default heartbeat + 3 extra + terminal = 5
	require.Len(t, events, 5)
	for i := 0; i < 4; i++ {
		require.NotNil(t, events[i].GetHeartbeat(), "event %d should be heartbeat", i)
	}
	require.NotNil(t, events[4].GetStreamClose().GetSuccess())
}

func TestDelayRespectsContextCancellation(t *testing.T) {
	s := New()
	s.WhenType("t.slow").Success(nil, false, "").Delay(500 * time.Millisecond)
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	stream, err := c.Execute(ctx, &genv1.ExecuteRequest{NodeType: "t.slow"})
	require.NoError(t, err)
	// Expect the stream to error out (deadline/cancel) rather than produce a full sequence.
	start := time.Now()
	var gotTerminal bool
	for {
		ev, err := stream.Recv()
		if err != nil {
			break
		}
		if ev.GetStreamClose().GetSuccess() != nil || ev.GetStreamClose().GetError() != nil || ev.GetStreamClose().GetError() != nil || ev.GetStreamClose().GetAwaitAsync() != nil {
			gotTerminal = true
		}
	}
	require.False(t, gotTerminal, "terminal should not be emitted before cancellation")
	require.Less(t, time.Since(start), 400*time.Millisecond, "should return quickly after cancellation")
}

func TestUnknownNodeTypeReturnsError(t *testing.T) {
	s := New()
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.unknown"})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no script for node_type")
}

// TestObservedRequestCapturesAttributes verifies the stub records the
// dispatch-time `attributes` field per spec §12.1 so supervisor /
// scenario tests can assert that rimsky wired the unified attribute
// bag through (post-2026-05-21 userdata-collapse: the wire-level
// `userdata` field is gone).
func TestObservedRequestCapturesAttributes(t *testing.T) {
	s := New()
	s.WhenType("t.obs").Success(map[string]any{}, false, "")
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	attrs, err := structpb.NewStruct(map[string]any{
		"items":    []any{"a", "b"},
		"count":    2,
		"endpoint": "https://example.test/api",
	})
	require.NoError(t, err)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{
		NodeId:     "n-1",
		InstanceId: "i-1",
		NodeType:   "t.obs",
		Attributes: attrs,
	})
	require.NoError(t, err)
	_ = drain(t, stream)

	obs := s.Observed()
	require.Len(t, obs, 1)
	require.Equal(t, "n-1", obs[0].NodeID)
	require.Equal(t, "i-1", obs[0].InstanceID)
	require.Equal(t, "t.obs", obs[0].NodeType)
	require.Equal(t, float64(2), obs[0].Attributes["count"])
	require.Equal(t, []any{"a", "b"}, obs[0].Attributes["items"])
	require.Equal(t, "https://example.test/api", obs[0].Attributes["endpoint"])
}

// TestStubModeReturnsImmediateComplete verifies stub mode short-circuits to
// a single terminal StreamClose with a Success outcome (no heartbeat, no
// scripted terminal) carrying attributes_delta sourced from
// StubAttributesFor and changed=true.
func TestStubModeReturnsImmediateComplete(t *testing.T) {
	s := New().EnableStubMode()
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	// Known fixture node_type: stub returns the fixture map.
	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "items.fetch"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 1)
	comp := events[0].GetStreamClose().GetSuccess()
	require.NotNil(t, comp)
	require.True(t, comp.GetChanged())
	require.Equal(t, "stub", comp.GetChangeSummary())
	require.Equal(t, []any{}, comp.GetAttributesDelta().AsMap()["items"])
	require.Equal(t, "1970-01-01T00:00:00Z", comp.GetAttributesDelta().AsMap()["fetched_at"])
}

// TestStubModeUnknownTypeReturnsEmptyDelta verifies stub mode tolerates
// unknown node_types — StubAttributesFor returns `{}` and the executor
// emits a StreamClose with a Success outcome carrying an empty
// attributes_delta object.
func TestStubModeUnknownTypeReturnsEmptyDelta(t *testing.T) {
	s := New().EnableStubMode()
	_, addr := listenForTest(t, s)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.never.heard.of"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 1)
	comp := events[0].GetStreamClose().GetSuccess()
	require.NotNil(t, comp)
	require.True(t, comp.GetChanged())
	require.Empty(t, comp.GetAttributesDelta().AsMap())
}

// TestStubAttributesForReturnsCopy verifies callers can mutate the returned
// map without affecting subsequent calls — the fixtures map is the source
// of truth and must not leak.
func TestStubAttributesForReturnsCopy(t *testing.T) {
	first := StubAttributesFor("items.fetch")
	first["fetched_at"] = "polluted"
	first["items"] = []any{"x"}
	second := StubAttributesFor("items.fetch")
	require.Equal(t, "1970-01-01T00:00:00Z", second["fetched_at"])
	require.Equal(t, []any{}, second["items"])
}

// TestStubAttributesForUnknownReturnsEmptyMap documents the contract in
// the StubAttributesFor godoc: unknown node_types return a non-nil empty
// map so the caller can convert it to an empty Struct without a nil-check.
func TestStubAttributesForUnknownReturnsEmptyMap(t *testing.T) {
	got := StubAttributesFor("does-not-exist")
	require.NotNil(t, got)
	require.Empty(t, got)
}
