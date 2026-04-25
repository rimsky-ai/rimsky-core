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
		Name: "stream_close_without_terminal",
		Run:  runStreamCloseWithoutTerminal,
	})
}

// runStreamCloseWithoutTerminal asserts that an Execute stream MUST emit a
// terminal event before EOF. If the stream closes cleanly with zero terminals,
// the executor violates spec §7.2.
func runStreamCloseWithoutTerminal(ctx context.Context, c executor.Client) error {
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-probe", Userdata: ud,
	}
	stream, err := c.Execute(ctx, req)
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
		if isTerminal(ev) {
			sawTerminal = true
		}
	}
	if !sawTerminal {
		return errors.New("spec §7.2 violated: stream closed with EOF but no terminal event")
	}
	return nil
}
