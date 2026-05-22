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

	"github.com/fallguy/rimsky/conformance"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name: "terminal_is_last",
		Run:  runTerminalIsLast,
	})
}

// runTerminalIsLast asserts that after a terminal StreamClose event (with any
// outcome: Success, Error, AwaitAsyncCallback, or Park) the next Recv()
// returns io.EOF — no more events follow. Per spec §7.2, terminals close the
// stream. This is gRPC-stream-only: a StreamClose with outcome
// AwaitAsyncCallback is itself a gRPC terminal, and the eventual callback POST
// is a separate transport.
func runTerminalIsLast(ctx context.Context, env conformance.Env) error {
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
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

	sawTerminal := false
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if !sawTerminal {
				return errors.New("EOF before any terminal event")
			}
			return nil
		}
		if err != nil {
			if sawTerminal {
				return nil // some transports surface a non-EOF close; acceptable post-terminal
			}
			return fmt.Errorf("recv: %w", err)
		}
		if sawTerminal {
			return fmt.Errorf("event received after terminal: %T", ev.Event)
		}
		if conformance.IsTerminal(ev) {
			sawTerminal = true
		}
	}
}
