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

	conformance "github.com/rimsky-ai/rimsky-core/protocols/conformance/executor"
	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name:         "heartbeats",
		RequiresStub: true,
		Run:          runHeartbeats,
	})
}

// runHeartbeats hints at an executor-side delay and asserts at least one
// Heartbeat event is seen before terminal. For Plan C v1, reference stub
// executors skip delays — so in practice this may still PASS via the opening
// heartbeat emitted by http-node, but the requirement is only "≥1 heartbeat".
// This scenario is gRPC-stream-only: the heartbeat must appear on the stream
// before the terminal StreamClose (whether the outcome is AwaitAsyncCallback
// or a synchronous Success/Error/Park).
func runHeartbeats(ctx context.Context, env conformance.Env) error {
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true, "delay_ms": 500})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-probe", Attributes: ud,
		CallbackUrl: env.Callbacks.URL(),
	}
	stream, err := env.Client.Execute(ctx, req)
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	defer stream.Close()

	sawHeartbeat := false
	sawTerminal := false
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if sawTerminal {
				break
			}
			return fmt.Errorf("recv: %w", err)
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_Heartbeat); ok {
			sawHeartbeat = true
			continue
		}
		if conformance.IsTerminal(ev) {
			sawTerminal = true
		}
	}
	if !sawHeartbeat {
		return errors.New("no heartbeat event observed before terminal")
	}
	return nil
}
