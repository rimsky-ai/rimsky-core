// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/fallguyconsulting/rimsky/protocols/conformance/executor"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name: "cancel",
		Run:  runCancel,
	})
}

// runCancel cancels the Execute context after 200ms and verifies the stream
// terminates within a reasonable window without panicking or hanging.
func runCancel(parentCtx context.Context, env conformance.Env) error {
	ctx, cancel := context.WithCancel(parentCtx)
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-probe", Attributes: ud,
		CallbackUrl: env.Callbacks.URL(),
	}
	stream, err := env.Client.Execute(ctx, req)
	if err != nil {
		cancel()
		return fmt.Errorf("execute: %w", err)
	}

	// Schedule cancel after 200ms.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		defer stream.Close()
		for {
			if _, err := stream.Recv(); err != nil {
				done <- nil
				return
			}
		}
	}()

	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		cancel()
		return fmt.Errorf("stream did not terminate within 5s after cancel")
	}
}
