// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type fakeHandler struct {
	fn func(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error)
}

func (f *fakeHandler) Execute(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error) {
	return f.fn(ctx, req, hctx)
}

func newFreshRequest(t *testing.T) *genv1.ExecuteRequest {
	t.Helper()
	return &genv1.ExecuteRequest{
		NodeId:     uuid.New().String(),
		DispatchId: uuid.New().String(),
	}
}

func successOutcome() *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{Changed: true}}}
}

func TestInProcessClient_HappyPath(t *testing.T) {
	const url = "inproc://test-happy"
	reg := NewInProcessRegistry()
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error) {
		return successOutcome(), nil
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	outcome, err := client.Execute(context.Background(), newFreshRequest(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("expected Success outcome, got %+v", outcome)
	}
}

func TestInProcessClient_HandlerPanicSurfacesAsError(t *testing.T) {
	const url = "inproc://test-panic"
	reg := NewInProcessRegistry()
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error) {
		panic("handler exploded")
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	if _, err := client.Execute(context.Background(), newFreshRequest(t)); err == nil {
		t.Fatalf("expected handler-panic error, got nil")
	}
}

func TestInProcessClient_HandlerErrorSurfaces(t *testing.T) {
	const url = "inproc://test-err"
	reg := NewInProcessRegistry()
	want := errors.New("kaboom")
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error) {
		return nil, want
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	_, err = client.Execute(context.Background(), newFreshRequest(t))
	if err == nil {
		t.Fatalf("expected handler error, got nil")
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected handler error %v, got %v", want, err)
	}
}

func TestInProcessClient_ScratchOnSuccessReachesConsumer(t *testing.T) {
	const url = "inproc://test-scratch"
	reg := NewInProcessRegistry()
	scratch := []byte("opaque-bytes")
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error) {
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{Scratch: scratch}}}, nil
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	outcome, err := client.Execute(context.Background(), newFreshRequest(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := outcome.GetSuccess().GetScratch()
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
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error) {
		return successOutcome(), nil
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
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error) {
		return successOutcome(), nil
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
	if err := reg.Register(url, &fakeHandler{fn: func(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error) {
		if hctx.Scratch != nil {
			cap.gotScratchWriter = true
		}
		return successOutcome(), nil
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	factory := HandlerContextFactory(func(_ context.Context, nodeRunID, nodeID shared.UUID) HandlerContext {
		if nodeRunID == (shared.UUID{}) || nodeID == (shared.UUID{}) {
			t.Errorf("expected non-zero typed UUIDs, dispatch=%v node=%v", nodeRunID, nodeID)
		}
		return HandlerContext{Scratch: &ScratchWriter{}}
	})
	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: url}, reg, factory)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	if _, err := client.Execute(context.Background(), newFreshRequest(t)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !cap.gotScratchWriter {
		t.Fatalf("expected handler to receive non-nil ScratchWriter from factory")
	}
}
