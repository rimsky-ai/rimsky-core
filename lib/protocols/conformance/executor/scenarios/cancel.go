// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: executor
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "cancel",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{"probe_cancel": true})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "cancel",
				InstanceId:  "cancel",
				NodeType:    "conformance",
				Attributes:  attrs,
				CallbackUrl: env.Callbacks.URL(),
			}
			observed := env.Callbacks.Register("cancel-observed")
			acknowledged := env.Callbacks.Register("cancel-acknowledged")

			execCtx, cancelExec := context.WithCancel(ctx)
			defer cancelExec()
			done := make(chan error, 1)
			go func() {
				_, execErr := env.Client.Execute(execCtx, req)
				done <- execErr
			}()

			select {
			case <-observed:
			case execErr := <-done:
				return fmt.Errorf("Execute settled with %v before signaling it had started the cancel probe; "+
					"expected the executor to be mid-flight when cancellation is issued", execErr)
			case <-ctx.Done():
				return fmt.Errorf("timed out waiting for the executor to signal it started the cancel probe")
			}

			cancelExec()

			select {
			case <-acknowledged:
			case <-ctx.Done():
				return fmt.Errorf("executor never acknowledged observing cancellation after the client canceled mid-flight")
			}

			execErr := <-done
			if execErr == nil {
				return fmt.Errorf("Execute returned nil error after mid-flight cancellation; the executor must surface cancellation on the RPC")
			}
			if status.Code(execErr) != codes.Canceled && !errors.Is(execErr, context.Canceled) {
				return fmt.Errorf("Execute returned a non-cancellation error after mid-flight cancellation: %w", execErr)
			}
			return nil
		},
	})
}
