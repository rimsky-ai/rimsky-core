// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Dual-role conformance for the fused `pkg:stores/postgres/` binary.
//
// The fused store registers both `concept:claim-producer` and
// `concept:executor` on the same gRPC endpoint per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6.
//
// This test invokes the SDK's conformance suites — the same code
// exercised by `cmd/rimsky-claim-producer-conformance` and
// `cmd/rimsky-executor-conformance` against a live deployment — as
// callable Go packages. The probes dial the in-process fused server's
// gRPC endpoint and exercise the protocol contracts end-to-end.
//
// Per spec §Item 6 — Operator registration: "the standard suites pass
// against the fused store."
//
// Pre-2026-05-24-repo-reorganization the test imported
// `pkg:conformance` (in-rimsky) and `pkg:runtime/remote` for the
// gRPC client. Both are now rimsky-internal and unreachable from
// lib/services. The rewrite uses
// `pkg:protocols/conformance/{claimproducer,executor}` and the
// `test/harness.DialClaimProducer` adapter (a tracked-duplicate of
// `pkg:runtime/peer.Dial`).
package atomicstaging

import (
	"context"
	"strings"
	"testing"
	"time"

	cpconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
	executorconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor/scenarios" // init() registration
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestPGFusedStore_ClaimProducerConformance dials the fused store via
// the SDK's harness-internal adapter and asserts every standard
// claim-producer conformance check passes. Pins the Capabilities
// handshake, the write-semantics envelope, the uniformity invariant,
// and the four runtime verbs (Open / Commit / Abandon / Release)
// against the live wire endpoint.
func TestPGFusedStore_ClaimProducerConformance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	endpoint, teardown := startPgStore(t, dsn, true)
	t.Cleanup(teardown)

	dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
	defer dialCancel()
	client, err := harness.DialClaimProducer(dialCtx, "conformance-target", "grpc://"+endpoint)
	if err != nil {
		t.Fatalf("DialClaimProducer: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	runCtx, runCancel := context.WithTimeout(ctx, 30*time.Second)
	defer runCancel()
	results := cpconf.Run(runCtx, client)
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
}

// TestPGFusedStore_ExecutorConformance dials the fused store's
// Executor service via the SDK's executor-conformance runner and
// asserts every non-stub-mode scenario passes. Stub-mode scenarios
// are skipped automatically because the fused executor is a verifier,
// not a stub.
func TestPGFusedStore_ExecutorConformance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	endpoint, teardown := startPgStore(t, dsn, true)
	t.Cleanup(teardown)

	runCtx, runCancel := context.WithTimeout(ctx, 90*time.Second)
	defer runCancel()
	results, err := executorconf.Run(runCtx, executorconf.RunnerOpts{
		Endpoint: executorconf.Endpoint{Transport: "grpc", URL: endpoint},
		Timeout:  15 * time.Second,
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
	if passed == 0 {
		t.Fatalf("executor conformance: 0 scenarios passed (skipped=%d)", skipped)
	}
	// Sanity: the happy_path scenario is not stub-gated; it MUST be in
	// the passing set.
	foundHappy := false
	for _, r := range results {
		if r.Scenario == "execute_happy_path" && r.Passed {
			foundHappy = true
			break
		}
	}
	if !foundHappy {
		t.Errorf("expected `execute_happy_path` scenario to pass; results: %v", scenarioNames(results))
	}
}

// scenarioNames condenses results for diagnostic output.
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
