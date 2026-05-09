// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/conformance"
	"github.com/fallguy/rimsky/modeling/executor"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func main() {
	endpoint := flag.String("endpoint", "", "executor endpoint URL")
	transport := flag.String("transport", "grpc", "grpc | http")
	timeout := flag.Duration("timeout", 15*time.Second, "request timeout")
	callbackBind := flag.String("callback-bind", "127.0.0.1", "interface for the callback receiver (use 0.0.0.0 with containerized executors)")
	callbackHost := flag.String("callback-host", "", "host the executor should reach the callback at (default: same as --callback-bind)")
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

	receiver, err := conformance.StartCallbackReceiver(conformance.ReceiverOptions{
		BindHost:      *callbackBind,
		AdvertiseHost: *callbackHost,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "callback receiver: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = receiver.Close() }()
	env := conformance.Env{Client: client, Callbacks: receiver}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
	req := &genv1.ExecuteRequest{
		NodeId:      "conformance-probe",
		InstanceId:  "conformance-probe",
		NodeType:    "conformance-probe",
		Userdata:    ud,
		CallbackUrl: receiver.URL(),
	}
	stream, err := client.Execute(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "execute: %v\n", err)
		os.Exit(1)
	}
	defer stream.Close()

	ev, err := conformance.AwaitTerminal(ctx, stream, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance: %v\n", err)
		os.Exit(1)
	}
	if c, ok := ev.Event.(*genv1.ExecuteEvent_Complete); ok {
		// Stub mode signals via attributes_delta after the §12 protocol
		// rewrite (Complete.Result was removed; terminal-final attribute
		// writeback replaces it).
		m := c.Complete.GetAttributesDelta().AsMap()
		if v, ok := m["stub"].(bool); !ok || !v {
			fmt.Fprintf(os.Stderr, "conformance: stub-mode probe did not return {stub:true}, got %+v\n", m)
			os.Exit(1)
		}
		fmt.Println("conformance: stub-mode probe OK")
		return
	}
	if e, ok := ev.Event.(*genv1.ExecuteEvent_Errored); ok {
		fmt.Fprintf(os.Stderr, "conformance: got Errored %s (%v)\n", e.Errored.ErrorClass, e.Errored.GetPayload().AsMap())
		os.Exit(1)
	}
	if _, ok := ev.Event.(*genv1.ExecuteEvent_AsyncAccepted); ok {
		fmt.Fprintln(os.Stderr, "conformance: stub-mode probe ended at AsyncAccepted but no callback arrived")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "conformance: unexpected terminal type %T\n", ev.Event)
	os.Exit(1)
}
