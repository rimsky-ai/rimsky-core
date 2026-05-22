// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package scenarios registers the built-in rimsky conformance scenarios.
// Import for side effects: `_ "github.com/fallguy/rimsky/conformance/scenarios"`.
package scenarios

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/conformance"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name: "execute_happy_path",
		Run:  runExecuteHappyPath,
	})
}

// runExecuteHappyPath opens an Execute stream and asserts a terminal
// StreamClose with outcome Success / Error / Park arrives. For async
// executors AwaitTerminal follows the callback POST to the conformance
// receiver and surfaces the synthesized terminal in place of the
// AwaitAsyncCallback bridge event.
func runExecuteHappyPath(ctx context.Context, env conformance.Env) error {
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

	ev, err := conformance.AwaitTerminal(ctx, stream, env)
	if err != nil {
		return err
	}
	sc, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose)
	if !ok {
		return fmt.Errorf("unexpected terminal type: %T", ev.Event)
	}
	switch sc.StreamClose.Outcome.(type) {
	case *genv1.StreamClose_Success, *genv1.StreamClose_Error, *genv1.StreamClose_Park:
		return nil
	case *genv1.StreamClose_AwaitAsync:
		return fmt.Errorf("happy-path outcome was AwaitAsyncCallback but no callback arrived to resolve it")
	}
	return fmt.Errorf("unexpected StreamClose outcome: %T", sc.StreamClose.Outcome)
}
