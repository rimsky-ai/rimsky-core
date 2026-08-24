// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type deadlineHonoringHandler struct{}

func (deadlineHonoringHandler) Execute(ctx context.Context, _ *genv1.ExecuteRequest, _ HandlerContext) (*genv1.Outcome, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestInProcessClient_PropagatesCallerCancellationToTheHandler(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	executed := make(chan error, 1)
	go func() {
		_, _, execErr := client.Execute(ctx, req)
		executed <- execErr
	}()
	cancel()

	if err = <-executed; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled — the handler blocks on the caller's context and the client returns its error", err)
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
