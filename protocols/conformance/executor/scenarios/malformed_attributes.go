// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/protocols/conformance/executor"
	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name: "malformed_attributes",
		// Stub-mode-only: a non-stub claude-agent run would actually
		// spawn the LLM CLI before any heuristic could detect the
		// malformed-attributes markers. Same gate applied to
		// `attributes_serialization` and `heartbeats`.
		RequiresStub: true,
		Run:          runMalformedAttributes,
	})
}

// runMalformedAttributes sends attributes that should fail validation for any
// conforming executor (missing url, empty stub_response not applied, etc.)
// and asserts a terminal StreamClose with an Error outcome carrying some
// error class. AwaitTerminal transparently follows the callback for async
// executors.
//
// Reserved-key contract: scenario authors MUST use `_`-prefixed keys
// (`_invalid`, `_missing_url`, …) for intentional malformed-shape
// markers. The `_` prefix is reserved across executors so plain field
// names (which a real template author might use legitimately) cannot
// silently trip the rejection heuristic.
func runMalformedAttributes(ctx context.Context, env conformance.Env) error {
	ud, _ := structpb.NewStruct(map[string]any{
		"_invalid":     map[string]any{"nested_null": nil},
		"_missing_url": true,
	})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-malformed", Attributes: ud,
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
		return fmt.Errorf("expected StreamClose, got %T", ev.Event)
	}
	er, ok := sc.StreamClose.Outcome.(*genv1.StreamClose_Error)
	if !ok {
		return fmt.Errorf("expected Error outcome, got %T", sc.StreamClose.Outcome)
	}
	if er.Error.ErrorClass == "" {
		return errors.New("Error outcome had empty error_class")
	}
	return nil
}
