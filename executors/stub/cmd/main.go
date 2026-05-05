// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// main.go — executor-stub. Standalone gRPC server wrapping the
// executors/stub Stub in stub mode. Used by the quickstart and any
// deployment that needs a no-op executor: every Execute call returns a
// single Complete with `changed: true` and `change_summary: "stub"`,
// keyed only on `node_type` via stub.StubAttributesFor.
package main

import (
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/fallguy/rimsky/executors/stub"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func main() {
	bind := flag.String("bind", envOr("EXECUTOR_STUB_BIND", "0.0.0.0:9300"), "host:port to listen on")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	lis, err := net.Listen("tcp", *bind)
	if err != nil {
		log.Error("listen failed", "addr", *bind, "err", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	s := stub.New().EnableStubMode()
	genv1.RegisterNodeExecutorServer(srv, s)
	stub.RegisterObservability(srv)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("shutdown signal received")
		srv.GracefulStop()
	}()

	log.Info("executor-stub listening", "addr", *bind)
	if err := srv.Serve(lis); err != nil {
		log.Error("serve failed", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
