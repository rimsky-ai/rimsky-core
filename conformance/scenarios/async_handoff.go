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
		Name:          "async_handoff",
		RequiresAsync: true,
		Run:           runAsyncHandoff,
	})
}

// runAsyncHandoff asserts the executor can emit an AsyncAccepted terminal
// when prompted via userdata.probe_async. The downstream callback flow is
// outside conformance scope; we only validate the executor's emission.
func runAsyncHandoff(ctx context.Context, c executor.Client) error {
	ud, _ := structpb.NewStruct(map[string]any{"probe_async": true})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-probe-async", Userdata: ud,
	}
	stream, err := c.Execute(ctx, req)
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	defer stream.Close()

	sawAsync := false
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if sawAsync {
				break
			}
			return fmt.Errorf("recv: %w", err)
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_AsyncAccepted); ok {
			sawAsync = true
			continue
		}
		if isTerminal(ev) && !sawAsync {
			return fmt.Errorf("expected AsyncAccepted, got %T", ev.Event)
		}
	}
	if !sawAsync {
		return errors.New("stream ended without AsyncAccepted")
	}
	return nil
}
