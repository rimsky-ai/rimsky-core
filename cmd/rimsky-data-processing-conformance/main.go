// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// rimsky-data-processing-conformance is a black-box conformance suite
// for the DataProcessing mix-in service-protocol. Any producer that
// advertises `data_processing` in Capabilities.protocols can be
// pointed at this binary; it exercises the seven RPCs (Capabilities +
// BeginCandidate + CommitCandidate + AbandonCandidate + ListVersions +
// ListPartitions + GetVersionSchema) plus per-RPC idempotency and
// concurrent-write coverage.
//
// Per plan:2026-05-15-data-platform-extensions-plan.md §M1.
//
// Usage:
//
//	rimsky-data-processing-conformance --endpoint grpc://localhost:9101 \
//	                                   [--timeout 30s]
//
// Exits 0 on success, 1 on any failure. Output is one line per check.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

func main() {
	endpoint := flag.String("endpoint", "", "data-processing-service gRPC endpoint (e.g. grpc://localhost:9101)")
	transport := flag.String("transport", "grpc", "transport: grpc (the http+json bridge is not yet implemented for DataProcessing)")
	timeout := flag.Duration("timeout", 30*time.Second, "per-suite timeout")
	flag.Parse()

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky-data-processing-conformance: --endpoint required")
		os.Exit(2)
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky-data-processing-conformance: --transport %q not supported; use grpc\n", *transport)
		os.Exit(2)
	}

	target := strings.TrimPrefix(*endpoint, "grpc://")
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-data-processing-conformance: dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := genv1.NewDataProcessingClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	results := RunDataProcessingConformance(ctx, client)
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
			continue
		}
		fmt.Printf("ok    %s\n", r.Name)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "rimsky-data-processing-conformance: %d/%d checks failed\n", failed, len(results))
		os.Exit(1)
	}
}
