package stub

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

func dial(t *testing.T, addr string) genv1.NodeExecutorClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return genv1.NewNodeExecutorClient(conn)
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
	s.WhenType("t.complete").Complete(map[string]any{"ok": true}, true, "did the thing")
	_, addr := s.Listen(t)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.complete"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].GetHeartbeat())
	comp := events[1].GetComplete()
	require.NotNil(t, comp)
	require.True(t, comp.GetChanged())
	require.Equal(t, "did the thing", comp.GetChangeSummary())
	require.Equal(t, true, comp.GetResult().GetStructValue().AsMap()["ok"])
}

func TestScriptedError(t *testing.T) {
	s := New()
	s.WhenType("t.err").Error("CONFIG", map[string]any{"hint": "bad"})
	_, addr := s.Listen(t)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.err"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].GetHeartbeat())
	e := events[1].GetErrored()
	require.NotNil(t, e)
	require.Equal(t, "CONFIG", e.GetErrorClass())
	require.Equal(t, "bad", e.GetPayload().GetStructValue().AsMap()["hint"])
}

func TestScriptedBlocked(t *testing.T) {
	s := New()
	s.WhenType("t.blk").Blocked("waiting for review", map[string]any{"ticket": "Z-1"})
	_, addr := s.Listen(t)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.blk"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].GetHeartbeat())
	b := events[1].GetBlocked()
	require.NotNil(t, b)
	require.Equal(t, "waiting for review", b.GetReason())
	require.Equal(t, "Z-1", b.GetContext().GetStructValue().AsMap()["ticket"])
}

func TestScriptedAsyncAccepted(t *testing.T) {
	s := New()
	s.WhenType("t.async").AsyncAccepted("ack-123", 5000)
	_, addr := s.Listen(t)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.async"})
	require.NoError(t, err)
	events := drain(t, stream)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].GetHeartbeat())
	a := events[1].GetAsyncAccepted()
	require.NotNil(t, a)
	require.Equal(t, "ack-123", a.GetAsyncAckId())
	require.Equal(t, int64(5000), a.GetExpectedCompletionMs())
}

func TestHeartbeatsCount(t *testing.T) {
	s := New()
	s.WhenType("t.hb").Complete(nil, false, "").Heartbeats(3)
	_, addr := s.Listen(t)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.hb"})
	require.NoError(t, err)
	events := drain(t, stream)
	// 1 default heartbeat + 3 extra + terminal = 5
	require.Len(t, events, 5)
	for i := 0; i < 4; i++ {
		require.NotNil(t, events[i].GetHeartbeat(), "event %d should be heartbeat", i)
	}
	require.NotNil(t, events[4].GetComplete())
}

func TestDelayRespectsContextCancellation(t *testing.T) {
	s := New()
	s.WhenType("t.slow").Complete(nil, false, "").Delay(500 * time.Millisecond)
	_, addr := s.Listen(t)
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
		if ev.GetComplete() != nil || ev.GetErrored() != nil || ev.GetBlocked() != nil || ev.GetAsyncAccepted() != nil {
			gotTerminal = true
		}
	}
	require.False(t, gotTerminal, "terminal should not be emitted before cancellation")
	require.Less(t, time.Since(start), 400*time.Millisecond, "should return quickly after cancellation")
}

func TestUnknownNodeTypeReturnsError(t *testing.T) {
	s := New()
	_, addr := s.Listen(t)
	c := dial(t, addr)

	stream, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.unknown"})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no script for node_type")
}
