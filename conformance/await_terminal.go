// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/fallguy/rimsky/modeling/executor"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// AwaitTerminal reads from the gRPC stream until it sees a terminal event:
// Complete, Blocked, Errored, AsyncAccepted, or ParkRequested.
//
// When the gRPC terminal is AsyncAccepted AND env.Callbacks is configured,
// AwaitTerminal extracts the executor-minted async_ack_id, registers it with
// the receiver, and waits on the resulting channel for the eventual callback
// POST. It returns a synthesized terminal ExecuteEvent (Complete, Blocked,
// Errored, or ParkRequested) instead of the AsyncAccepted bridge event.
//
// AwaitTerminal returns the gRPC AsyncAccepted as-is when env.Callbacks is
// nil or no callback arrives within the context's deadline.
func AwaitTerminal(ctx context.Context, stream executor.EventStream, env Env) (*genv1.ExecuteEvent, error) {
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("stream ended without terminal event")
		}
		if err != nil {
			return nil, fmt.Errorf("recv: %w", err)
		}
		if !IsTerminal(ev) {
			continue
		}
		async, isAsync := ev.Event.(*genv1.ExecuteEvent_AsyncAccepted)
		if !isAsync {
			return ev, nil
		}
		if env.Callbacks == nil {
			return ev, nil
		}
		ackID := async.AsyncAccepted.GetAsyncAckId()
		if ackID == "" {
			return nil, errors.New("AsyncAccepted with empty async_ack_id; cannot route callback")
		}
		ch := env.Callbacks.Register(ackID)
		select {
		case cbEv := <-ch:
			if cbEv == nil {
				return ev, nil
			}
			return cbEv, nil
		case <-ctx.Done():
			return nil, fmt.Errorf("await callback for %s: %w", ackID, ctx.Err())
		}
	}
}

// IsTerminal reports whether ev is a terminal event per spec §7.
func IsTerminal(ev *genv1.ExecuteEvent) bool {
	switch ev.Event.(type) {
	case *genv1.ExecuteEvent_Complete,
		*genv1.ExecuteEvent_Blocked,
		*genv1.ExecuteEvent_Errored,
		*genv1.ExecuteEvent_AsyncAccepted,
		*genv1.ExecuteEvent_ParkRequested:
		return true
	}
	return false
}
