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
	"github.com/fallguy/rimsky/modeling/executor"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name: "terminal_is_last",
		Run:  runTerminalIsLast,
	})
}

// runTerminalIsLast asserts that after a terminal event (Complete, Blocked,
// Errored, or AsyncAccepted), the next Recv() returns io.EOF — no more events
// follow. Per spec §7.2, terminals close the stream.
func runTerminalIsLast(ctx context.Context, c executor.Client) error {
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-probe", Userdata: ud,
	}
	stream, err := c.Execute(ctx, req)
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
		if isTerminal(ev) {
			sawTerminal = true
		}
	}
}
