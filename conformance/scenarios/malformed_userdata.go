// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/conformance"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name: "malformed_userdata",
		// Stub-mode-only: a non-stub claude-agent run would actually
		// spawn the LLM CLI before any heuristic could detect the
		// malformed-userdata markers. Same gate applied to
		// `attributes_serialization` and `heartbeats`.
		RequiresStub: true,
		Run:          runMalformedUserdata,
	})
}

// runMalformedUserdata sends userdata that should fail validation for any
// conforming executor (missing url, empty stub_response not applied, etc.)
// and asserts an Errored terminal with some error class. AwaitTerminal
// transparently follows the callback for async executors.
//
// Reserved-key contract: scenario authors MUST use `_`-prefixed keys
// (`_invalid`, `_missing_url`, …) for intentional malformed-shape
// markers. The `_` prefix is reserved across executors so plain field
// names (which a real template author might use legitimately) cannot
// silently trip the rejection heuristic. Keep this list aligned with
// `executors/claude-agent/src/agent-run.ts::malformedUserdataReason`.
func runMalformedUserdata(ctx context.Context, env conformance.Env) error {
	ud, _ := structpb.NewStruct(map[string]any{
		"_invalid":     map[string]any{"nested_null": nil},
		"_missing_url": true,
	})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-malformed", Userdata: ud,
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
	if er, ok := ev.Event.(*genv1.ExecuteEvent_Errored); ok {
		if er.Errored.ErrorClass == "" {
			return errors.New("Errored terminal had empty error_class")
		}
		return nil
	}
	return fmt.Errorf("expected Errored, got %T", ev.Event)
}
