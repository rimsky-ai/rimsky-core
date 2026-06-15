// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// fakeHandler is a test-only InProcessHandler whose Execute body is
// driven by a caller-provided closure.
type fakeHandler struct {
	fn func(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error
}

func (f *fakeHandler) Execute(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error {
	return f.fn(ctx, req, sink, hctx)
}

func newFreshRequest(t *testing.T) *genv1.ExecuteRequest {
	t.Helper()
	return &genv1.ExecuteRequest{
		NodeId:     uuid.New().String(),
		DispatchId: uuid.New().String(),
	}
}

func TestInProcessClient_HappyPathStreamsAndEOFs(t *testing.T) {
	const url = "inproc://test-happy"
	reg := NewInProcessRegistry()
	hb := &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_NamedEvent{NamedEvent: &genv1.NamedEvent{Name: "tick"}}}
	close1 := &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{Changed: true}}}}}
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error {
		if err := sink.Send(hb); err != nil {
			return err
		}
		return sink.Send(close1)
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	stream, err := client.Execute(context.Background(), newFreshRequest(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	ev1, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv 1: %v", err)
	}
	if ev1.GetNamedEvent().GetName() != "tick" {
		t.Fatalf("expected NamedEvent name=tick, got %+v", ev1)
	}
	ev2, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv 2: %v", err)
	}
	if ev2.GetStreamClose().GetSuccess() == nil {
		t.Fatalf("expected Success StreamClose, got %+v", ev2)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after handler return, got %v", err)
	}
}

// TestInProcessClient_HandlerPanicSurfacesAsError pins the goroutine
// panic-recovery contract: a handler that panics MUST surface a
// non-nil error to the consumer's Recv (after the channel closes) and
// MUST NOT wedge the consumer or crash the supervisor. Without the
// recover-deferred + close(errCh) in the goroutine, a panic would
// close ch but leave errCh open-and-never-sent, and the consumer
// (inprocEventStream.Recv) would block forever on the errCh read
// after seeing EOF on ch.
func TestInProcessClient_HandlerPanicSurfacesAsError(t *testing.T) {
	const url = "inproc://test-panic"
	reg := NewInProcessRegistry()
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error {
		panic("handler exploded")
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	stream, err := client.Execute(context.Background(), newFreshRequest(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatalf("expected handler-panic error from Recv, got nil")
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("expected non-EOF panic error from Recv, got io.EOF")
	}
}

func TestInProcessClient_HandlerErrorSurfacesAfterClose(t *testing.T) {
	const url = "inproc://test-err"
	reg := NewInProcessRegistry()
	want := errors.New("kaboom")
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error {
		return want
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	stream, err := client.Execute(context.Background(), newFreshRequest(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatalf("expected handler error from Recv, got nil")
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected handler error %v, got %v", want, err)
	}
}

func TestInProcessClient_ScratchOnSuccessReachesConsumer(t *testing.T) {
	const url = "inproc://test-scratch"
	reg := NewInProcessRegistry()
	scratch := []byte("opaque-bytes")
	close1 := &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{Scratch: scratch}}}}}
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error {
		return sink.Send(close1)
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	stream, err := client.Execute(context.Background(), newFreshRequest(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	ev, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	got := ev.GetStreamClose().GetSuccess().GetScratch()
	if string(got) != string(scratch) {
		t.Fatalf("expected scratch %q, got %q", scratch, got)
	}
}

func TestNewInProcessClient_RejectsUnregisteredURL(t *testing.T) {
	reg := NewInProcessRegistry()
	_, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: "inproc://nope"}, reg, nil)
	if err == nil {
		t.Fatalf("expected error for unregistered URL, got nil")
	}
}

func TestNewInProcessClient_RejectsNilRegistry(t *testing.T) {
	_, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: "inproc://anything"}, nil, nil)
	if err == nil {
		t.Fatalf("expected error for nil registry, got nil")
	}
}

func TestNewInProcessClient_RejectsWrongTransport(t *testing.T) {
	reg := NewInProcessRegistry()
	_, err := NewInProcessClient(Endpoint{Transport: "grpc", URL: "host:1234"}, reg, nil)
	if err == nil {
		t.Fatalf("expected error for wrong transport, got nil")
	}
}

func TestInProcessClient_ParsesTypedUUIDsAtBoundary(t *testing.T) {
	const url = "inproc://test-uuid"
	reg := NewInProcessRegistry()
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error {
		return sink.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{}}}}})
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	bad := &genv1.ExecuteRequest{NodeId: uuid.New().String(), DispatchId: "not-a-uuid"}
	if _, err := client.Execute(context.Background(), bad); err == nil {
		t.Fatalf("expected parse error for malformed dispatch_id, got nil")
	}
	bad2 := &genv1.ExecuteRequest{NodeId: "still-not-a-uuid", DispatchId: uuid.New().String()}
	if _, err := client.Execute(context.Background(), bad2); err == nil {
		t.Fatalf("expected parse error for malformed node_id, got nil")
	}
}

func TestInProcessClient_PoolWiresInprocCase(t *testing.T) {
	const url = "inproc://test-pool"
	reg := NewInProcessRegistry()
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error {
		return sink.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{}}}}})
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	pool := NewClientPoolWithInProcess(reg, nil)
	c, err := pool.GetOrCreate(Endpoint{Transport: "inproc", URL: url})
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if c == nil {
		t.Fatalf("expected non-nil client")
	}
	c2, err := pool.GetOrCreate(Endpoint{Transport: "inproc", URL: url})
	if err != nil {
		t.Fatalf("GetOrCreate cached: %v", err)
	}
	if c != c2 {
		t.Fatalf("expected pool to cache inproc client by key")
	}
}

func TestClientPool_InprocRequiresRegistry(t *testing.T) {
	pool := NewClientPool()
	if _, err := pool.GetOrCreate(Endpoint{Transport: "inproc", URL: "inproc://x"}); err == nil {
		t.Fatalf("expected error for inproc without registry, got nil")
	}
}

func TestInProcessClient_HandlerContextFactoryReceivesTypedIDs(t *testing.T) {
	const url = "inproc://test-hctx"
	reg := NewInProcessRegistry()
	type capture struct {
		gotScratchWriter bool
	}
	cap := &capture{}
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error {
		if hctx.Scratch != nil {
			cap.gotScratchWriter = true
		}
		return sink.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{}}}}})
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	factory := HandlerContextFactory(func(dispatchID, nodeID shared.UUID) HandlerContext {
		if dispatchID == (shared.UUID{}) || nodeID == (shared.UUID{}) {
			t.Errorf("expected non-zero typed UUIDs, dispatch=%v node=%v", dispatchID, nodeID)
		}
		return HandlerContext{Scratch: &ScratchWriter{}}
	})
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, factory)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	stream, err := client.Execute(context.Background(), newFreshRequest(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv 1: %v", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv EOF: %v", err)
	}
	if !cap.gotScratchWriter {
		t.Fatalf("expected handler to receive non-nil ScratchWriter from factory")
	}
}
