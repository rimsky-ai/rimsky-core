// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package executor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type deadlineHonoringHandler struct{}

func (deadlineHonoringHandler) Execute(ctx context.Context, _ *genv1.ExecuteRequest, _ HandlerContext) (*genv1.Outcome, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		t := &genv1.Outcome_Success{Success: &genv1.Success{Changed: false}}
		return &genv1.Outcome{Outcome: t}, nil
	}
}

func TestInProcessClient_HonorsCallerDeadline(t *testing.T) {
	t.Parallel()
	reg := NewInProcessRegistry()
	if regErr := reg.Register("inproc://test/slow", deadlineHonoringHandler{}); regErr != nil {
		t.Fatalf("Register: %v", regErr)
	}

	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: "inproc://test/slow"}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}

	nodeRunID := shared.UUID(uuid.New()).String()
	nodeID := shared.UUID(uuid.New()).String()
	req := &genv1.ExecuteRequest{
		DispatchId: nodeRunID,
		NodeId:     nodeID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err = client.Execute(ctx, req)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("elapsed = %v, want < 1s (handler should have honored 50ms deadline)", elapsed)
	}
}

type ctxIgnoringHandler struct {
	started chan struct{}
	release chan struct{}
}

func (h *ctxIgnoringHandler) Execute(_ context.Context, _ *genv1.ExecuteRequest, _ HandlerContext) (*genv1.Outcome, error) {
	close(h.started)
	<-h.release
	t := &genv1.Outcome_Success{Success: &genv1.Success{Changed: false}}
	return &genv1.Outcome{Outcome: t}, nil
}

func TestInProcessClient_CtxIgnoringHandler_CallerUnblocksOnCancel(t *testing.T) {
	t.Parallel()
	reg := NewInProcessRegistry()
	h := &ctxIgnoringHandler{started: make(chan struct{}), release: make(chan struct{})}
	if regErr := reg.Register("inproc://test/ignores-ctx", h); regErr != nil {
		t.Fatalf("Register: %v", regErr)
	}
	defer close(h.release)

	client, err := NewInProcessClient(Endpoint{Transport: "inproc", URL: "inproc://test/ignores-ctx"}, reg, nil)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}

	nodeRunID := shared.UUID(uuid.New()).String()
	nodeID := shared.UUID(uuid.New()).String()
	req := &genv1.ExecuteRequest{
		DispatchId: nodeRunID,
		NodeId:     nodeID,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type execResult struct {
		err error
	}
	resCh := make(chan execResult, 1)
	go func() {
		_, _, execErr := client.Execute(ctx, req)
		resCh <- execResult{err: execErr}
	}()

	<-h.started
	cancel()

	res := <-resCh
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled (caller must unblock on ctx cancellation even though the handler never observes ctx.Done)", res.err)
	}
}
