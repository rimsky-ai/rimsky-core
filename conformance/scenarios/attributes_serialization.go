package scenarios

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/conformance"
	"github.com/fallguy/rimsky/modeling/executor"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
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
// `Complete.attributes_delta` (a *structpb.Struct) instead of the removed
// `Complete.result` (a *structpb.Value); the top-level value must therefore
// be a JSON object — list / scalar stub_response payloads are no longer
// first-class for this scenario.
func runAttributesSerialization(ctx context.Context, c executor.Client) error {
	expected := map[string]any{
		"nested": map[string]any{
			"list": []any{float64(1), 2.5, "x", true, nil},
		},
	}
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true, "stub_response": expected})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-probe", Userdata: ud,
	}
	stream, err := c.Execute(ctx, req)
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	defer stream.Close()

	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("recv: %w", err)
		}
		if ce, ok := ev.Event.(*genv1.ExecuteEvent_Complete); ok {
			got := ce.Complete.GetAttributesDelta().AsMap()
			if !reflect.DeepEqual(got, expected) {
				return fmt.Errorf("attributes_delta mismatch: got=%#v want=%#v", got, expected)
			}
			return nil
		}
		if er, ok := ev.Event.(*genv1.ExecuteEvent_Errored); ok {
			return fmt.Errorf("unexpected Errored: class=%s", er.Errored.ErrorClass)
		}
	}
	return errors.New("stream ended without Complete terminal")
}
