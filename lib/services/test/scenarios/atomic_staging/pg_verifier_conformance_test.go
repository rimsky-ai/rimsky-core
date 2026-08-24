// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package atomicstaging

import (
	"context"
	"strings"
	"testing"
	"time"

	cpconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
	executorconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor/scenarios"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestPGFusedStore_ClaimProducerConformance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	endpoint, teardown := startPgStore(t, dsn, true)
	t.Cleanup(teardown)

	client, err := harness.DialClaimProducer(ctx, "conformance-target", "grpc://"+endpoint)
	if err != nil {
		t.Fatalf("DialClaimProducer: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	results := cpconf.Run(ctx, client)
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			t.Errorf("claim-producer conformance FAIL %s: %v", r.Name, r.Err)
		} else {
			t.Logf("claim-producer conformance ok %s", r.Name)
		}
	}
	if failed > 0 {
		t.Fatalf("%d/%d claim-producer conformance checks failed", failed, len(results))
	}

	// @story: claim-producer-conformance
	for _, name := range []string{"Commit", "Abandon", "Release", "TerminalIdempotency"} {
		assertResultPassing(t, results, name)
	}
}

func assertResultPassing(t *testing.T, results []cpconf.CheckResult, name string) {
	t.Helper()
	for _, r := range results {
		if r.Name != name {
			continue
		}
		if r.Err != nil {
			t.Errorf("claim-producer conformance row %q present but FAILED: %v", name, r.Err)
		}
		return
	}
	t.Errorf("claim-producer conformance result set is missing the %q row (have: %s)",
		name, resultRowNames(results))
}

func resultRowNames(results []cpconf.CheckResult) string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Name)
	}
	return "[" + strings.Join(out, ", ") + "]"
}

func TestPGFusedStore_ExecutorConformance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	endpoint, teardown := startPgStore(t, dsn, true)
	t.Cleanup(teardown)

	results, err := executorconf.Run(ctx, executorconf.RunnerOpts{
		Endpoint:  executorconf.Endpoint{Transport: "grpc", URL: endpoint},
		AllowLive: true,
		Timeout:   15 * time.Second,
	})
	if err != nil {
		t.Fatalf("executor conformance Run: %v", err)
	}
	failed := 0
	passed := 0
	skipped := 0
	for _, r := range results {
		switch {
		case r.Skipped:
			skipped++
			t.Logf("executor conformance SKIP %s (%s)", r.Scenario, r.Error)
		case !r.Passed:
			failed++
			t.Errorf("executor conformance FAIL %s: %s", r.Scenario, r.Error)
		default:
			passed++
			t.Logf("executor conformance ok %s (%.3fs)", r.Scenario, r.Duration.Seconds())
		}
	}
	if failed > 0 {
		t.Fatalf("%d/%d executor conformance scenarios failed (passed=%d skipped=%d)",
			failed, len(results), passed, skipped)
	}
	if len(results) == 0 {
		t.Fatal("executor conformance: no scenarios registered")
	}
	if skipped != len(results) {
		t.Fatalf("executor conformance: expected every scenario to skip against the postgres-store "+
			"verifier executor (not stub-mode conformant); passed=%d skipped=%d results=%v",
			passed, skipped, scenarioNames(results))
	}
	for _, r := range results {
		if r.Error != "stub mode required" {
			t.Errorf("executor conformance %s: skipped for unexpected reason %q", r.Scenario, r.Error)
		}
	}
}

func scenarioNames(rs []executorconf.Result) string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		status := "PASS"
		if r.Skipped {
			status = "SKIP"
		} else if !r.Passed {
			status = "FAIL"
		}
		out = append(out, r.Scenario+":"+status)
	}
	return "[" + strings.Join(out, ", ") + "]"
}
