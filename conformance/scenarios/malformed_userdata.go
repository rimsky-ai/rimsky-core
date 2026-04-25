package scenarios

import (
	"context"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/conformance"
	"github.com/fallguy/rimsky/core/executor"
	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name: "malformed_userdata",
		Run:  runMalformedUserdata,
	})
}

// runMalformedUserdata sends userdata that should fail validation for any
// conforming executor (missing url, empty stub_response not applied, etc.)
// and asserts an Errored terminal with some error class.
func runMalformedUserdata(ctx context.Context, c executor.Client) error {
	ud, _ := structpb.NewStruct(map[string]any{
		"_invalid":     map[string]any{"nested_null": nil},
		"missing_url":  true,
	})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-malformed", Userdata: ud,
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
		if er, ok := ev.Event.(*genv1.ExecuteEvent_Errored); ok {
			if er.Errored.ErrorClass == "" {
				return errors.New("Errored terminal had empty error_class")
			}
			return nil
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_Complete); ok {
			return errors.New("expected Errored but saw Complete for malformed userdata")
		}
	}
	return errors.New("stream ended without Errored terminal for malformed userdata")
}
