// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unit coverage for the in-process executor's sync_rpc_deadline path
// (TD-three-dispatch-deadlines). The deadline is applied by
// runner_dispatch.go before calling client.Execute; this test pins
// that an InProcessClient passes the deadlined ctx through to the
// handler unchanged, so a handler that honors ctx.Err() reflects the
// deadline as context.DeadlineExceeded.

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

// deadlineHonoringHandler blocks until ctx is canceled, then returns
// ctx.Err(). Models a well-behaved in-process executor that respects
// the supervisor's sync_rpc_deadline.
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

	dispatchID := shared.UUID(uuid.New()).String()
	nodeID := shared.UUID(uuid.New()).String()
	req := &genv1.ExecuteRequest{
		DispatchId: dispatchID,
		NodeId:     nodeID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = client.Execute(ctx, req)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("elapsed = %v, want < 1s (handler should have honored 50ms deadline)", elapsed)
	}
}
