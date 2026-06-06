// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

// main stands up the subscriber as a gRPC server. For non-Go callers, serverkit
// also exposes an HTTP+JSON bridge for this protocol — mount it on a second
// listener:
//
//	mux := http.NewServeMux()
//	serverkit.MountLifecycle(mux, sub)
//	go http.ListenAndServe("0.0.0.0:9501", mux)
//
// (The executor protocol has no such bridge; lifecycle-subscriber and
// claim-producer do.)
func main() {
	sub := &Subscriber{}

	lis, err := serverkit.Listen("0.0.0.0", 9500)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterLifecycleSubscriberServer(srv, sub)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverkit.RunGRPC(ctx, srv, lis, "example-lifecycle-subscriber")
}
