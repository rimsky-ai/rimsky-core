// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: executor
func init() {
	conformance.Register(conformance.Scenario{
		Name: "cancel",
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{"probe_async": true})
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
			cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			outcome, err := env.Client.Execute(cctx, req)
			if err != nil {
				if cctx.Err() == nil {
					return fmt.Errorf("Execute returned non-cancel error: %w", err)
				}
				return nil
			}
			if outcome == nil {
				return fmt.Errorf("Execute returned nil outcome with nil error after cancel")
			}
			return nil
		},
	})
}
