// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance9b

import (
	"context"
	"strings"
	"testing"
	"time"

	cpconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestClaimProducer9b_Probe drives the REAL claim-producer conformance
// runner (cpconf.Run) over the wire against two real staged_async
// producers and asserts the suite carries a `Serialization9b` check that
// distinguishes honest snapshot delegation from the reader-lease
// serialization @blessed-invariant 9b forbids:
//
//   - honest producer A: Serialization9b present and ok (Err == nil) —
//     reader Opens against an open writer return promptly.
//   - dishonest producer B: Serialization9b present and FAIL (Err != nil)
//     with an error naming invariant 9b — a reader Open blocks until the
//     writer's claim is terminal-ed.
//
// This is the RED proof for the 9b probe: the runner does not yet emit a
// `Serialization9b` row, so the assertions that such a row exists fail
// today. A later pass adds checkSerialization9b to the runner and turns
// this test green.
func TestClaimProducer9b_Probe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Producer A — honest snapshot-delegating staged_async producer.
	endpointA := startProducer(t, &honestProducer{})
	clientA := dialProducer(t, ctx, "honest-9b", endpointA)
	resultsA := runConformance(t, ctx, clientA)
	checkA, foundA := findCheck(resultsA, "Serialization9b")
	if !foundA {
		t.Fatalf("honest producer: no Serialization9b check in conformance results; got %v", checkNames(resultsA))
	}
	if checkA.Err != nil {
		t.Fatalf("honest producer: Serialization9b reported FAIL, want ok: %v", checkA.Err)
	}

	// Producer B — dishonest reader-lease-serializing staged_async producer.
	endpointB := startProducer(t, newDishonestProducer())
	clientB := dialProducer(t, ctx, "dishonest-9b", endpointB)
	resultsB := runConformance(t, ctx, clientB)
	checkB, foundB := findCheck(resultsB, "Serialization9b")
	if !foundB {
		t.Fatalf("dishonest producer: no Serialization9b check in conformance results; got %v", checkNames(resultsB))
	}
	if checkB.Err == nil {
		t.Fatalf("dishonest producer: Serialization9b reported ok, want FAIL (reader Open blocks behind an open writer)")
	}
	if !strings.Contains(checkB.Err.Error(), "9b") {
		t.Fatalf("dishonest producer: Serialization9b error must name invariant 9b; got %q", checkB.Err.Error())
	}
}

// dialProducer dials a producer endpoint via the SDK harness adapter
// (the same wire path a deployed conformance run uses) and registers
// cleanup. The returned client satisfies the Go ClaimProducer interface
// the conformance runner consumes.
func dialProducer(t *testing.T, ctx context.Context, name, endpoint string) *harness.ClaimProducerClient {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := harness.DialClaimProducer(dialCtx, name, "grpc://"+endpoint)
	if err != nil {
		t.Fatalf("DialClaimProducer(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// runConformance runs the full claim-producer conformance suite against
// the dialed client under a bounded context so a producer that
// dishonestly blocks a reader Open forever cannot wedge the test.
func runConformance(t *testing.T, ctx context.Context, client *harness.ClaimProducerClient) []cpconf.CheckResult {
	t.Helper()
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return cpconf.Run(runCtx, client)
}

// findCheck returns the conformance result row with the given name.
func findCheck(results []cpconf.CheckResult, name string) (cpconf.CheckResult, bool) {
	for _, r := range results {
		if r.Name == name {
			return r, true
		}
	}
	return cpconf.CheckResult{}, false
}

// checkNames lists the conformance result names for diagnostics.
func checkNames(results []cpconf.CheckResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Name)
	}
	return out
}
