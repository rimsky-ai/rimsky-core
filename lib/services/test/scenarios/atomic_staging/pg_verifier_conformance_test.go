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
	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor/scenarios" // @constraint: blank import drives init()-time scenario registration into the executor conformance runner
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestPGFusedStore_ClaimProducerConformance dials the fused store via
// the SDK's harness-internal adapter and asserts every standard
// claim-producer conformance check passes. Pins the Capabilities
// handshake, the write-semantics envelope, the uniformity invariant,
// the terminal verbs (Commit / Abandon / Release) driven against real
// claims the suite Open'd, and the retried-terminal idempotency probe
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

	// @story: claim-producer-conformance — the suite MUST drive the full
	// claim lifecycle (Commit, Abandon, Release on real claims it Open'd)
	// plus a retried-terminal idempotency check, each reported as its own
	// pass/fail row. Against the real fused postgres producer (whose
	// terminal verbs are idempotent in claim_id per
	// code:lib/services/stores/postgres/store.go::Commit), every one of
	// these rows MUST be present AND passing (Err == nil).
	for _, name := range []string{"Commit", "Abandon", "Release", "TerminalIdempotency"} {
		assertResultPassing(t, results, name)
	}
}

// assertResultPassing fails the test unless the named conformance row is
// present in results with a nil Err. A missing row is as much a failure
// as a failing one: the suite is contracted to REPORT each terminal verb,
// not silently skip it.
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

// resultRowNames condenses the result row names for diagnostic output.
func resultRowNames(results []cpconf.CheckResult) string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Name)
	}
	return "[" + strings.Join(out, ", ") + "]"
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
	// @constraint: The non-stub baseline pair (`cancel`,
	// `unknown_ack_id`) are not stub-gated, so a real (non-stub)
	// executor MUST land them in the passing set; a missing or failing
	// row here means the runner silently skipped one half of the
	// non-stub conformance surface. Stub-gated scenarios that the pg
	// verifier legitimately skips (every Execute-driving scenario,
	// since a verifier has no stub mode) are NOT in this list — the
	// `passed > 0` floor above plus an explicit per-scenario assertion
	// would conflate "executor skipped because it can't stub" with
	// "executor failed silently."
	for _, want := range []string{"cancel", "unknown_ack_id"} {
		found := false
		for _, r := range results {
			if r.Scenario == want && r.Passed {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected non-stub scenario %q to pass; results: %v", want, scenarioNames(results))
		}
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
