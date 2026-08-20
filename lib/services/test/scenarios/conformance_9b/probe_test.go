// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance9b

import (
	"context"
	"strings"
	"testing"

	cpconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestClaimProducer9b_Probe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

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
	if !strings.Contains(checkB.Err.Error(), "reader-lease") {
		t.Fatalf("dishonest producer: Serialization9b error must name the reader-lease violation; got %q", checkB.Err.Error())
	}
}

func dialProducer(t *testing.T, ctx context.Context, name, endpoint string) *harness.ClaimProducerClient {
	t.Helper()
	client, err := harness.DialClaimProducer(ctx, name, "grpc://"+endpoint)
	if err != nil {
		t.Fatalf("DialClaimProducer(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func runConformance(t *testing.T, ctx context.Context, client *harness.ClaimProducerClient) []cpconf.CheckResult {
	t.Helper()
	return cpconf.Run(ctx, client)
}

func findCheck(results []cpconf.CheckResult, name string) (cpconf.CheckResult, bool) {
	for _, r := range results {
		if r.Name == name {
			return r, true
		}
	}
	return cpconf.CheckResult{}, false
}

func checkNames(results []cpconf.CheckResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Name)
	}
	return out
}
