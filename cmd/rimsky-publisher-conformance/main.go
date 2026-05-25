// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// rimsky-publisher-conformance is a black-box conformance suite for the
// Publisher service-protocol (bundled sensor binaries:
// sensors/sensor-cron, sensors/sensor-http, sensors/sensor-object-store,
// sensors/sensor-webhook). Custom publisher authors can point this
// binary at their service to verify lifecycle + message-push shape.
//
// Per spec
// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification.
//
// The suite spins up a fake rimsky receiver on an ephemeral loopback
// port and waits for the publisher to POST at least one message
// envelope to `POST /instances/{instance_id}/messages` (sensor-cron
// with a `* * * * *` cron expression fires within ~1s of Subscribe).
//
// Usage:
//
//	rimsky-publisher-conformance --endpoint grpc://localhost:9202 \
//	                             --kind cron --resolved-config '{"cron":"* * * * *"}' \
//	                             --instance-id i1 [--timeout 30s]
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

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

func main() {
	endpoint := flag.String("endpoint", "", "publisher gRPC endpoint (e.g. grpc://localhost:9202)")
	transport := flag.String("transport", "grpc", "transport: grpc")
	kind := flag.String("kind", "", "publisher kind to exercise (e.g. cron, http, object-store, webhook)")
	resolvedConfig := flag.String("resolved-config", "", "JSON resolved_config to drive Subscribe (kind-specific)")
	timeout := flag.Duration("timeout", 30*time.Second, "per-suite timeout")
	instanceID := flag.String("instance-id", "", "instance_id passed to Subscribe; required when publisher pushes to /instances/{id}/messages")
	flag.Parse()

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky-publisher-conformance: --endpoint required")
		os.Exit(2)
	}
	if *kind == "" {
		fmt.Fprintln(os.Stderr, "rimsky-publisher-conformance: --kind required")
		os.Exit(2)
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky-publisher-conformance: --transport %q not supported; use grpc\n", *transport)
		os.Exit(2)
	}

	target := strings.TrimPrefix(*endpoint, "grpc://")
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-publisher-conformance: dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := genv1.NewPublisherClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	opts := RunOpts{
		Kind:           *kind,
		ResolvedConfig: []byte(*resolvedConfig),
		InstanceID:     *instanceID,
	}
	results := RunPublisherConformance(ctx, client, opts)
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
		fmt.Fprintf(os.Stderr, "rimsky-publisher-conformance: %d/%d checks failed\n", failed, len(results))
		os.Exit(1)
	}
}
