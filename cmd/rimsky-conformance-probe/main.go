// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// rimsky-conformance-probe is the protocol-agnostic stub-mode probe.
// Issues one Execute RPC with `attributes: {stub_probe: true}` and
// asserts the terminal carries `attributes_delta = {stub: true}`. Used
// as a sidecar by every conformance binary.
//
// The conformance scaffolding (callback receiver, AwaitTerminal,
// minimal Client) lives in `pkg:protocols/conformance/executor`; this
// binary uses that surface so it does not import any rimsky-internal
// runtime package.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/fallguyconsulting/rimsky/protocols/conformance/executor"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
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

	ep := conformance.Endpoint{Transport: *transport, URL: *endpoint}
	pool := conformance.NewClientPool()
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
		Attributes:  ud,
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
	sc, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose)
	if !ok {
		fmt.Fprintf(os.Stderr, "conformance: unexpected terminal type %T\n", ev.Event)
		os.Exit(1)
	}
	switch oc := sc.StreamClose.Outcome.(type) {
	case *genv1.StreamClose_Success:
		// Stub mode signals via attributes_delta on Success.
		m := oc.Success.GetAttributesDelta().AsMap()
		if v, ok := m["stub"].(bool); !ok || !v {
			fmt.Fprintf(os.Stderr, "conformance: stub-mode probe did not return {stub:true}, got %+v\n", m)
			os.Exit(1)
		}
		fmt.Println("conformance: stub-mode probe OK")
		return
	case *genv1.StreamClose_Error:
		fmt.Fprintf(os.Stderr, "conformance: got Error %s (%v)\n", oc.Error.ErrorClass, oc.Error.GetPayload().AsMap())
		os.Exit(1)
	case *genv1.StreamClose_AwaitAsync:
		fmt.Fprintln(os.Stderr, "conformance: stub-mode probe ended at AwaitAsyncCallback but no callback arrived")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "conformance: unexpected StreamClose outcome %T\n", sc.StreamClose.Outcome)
	os.Exit(1)
}
