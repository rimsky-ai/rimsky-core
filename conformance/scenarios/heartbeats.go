package scenarios

import (
	"context"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/conformance"
	"github.com/fallguy/rimsky/modeling/executor"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name:         "heartbeats",
		RequiresStub: true,
		Run:          runHeartbeats,
	})
}

// runHeartbeats hints at an executor-side delay and asserts at least one
// Heartbeat event is seen before terminal. For Plan C v1, reference stub
// executors skip delays — so in practice this may still PASS via the opening
// heartbeat emitted by http-node, but the requirement is only "≥1 heartbeat".
func runHeartbeats(ctx context.Context, c executor.Client) error {
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true, "delay_ms": 500})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-probe", Userdata: ud,
	}
	stream, err := c.Execute(ctx, req)
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	defer stream.Close()

	sawHeartbeat := false
	sawTerminal := false
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if sawTerminal {
				break
			}
			return fmt.Errorf("recv: %w", err)
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_Heartbeat); ok {
			sawHeartbeat = true
			continue
		}
		if isTerminal(ev) {
			sawTerminal = true
		}
	}
	if !sawHeartbeat {
		return errors.New("no heartbeat event observed before terminal")
	}
	return nil
}
