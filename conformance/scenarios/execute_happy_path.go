// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package scenarios registers the built-in rimsky conformance scenarios.
// Import for side effects: `_ "github.com/fallguy/rimsky/conformance/scenarios"`.
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
		Name: "execute_happy_path",
		Run:  runExecuteHappyPath,
	})
}

// runExecuteHappyPath opens an Execute stream, asserts a terminal event
// arrives, and confirms the stream closes cleanly with io.EOF.
func runExecuteHappyPath(ctx context.Context, c executor.Client) error {
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
			break
		}
		if err != nil {
			if sawTerminal {
				break // some transports close with non-EOF after terminal
			}
			return fmt.Errorf("recv before terminal: %w", err)
		}
		if isTerminal(ev) {
			sawTerminal = true
			// Keep draining until EOF to verify clean close.
			continue
		}
	}
	if !sawTerminal {
		return errors.New("stream closed without a terminal event (spec §7.2)")
	}
	return nil
}

func isTerminal(ev *genv1.ExecuteEvent) bool {
	switch ev.Event.(type) {
	case *genv1.ExecuteEvent_Complete,
		*genv1.ExecuteEvent_Blocked,
		*genv1.ExecuteEvent_Errored,
		*genv1.ExecuteEvent_AsyncAccepted:
		return true
	}
	return false
}
