package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/core/executor"
	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

func main() {
	endpoint := flag.String("endpoint", "", "executor endpoint URL")
	transport := flag.String("transport", "grpc", "grpc | http")
	timeout := flag.Duration("timeout", 15*time.Second, "request timeout")
	flag.Parse()
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky-conformance-probe: --endpoint required")
		os.Exit(1)
	}

	ep := executor.Endpoint{Transport: *transport, URL: *endpoint}
	pool := executor.NewClientPool()
	defer pool.Close()
	client, err := pool.GetOrCreate(ep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
	req := &genv1.ExecuteRequest{
		NodeId:     "conformance-probe",
		InstanceId: "conformance-probe",
		NodeType:   "conformance-probe",
		Userdata:   ud,
	}
	stream, err := client.Execute(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "execute: %v\n", err)
		os.Exit(1)
	}
	defer stream.Close()

	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "recv: %v\n", err)
			os.Exit(1)
		}
		if c, ok := ev.Event.(*genv1.ExecuteEvent_Complete); ok {
			result := c.Complete.Result.AsInterface()
			m, ok := result.(map[string]any)
			if !ok {
				fmt.Fprintf(os.Stderr, "conformance: Complete.result not an object: %T\n", result)
				os.Exit(1)
			}
			if v, ok := m["stub"].(bool); !ok || !v {
				fmt.Fprintf(os.Stderr, "conformance: stub-mode probe did not return {stub:true}, got %+v\n", m)
				os.Exit(1)
			}
			fmt.Println("conformance: stub-mode probe OK")
			return
		}
		if e, ok := ev.Event.(*genv1.ExecuteEvent_Errored); ok {
			fmt.Fprintf(os.Stderr, "conformance: got Errored %s (%v)\n", e.Errored.ErrorClass, e.Errored.Payload.AsInterface())
			os.Exit(1)
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_AsyncAccepted); ok {
			fmt.Fprintln(os.Stderr, "conformance: stub-mode probe returned AsyncAccepted; expected Complete")
			os.Exit(1)
		}
		// Ignore Heartbeat; loop.
	}
	fmt.Fprintln(os.Stderr, "conformance: stream ended without terminal")
	os.Exit(1)
}
