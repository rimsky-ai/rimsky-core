// The dashboard side of the executor-observability protocol: a standalone
// client whose only rimsky dependency is the permissively licensed protocols
// module, built the same way as the permissive-peer-build experiment's peer.
//
// Verbs:
//
//	caps   <endpoint>                -> the executor's advertised capabilities
//	stream <endpoint> <dispatch_id>  -> one JSON line per live trace event
//	get    <endpoint> <dispatch_id>  -> the whole trace record as JSON
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func dial(endpoint string) genv1.ExecutorObservabilityClient {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", endpoint, err)
		os.Exit(2)
	}
	return genv1.NewExecutorObservabilityClient(conn)
}

func marshal(m proto.Message) string {
	out, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(2)
	}
	return string(out)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: trace-client caps|stream|get <endpoint> [dispatch_id]")
		os.Exit(2)
	}
	verb, endpoint := os.Args[1], os.Args[2]
	client := dial(endpoint)
	ctx := context.Background()

	switch verb {
	case "caps":
		caps, err := client.Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Capabilities: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(marshal(caps))
	case "get":
		trace, err := client.GetTrace(ctx, &genv1.GetTraceRequest{DispatchId: os.Args[3]})
		if err != nil {
			fmt.Fprintf(os.Stderr, "GetTrace: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(marshal(trace))
	case "stream":
		stream, err := client.StreamTrace(ctx, &genv1.StreamTraceRequest{DispatchId: os.Args[3]})
		if err != nil {
			fmt.Fprintf(os.Stderr, "StreamTrace: %v\n", err)
			os.Exit(1)
		}
		for {
			ev, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "StreamTrace recv: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(marshal(ev))
			_ = os.Stdout.Sync()
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown verb %q\n", verb)
		os.Exit(2)
	}
}
