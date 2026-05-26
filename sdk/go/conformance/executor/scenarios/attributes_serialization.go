// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	conformance "github.com/fallguyconsulting/rimsky/sdk/go/conformance/executor"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name:         "attributes_serialization",
		RequiresStub: true,
		Run:          runAttributesSerialization,
	})
}

// runAttributesSerialization verifies structured terminal-final attribute
// writeback (nested maps, lists, mixed scalar types) round-trips through the
// protocol unchanged across encoder boundaries (proto wire ↔ HTTP+JSON
// bridge). After the §12 protocol rewrite the scenario exercises
// `StreamClose.Success.attributes_delta` (a *structpb.Struct) instead of the
// removed standalone `Complete.result` (a *structpb.Value); the top-level
// value must therefore be a JSON object — list / scalar stub_response
// payloads are no longer first-class for this scenario. AwaitTerminal handles
// async executors by following the callback after the AwaitAsyncCallback
// outcome.
func runAttributesSerialization(ctx context.Context, env conformance.Env) error {
	expected := map[string]any{
		"nested": map[string]any{
			"list": []any{float64(1), 2.5, "x", true, nil},
		},
	}
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true, "stub_response": expected})
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
		return fmt.Errorf("unexpected terminal: %T", ev.Event)
	}
	switch oc := sc.StreamClose.Outcome.(type) {
	case *genv1.StreamClose_Success:
		got := oc.Success.GetAttributesDelta().AsMap()
		if !reflect.DeepEqual(got, expected) {
			return fmt.Errorf("attributes_delta mismatch: got=%#v want=%#v", got, expected)
		}
		return nil
	case *genv1.StreamClose_Error:
		return fmt.Errorf("unexpected Error: class=%s", oc.Error.ErrorClass)
	}
	return fmt.Errorf("unexpected StreamClose outcome: %T", sc.StreamClose.Outcome)
}
