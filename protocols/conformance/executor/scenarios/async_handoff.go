// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/fallguyconsulting/rimsky/protocols/conformance/executor"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name:          "async_handoff",
		RequiresAsync: true,
		Run:           runAsyncHandoff,
	})
}

// runAsyncHandoff asserts the executor can emit an AwaitAsyncCallback
// terminal StreamClose outcome when prompted via attributes.probe_async,
// AND that the executor follows through with a callback POST resolving
// to a real terminal verdict (Success / Error / Park) at the
// conformance receiver.
func runAsyncHandoff(ctx context.Context, env conformance.Env) error {
	ud, _ := structpb.NewStruct(map[string]any{"probe_async": true})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-probe-async", Attributes: ud,
		CallbackUrl: env.Callbacks.URL(),
	}
	stream, err := env.Client.Execute(ctx, req)
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	defer stream.Close()

	var asyncAckID string
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if asyncAckID != "" {
				break
			}
			return fmt.Errorf("recv: %w", err)
		}
		if sc, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose); ok {
			if aw, ok := sc.StreamClose.Outcome.(*genv1.StreamClose_AwaitAsync); ok {
				asyncAckID = aw.AwaitAsync.GetAsyncAckId()
				continue
			}
			if asyncAckID == "" {
				return fmt.Errorf("expected AwaitAsyncCallback, got %T", sc.StreamClose.Outcome)
			}
		}
	}
	if asyncAckID == "" {
		return errors.New("stream ended without AwaitAsyncCallback")
	}
	ch := env.Callbacks.Register(asyncAckID)
	select {
	case cbEv := <-ch:
		if cbEv == nil {
			return errors.New("callback channel closed without delivering a terminal")
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("await callback for %s: %w", asyncAckID, ctx.Err())
	}
}
