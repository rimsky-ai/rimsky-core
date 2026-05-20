// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// rimsky-claim-producer-conformance runs the ClaimProducer conformance
// suite against a remote producer-service endpoint. Checks include the
// Capabilities handshake, write-semantics envelope conformance, the
// uniformity invariant (byte-equal Scope ⇒ identical
// RealizedWriteSemantics), and the four runtime verbs.
//
// The conformance logic lives in `conformance/claimproducer/` so tests
// (e.g. `test/scenarios/atomic_staging/pg_verifier_conformance_test.go`)
// can invoke the same code path against an in-process fused server
// without forking the binary.
//
// Lifecycle conformance lives in `rimsky-executor-conformance --check-lifecycle`
// per the layer-crystallization plan (Phase 4 / Task 29).
//
// Usage:
//
//	rimsky-claim-producer-conformance --endpoint grpc://localhost:9101 [--timeout 10s]
//
// Exits 0 on success, 1 on any failure. Output is one line per check.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fallguy/rimsky/conformance/claimproducer"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/runtime/remote"
)

func main() {
	endpoint := flag.String("endpoint", "", "claim-producer-service gRPC endpoint (e.g. grpc://localhost:9101)")
	timeout := flag.Duration("timeout", 10*time.Second, "per-check timeout")
	checkObs := flag.Bool("check-observability", false, "additionally probe ClaimProducerObservability")
	retentionSec := flag.Int("retention-test-seconds", 0, "if >0, drive a canned claim then sleep this long and verify GetClaim returns evicted")
	flag.Parse()
	obsRetentionTestSeconds = *retentionSec

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky-claim-producer-conformance: --endpoint required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := remote.Dial(ctx, "conformance-target", *endpoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-claim-producer-conformance: dial: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	results := RunClaimProducerConformance(ctx, client)
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
		fmt.Fprintf(os.Stderr, "rimsky-claim-producer-conformance: %d/%d checks failed\n", failed, len(results))
		os.Exit(1)
	}

	if *checkObs {
		if err := runObservabilityCheck(ctx, *endpoint); err != nil {
			fmt.Fprintf(os.Stderr, "observability: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "observability: ok")
	}
}

// CheckResult mirrors `conformance/claimproducer.CheckResult` so the
// existing tests at cmd/rimsky-claim-producer-conformance/main_test.go
// keep their existing shape. The binary delegates to the importable
// package; this thin alias avoids churn in callers.
type CheckResult = claimproducer.CheckResult

// RunClaimProducerConformance delegates to the importable package.
// Retained as a binary-local entry point so tests under
// `cmd/rimsky-claim-producer-conformance/` keep their existing import.
func RunClaimProducerConformance(ctx context.Context, c locks.ClaimProducer) []CheckResult {
	return claimproducer.Run(ctx, c)
}
