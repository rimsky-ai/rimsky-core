// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// rimsky-validation-conformance is a black-box conformance suite for
// the Validation mix-in service-protocol. The runner library lives in
// `pkg:protocols/conformance/validation`; this binary is a thin CLI
// wrapper.
//
// Per plan:2026-05-15-data-platform-extensions-plan.md §M2.
//
// Usage:
//
//	rimsky-validation-conformance --endpoint grpc://localhost:9095 --role executor \
//	                              [--timeout 30s]
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

	"github.com/rimsky-ai/rimsky-core/protocols/conformance/validation"
	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

func main() {
	endpoint := flag.String("endpoint", "", "validation-advertising service gRPC endpoint (e.g. grpc://localhost:9095)")
	transport := flag.String("transport", "grpc", "transport: grpc (the http+json bridge is not implemented for Validation)")
	role := flag.String("role", "executor", "role to validate against: executor | claim_producer | lifecycle_subscriber | sensor")
	timeout := flag.Duration("timeout", 30*time.Second, "per-suite timeout")
	flag.Parse()

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky-validation-conformance: --endpoint required")
		os.Exit(2)
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky-validation-conformance: --transport %q not supported; use grpc\n", *transport)
		os.Exit(2)
	}

	target := strings.TrimPrefix(*endpoint, "grpc://")
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-validation-conformance: dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := genv1.NewValidationClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	results := RunValidationConformance(ctx, client, *role)
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
		fmt.Fprintf(os.Stderr, "rimsky-validation-conformance: %d/%d checks failed\n", failed, len(results))
		os.Exit(1)
	}
}

// CheckResult mirrors `pkg:protocols/conformance/validation.CheckResult`
// so the existing tests at cmd/rimsky-validation-conformance/main_test.go
// keep their existing shape.
type CheckResult = validation.CheckResult

// RunValidationConformance delegates to the importable package.
func RunValidationConformance(ctx context.Context, c genv1.ValidationClient, role string) []CheckResult {
	return validation.Run(ctx, c, role)
}
