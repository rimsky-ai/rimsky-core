// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	"github.com/fallguyconsulting/rimsky/runtime/executor"
)

// AwaitTerminal reads from the gRPC stream until it sees the terminal
// StreamClose event. StreamClose carries one of four outcome variants:
// Success, Error (with error_class — "executor_blocked" is the post-E.2
// collapsed-Blocked path), AwaitAsyncCallback, or Park.
//
// When the gRPC outcome is AwaitAsyncCallback AND env.Callbacks is
// configured, AwaitTerminal extracts the executor-minted async_ack_id,
// registers it with the receiver, and waits on the resulting channel for
// the eventual callback POST. It returns a synthesized terminal
// ExecuteEvent (Success, Error, or Park) instead of the
// AwaitAsyncCallback bridge event.
//
// AwaitTerminal returns the gRPC AwaitAsyncCallback as-is when
// env.Callbacks is nil or no callback arrives within the context's
// deadline.
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
		sc, isStreamClose := ev.Event.(*genv1.ExecuteEvent_StreamClose)
		if !isStreamClose {
			return ev, nil
		}
		await, isAsync := sc.StreamClose.Outcome.(*genv1.StreamClose_AwaitAsync)
		if !isAsync {
			return ev, nil
		}
		if env.Callbacks == nil {
			return ev, nil
		}
		ackID := await.AwaitAsync.GetAsyncAckId()
		if ackID == "" {
			return nil, errors.New("AwaitAsyncCallback with empty async_ack_id; cannot route callback")
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

// IsTerminal reports whether ev is the stream-close terminal event per
// the post-2026-05-12 protocol shape. Only ExecuteEvent_StreamClose is
// terminal; the legacy per-terminal-type discriminants collapsed into
// the outcome oneof on StreamClose.
func IsTerminal(ev *genv1.ExecuteEvent) bool {
	_, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose)
	return ok
}
