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

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	conformance "github.com/fallguyconsulting/rimsky/sdk/go/conformance/executor"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name: "stream_close_without_terminal",
		Run:  runStreamCloseWithoutTerminal,
	})
}

// runStreamCloseWithoutTerminal asserts that an Execute stream MUST emit a
// terminal StreamClose event before EOF. If the stream closes cleanly with
// zero terminals, the executor violates spec §7.2. (For async executors a
// StreamClose with outcome AwaitAsyncCallback IS the gRPC-side terminal —
// the spec is satisfied at the gRPC layer.)
func runStreamCloseWithoutTerminal(ctx context.Context, env conformance.Env) error {
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
			break
		}
		if err != nil {
			if sawTerminal {
				return nil
			}
			return fmt.Errorf("recv before terminal: %w", err)
		}
		if conformance.IsTerminal(ev) {
			sawTerminal = true
		}
	}
	if !sawTerminal {
		return errors.New("spec §7.2 violated: stream closed with EOF but no terminal event")
	}
	return nil
}
