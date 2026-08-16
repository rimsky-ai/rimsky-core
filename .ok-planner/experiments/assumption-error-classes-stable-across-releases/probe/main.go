package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

func main() {
	addr := flag.String("addr", "", "host:port of the executor")
	flag.Parse()

	cc, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", *addr, err)
		os.Exit(3)
	}
	defer func() { _ = cc.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	caps, err := genv1.NewExecutorObservabilityClient(cc).Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "capabilities: %v\n", err)
		os.Exit(4)
	}
	out, err := protojson.Marshal(caps)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(5)
	}
	fmt.Println(string(out))
}
