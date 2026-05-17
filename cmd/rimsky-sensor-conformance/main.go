// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// rimsky-sensor-conformance is a black-box conformance suite for the
// Sensor service-protocol (bundled sensor binaries:
// sensors/sensor-cron, sensors/sensor-http, sensors/sensor-object-store,
// sensors/sensor-webhook). Custom sensor authors can point this
// binary at their service to verify lifecycle + observation-push
// shape.
//
// Per plan:2026-05-15-data-platform-extensions-plan.md §M3.
//
// The suite spins up a fake rimsky receiver on an ephemeral loopback
// port and waits for the sensor to POST at least one observation
// (sensor-cron with a `* * * * *` cron expression fires within ~1s
// of StartWatch).
//
// Usage:
//
//	rimsky-sensor-conformance --endpoint grpc://localhost:9202 \
//	                          --kind cron --resolved-config '{"cron":"* * * * *"}' \
//	                          [--timeout 30s]
//
// Exits 0 on success, 1 on any failure.
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

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func main() {
	endpoint := flag.String("endpoint", "", "sensor-service gRPC endpoint (e.g. grpc://localhost:9202)")
	transport := flag.String("transport", "grpc", "transport: grpc")
	kind := flag.String("kind", "", "sensor kind to exercise (e.g. cron, http, object_store, webhook)")
	resolvedConfig := flag.String("resolved-config", "", "JSON resolved_config to drive StartWatch (kind-specific)")
	timeout := flag.Duration("timeout", 30*time.Second, "per-suite timeout")
	flag.Parse()

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky-sensor-conformance: --endpoint required")
		os.Exit(2)
	}
	if *kind == "" {
		fmt.Fprintln(os.Stderr, "rimsky-sensor-conformance: --kind required")
		os.Exit(2)
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky-sensor-conformance: --transport %q not supported; use grpc\n", *transport)
		os.Exit(2)
	}

	target := strings.TrimPrefix(*endpoint, "grpc://")
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-sensor-conformance: dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := genv1.NewSensorClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	opts := RunOpts{
		Kind:           *kind,
		ResolvedConfig: []byte(*resolvedConfig),
	}
	results := RunSensorConformance(ctx, client, opts)
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
		fmt.Fprintf(os.Stderr, "rimsky-sensor-conformance: %d/%d checks failed\n", failed, len(results))
		os.Exit(1)
	}
}
