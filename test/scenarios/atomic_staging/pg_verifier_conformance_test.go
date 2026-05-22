// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Dual-role conformance for the fused `stores/postgres/` binary.
//
// The fused store registers both `concept:claim-producer` (the
// `ClaimProducer` gRPC service) and `concept:executor` (the
// `Executor` gRPC service) on a single endpoint per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6.
//
// This test invokes the standard conformance suites — the same code
// exercised by `cmd/rimsky-claim-producer-conformance` and
// `cmd/rimsky-executor-conformance` against a live deployment — as
// callable Go packages. The probes dial the in-process fused server's
// gRPC endpoint and exercise the protocol contracts end-to-end.
//
// Stub-mode-only executor scenarios (`malformed_attributes`,
// `attributes_serialization`, `heartbeats`, `park_reason_emission`) are
// skipped automatically by the conformance runner because the fused
// store's executor role is a verifier, not a stub. The non-stub-gated
// scenarios (`execute_happy_path`, `cancel`, `stream_close_without_
// terminal`, `terminal_is_last`) all run and must pass.
//
// Per spec §Item 6 — Operator registration: "the standard suites pass
// against the fused store."

package atomicstaging

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/fallguy/rimsky/conformance"
	cpconf "github.com/fallguy/rimsky/conformance/claimproducer"
	_ "github.com/fallguy/rimsky/conformance/scenarios" // init() registration
	corestore "github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/runtime/executor"
	"github.com/fallguy/rimsky/runtime/remote"
	pgtestfixture "github.com/fallguy/rimsky/stores/postgres/testfixture"
)

// startFusedStoreForConformance boots a real Postgres + the fused
// stores/postgres/ gRPC server (ClaimProducer + Executor on one
// endpoint). Returns the gRPC endpoint host:port and a teardown.
func startFusedStoreForConformance(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	container, err := pgmodule.Run(ctx,
		"postgres:14-alpine",
		pgmodule.WithDatabase("rimsky"),
		pgmodule.WithUsername("rimsky"),
		pgmodule.WithPassword("rimsky"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("postgres testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() {
		termCtx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = container.Terminate(termCtx)
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	endpoint, _, teardown := pgtestfixture.Start(t, pgtestfixture.Config{
		Connection:     dsn,
		WriteSemantics: corestore.WriteSemanticsStagedAsync,
		EnableExecutor: true,
	})
	t.Cleanup(teardown)
	return endpoint
}

// TestPGFusedStore_ClaimProducerConformance dials the fused store via
// the same callable used by `cmd/rimsky-claim-producer-conformance`
// (RunClaimProducerConformance) and asserts every check passes. Pins
// the Capabilities handshake, write-semantics envelope, uniformity
// invariant, and the four runtime verbs (Open / Commit / Abandon /
// Release) against the live wire endpoint.
func TestPGFusedStore_ClaimProducerConformance(t *testing.T) {
	t.Parallel()
	endpoint := startFusedStoreForConformance(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := remote.Dial(ctx, "conformance-target", "grpc://"+endpoint)
	if err != nil {
		t.Fatalf("remote.Dial: %v", err)
	}
	t.Cleanup(client.Close)

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
}

// TestPGFusedStore_ExecutorConformance dials the fused store's
// Executor service via the same callable used by
// `cmd/rimsky-executor-conformance` (conformance.Run) and asserts every
// non-stub-mode scenario passes. Stub-mode-gated scenarios are skipped
// automatically by the runner because the fused store's executor role
// is a verifier (rejects `stub_probe: true` attributes with
// invalid_attribute, which is a valid Error terminal — the happy-path
// scenario tolerates Error outcomes).
//
// Async-gated scenarios are skipped because the postgres executor is
// synchronous-only.
func TestPGFusedStore_ExecutorConformance(t *testing.T) {
	t.Parallel()
	endpoint := startFusedStoreForConformance(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint: executor.Endpoint{Transport: "grpc", URL: endpoint},
		Timeout:  15 * time.Second,
		// CallbackBind/Host left empty → defaults to 127.0.0.1.
	})
	if err != nil {
		t.Fatalf("conformance.Run: %v", err)
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
		t.Fatalf("executor conformance: 0 scenarios passed (skipped=%d) — every scenario was gated out, suite degenerate", skipped)
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

func scenarioNames(rs []conformance.Result) string {
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
