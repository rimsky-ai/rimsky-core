// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package stub

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

func listenForTest(t testing.TB, s *Stub) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, s)
	RegisterObservability(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })
	return lis.Addr().String()
}

func dial(t *testing.T, addr string) genv1.ExecutorClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return genv1.NewExecutorClient(conn)
}

func TestScripted_Success(t *testing.T) {
	s := New()
	s.WhenType("t.complete").Success(map[string]any{"ok": true}, true, "did the thing")
	addr := listenForTest(t, s)
	c := dial(t, addr)

	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.complete"})
	require.NoError(t, err)
	success := outcome.GetSuccess()
	require.NotNil(t, success, "expected Success outcome")
	require.True(t, success.GetChanged())
	require.Equal(t, "did the thing", success.GetChangeSummary())
	require.Equal(t, true, success.GetAttributesDelta().AsMap()["ok"])
}

func TestScripted_Error(t *testing.T) {
	s := New()
	s.WhenType("t.err").Error("CONFIG", map[string]any{"hint": "bad"})
	addr := listenForTest(t, s)
	c := dial(t, addr)

	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.err"})
	require.NoError(t, err)
	e := outcome.GetError()
	require.NotNil(t, e, "expected Error outcome")
	require.Equal(t, "stub/CONFIG", e.GetErrorClass())
	require.Equal(t, "bad", e.GetPayload().AsMap()["hint"])
}

func TestScripted_ErrorHierarchicalClassPassesThrough(t *testing.T) {
	s := New()
	s.WhenType("t.err2").Error("executor_blocked/quota", nil)
	addr := listenForTest(t, s)
	c := dial(t, addr)

	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.err2"})
	require.NoError(t, err)
	require.Equal(t, "executor_blocked/quota", outcome.GetError().GetErrorClass())
}

func TestScripted_Park(t *testing.T) {
	s := New()
	resumeAt := time.Now().Add(time.Hour)
	s.WhenType("t.park").Park(resumeAt)
	addr := listenForTest(t, s)
	c := dial(t, addr)

	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.park"})
	require.NoError(t, err)
	park := outcome.GetPark()
	require.NotNil(t, park, "expected Park outcome")
	require.NotNil(t, park.GetResumeAt())
	require.True(t, park.GetResumeAt().AsTime().Equal(resumeAt), "resume_at should round-trip")
}

func TestScripted_AwaitAsyncCallback(t *testing.T) {
	s := New()
	s.WhenType("t.async").AwaitAsyncCallback("ack-123", 5000)
	addr := listenForTest(t, s)
	c := dial(t, addr)

	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.async"})
	require.NoError(t, err)
	a := outcome.GetAwaitAsync()
	require.NotNil(t, a, "expected AwaitAsyncCallback outcome")
	require.Equal(t, "ack-123", a.GetAsyncAckId())
	require.Equal(t, int64(5000), a.GetExpectedCompletionMs())
}

func TestScripted_Tags(t *testing.T) {
	s := New()
	s.WhenType("t.tags").Success(nil, true, "").Tags("alpha", "beta")
	addr := listenForTest(t, s)
	c := dial(t, addr)

	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.tags"})
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta"}, outcome.GetSuccess().GetTags())
}

func TestDelayRespectsContextCancellation(t *testing.T) {
	s := New()
	s.WhenType("t.slow").Success(nil, false, "").Delay(500 * time.Millisecond)
	addr := listenForTest(t, s)
	c := dial(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.Execute(ctx, &genv1.ExecuteRequest{NodeType: "t.slow"})
	require.Error(t, err)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err),
		"the delay must abandon on the caller's cancellation rather than run to completion; got %v", err)
}

func TestUnknownNodeTypeReturnsError(t *testing.T) {
	s := New()
	addr := listenForTest(t, s)
	c := dial(t, addr)

	_, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.unknown"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no script for node_type")
}

func TestObservedRequestCapturesAttributes(t *testing.T) {
	s := New()
	s.WhenType("t.obs").Success(map[string]any{}, false, "")
	addr := listenForTest(t, s)
	c := dial(t, addr)

	attrs, err := structpb.NewStruct(map[string]any{
		"items":    []any{"a", "b"},
		"count":    2,
		"endpoint": "https://example.test/api",
	})
	require.NoError(t, err)

	_, err = c.Execute(context.Background(), &genv1.ExecuteRequest{
		NodeId:     "n-1",
		InstanceId: "i-1",
		NodeType:   "t.obs",
		Attributes: attrs,
	})
	require.NoError(t, err)

	obs := s.Observed()
	require.Len(t, obs, 1)
	require.Equal(t, "n-1", obs[0].NodeID)
	require.Equal(t, "i-1", obs[0].InstanceID)
	require.Equal(t, "t.obs", obs[0].NodeType)
	require.Equal(t, float64(2), obs[0].Attributes["count"])
	require.Equal(t, []any{"a", "b"}, obs[0].Attributes["items"])
	require.Equal(t, "https://example.test/api", obs[0].Attributes["endpoint"])
}

func TestObservedRequest_CapturesCandidateHandles(t *testing.T) {
	s := New()
	s.WhenType("t.handles").Success(nil, false, "")
	addr := listenForTest(t, s)
	c := dial(t, addr)

	_, err := c.Execute(context.Background(), &genv1.ExecuteRequest{
		NodeType: "t.handles",
		ClaimProducers: map[string]*genv1.ClaimProducerHandle{
			"primary": {CandidateHandle: []byte("ch-1")},
			"shadow":  {CandidateHandle: []byte("ch-2")},
		},
	})
	require.NoError(t, err)

	obs := s.Observed()
	require.Len(t, obs, 1)
	require.Equal(t, []byte("ch-1"), obs[0].CandidateHandles["primary"])
	require.Equal(t, []byte("ch-2"), obs[0].CandidateHandles["shadow"])
}

func TestStubModeReturnsImmediateComplete(t *testing.T) {
	s := New().EnableStubMode()
	addr := listenForTest(t, s)
	c := dial(t, addr)

	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "items.fetch"})
	require.NoError(t, err)
	success := outcome.GetSuccess()
	require.NotNil(t, success, "expected Success outcome in stub mode")
	require.True(t, success.GetChanged())
	require.Equal(t, "stub", success.GetChangeSummary())
	require.Equal(t, []any{}, success.GetAttributesDelta().AsMap()["items"])
	require.Equal(t, "1970-01-01T00:00:00Z", success.GetAttributesDelta().AsMap()["fetched_at"])
}

func TestStubModeUnknownTypeReturnsEmptyDelta(t *testing.T) {
	s := New().EnableStubMode()
	addr := listenForTest(t, s)
	c := dial(t, addr)

	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "completely.unknown"})
	require.NoError(t, err)
	success := outcome.GetSuccess()
	require.NotNil(t, success)
	require.True(t, success.GetChanged())
	require.Equal(t, map[string]any{}, success.GetAttributesDelta().AsMap())
}

func TestStubMode_ParkProbeReturnsPark(t *testing.T) {
	s := New().EnableStubMode()
	addr := listenForTest(t, s)
	c := dial(t, addr)

	resumeAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	attrs, _ := structpb.NewStruct(map[string]any{
		"probe_park":     true,
		"park_resume_at": resumeAt.Format(time.RFC3339Nano),
	})
	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{
		NodeType:   "any",
		Attributes: attrs,
	})
	require.NoError(t, err)
	park := outcome.GetPark()
	require.NotNil(t, park, "expected Park outcome in stub mode under probe_park")
	require.NotNil(t, park.GetResumeAt(), "probe_park must emit resume_at")
	require.True(t, park.GetResumeAt().AsTime().Equal(resumeAt), "resume_at should round-trip")
}

func TestThenAdvancesQueuePerCall(t *testing.T) {
	s := New()
	s.WhenType("t.seq").
		Success(map[string]any{"n": 1}, true, "one").
		Then().Success(map[string]any{"n": 2}, true, "two").
		Then().Success(map[string]any{"n": 3}, true, "three")
	addr := listenForTest(t, s)
	c := dial(t, addr)

	for i, want := range []float64{1, 2, 3, 3, 3} {
		outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.seq"})
		require.NoError(t, err, "call %d", i)
		got := outcome.GetSuccess().GetAttributesDelta().AsMap()["n"]
		require.Equal(t, want, got, "call %d: queue exhausts to last, then repeats", i)
	}
}

func TestWhenTypeResetsQueue(t *testing.T) {
	s := New()
	s.WhenType("t.reset").
		Success(map[string]any{"phase": "a"}, true, "").
		Then().Success(map[string]any{"phase": "b"}, true, "")
	addr := listenForTest(t, s)
	c := dial(t, addr)

	_, _ = c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.reset"})
	s.WhenType("t.reset").Success(map[string]any{"phase": "fresh"}, true, "")

	outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.reset"})
	require.NoError(t, err)
	require.Equal(t, "fresh", outcome.GetSuccess().GetAttributesDelta().AsMap()["phase"])
}

func TestHoldUntilBlocksUntilSignal(t *testing.T) {
	hold := make(chan struct{})
	s := New()
	s.WhenType("t.hold").Success(nil, true, "").HoldUntil(hold)
	addr := listenForTest(t, s)
	c := dial(t, addr)

	done := make(chan *genv1.Outcome, 1)
	go func() {
		outcome, err := c.Execute(context.Background(), &genv1.ExecuteRequest{NodeType: "t.hold"})
		require.NoError(t, err)
		done <- outcome
	}()

	awaited.Until(t, "the dispatch to block inside the stub's hold", func() bool { return s.Holding() == 1 })

	select {
	case <-done:
		t.Fatal("Execute returned before hold released")
	default:
	}

	close(hold)

	outcome := <-done
	require.NotNil(t, outcome.GetSuccess())
}
